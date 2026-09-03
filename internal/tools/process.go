package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/tools/internal/executor"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

type processTool struct {
	guard  *PathGuard
	runner *executor.Runner
}

type processInput struct {
	Command     string            `json:"command"`
	Arguments   []string          `json:"arguments,omitempty"`
	Directory   string            `json:"directory"`
	Environment map[string]string `json:"environment,omitempty"`
}

func newProcessTool(guard *PathGuard, runner *executor.Runner) (Tool, error) {
	if runner == nil {
		return nil, errors.New("process runner is required")
	}
	if guard == nil {
		return nil, errors.New("path guard is required")
	}
	return &processTool{guard: guard, runner: runner}, nil
}

func newProcessShellTool(guard *PathGuard, runner *executor.Runner) (Tool, error) {
	if runner == nil {
		return nil, errors.New("process runner is required")
	}
	if guard == nil {
		return nil, errors.New("path guard is required")
	}
	return &processShellTool{guard: guard, runner: runner}, nil
}

type processShellTool struct {
	guard  *PathGuard
	runner *executor.Runner
}

type processShellInput struct {
	Command     string            `json:"command"`
	Directory   string            `json:"directory"`
	Environment map[string]string `json:"environment,omitempty"`
}

const processShellBin = "/bin/sh"

func (t *processShellTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "process_shell", Description: "Run tests or an approved /bin/sh -c script (not for find/grep). Interactive TUI: call this tool; the user approves via /perm (once/similar/permanent/deny). Do not tell the user to edit agent.json. Prefer process_exec argv when you do not need a shell. Non-zero exit is a result (exit_code/stdout/stderr), not a crash — read it and fix. Plan and cron never receive this grant.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 30000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["command","directory"],"properties":{"command":{"type":"string","description":"Script passed to /bin/sh -c"},"directory":{"type":"string"},"environment":{"type":"object","additionalProperties":{"type":"string"}}}}`),
	}
}

func (t *processShellTool) Authorization(ctx context.Context, raw json.RawMessage) (Authorization, error) {
	var input processShellInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	script := strings.TrimSpace(input.Command)
	if script == "" {
		return Authorization{}, errors.New("process shell command is required")
	}
	directory, extra, err := t.guard.ResolveForAuth(ctx, input.Directory)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{
		Capability: "process_shell", Path: directory, ExtraRoot: extra,
		Command: processShellBin, Arguments: []string{"-c", script},
	}, nil
}

func (t *processShellTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input processShellInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	script := strings.TrimSpace(input.Command)
	if script == "" {
		return nil, errors.New("process shell command is required")
	}
	directory, err := t.guard.ResolveContext(ctx, input.Directory)
	if err != nil {
		return nil, err
	}
	result, runErr := t.runner.Run(ctx, executor.Request{
		Command: processShellBin, Arguments: []string{"-c", script},
		Directory: directory, Environment: input.Environment,
		CallID: toolCallIDFromContext(ctx),
	})
	return encodeProcessResult(result, runErr)
}

func (t *processTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "process_exec", Description: "Run tests or an approved command as argv (not a shell). Not for find/grep (use fs_glob/fs_grep). Do not reimplement a configured mcp_* tool. Interactive TUI: call this tool; the user approves via /perm. Do not tell the user to edit agent.json. directory should be absolute. Non-zero exit is a result (exit_code/stdout/stderr), not a crash — read it and fix.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 30000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["command","directory"],"properties":{"command":{"type":"string"},"arguments":{"type":"array","items":{"type":"string"}},"directory":{"type":"string"},"environment":{"type":"object","additionalProperties":{"type":"string"}}}}`),
	}
}

func (t *processTool) Authorization(ctx context.Context, raw json.RawMessage) (Authorization, error) {
	var input processInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return Authorization{}, errors.New("process command is required")
	}
	directory, extra, err := t.guard.ResolveForAuth(ctx, input.Directory)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: "process_exec", Path: directory, ExtraRoot: extra, Command: input.Command, Arguments: append([]string(nil), input.Arguments...)}, nil
}

func (t *processTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input processInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" {
		return nil, errors.New("process command is required")
	}
	directory, err := t.guard.ResolveContext(ctx, input.Directory)
	if err != nil {
		return nil, err
	}
	result, runErr := t.runner.Run(ctx, executor.Request{
		Command: input.Command, Arguments: input.Arguments, Directory: directory, Environment: input.Environment,
		CallID: toolCallIDFromContext(ctx),
	})
	return encodeProcessResult(result, runErr)
}
