package providerconfig

import (
	"errors"
	"fmt"
	"net/url"
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
	config, err := decodeConfigFile(path)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(config.Model), collectModelRefs(config), nil
}

func collectModelRefs(config File) []ModelRef {
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
	add(config.Model, "")
	for _, ref := range config.Models {
		add(ref, "")
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
			// Catalog keys may contain '/'.
			ref := providerID + "/" + strings.TrimSpace(modelID)
			add(ref, provider.Models[modelID].Name)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func (c *ChatConfig) resolveWeb(configDirectory string) error {
	if c.Web == nil || !c.WebEnabled() {
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

func (c MCPConfig) validate() error {
	if !c.IsEnabled() {
		return nil
	}
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
