package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/mcp"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

var nonNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// MCPServerStatus is one configured MCP server (no secrets / no URL / no headers).
type MCPServerStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport,omitempty"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

// MCPStatusSnapshot is a read-only MCP runtime summary for Gateway/TUI.
type MCPStatusSnapshot struct {
	Enabled bool              `json:"enabled"`
	Total   int               `json:"total"`
	OK      int               `json:"ok"`
	Error   int               `json:"error"`
	Tools   int               `json:"tools"`
	Servers []MCPServerStatus `json:"servers,omitempty"`
}

// MCPRegistry holds live MCP sessions for daemon shutdown and status.
type MCPRegistry struct {
	mu       sync.Mutex
	sessions []mcp.Session
	names    []string
	servers  []MCPServerStatus
}

// ToolNames returns registered mcp_* tool names.
func (r *MCPRegistry) ToolNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// Status returns a snapshot (Enabled false when nothing configured/registered).
func (r *MCPRegistry) Status() MCPStatusSnapshot {
	if r == nil {
		return MCPStatusSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.servers) == 0 && len(r.names) == 0 {
		return MCPStatusSnapshot{}
	}
	ok, errN := 0, 0
	for _, s := range r.servers {
		if s.Connected {
			ok++
		} else {
			errN++
		}
	}
	return MCPStatusSnapshot{
		Enabled: true,
		Total:   len(r.servers),
		OK:      ok,
		Error:   errN,
		Tools:   len(r.names),
		Servers: append([]MCPServerStatus(nil), r.servers...),
	}
}

// Close stops all MCP sessions.
func (r *MCPRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		if err := s.Close(); err != nil {
			slog.Warn("mcp server close", "component", "mcp", "error", err)
		}
	}
	r.sessions = nil
}

type mcpTool struct {
	session     mcp.Session
	serverName  string
	remoteName  string
	localName   string
	description string
	schema      json.RawMessage
}

func (t *mcpTool) Definition() toolapi.Definition {
	desc := strings.TrimSpace(t.description)
	if desc == "" {
		desc = "MCP tool " + t.remoteName + " from server " + t.serverName
	}
	desc = "Configured MCP tool — prefer over process_exec or custom scripts. " + desc
	schema := t.schema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	// Ensure object schema for broker validation.
	if err := validateInputSchema(schema); err != nil {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return toolapi.Definition{
		Name: t.localName, Description: desc,
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 60000,
		InputSchema: schema,
	}
}

func (t *mcpTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: t.localName}, nil
}

func (t *mcpTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return t.session.CallTool(ctx, t.remoteName, raw)
}

// RegisterMCP starts configured MCP servers, lists tools, and registers them on the broker.
// Failures for individual servers are logged; successful servers still register.
// Returns registry for shutdown and the list of local tool names.
func RegisterMCP(ctx context.Context, broker *Broker, config providerconfig.MCPConfig) (*MCPRegistry, []string, error) {
	if broker == nil {
		return nil, nil, fmt.Errorf("tool broker is required")
	}
	reg := &MCPRegistry{}
	if len(config.Servers) == 0 {
		return reg, nil, nil
	}
	var names []string
	serverNames := make([]string, 0, len(config.Servers))
	for serverName := range config.Servers {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)
	for _, serverName := range serverNames {
		server := config.Servers[serverName]
		serverName = strings.TrimSpace(serverName)
		session, transport, err := openMCPSession(ctx, serverName, server)
		if err != nil {
			slog.Error("mcp server start failed", "component", "mcp", "server", serverName, "transport", transport, "error", err)
			reg.mu.Lock()
			reg.servers = append(reg.servers, MCPServerStatus{
				Name: serverName, Transport: transport, Connected: false, Error: err.Error(),
			})
			reg.mu.Unlock()
			continue
		}
		tools, err := session.ListTools(ctx)
		if err != nil {
			slog.Error("mcp tools/list failed", "component", "mcp", "server", serverName, "transport", session.Transport(), "error", err)
			_ = session.Close()
			reg.mu.Lock()
			reg.servers = append(reg.servers, MCPServerStatus{
				Name: serverName, Transport: session.Transport(), Connected: false, Error: err.Error(),
			})
			reg.mu.Unlock()
			continue
		}
		toolCount := 0
		reg.mu.Lock()
		reg.sessions = append(reg.sessions, session)
		reg.mu.Unlock()
		for _, desc := range tools {
			local := mcpLocalName(serverName, desc.Name)
			if !toolapi.ValidName(local) {
				slog.Warn("mcp tool name skipped", "component", "mcp", "server", serverName, "tool", desc.Name, "local", local)
				continue
			}
			tool := &mcpTool{
				session: session, serverName: serverName, remoteName: desc.Name,
				localName: local, description: desc.Description, schema: desc.InputSchema,
			}
			if err := broker.Register(tool); err != nil {
				slog.Error("mcp tool register failed", "component", "mcp", "tool", local, "error", err)
				continue
			}
			names = append(names, local)
			toolCount++
			reg.mu.Lock()
			reg.names = append(reg.names, local)
			reg.mu.Unlock()
			slog.Info("mcp tool registered", "component", "mcp", "server", serverName, "transport", session.Transport(), "tool", local)
		}
		reg.mu.Lock()
		reg.servers = append(reg.servers, MCPServerStatus{
			Name: serverName, Transport: session.Transport(), Connected: true, ToolCount: toolCount,
		})
		reg.mu.Unlock()
	}
	return reg, names, nil
}

func openMCPSession(ctx context.Context, serverName string, server providerconfig.MCPServer) (mcp.Session, string, error) {
	typ, err := server.ResolvedType()
	if err != nil {
		return nil, "", err
	}
	switch typ {
	case providerconfig.MCPTypeStdio:
		client, err := mcp.Start(ctx, mcp.ServerConfig{
			Name: serverName, Command: server.Command, Args: server.Args, Env: server.Env,
		})
		if err != nil {
			return nil, "stdio", err
		}
		return client, "stdio", nil
	case providerconfig.MCPTypeHTTP, providerconfig.MCPTypeSSE, providerconfig.MCPTypeRemote:
		mode := typ
		if typ == providerconfig.MCPTypeRemote {
			mode = "remote"
		}
		client, err := mcp.Dial(ctx, mcp.RemoteConfig{
			Name: serverName, URL: server.URL, Headers: server.Headers, Mode: mode,
		})
		if err != nil {
			return nil, typ, err
		}
		return client, client.Transport(), nil
	default:
		return nil, typ, fmt.Errorf("unsupported mcp type %q", typ)
	}
}

func mcpLocalName(server, remote string) string {
	server = nonNameChars.ReplaceAllString(server, "_")
	remote = nonNameChars.ReplaceAllString(remote, "_")
	server = strings.Trim(server, "_")
	remote = strings.Trim(remote, "_")
	if server == "" {
		server = "server"
	}
	if remote == "" {
		remote = "tool"
	}
	return "mcp_" + server + "_" + remote
}
