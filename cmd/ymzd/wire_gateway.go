package main

import (
	"context"
	"log/slog"

	"github.com/yyZe0122/yunmengze-agent/internal/app"
	"github.com/yyZe0122/yunmengze-agent/internal/chatsession"
	"github.com/yyZe0122/yunmengze-agent/internal/gateway"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/internal/providerruntime"
	"github.com/yyZe0122/yunmengze-agent/internal/scheduledtasks"
	"github.com/yyZe0122/yunmengze-agent/internal/scheduler"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/skillmaintain"
	"github.com/yyZe0122/yunmengze-agent/internal/taskcontrol"
	"github.com/yyZe0122/yunmengze-agent/internal/tasksubmission"
	"github.com/yyZe0122/yunmengze-agent/internal/toolpermission"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func wireAppAndJobs(
	stores coreStores,
	stack toolStack,
	chat chatStack,
	skillCatalog *skillcatalog.Catalog,
	layout paths.Layout,
	workingDirectory string,
) (*tasksubmission.Service, *taskcontrol.Service, *skillmaintain.Service, *scheduler.Store, *app.Core, error) {
	var chatInterrupt taskcontrol.ChatInterrupter
	if chat.chatService != nil {
		chatInterrupt = chat.chatService
	}
	taskControl, err := taskcontrol.New(taskcontrol.Config{
		DB: stores.database.SQL(), Approvals: stores.approvalRepository, Repository: stores.kernelRepository, Chat: chatInterrupt,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	taskSubmissionConfig := tasksubmission.Config{Repository: stores.kernelRepository, Skills: skillCatalog}
	if chat.chatService != nil {
		taskSubmissionConfig.Chat = chatsession.AsTaskChat(chat.chatService)
	}
	skillStore, err := skillmaintain.NewStore(stores.database.SQL())
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	skillMaintain, err := skillmaintain.New(skillmaintain.Config{
		Store: skillStore, Catalog: skillCatalog, UnusedTTL: chat.chatCfg.SkillsUnusedTTL(),
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	skillMaintain.Maintain(context.Background())
	if err := tools.RegisterSkillTools(stack.broker, skillCatalog, skillMaintain); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if chat.chatService != nil {
		chat.chatService.SetSkills(skillCatalog)
	}
	if stack.taskTool != nil {
		stack.taskTool.SetAgentsOverlay(layout.ConfigDir, workingDirectory)
	}
	if err := tools.RegisterSkillDraftTool(stack.broker, tools.SkillDraftAdapter{
		Catalog: skillCatalog, Maintain: skillMaintain,
	}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	taskSubmissionConfig.SkillUsage = skillMaintain
	taskSubmissionService, err := tasksubmission.New(taskSubmissionConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	var mainModelRef scheduler.MainModelRefFunc
	if chat.providerRuntime != nil {
		mainModelRef = chat.providerRuntime.SelectedRef
	}
	schedulerStore, err := scheduler.NewStoreWithMainRef(stores.database.SQL(), mainModelRef)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	jobRunner, err := scheduledtasks.New(scheduledtasks.Config{
		Client:      schedulerStore,
		Submissions: taskSubmissionService,
		Owner:       "ymzd",
		OnError: func(err error) {
			slog.Error("scheduled job runner failure", "component", "scheduledtasks", "operation", "poll", "result", "failed", "error", err)
		},
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	core, err := app.New(app.Config{
		Name:              "ymzd",
		Version:           version.Version,
		Runtime:           layout,
		BackgroundRunners: []app.BackgroundRunner{jobRunner},
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return taskSubmissionService, taskControl, skillMaintain, schedulerStore, core, nil
}

func wireGatewayAPI(
	stores coreStores,
	stack toolStack,
	chat chatStack,
	layout paths.Layout,
	skillCatalog *skillcatalog.Catalog,
	taskSubmissionService *tasksubmission.Service,
	taskControl *taskcontrol.Service,
	skillMaintain *skillmaintain.Service,
	schedulerStore *scheduler.Store,
	core *app.Core,
) (*gateway.API, error) {
	var modelSwitcher gateway.ModelSwitcher
	var modelConfig gateway.ModelConfig
	var modelConfigError string
	if chat.providerRuntime != nil {
		var mainEP providerruntime.MainEndpoint
		if chat.agentRunner != nil {
			mainEP = chat.agentRunner
		}
		var chatCW providerruntime.ContextWindow
		if chat.chatService != nil {
			chatCW = chat.chatService
		}
		chat.providerRuntime.Bind(mainEP, chatCW, nil)
		modelConfig, modelConfigError = chat.providerRuntime.Snapshot()
		modelSwitcher = chat.providerRuntime
	} else {
		modelConfig = gateway.ModelConfig{Models: []string{}, Ready: false, Error: "provider runtime is not configured"}
		modelConfigError = modelConfig.Error
	}
	var mcpStatus gateway.MCPStatusProvider
	if stack.mcpRegistry != nil {
		mcpStatus = mcpStatusAdapter{registry: stack.mcpRegistry}
	}
	chatCommandsProvider := chatCommandsAdapter{cfg: chat.chatCfg}
	var sessionCompact gateway.SessionCompactor
	if chat.chatService != nil {
		sessionCompact = gateway.SessionCompactFunc(func(ctx context.Context, sessionID kernel.SessionID, focus string) (gateway.SessionCompactResult, error) {
			r, err := chat.chatService.ForceCompact(ctx, sessionID, focus)
			if err != nil {
				return gateway.SessionCompactResult{}, err
			}
			return gateway.SessionCompactResult{
				SessionID: r.SessionID, Summary: r.Summary, Source: r.Source, CompactionID: r.CompactionID,
			}, nil
		})
	}
	var sessionRewind gateway.SessionRewinder
	if chat.chatService != nil {
		sessionRewind = gateway.SessionRewindFunc(func(ctx context.Context, sessionID kernel.SessionID, revisionID string) (gateway.SessionRewindResult, error) {
			r, err := chat.chatService.RewindEdit(ctx, sessionID, revisionID)
			if err != nil {
				return gateway.SessionRewindResult{}, err
			}
			return gateway.SessionRewindResult{SessionID: r.SessionID, RevisionID: r.RevisionID, Path: r.Path}, nil
		})
	}
	var sessionRetract gateway.SessionRetractor
	if chat.chatService != nil {
		sessionRetract = gateway.SessionRetractFunc(func(ctx context.Context, sessionID kernel.SessionID, rewindFiles bool) (gateway.SessionRetractResult, error) {
			r, err := chat.chatService.RetractLastTurn(ctx, sessionID, rewindFiles)
			out := gateway.SessionRetractResult{
				SessionID: r.SessionID, TaskID: r.TaskID, UserText: r.UserText,
				RewindFiles: r.RewindFiles, RewoundPaths: r.RewoundPaths,
				FailedPath: r.FailedPath, FailedReason: r.FailedReason, CancelledTurn: r.CancelledTurn,
			}
			if err != nil {
				return out, err
			}
			return out, nil
		})
	}
	var sessionSteer gateway.SessionSteerer
	if chat.chatService != nil {
		sessionSteer = gateway.SessionSteerFunc(func(ctx context.Context, sessionID kernel.SessionID, text string) (gateway.SessionSteerResult, error) {
			r, err := chat.chatService.Steer(ctx, sessionID, text)
			if err != nil {
				return gateway.SessionSteerResult{}, err
			}
			return gateway.SessionSteerResult{
				SessionID: r.SessionID, TaskID: r.TaskID, RunID: r.RunID, ItemID: r.ItemID,
			}, nil
		})
	}
	var memoryControl gateway.MemoryControlService
	if chat.chatService != nil && chat.memoryManager != nil {
		memoryControl = memoryControlAdapter{chat: chat.chatService}
	}
	return gateway.NewAPI(gateway.APIConfig{
		Queries: chat.queries, TaskSubmissions: taskSubmissionService,
		TaskControls: taskControl, Jobs: schedulerStore,
		Core: core, Events: stores.eventStore, Skills: skillCatalog, ModelConfig: modelConfig, ModelSwitcher: modelSwitcher,
		ModelConfigError: modelConfigError,
		ModelStream:      chat.modelHub, MCP: mcpStatus, ChatCommands: chatCommandsProvider, SessionCompact: sessionCompact,
		SessionRewind:  sessionRewind,
		SessionRetract: sessionRetract,
		SessionSteer:   sessionSteer,
		UserQuestions:  gateway.UserQuestionAdapter{Service: chat.questionService},
		ToolPermissions: gateway.ToolPermissionAdapter{
			Service:   chat.permService,
			TrustPath: toolpermission.DefaultTrustPath(layout.ConfigDir),
		},
		MemoryControl: memoryControl,
		SkillControl:  skillControlAdapter{svc: skillMaintain},
		SessionPrefs:  sessionPrefsAdapter{repo: stores.kernelRepository},
	})
}

func bindProviderGateway(chat chatStack, gatewayAPI *gateway.API) {
	if chat.providerRuntime == nil {
		return
	}
	var mainEP providerruntime.MainEndpoint
	if chat.agentRunner != nil {
		mainEP = chat.agentRunner
	}
	var chatCW providerruntime.ContextWindow
	if chat.chatService != nil {
		chatCW = chat.chatService
	}
	chat.providerRuntime.Bind(mainEP, chatCW, gatewayAPI)
}

func newGatewayRunner(layout paths.Layout, gatewayAPI *gateway.API) (*gateway.LocalRunner, error) {
	return gateway.NewLocalRunner(gateway.LocalRunnerConfig{
		RuntimeDir: layout.RuntimeDir,
		Handler:    gatewayAPI,
		OnError: func(err error) {
			slog.Error("gateway failure", "component", "gateway", "operation", "serve", "result", "failed", "error", err)
		},
	})
}
