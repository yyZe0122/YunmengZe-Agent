package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type limitedOutput struct {
	mu           sync.Mutex
	head, tail   []byte
	total        int
	progress     func(string)
	lastProgress time.Time
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.total += n
	if len(w.head) < 24000 {
		keep := min(24000-len(w.head), len(p))
		w.head = append(w.head, p[:keep]...)
		p = p[keep:]
	}
	if len(p) > 0 {
		if len(p) > 24000 {
			p = p[len(p)-24000:]
		}
		w.tail = append(w.tail, p...)
		if len(w.tail) > 24000 {
			w.tail = w.tail[len(w.tail)-24000:]
		}
	}
	if w.progress != nil && time.Since(w.lastProgress) >= 250*time.Millisecond {
		text := w.text()
		w.progress(BoundText(text, 2000))
		w.lastProgress = time.Now()
	}
	return n, nil
}
func (w *limitedOutput) text() string {
	marker := ""
	if w.total > 48000 {
		marker = "\n...[middle output omitted]...\n"
	}
	return strings.ToValidUTF8(string(w.head)+marker+string(w.tail), "�")
}
func runShell(ctx context.Context, e Env, raw json.RawMessage) (any, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := Decode(raw, &a); err != nil {
		return nil, err
	}
	if a.Command == "" {
		return nil, errors.New("command is required")
	}
	if a.Timeout <= 0 {
		a.Timeout = 120
	}
	if a.Timeout > 600 {
		a.Timeout = 600
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		shell, err := exec.LookPath("pwsh")
		if err != nil {
			shell = "powershell.exe"
		}
		cmd = exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}
	cmd.Dir = e.Workspace
	prepareCommand(cmd)
	output := limitedOutput{progress: e.Progress}
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if ctx.Err() == nil {
			return nil, err
		} else {
			code = -1
		}
	}
	result := map[string]any{"output": output.text(), "exit_code": code, "truncated": output.total > 48000}
	if ctx.Err() != nil {
		result["error"] = ctx.Err().Error()
	}
	return result, nil
}
