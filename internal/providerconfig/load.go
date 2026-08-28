package providerconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ListModelRefs(configDir string) (selected string, models []ModelRef, err error) {
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return "", nil, err
	}
	path, err := findConfigPath(configDir)
	if err != nil {
		return "", nil, err
	}
	if path == "" {
		return "", nil, nil
	}
	return listModelRefsFromFile(path)
}

func listModelRefsFromFile(path string) (string, []ModelRef, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open provider config %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return "", nil, fmt.Errorf("decode provider config %s: %w", path, err)
	}
	if err := requireEOF(decoder); err != nil {
		return "", nil, fmt.Errorf("decode provider config %s: %w", path, err)
	}
	selected := strings.TrimSpace(config.Model)
	seen := make(map[string]struct{})
	var models []ModelRef
	add := func(id, name string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, ModelRef{ID: id, Name: strings.TrimSpace(name)})
	}
	if selected != "" {
		add(selected, "")
	}
	providerIDs := make([]string, 0, len(config.Provider))
	for providerID := range config.Provider {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		provider := config.Provider[providerID]
		modelIDs := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, modelID := range modelIDs {
			// OpenCode: list ref = providerID + "/" + catalog key (key may contain '/').
			ref := providerID + "/" + strings.TrimSpace(modelID)
			add(ref, provider.Models[modelID].Name)
		}
	}
	return selected, models, nil
}

// Load reads provider configuration only from configDir
// (agent.local.json, then agent.json). Project directories are not searched.
// Optional ConfigDir/env is loaded first (does not override existing process env).
func Load(configDir string) (*Resolved, error) {
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return nil, err
	}
	path, err := findConfigPath(configDir)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	resolved, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}

// LoadChat reads the optional chat section from the first resolvable config file.
// Missing file or missing chat block returns a zero ChatConfig (empty roots; agent write allowed).
// chat.web.searxng_url / tavily_key resolve {env:}/{file:} like MCP secrets.
func LoadChat(configDir string) (ChatConfig, error) {
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return ChatConfig{}, err
	}
	path, err := findConfigPath(configDir)
	if err != nil {
		return ChatConfig{}, err
	}
	if path == "" {
		return ChatConfig{}, nil
	}
	file, err := decodeConfigFile(path)
	if err != nil {
		return ChatConfig{}, err
	}
	if file.Chat == nil {
		return ChatConfig{}, nil
	}
	chat := *file.Chat
	if err := chat.resolveWeb(filepath.Dir(path)); err != nil {
		return ChatConfig{}, err
	}
	if err := chat.validate(); err != nil {
		return ChatConfig{}, err
	}
	return chat, nil
}

func (c *ChatConfig) resolveWeb(configDirectory string) error {
	if c == nil || c.Web == nil {
		return nil
	}
	search := strings.ToLower(strings.TrimSpace(c.Web.Search))
	if search == "" {
		search = WebSearchDDG
	}
	switch search {
	case WebSearchDDG:
		return nil
	case WebSearchSearxNG:
		resolved, err := resolveValue(c.Web.SearxNGURL, configDirectory)
		if err != nil {
			return fmt.Errorf("chat.web.searxng_url: %w", err)
		}
		c.Web.SearxNGURL = resolved
		return nil
	case WebSearchTavily:
		resolved, err := resolveValue(c.Web.TavilyKey, configDirectory)
		if err != nil {
			return fmt.Errorf("chat.web.tavily_key: %w", err)
		}
		c.Web.TavilyKey = resolved
		return nil
	default:
		return nil
	}
}

// LoadMCP reads the optional mcp section. Missing file or mcp block returns zero config.
// Env and header values may use {env:VAR} / {file:...} and are resolved relative to the config directory.
func LoadMCP(configDir string) (MCPConfig, error) {
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return MCPConfig{}, err
	}
	path, err := findConfigPath(configDir)
	if err != nil {
		return MCPConfig{}, err
	}
	if path == "" {
		return MCPConfig{}, nil
	}
	file, err := decodeConfigFile(path)
	if err != nil {
		return MCPConfig{}, err
	}
	if file.MCP == nil {
		return MCPConfig{}, nil
	}
	mcp := *file.MCP
	if err := mcp.validate(); err != nil {
		return MCPConfig{}, err
	}
	baseDir := filepath.Dir(path)
	for name, server := range mcp.Servers {
		env := make(map[string]string, len(server.Env))
		for k, v := range server.Env {
			resolved, err := resolveValue(v, baseDir)
			if err != nil {
				return MCPConfig{}, fmt.Errorf("mcp server %q env %q: %w", name, k, err)
			}
			env[k] = resolved
		}
		server.Env = env
		headers := make(map[string]string, len(server.Headers))
		for k, v := range server.Headers {
			resolved, err := resolveValue(v, baseDir)
			if err != nil {
				return MCPConfig{}, fmt.Errorf("mcp server %q header %q: %w", name, k, err)
			}
			headers[k] = resolved
		}
		server.Headers = headers
		mcp.Servers[name] = server
	}
	return mcp, nil
}

func (c MCPConfig) validate() error {
	for name, server := range c.Servers {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("mcp server name is required")
		}
		if !ValidMCPServerName(name) {
			return fmt.Errorf("mcp server name %q must match [a-zA-Z0-9_-]+", name)
		}
		if err := server.validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (s MCPServer) validate(name string) error {
	typ, err := s.ResolvedType()
	if err != nil {
		return fmt.Errorf("mcp server %q: %w", name, err)
	}
	switch typ {
	case MCPTypeStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("mcp server %q: command is required for stdio", name)
		}
	case MCPTypeHTTP, MCPTypeSSE, MCPTypeRemote:
		u := strings.TrimSpace(s.URL)
		if u == "" {
			return fmt.Errorf("mcp server %q: url is required for remote transport", name)
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("mcp server %q: url must be absolute http(s)", name)
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("mcp server %q: url scheme must be http or https", name)
		}
	default:
		return fmt.Errorf("mcp server %q: unknown type %q", name, typ)
	}
	return nil
}

// ResolvedType returns the effective transport type for this server.
func (s MCPServer) ResolvedType() (string, error) {
	typ := strings.ToLower(strings.TrimSpace(s.Type))
	hasCmd := strings.TrimSpace(s.Command) != ""
	hasURL := strings.TrimSpace(s.URL) != ""
	switch typ {
	case "":
		if hasURL && !hasCmd {
			return MCPTypeRemote, nil
		}
		if hasCmd {
			return MCPTypeStdio, nil
		}
		return "", errors.New("command or url is required")
	case MCPTypeStdio:
		return MCPTypeStdio, nil
	case MCPTypeHTTP, MCPTypeSSE, MCPTypeRemote:
		return typ, nil
	case "local":
		return MCPTypeStdio, nil
	default:
		return "", fmt.Errorf("unsupported type %q (use stdio|http|sse|remote)", typ)
	}
}

// ValidMCPServerName matches tool name safe fragment for mcp_<server>_*.
func ValidMCPServerName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// EffectiveRoots returns configured absolute roots, or fallback when roots are empty.
// Prefer WorkspaceCeiling / ResolveSessionWorkspace for ADR-046 session binding.
