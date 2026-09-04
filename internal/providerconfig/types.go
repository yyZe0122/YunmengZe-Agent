// Package providerconfig loads the selected model and provider from YunmengZe Agent JSON configuration.
package providerconfig

import (
	"slices"
	"strings"
	"time"
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
	// Models maps optional role → provider/model (ADR-045). Unset role falls back to Model.
	// Allowed keys: subagent, compact, web, vision, speech. Unknown keys are rejected.
	Models   map[string]string   `json:"models,omitempty"`
	Provider map[string]Provider `json:"provider"`
	// Chat configures agent-mode session chat workspace grants (optional).
	Chat *ChatConfig `json:"chat,omitempty"`
	// MCP configures stdio and/or remote MCP servers (optional; ADR-040).
	MCP *MCPConfig `json:"mcp,omitempty"`
}

// Model role names for optional models map (ADR-045).
const (
	RoleMain     = "main"
	RoleSubagent = "subagent"
	RoleCompact  = "compact"
	RoleWeb      = "web"
	RoleVision   = "vision"
	RoleSpeech   = "speech"
)

// AllowedModelRoles are keys permitted under top-level "models" (excluding main).
var AllowedModelRoles = map[string]struct{}{
	RoleSubagent: {},
	RoleCompact:  {},
	RoleWeb:      {},
	RoleVision:   {},
	RoleSpeech:   {},
}

// AllowedModelRoleList is a sorted comma-separated list of AllowedModelRoles keys.
func AllowedModelRoleList() string {
	return allowedModelRoleList()
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
	Servers map[string]MCPServer `json:"servers,omitempty"`
}

// MCP transport types (ADR-040 / O2). Empty + command → stdio; empty + url → remote.
const (
	MCPTypeStdio  = "stdio"
	MCPTypeHTTP   = "http"   // Streamable HTTP
	MCPTypeSSE    = "sse"    // legacy HTTP+SSE or Streamable with SSE bodies
	MCPTypeRemote = "remote" // auto: Streamable HTTP, fallback legacy SSE
)

// MCPServer is one MCP server: stdio process and/or remote URL (ADR-040 O2).
type MCPServer struct {
	// Type is stdio | http | sse | remote. Empty infers from command/url.
	Type string `json:"type,omitempty"`
	// Command/Args/Env are for stdio transport.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// URL/Headers are for remote transports. Header values may use {env:}/{file:}.
	// Never expose URL/headers on Gateway status.
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Permission modes accepted on chat.permission.mode (load-only; ignored at runtime).
const (
	PermissionModePreauth = "preauth"
	PermissionModeAsk     = "ask"
)

// ChatConfig is the optional session chat workspace section of agent.json.
// Tab agent (build) vs plan (read-only) selects tools; this block sets
// workspace ceiling, agent write ceiling, and optional packing/loop limits (ADR-041).
type ChatConfig struct {
	// Workspace controls default session root and path ceiling (ADR-046).
	Workspace *ChatWorkspaceConfig `json:"workspace,omitempty"`
	// AllowWrite is a ceiling for agent (build) mode write tools.
	// nil / omitted → true (agent may write). false → agent is also read-only.
	// Plan mode is always read-only regardless of this field.
	AllowWrite *bool `json:"allow_write,omitempty"`
	// Tools opt-in high-risk agent tools (default all off). Plan mode never receives these.
	Tools *ChatToolsConfig `json:"tools,omitempty"`
	// Compaction controls session head summarization (ADR-041). Omit → enabled.
	Compaction *ChatCompactionConfig `json:"compaction,omitempty"`
	// MaxIterations caps agent tool-loop steps per turn (1–256). Omit / 0 → no hard cap (ADR-052).
	MaxIterations int `json:"max_iterations,omitempty"`
	// Permission remembers high-risk families (allow). Mode is load-only compatibility.
	Permission *ChatPermissionConfig `json:"permission,omitempty"`
	// Memory controls in-process layered memory (ADR-044). Omit → enabled defaults.
	Memory *ChatMemoryConfig `json:"memory,omitempty"`
	// Skills is optional unused-ttl / archive (ADR-050 H5-skill).
	Skills *ChatSkillsConfig `json:"skills,omitempty"`
	// Commands are user slash templates (O3). Instruction text only — no grants.
	// Key is slash name without leading slash. Builtin TUI names are rejected.
	Commands map[string]ChatCommandConfig `json:"commands,omitempty"`
	// Web selects the search backend for web_search (Phase W). Omit → ddg.
	Web *ChatWebConfig `json:"web,omitempty"`
}

const (
	WebSearchDDG     = "ddg"
	WebSearchSearxNG = "searxng"
	WebSearchTavily  = "tavily"
)

// ChatWebConfig is optional chat.web (Phase W). Secrets may use {env:}/{file:}.
type ChatWebConfig struct {
	// Search is ddg (default) | searxng | tavily.
	Search string `json:"search,omitempty"`
	// SearxNGURL is required when Search is searxng. {env:}/{file:} resolved at LoadChat.
	SearxNGURL string `json:"searxng_url,omitempty"`
	// TavilyKey is required when Search is tavily. Never log or serve this value.
	TavilyKey string `json:"tavily_key,omitempty"`
}

// ChatCommandConfig is one chat.commands entry (O3).
type ChatCommandConfig struct {
	// Description is short help for completer / list API.
	Description string `json:"description,omitempty"`
	// Template is the user-message body. Optional $ARGUMENTS is replaced by slash args.
	Template string `json:"template"`
}

// Workspace default modes (chat.workspace.default).
const (
	WorkspaceDefaultClientCWD = "client_cwd"
	WorkspaceDefaultDaemonCWD = "daemon_cwd"
)

// ChatWorkspaceConfig is optional chat.workspace (ADR-046).
type ChatWorkspaceConfig struct {
	// Default is client_cwd (omit) | daemon_cwd | absolute path.
	Default string `json:"default,omitempty"`
	// Allow are extra absolute roots always on the path ceiling.
	Allow []string `json:"allow,omitempty"`
	// AllowAll disables path-root containment (local single-user only).
	AllowAll bool `json:"allow_all,omitempty"`
}

// ChatMemoryConfig is the optional chat.memory object (ADR-044 productization).
type ChatMemoryConfig struct {
	// Enabled defaults true when Memory is nil or Enabled is nil.
	Enabled *bool `json:"enabled,omitempty"`
	// MaxInjectRunes caps frozen system memory block (default 2000).
	MaxInjectRunes int `json:"max_inject_runes,omitempty"`
	// InjectMode is session_start (frozen snapshot). Other values reserved.
	InjectMode string `json:"inject_mode,omitempty"`
	// SessionSearch enables transcript FTS tool (default true).
	SessionSearch *bool `json:"session_search,omitempty"`
	// DefaultTTL is a Go duration applied when session/detail writes omit expires_at.
	// Empty = no automatic expiry. Global curated never receives this TTL.
	DefaultTTL string `json:"default_ttl,omitempty"`
	// Curator is optional post-turn LLM fact extraction (H1-lite).
	Curator *ChatMemoryCuratorConfig `json:"curator,omitempty"`
}

// ChatMemoryCuratorConfig is optional chat.memory.curator (H1-lite).
type ChatMemoryCuratorConfig struct {
	// Enabled defaults true when Curator is nil or Enabled is nil (and memory is on).
	Enabled *bool `json:"enabled,omitempty"`
	// MaxFacts caps facts written per turn (1–8). Omit → 3.
	MaxFacts int `json:"max_facts,omitempty"`
	// TimeoutMS bounds the aux call (1000–120000). Omit → 15000.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// ChatSkillsConfig is optional chat.skills (ADR-050).
type ChatSkillsConfig struct {
	// UnusedTTL is a Go duration; skills with last_used_at older than this are soft-archived.
	// Empty = no automatic archive. Skills that were never used are never auto-archived.
	UnusedTTL string `json:"unused_ttl,omitempty"`
}

// ChatPermissionConfig is the optional chat.permission object.
type ChatPermissionConfig struct {
	// Mode is accepted for old configs (preauth|ask) and ignored at runtime.
	// Wait is TUI interactive; CLI/cron stay fail-closed. Auto is a session stance.
	Mode string `json:"mode,omitempty"`
	// Allow remembers high-risk families so Agent does not prompt: process | git.
	Allow []string `json:"allow,omitempty"`
}

// ChatToolsConfig is the optional chat.tools allowlist for agent mode only.
type ChatToolsConfig struct {
	// Git enables git_status / git_diff / git_add / git_commit path-scoped grants (default false).
	Git bool `json:"git,omitempty"`
	// Process enables process_exec and process_shell path-scoped grants (default false).
	Process bool `json:"process,omitempty"`
}

// ChatCompactionConfig is the optional chat.compaction object.
type ChatCompactionConfig struct {
	// Enabled defaults true when Compaction is nil or Enabled is nil.
	Enabled *bool `json:"enabled,omitempty"`
}

// AgentWriteCeiling reports whether agent mode may receive write grants.
func (c ChatConfig) AgentWriteCeiling() bool {
	if c.AllowWrite == nil {
		return true
	}
	return *c.AllowWrite
}

// AgentGitEnabled reports whether agent mode may receive git_* grants.
// True when chat.tools.git or chat.permission.allow contains "git".
func (c ChatConfig) AgentGitEnabled() bool {
	if c.Tools != nil && c.Tools.Git {
		return true
	}
	return c.permissionAllowContains("git")
}

// AgentProcessEnabled reports whether agent mode may receive process_exec/process_shell grants.
// True when chat.tools.process or chat.permission.allow contains "process".
func (c ChatConfig) AgentProcessEnabled() bool {
	if c.Tools != nil && c.Tools.Process {
		return true
	}
	return c.permissionAllowContains("process")
}

// WebSearchBackend returns ddg | searxng | tavily. Omit / empty → ddg.
func (c ChatConfig) WebSearchBackend() string {
	if c.Web == nil {
		return WebSearchDDG
	}
	search := strings.ToLower(strings.TrimSpace(c.Web.Search))
	if search == "" {
		return WebSearchDDG
	}
	return search
}

func (c ChatConfig) permissionAllowContains(name string) bool {
	if c.Permission == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, item := range c.Permission.Allow {
		if strings.ToLower(strings.TrimSpace(item)) == want {
			return true
		}
	}
	return false
}

// CompactionEnabled reports whether LLM/extractive session compaction is on.
// Default true when chat.compaction is omitted or enabled is omitted.
func (c ChatConfig) CompactionEnabled() bool {
	if c.Compaction == nil || c.Compaction.Enabled == nil {
		return true
	}
	return *c.Compaction.Enabled
}

// MaxIterationsOrDefault returns MaxIterations, or 0 (no hard cap) when unset.
func (c ChatConfig) MaxIterationsOrDefault() int {
	if c.MaxIterations < 0 {
		return 0
	}
	return c.MaxIterations
}

// MemoryEnabled reports whether in-process memory is on (default true).
func (c ChatConfig) MemoryEnabled() bool {
	if c.Memory == nil || c.Memory.Enabled == nil {
		return true
	}
	return *c.Memory.Enabled
}

// MemoryMaxInjectRunes returns inject cap or 2000.
func (c ChatConfig) MemoryMaxInjectRunes() int {
	if c.Memory == nil || c.Memory.MaxInjectRunes <= 0 {
		return 2000
	}
	return c.Memory.MaxInjectRunes
}

// MemorySessionSearchEnabled reports whether session_search is on (default true).
func (c ChatConfig) MemorySessionSearchEnabled() bool {
	if c.Memory == nil || c.Memory.SessionSearch == nil {
		return true
	}
	return *c.Memory.SessionSearch
}

// MemoryDefaultTTL returns chat.memory.default_ttl or 0 (no automatic expiry).
func (c ChatConfig) MemoryDefaultTTL() time.Duration {
	if c.Memory == nil {
		return 0
	}
	raw := strings.TrimSpace(c.Memory.DefaultTTL)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// SkillsUnusedTTL returns chat.skills.unused_ttl or 0 (no automatic archive).
func (c ChatConfig) SkillsUnusedTTL() time.Duration {
	if c.Skills == nil {
		return 0
	}
	raw := strings.TrimSpace(c.Skills.UnusedTTL)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// MemoryCuratorEnabled reports whether post-turn LLM curator is on (default true when memory on).
func (c ChatConfig) MemoryCuratorEnabled() bool {
	if !c.MemoryEnabled() {
		return false
	}
	if c.Memory == nil || c.Memory.Curator == nil || c.Memory.Curator.Enabled == nil {
		return true
	}
	return *c.Memory.Curator.Enabled
}

// MemoryCuratorMaxFacts returns max facts per turn (default 3).
func (c ChatConfig) MemoryCuratorMaxFacts() int {
	if c.Memory == nil || c.Memory.Curator == nil || c.Memory.Curator.MaxFacts <= 0 {
		return 3
	}
	return c.Memory.Curator.MaxFacts
}

// MemoryCuratorTimeoutMS returns aux timeout in ms (default 15000).
func (c ChatConfig) MemoryCuratorTimeoutMS() int {
	if c.Memory == nil || c.Memory.Curator == nil || c.Memory.Curator.TimeoutMS <= 0 {
		return 15_000
	}
	return c.Memory.Curator.TimeoutMS
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
	ModelsPath       string            `json:"modelsPath,omitempty"`
	ResponseFormat   string            `json:"responseFormat,omitempty"`
	AnthropicVersion string            `json:"anthropicVersion,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
}

type Model struct {
	Name string `json:"name,omitempty"`
	// ID is the wire/API model id sent to the provider (OpenCode model.id).
	// When empty, the catalog key / selection model segment is used as the wire id.
	ID             string   `json:"id,omitempty"`
	ResponseFormat string   `json:"responseFormat,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTokens      int64    `json:"maxTokens,omitempty"`
	// ContextWindow is the model context length in tokens (not maxTokens output cap).
	// Omit or 0 to fill from models.dev or the 2026-08 default (1M).
	ContextWindow int64 `json:"contextWindow,omitempty"`
	// Limit is the OpenCode-style alias for context/output; explicit fields win when set.
	Limit           *ModelLimit `json:"limit,omitempty"`
	ReasoningEffort string      `json:"reasoningEffort,omitempty"`
}

// ModelLimit is OpenCode provider.models.*.limit.
type ModelLimit struct {
	Context int64 `json:"context,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

type Resolved struct {
	Source string
	// SelectionRef is the full selection string providerID/modelID (modelID may contain '/').
	SelectionRef string
	ProviderID   string
	Protocol     string
	// ModelID is the wire/API model id (OpenCode api.id): models.<key>.id or the model segment.
	ModelID          string
	BaseURL          string
	APIKey           string
	CompletionPath   string
	ModelsPath       string
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
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ListModelRefs returns the selected model plus every provider/model key from the
// first resolvable file under configDir. API keys and other secrets are never included.
