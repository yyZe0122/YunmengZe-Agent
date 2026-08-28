package tools

import (
	"database/sql"
	"errors"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/tools/internal/executor"
)

// ExecutorConfig controls the broker-owned process runner. The concrete runner
// stays in tools/internal/executor (unimportable outside tools).
type ExecutorConfig struct {
	MaxOutputBytes int
	AllowedEnv     []string
	UID            *uint32
	GID            *uint32
	Isolation      executor.IsolationConfig
}

// IsolationStatus is the effective process isolation baseline after runner probe.
type IsolationStatus = executor.IsolationStatus

// RegisterBuiltins registers FS/git/process/http tools under a shared PathGuard.
// Returns the guard so the daemon can AddRoot for session workspaces (ADR-046).
func RegisterBuiltins(broker *Broker, roots []string, config ExecutorConfig) (*PathGuard, error) {
	return RegisterBuiltinsWithOptions(broker, roots, false, config)
}

// RegisterBuiltinsWithOptions is RegisterBuiltins with allow_all support.
func RegisterBuiltinsWithOptions(broker *Broker, roots []string, allowAll bool, config ExecutorConfig) (*PathGuard, error) {
	if broker == nil {
		return nil, errors.New("tool broker is required")
	}
	runner, err := executor.NewRunner(executor.Config{
		MaxOutputBytes: config.MaxOutputBytes,
		AllowedEnv:     config.AllowedEnv,
		UID:            config.UID,
		GID:            config.GID,
		Isolation:      config.Isolation,
	})
	if err != nil {
		return nil, err
	}
	broker.recordIsolationProbe(runner.IsolationStatus())

	var guard *PathGuard
	if allowAll {
		guard = NewPathGuardAllowAll()
	} else {
		guard, err = NewPathGuard(roots)
		if err != nil {
			return nil, err
		}
	}
	fileTools := newFileTools(guard)
	process, err := newProcessTool(guard, runner)
	if err != nil {
		return nil, err
	}
	shell, err := newProcessShellTool(guard, runner)
	if err != nil {
		return nil, err
	}
	gitTools, err := newGitTools(guard, runner)
	if err != nil {
		return nil, err
	}
	all := append(fileTools, process, shell)
	all = append(all, gitTools...)
	all = append(all, newHTTPGetTool(16*1024*1024))
	for _, tool := range all {
		if err := broker.Register(tool); err != nil {
			return nil, err
		}
	}
	return guard, nil
}

// RegisterWebTools registers web_search and web_extract (Phase W). chat.web selects the search backend.
func RegisterWebTools(broker *Broker, chat providerconfig.ChatConfig) error {
	if broker == nil {
		return errors.New("tool broker is required")
	}
	cfg, err := newWebSearchConfig(chat)
	if err != nil {
		return err
	}
	for _, tool := range []Tool{newWebSearchTool(cfg), newWebExtractTool(16 * 1024 * 1024)} {
		if err := broker.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// RegisterTaskTool registers the ADR-039 task tool. Runner may be set later via SetRunner.
func RegisterTaskTool(broker *Broker, db *sql.DB, runner SubagentRunner) (*taskTool, error) {
	if broker == nil {
		return nil, errors.New("tool broker is required")
	}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: runner})
	if err != nil {
		return nil, err
	}
	if err := broker.Register(tool); err != nil {
		return nil, err
	}
	return tool, nil
}
