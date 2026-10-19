// Package providerconfig loads the selected model and provider from YunmengZe Agent JSON configuration.
package providerconfig

import (
	"slices"
	"strings"
)

const (
	Filename       = "agent.json"
	LocalFilename  = "agent.local.json"
	AgentsFilename = "AGENTS.md"
	MaxAgentsRunes = 8000

	ProtocolOpenAIChat        = "openai-chat"
	ProtocolOpenAIResponses   = "openai-responses"
	ProtocolAnthropicMessages = "anthropic-messages"
	ProtocolGeminiGenerate    = "gemini-generate-content"
)

type File struct {
	Schema string `json:"$schema,omitempty"`
	Model  string `json:"model"`
	// Models maps optional role → provider/model . Unset role falls back to Model.
	// Allowed keys: subagent, compact, web, vision, memory. Unknown keys are rejected.
	Models   map[string]string   `json:"models,omitempty"`
	Provider map[string]Provider `json:"provider"`
	// Chat configures optional features of the local loop.
	Chat *ChatConfig `json:"chat,omitempty"`
	// MCP configures stdio and/or remote MCP servers (optional).
	MCP *MCPConfig `json:"mcp,omitempty"`
}

// Model role names for optional models map .
const (
	RoleMain     = "main"
	RoleSubagent = "subagent"
	RoleCompact  = "compact"
	RoleWeb      = "web"
	RoleVision   = "vision"
	RoleMemory   = "memory"
)

// AllowedModelRoles are keys permitted under top-level "models" (excluding main).
var AllowedModelRoles = map[string]struct{}{
	RoleSubagent: {},
	RoleCompact:  {},
	RoleWeb:      {},
	RoleVision:   {},
	RoleMemory:   {},
}

func allowedModelRoleList() string {
	names := make([]string, 0, len(AllowedModelRoles))
	for role := range AllowedModelRoles {
		names = append(names, role)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// MCPConfig is the optional MCP servers section of agent.json.
type MCPConfig struct {
	Enabled *bool                `json:"enabled,omitempty"`
	Servers map[string]MCPServer `json:"servers,omitempty"`
}

func (c MCPConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// MCP transport types. Empty + command → stdio; empty + url → remote.
const (
	MCPTypeStdio  = "stdio"
	MCPTypeHTTP   = "http"   // Streamable HTTP
	MCPTypeSSE    = "sse"    // legacy HTTP+SSE or Streamable with SSE bodies
	MCPTypeRemote = "remote" // auto: Streamable HTTP, fallback legacy SSE
)

// MCPServer is one MCP server: stdio process and/or remote URL.
type MCPServer struct {
	// Type is stdio | http | sse | remote. Empty infers from command/url.
	Type string `json:"type,omitempty"`
	// Command/Args/Env are for stdio transport.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// URL/Headers are for remote transports. Header values may use {env:}/{file:}.
	// Never expose resolved headers in user-facing status.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Provider struct {
	Protocol string           `json:"protocol,omitempty"`
	Type     string           `json:"type,omitempty"`
	Options  ProviderOptions  `json:"options"`
	Models   map[string]Model `json:"models,omitempty"`
}

type ProviderOptions struct {
	BaseURL          string            `json:"baseURL"`
	APIKey           string            `json:"apiKey,omitempty"`
	CompletionPath   string            `json:"completionPath,omitempty"`
	ResponseFormat   string            `json:"responseFormat,omitempty"`
	AnthropicVersion string            `json:"anthropicVersion,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
}

type Model struct {
	NativeCompaction bool   `json:"nativeCompaction,omitempty"`
	Name             string `json:"name,omitempty"`
	// ID is the wire/API model id sent to the provider (OpenCode model.id).
	// When empty, the catalog key / selection model segment is used as the wire id.
	ID             string   `json:"id,omitempty"`
	ResponseFormat string   `json:"responseFormat,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTokens      int64    `json:"maxTokens,omitempty"`
	// ContextWindow is the model context length in tokens (not maxTokens output cap).
	// Omit or 0 to fill from models.dev or the conservative 32K fallback.
	ContextWindow int64 `json:"contextWindow,omitempty"`
	// Limit is the OpenCode-style alias for context/output; explicit fields win when set.
	Limit           *ModelLimit `json:"limit,omitempty"`
	ReasoningEffort string      `json:"reasoningEffort,omitempty"`
	SupportsImages  *bool       `json:"supportsImages,omitempty"`
}

// ModelLimit is OpenCode provider.models.*.limit.
type ModelLimit struct {
	Context int64 `json:"context,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

type Resolved struct {
	NativeCompaction     bool
	SupportsImages       bool
	ImageCapabilityKnown bool
	Source               string
	// SelectionRef is the full selection string providerID/modelID (modelID may contain '/').
	SelectionRef string
	ProviderID   string
	Protocol     string
	// ModelID is the wire/API model id (OpenCode api.id): models.<key>.id or the model segment.
	ModelID          string
	BaseURL          string
	APIKey           string
	CompletionPath   string
	ResponseFormat   string
	Temperature      *float64
	MaxTokens        int64
	ContextWindow    int64
	ReasoningEffort  string
	AnthropicVersion string
	Headers          map[string]string
}

// ModelRef is a configured model identifier in provider/model form (no secrets).
type ModelRef struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}
