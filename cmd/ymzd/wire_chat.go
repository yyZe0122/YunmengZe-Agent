package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/chatsession"
	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/editrev"
	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/modelresolve"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providerruntime"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
	"github.com/yyZe0122/yunmengze-agent/internal/sessiontodo"
	"github.com/yyZe0122/yunmengze-agent/internal/toolpermission"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/internal/userquestion"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type chatStack struct {
	queries         *corequery.Store
	providerRuntime *providerruntime.Runtime
	modelHub        *modelstream.Hub
	contextStore    *contextpack.Store
	calibrator      *contextpack.Calibrator
	contextWindow   int64
	maxOutputTokens int64
	chatCfg         providerconfig.ChatConfig
	permService     *toolpermission.Service
	questionService *userquestion.Service
	memoryManager   *memory.Manager
	agentRunner     *agent.Runner
	chatService     *chatsession.Service
}

func wireChat(
	stores coreStores,
	stack toolStack,
	layout paths.Layout,
	workingDirectory string,
) (chatStack, error) {
	var out chatStack
	out.chatCfg = stack.chatCfg

	providerRuntime, err := providerruntime.FromConfigDir(layout.ConfigDir)
	if err != nil {
		return out, err
	}
	out.providerRuntime = providerRuntime

	queries, err := corequery.New(stores.database.SQL())
	if err != nil {
		return out, err
	}
	out.queries = queries

	recordStore, err := agent.NewRecordStore(stores.database.SQL())
	if err != nil {
		return out, err
	}
	out.modelHub = modelstream.NewHub()
	contextStore, err := contextpack.NewStore(stores.database.SQL())
	if err != nil {
		return out, err
	}
	out.contextStore = contextStore
	out.calibrator = contextpack.NewCalibrator()

	if providerRuntime != nil && providerRuntime.SelectedRef() != "" {
		if resolved, resolveErr := providerconfig.ResolveModel(layout.ConfigDir, providerRuntime.SelectedRef()); resolveErr == nil && resolved != nil {
			out.contextWindow = resolved.ContextWindow
			out.maxOutputTokens = resolved.MaxTokens
		}
	}

	maxIterations := out.chatCfg.MaxIterationsOrDefault()
	compactionEnabled := out.chatCfg.CompactionEnabled()

	permStore, err := toolpermission.NewStore(stores.database.SQL())
	if err != nil {
		return out, err
	}
	permService, err := toolpermission.New(toolpermission.Config{
		Events: stores.eventStore,
		DB:     stores.database.SQL(), Store: permStore, Approvals: stores.approvalRepository,
	})
	if err != nil {
		return out, err
	}
	out.permService = permService
	stack.broker.SetPermission(toolpermission.NewGate(permService))

	qStore, err := userquestion.NewStore(stores.database.SQL())
	if err != nil {
		return out, err
	}
	qService, err := userquestion.New(userquestion.Config{
		Store: qStore, Events: stores.eventStore,
	})
	if err != nil {
		return out, err
	}
	out.questionService = qService
	if stack.askUserTools != nil {
		stack.askUserTools.SetBackend(qService)
	}

	if out.chatCfg.MemoryEnabled() {
		memoryStore, err := memory.NewStore(stores.database.SQL())
		if err != nil {
			return out, err
		}
		memoryManager, err := memory.New(memory.Config{
			Store: memoryStore, MaxInjectRunes: out.chatCfg.MemoryMaxInjectRunes(),
			DefaultTTL: out.chatCfg.MemoryDefaultTTL(),
		})
		if err != nil {
			return out, err
		}
		if err := memoryManager.Initialize(context.Background()); err != nil {
			return out, fmt.Errorf("memory initialize: %w", err)
		}
		out.memoryManager = memoryManager
		stack.memTools.SetBackend(memoryManager)
	}

	todoStore, err := sessiontodo.NewStore(stores.database.SQL())
	if err != nil {
		return out, err
	}
	if stack.todoTools != nil {
		stack.todoTools.SetBackend(todoStore)
	}

	editStore, err := editrev.NewStore(stores.database.SQL(), stores.artifactStore)
	if err != nil {
		return out, err
	}
	stack.broker.SetEditCheckpointer(editStore)

	var configuredRoles []string
	if _, roles, roleMapErr := providerconfig.LoadModelRoles(layout.ConfigDir); roleMapErr == nil {
		for role := range roles {
			configuredRoles = append(configuredRoles, role)
		}
	}
	if err := registerAuxMedia(stack, layout.ConfigDir, configuredRoles); err != nil {
		return out, err
	}
	if providerRuntime != nil && providerRuntime.Provider() != nil && strings.TrimSpace(providerRuntime.LoadError()) == "" {
		roleEndpoints, roleErr := providerruntime.BuildRoleEndpoints(layout.ConfigDir, providerRuntime.SelectedRef())
		if roleErr != nil {
			return out, roleErr
		}
		agentRunner, err := agent.New(agent.Config{
			Provider: providerRuntime.Provider(), Broker: stack.broker, Records: recordStore,
			Model: providerRuntime.Model(), Stream: out.modelHub,
			MaxIterations: maxIterations,
			ContextWindow: out.contextWindow, Roles: roleEndpoints,
			Context: contextStore, Calibrator: out.calibrator, Transcript: out.memoryManager,
		})
		if err != nil {
			return out, err
		}
		out.agentRunner = agentRunner
		stack.taskTool.SetRunner(agentRunner)
		stack.taskTool.SetConfiguredRoles(configuredRoles)
	}

	if out.agentRunner != nil {
		chatRoots := out.chatCfg.PathCeilingRoots(workingDirectory)
		if len(chatRoots) == 0 {
			chatRoots = []string{workingDirectory}
		}
		writeCeiling := out.chatCfg.AgentWriteCeiling()
		allowGit := out.chatCfg.AgentGitEnabled()
		allowProcess := out.chatCfg.AgentProcessEnabled()
		chatCfgCopy := out.chatCfg
		modelResolver := modelresolve.New(layout.ConfigDir)
		chatService, err := chatsession.New(chatsession.Config{
			DB: stores.database.SQL(), Repository: stores.kernelRepository, Approvals: stores.approvalRepository,
			Agent: out.agentRunner, Transcript: queries, WorkspaceRoots: chatRoots,
			PathGuard: stack.pathGuard, DaemonCWD: workingDirectory, ConfigDir: layout.ConfigDir, ChatConfig: &chatCfgCopy,
			AllowWriteCeiling: &writeCeiling, AllowGit: allowGit, AllowProcess: allowProcess,
			ExtraTools:      stack.mcpToolNames,
			ConfiguredRoles: configuredRoles,
			ContextWindow:   out.contextWindow, MaxOutputTokens: out.maxOutputTokens,
			Context: contextStore, Compactor: out.agentRunner,
			MemoryCurator: out.agentRunner, CompactionEnabled: &compactionEnabled, Calibrator: out.calibrator,
			MainModel: providerRuntime.Model(),
			Memory:    out.memoryManager, Todos: todoStore, Edits: editStore, Stream: out.modelHub, ToolCalls: stack.broker,
			ModelResolver: modelResolver.AsChatResolver(),
			OnError: func(err error) {
				slog.Error("chat session failure", "component", "chatsession", "operation", "execute", "result", "failed", "error", err)
			},
		})
		if err != nil {
			return out, err
		}
		out.chatService = chatService
		permService.SetExpand(chatService.ExpandSessionRoot)
		slog.Info("chat workspace configured", "component", "daemon", "operation", "chat_config", "result", "succeeded",
			"ceiling_roots", chatRoots, "allow_all", out.chatCfg.WorkspaceAllowAll(),
			"agent_write_ceiling", writeCeiling, "agent_git", allowGit, "agent_process", allowProcess,
			"context_window", out.contextWindow, "max_iterations", maxIterations, "compaction_enabled", compactionEnabled)
	}
	return out, nil
}

type visionToolAdapter struct {
	inner providers.VisionBackend
}

func (a visionToolAdapter) Complete(ctx context.Context, prompt string, images []tools.ImageBytes) (string, error) {
	parts := make([]providerapi.ImagePart, 0, len(images))
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		parts = append(parts, providerapi.ImagePart{MIME: img.MIME, Base64: encodeStdBase64(img.Data)})
	}
	return a.inner.Complete(ctx, prompt, parts)
}

func registerAuxMedia(stack toolStack, configDir string, configuredRoles []string) error {
	hasVision, hasSpeech := false, false
	for _, role := range configuredRoles {
		switch role {
		case providerconfig.RoleVision:
			hasVision = true
		case providerconfig.RoleSpeech:
			hasSpeech = true
		}
	}
	if !hasVision && !hasSpeech {
		return nil
	}
	_, roles, err := providerconfig.LoadModelRoles(configDir)
	if err != nil {
		return err
	}
	var vision tools.VisionCompleter
	var speech tools.AudioTranscriber
	if hasVision {
		ref := strings.TrimSpace(roles[providerconfig.RoleVision])
		if ref == "" {
			return fmt.Errorf("models.vision is configured but empty")
		}
		resolved, err := providerconfig.ResolveModel(configDir, ref)
		if err != nil {
			return fmt.Errorf("resolve models.vision: %w", err)
		}
		provider, err := providers.NewConfigured(*resolved)
		if err != nil {
			return fmt.Errorf("configure models.vision: %w", err)
		}
		vision = visionToolAdapter{inner: providers.VisionBackend{Provider: provider, Model: resolved.ModelID}}
	}
	if hasSpeech {
		ref := strings.TrimSpace(roles[providerconfig.RoleSpeech])
		if ref == "" {
			return fmt.Errorf("models.speech is configured but empty")
		}
		resolved, err := providerconfig.ResolveModel(configDir, ref)
		if err != nil {
			return fmt.Errorf("resolve models.speech: %w", err)
		}
		backend, err := providers.NewWhisperBackend(*resolved)
		if err != nil {
			return fmt.Errorf("configure models.speech: %w", err)
		}
		speech = backend
	}
	return tools.RegisterMediaTools(stack.broker, stack.pathGuard, vision, speech)
}

func encodeStdBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
