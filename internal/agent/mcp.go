package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/mcp"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

type mcpConnection struct {
	session mcp.Session
	cancel  context.CancelFunc
}

func (e *Engine) mcpClient(ctx context.Context, id, name string, server providerconfig.MCPServer, workspace string) (mcp.Session, error) {
	e.mcpMu.Lock()
	key := id + "/" + name
	client, ok := e.mcpClients[key]
	e.mcpMu.Unlock()
	if ok {
		return client.session, nil
	}
	kind, err := server.ResolvedType()
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(e.life)
	stop := context.AfterFunc(ctx, cancel)
	timer := time.AfterFunc(15*time.Second, cancel)
	var connected mcp.Session
	if kind == providerconfig.MCPTypeStdio {
		connected, err = mcp.Start(life, mcp.ServerConfig{Name: name, Command: server.Command, Args: server.Args, Env: server.Env, Dir: workspace})
	} else {
		connected, err = mcp.Dial(life, mcp.RemoteConfig{Name: name, URL: server.URL, Headers: server.Headers, Mode: kind})
	}
	timer.Stop()
	stop()
	if err != nil {
		cancel()
		return nil, err
	}
	if life.Err() != nil {
		cancel()
		_ = connected.Close()
		return nil, life.Err()
	}
	// The session lease serializes calls for this key; other sessions may connect independently.
	e.mcpMu.Lock()
	e.mcpClients[key] = mcpConnection{session: connected, cancel: cancel}
	e.mcpMu.Unlock()
	return connected, nil
}
func (e *Engine) dropMCP(id, name string) {
	e.mcpMu.Lock()
	key := id + "/" + name
	client, ok := e.mcpClients[key]
	delete(e.mcpClients, key)
	e.mcpMu.Unlock()
	if ok {
		client.cancel()
		_ = client.session.Close()
	}
}

func (e *Engine) registerMCP(r *tools.Registry) {
	if !e.mcp.IsEnabled() {
		return
	}
	for name, server := range e.mcp.Servers {
		r.Register(tools.Tool{Name: "mcp_" + name, Description: "MCP server " + name + ". Omit tool to list its tools and schemas; otherwise call tool with arguments. Available only in agent mode.", Schema: tools.Schema(`{"tool":{"type":"string"},"arguments":{"type":"object"}}`), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
			var a struct {
				Tool      string          `json:"tool"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := tools.Decode(raw, &a); err != nil {
				return nil, err
			}
			client, err := e.mcpClient(ctx, env.SessionID, name, server, env.Workspace)
			if err != nil {
				return nil, err
			}

			if a.Tool == "" {
				result, err := client.ListTools(ctx)
				if err != nil {
					e.dropMCP(env.SessionID, name)
				}
				return result, err
			}
			if len(a.Arguments) == 0 {
				a.Arguments = json.RawMessage(`{}`)
			}
			result, err := client.CallTool(ctx, a.Tool, a.Arguments)
			if err != nil {
				e.dropMCP(env.SessionID, name)
			}
			return result, err
		}})
	}
}
