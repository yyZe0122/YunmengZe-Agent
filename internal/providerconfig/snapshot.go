package providerconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is immutable for the Engine lifetime, including resolved secret values.
type Snapshot struct {
	path           string
	file           File
	providerErrors map[string]error
	Chat           ChatConfig
	MCP            MCPConfig
	Main           string
	Roles          map[string]string
	Models         []ModelRef
}

func LoadSnapshot(dir string) (*Snapshot, error) {
	if err := LoadEnvFromConfigDir(dir); err != nil {
		return nil, err
	}
	path, err := findConfigPath(dir)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("provider config not found")
	}
	file, err := decodeConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err = validateModelRoles(file); err != nil {
		return nil, err
	}
	snapshot := &Snapshot{path: path, file: file, providerErrors: make(map[string]error), Main: strings.TrimSpace(file.Model), Roles: make(map[string]string)}
	if snapshot.Main == "" {
		return nil, errors.New("provider config model is required")
	}
	for role, ref := range file.Models {
		if ref = strings.TrimSpace(ref); ref != "" {
			snapshot.Roles[strings.TrimSpace(role)] = ref
		}
	}
	base := filepath.Dir(path)
	for name, provider := range file.Provider {
		provider.Options.APIKey, err = resolveValue(provider.Options.APIKey, base)
		if err != nil {
			snapshot.providerErrors[name] = fmt.Errorf("provider %s apiKey: %w", name, err)
			continue
		}
		provider.Options.Headers, err = resolveValues(provider.Options.Headers, base)
		if err != nil {
			snapshot.providerErrors[name] = fmt.Errorf("provider %s headers: %w", name, err)
			continue
		}
		file.Provider[name] = provider
	}
	if file.Chat != nil {
		snapshot.Chat = *file.Chat
	}
	if err = snapshot.Chat.resolveWeb(base); err != nil {
		return nil, err
	}
	if err = snapshot.Chat.validate(); err != nil {
		return nil, err
	}
	if file.MCP != nil {
		snapshot.MCP = *file.MCP
	}
	if err = snapshot.MCP.validate(); err != nil {
		return nil, err
	}
	if snapshot.MCP.IsEnabled() {
		for name, server := range snapshot.MCP.Servers {
			server.Env, err = resolveValues(server.Env, base)
			if err != nil {
				return nil, fmt.Errorf("mcp %s env: %w", name, err)
			}
			server.Headers, err = resolveValues(server.Headers, base)
			if err != nil {
				return nil, fmt.Errorf("mcp %s headers: %w", name, err)
			}
			snapshot.MCP.Servers[name] = server
		}
	}
	seen := map[string]bool{}
	add := func(ref, name string) {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			snapshot.Models = append(snapshot.Models, ModelRef{ID: ref, Name: name})
		}
	}
	add(snapshot.Main, "")
	for _, ref := range snapshot.Roles {
		add(ref, "")
	}
	for provider, p := range file.Provider {
		for model, m := range p.Models {
			add(provider+"/"+model, m.Name)
		}
	}
	sort.Slice(snapshot.Models, func(i, j int) bool { return snapshot.Models[i].ID < snapshot.Models[j].ID })
	for i, model := range snapshot.Models {
		if _, err = snapshot.Resolve(model.ID); err != nil {
			snapshot.Models[i].Unavailable = err.Error()
		}
	}
	if _, err = snapshot.Resolve(snapshot.Main); err != nil {
		return nil, fmt.Errorf("main model: %w", err)
	}
	for _, role := range []struct {
		name    string
		enabled bool
	}{
		{RoleSubagent, snapshot.Chat.TaskEnabled()},
		{RoleCompact, snapshot.Chat.CompactionEnabled()},
		{RoleWeb, snapshot.Chat.TaskEnabled() && snapshot.Chat.WebEnabled()},
		{RoleVision, snapshot.Chat.VisionEnabled()},
		{RoleMemory, snapshot.Chat.MemoryCuratorEnabled()},
	} {
		if ref := snapshot.Roles[role.name]; role.enabled && ref != "" {
			if _, err = snapshot.Resolve(ref); err != nil {
				return nil, fmt.Errorf("models.%s: %w", role.name, err)
			}
		}
	}
	return snapshot, nil
}
func (s *Snapshot) Resolve(ref string) (*Resolved, error) {
	provider, model, ok := strings.Cut(strings.TrimSpace(ref), "/")
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if !ok || provider == "" || model == "" {
		return nil, errors.New("model must use provider/model format")
	}
	configured, exists := s.file.Provider[provider]
	if !exists {
		return nil, fmt.Errorf("selected provider %q is not configured", provider)
	}
	if err := s.providerErrors[provider]; err != nil {
		return nil, err
	}
	resolved, err := resolveProvider(s.path, provider, model, configured)
	return &resolved, err
}
