package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/tools/internal/executor"
)

func TestProcessShellAuthorizationAndEcho(t *testing.T) {
	root := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := executor.NewRunner(executor.Config{MaxOutputBytes: 64 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := newProcessShellTool(guard, runner)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Definition().Name != "process_shell" {
		t.Fatalf("name=%s", tool.Definition().Name)
	}
	auth, err := tool.Authorization(context.Background(), mustJSON(t, map[string]any{"command": "echo hi", "directory": root}))
	if err != nil {
		t.Fatal(err)
	}
	if auth.Capability != "process_shell" || auth.Command != "/bin/sh" {
		t.Fatalf("auth=%+v", auth)
	}
	if len(auth.Arguments) != 2 || auth.Arguments[0] != "-c" || auth.Arguments[1] != "echo hi" {
		t.Fatalf("args=%v", auth.Arguments)
	}
}

func TestProcessExecNonZeroExitIsResult(t *testing.T) {
	root := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := executor.NewRunner(executor.Config{MaxOutputBytes: 64 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	tool, err := newProcessTool(guard, runner)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"command": "/bin/sh", "arguments": []string{"-c", "echo fail-out; echo fail-err >&2; exit 7"}, "directory": root,
	}))
	if err != nil {
		t.Fatalf("Execute error = %v, want nil (non-zero exit is a result)", err)
	}
	var payload struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExitCode != 7 {
		t.Fatalf("exit_code = %d, want 7; raw=%s", payload.ExitCode, out)
	}
	if payload.Stdout == "" || payload.Stderr == "" {
		t.Fatalf("want stdout/stderr in result: %s", out)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
