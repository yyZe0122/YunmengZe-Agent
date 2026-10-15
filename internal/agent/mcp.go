package agent

import (
	"context"
	"github.com/yyZe0122/yunmengze-agent/internal/mcp"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"time"
)

type mcpConnection struct {
	session mcp.Session
	cancel  context.CancelFunc
}

func (e *Engine) mcpClient(ctx context.Context, id, name string, server providerconfig.MCPServer, workspace string) (mcp.Session, error) {
	e.mcpMu.Lock()
	defer e.mcpMu.Unlock()
	key := id + "/" + name
	if client, ok := e.mcpClients[key]; ok {
		return client.session, nil
	}
	kind, err := server.ResolvedType()
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(e.life)
	stop := context.AfterFunc(ctx, cancel)
	timer := time.AfterFunc(15*time.Second, cancel)
	var client mcp.Session
	if kind == providerconfig.MCPTypeStdio {
		client, err = mcp.Start(life, mcp.ServerConfig{Name: name, Command: server.Command, Args: server.Args, Env: server.Env, Dir: workspace})
	} else {
		client, err = mcp.Dial(life, mcp.RemoteConfig{Name: name, URL: server.URL, Headers: server.Headers, Mode: kind})
	}
	timer.Stop()
	stop()
	if err != nil {
		cancel()
		return nil, err
	}
	if life.Err() != nil {
		cancel()
		_ = client.Close()
		return nil, life.Err()
	}
	e.mcpClients[key] = mcpConnection{session: client, cancel: cancel}
	return client, nil
}
func (e *Engine) dropMCP(id, name string) {
	e.mcpMu.Lock()
	defer e.mcpMu.Unlock()
	key := id + "/" + name
	if client, ok := e.mcpClients[key]; ok {
		delete(e.mcpClients, key)
		client.cancel()
		_ = client.session.Close()
	}
}
