package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

type taskRunnerSetter interface {
	SetRunner(tools.SubagentRunner)
	SetAgentsOverlay(configDir, workspace string)
	SetConfiguredRoles(roles []string)
}

type memoryBackendSetter interface {
	SetBackend(tools.MemoryBackend)
}

type todoBackendSetter interface {
	SetBackend(tools.TodoBackend)
}

type askUserBackendSetter interface {
	SetBackend(tools.AskUserBackend)
}

type toolStack struct {
	broker       *tools.Broker
	pathGuard    *tools.PathGuard
	taskTool     taskRunnerSetter
	memTools     memoryBackendSetter
	todoTools    todoBackendSetter
	askUserTools askUserBackendSetter
	mcpRegistry  *tools.MCPRegistry
	mcpToolNames []string
	chatCfg      providerconfig.ChatConfig
}

func wireTools(stores coreStores, layout paths.Layout, workingDirectory string) (toolStack, error) {
	var out toolStack
	broker, err := tools.NewBroker(tools.Config{
		DB: stores.database.SQL(), Approvals: stores.approvalRepository,
		Policy: policy.NewEvaluator(policy.DefaultConfig()), Artifacts: stores.artifactStore,
	})
	if err != nil {
		return out, err
	}
	out.broker = broker

	ensureResult, err := providerconfig.EnsureConfig(layout.ConfigDir)
	if err != nil {
		return out, fmt.Errorf("ensure provider config: %w", err)
	}
	switch {
	case ensureResult.Created:
		slog.Info("provider config template created", "component", "daemon", "operation", "ensure_config", "result", "succeeded",
			"config_path", ensureResult.Path)
	default:
		slog.Info("provider config ready", "component", "daemon", "operation", "ensure_config", "result", "succeeded",
			"config_path", ensureResult.Path)
	}

	chatCfg, err := providerconfig.LoadChat(layout.ConfigDir)
	if err != nil {
		return out, fmt.Errorf("load chat config: %w", err)
	}
	out.chatCfg = chatCfg
	pathRoots := chatCfg.PathCeilingRoots(workingDirectory)
	if len(pathRoots) == 0 && !chatCfg.WorkspaceAllowAll() {
		pathRoots = []string{workingDirectory}
	}
	pathGuard, err := tools.RegisterBuiltinsWithOptions(broker, pathRoots, chatCfg.WorkspaceAllowAll(), tools.ExecutorConfig{
		MaxOutputBytes: 4 << 20,
		AllowedEnv:     []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE"},
	})
	if err != nil {
		return out, err
	}
	out.pathGuard = pathGuard
	if err := tools.RegisterWebTools(broker, chatCfg); err != nil {
		return out, err
	}
	if chatCfg.WorkspaceAllowAll() {
		slog.Warn("chat.workspace.allow_all enabled: path containment disabled",
			"component", "daemon", "operation", "path_guard", "result", "warning")
	}

	taskTool, err := tools.RegisterTaskTool(broker, stores.database.SQL(), nil)
	if err != nil {
		return out, err
	}
	out.taskTool = taskTool

	memTools, err := tools.RegisterMemoryTools(broker, nil)
	if err != nil {
		return out, err
	}
	out.memTools = memTools

	todoTools, err := tools.RegisterTodoTools(broker, nil)
	if err != nil {
		return out, err
	}
	out.todoTools = todoTools

	askUserTools, err := tools.RegisterAskUserTool(broker, nil)
	if err != nil {
		return out, err
	}
	out.askUserTools = askUserTools

	mcpConfig, err := providerconfig.LoadMCP(layout.ConfigDir)
	if err != nil {
		return out, fmt.Errorf("load mcp config: %w", err)
	}
	mcpRegistry, mcpToolNames, err := tools.RegisterMCP(context.Background(), broker, mcpConfig)
	if err != nil {
		return out, err
	}
	out.mcpRegistry = mcpRegistry
	out.mcpToolNames = mcpToolNames
	if len(mcpToolNames) > 0 {
		slog.Info("mcp tools ready", "component", "daemon", "operation", "mcp_register", "result", "succeeded",
			"tools", len(mcpToolNames))
	}
	return out, nil
}
