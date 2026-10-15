package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStartListCallFakeServer(t *testing.T) {
	// stdio path (newline-delimited JSON)
	dir := t.TempDir()
	script := filepath.Join(dir, "fake_mcp.py")
	// Minimal MCP stdio server: initialize, tools/list, tools/call echo.
	code := `
import json, sys

def read_msg():
    line = sys.stdin.buffer.readline()
    return json.loads(line) if line else None

def write_msg(obj):
    data = json.dumps(obj).encode("utf-8")
    sys.stdout.buffer.write(data + b"\n")
    sys.stdout.buffer.flush()

while True:
    msg = read_msg()
    if msg is None:
        break
    method = msg.get("method")
    mid = msg.get("id")
    if method == "initialize":
        write_msg({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"0"}}})
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        write_msg({"jsonrpc":"2.0","id":mid,"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]}})
    elif method == "tools/call":
        args = (msg.get("params") or {}).get("arguments") or {}
        write_msg({"jsonrpc":"2.0","id":mid,"result":{"content":[{"type":"text","text":args.get("text","")}],"isError":False}})
    else:
        if mid is not None:
            write_msg({"jsonrpc":"2.0","id":mid,"error":{"code":-32601,"message":"unknown"}})
`
	if err := os.WriteFile(script, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Start(ctx, ServerConfig{
		Name: "fake", Command: "python3", Args: []string{script},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	out, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == "" || !json.Valid(out) {
		t.Fatalf("out = %s", out)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
}
