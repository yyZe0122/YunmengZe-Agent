package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

const maxStdioMessageBytes = 8 << 20

// ServerConfig launches one MCP server over stdio.
type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Dir     string
}

// Client is one connected MCP server process (stdio).
type Client struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
	nextID atomic.Int64
	closed atomic.Bool
}

// Start launches the server and completes initialize + initialized.
func Start(ctx context.Context, config ServerConfig) (*Client, error) {
	if strings.TrimSpace(config.Command) == "" {
		return nil, errors.New("mcp command is required")
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return nil, errors.New("mcp server name is required")
	}
	cmd := exec.CommandContext(ctx, config.Command, config.Args...)
	cmd.Dir = config.Dir
	prepareCommand(cmd)
	if len(config.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range config.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// Protocol output is stdout. Do not let arbitrary server diagnostics write
	// terminal control sequences into the interactive UI.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start mcp %s: %w", name, err)
	}
	messages := bufio.NewScanner(stdout)
	messages.Buffer(make([]byte, 4096), maxStdioMessageBytes+2)
	c := &Client{name: name, cmd: cmd, stdin: stdin, stdout: messages}
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Close terminates the process.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Cancel()
	}
	return c.cmd.Wait()
}

// ListTools calls tools/list.
func (c *Client) ListTools(ctx context.Context) ([]ToolDesc, error) {
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return parseToolList(result), nil
}

// CallTool invokes tools/call and returns a JSON-friendly result payload.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var args any
	if err := json.Unmarshal(arguments, &args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	var result map[string]any
	if err := c.call(ctx, "tools/call", map[string]any{
		"name": name, "arguments": args,
	}, &result); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (c *Client) initialize(ctx context.Context) error {
	var result map[string]any
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "yunmengze", "version": "0"},
	}, &result); err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.writeMessage(req); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := c.readMessage()
		if err != nil {
			return err
		}
		rawID, hasID := msg["id"]
		if !hasID {
			continue
		}
		if !idEqual(rawID, id) {
			continue
		}
		if errObj, ok := msg["error"]; ok && errObj != nil {
			return fmt.Errorf("mcp %s %s: %v", c.name, method, errObj)
		}
		if result == nil {
			return nil
		}
		payload, err := json.Marshal(msg["result"])
		if err != nil {
			return err
		}
		if string(payload) == "null" {
			return nil
		}
		return json.Unmarshal(payload, result)
	}
}

func (c *Client) notify(_ context.Context, method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	}
	return c.writeMessage(req)
}

func (c *Client) writeMessage(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(body) > maxStdioMessageBytes {
		return errors.New("mcp message exceeds 8 MiB")
	}
	_, err = c.stdin.Write(append(body, '\n'))
	return err
}

func (c *Client) readMessage() (map[string]any, error) {
	if !c.stdout.Scan() {
		if err := c.stdout.Err(); err != nil {
			return nil, fmt.Errorf("read mcp message (maximum 8 MiB): %w", err)
		}
		return nil, io.EOF
	}
	body := c.stdout.Bytes()
	if len(body) > maxStdioMessageBytes {
		return nil, errors.New("mcp message exceeds 8 MiB")
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, errors.New("mcp message must be a JSON object")
	}
	return msg, nil
}
