// Package opencodeimport maps OpenCode opencode.json into YunmengZe agent.local.json.
package opencodeimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
)

// Result is the mapped YMZ config plus human-readable warnings for dropped fields.
type Result struct {
	File     providerconfig.File
	Warnings []string
	Source   string
}

// ConvertFile reads an OpenCode config path and maps it to a YMZ File.
func ConvertFile(path string) (Result, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return Result{}, errors.New("opencode config path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read opencode config %s: %w", path, err)
	}
	res, err := Convert(raw)
	if err != nil {
		return Result{}, err
	}
	res.Source = path
	return res, nil
}

// Convert maps OpenCode JSON bytes to a YMZ providerconfig.File.
// Unknown / unsupported OpenCode fields produce warnings and are dropped.
func Convert(raw []byte) (Result, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Result{}, errors.New("opencode config is empty")
	}
	// Strip // and /* */ is not full JSONC; try standard JSON first, then jsonc-lite.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		stripped := stripJSONC(raw)
		if err2 := json.Unmarshal(stripped, &root); err2 != nil {
			return Result{}, fmt.Errorf("decode opencode config: %w", err)
		}
	}
	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}

	// Top-level keys we intentionally drop.
	dropTop := map[string]string{
		"plugin":             "plugins are not supported",
		"plugins":            "plugins are not supported",
		"lsp":                "LSP is not supported",
		"theme":              "theme is not imported (TUI-local)",
		"keybinds":           "keybinds are not imported",
		"tui":                "tui block is not imported",
		"agent":              "opencode agent profiles are not imported",
		"mode":               "opencode mode map is not imported",
		"permission":         "opencode permission map is not imported (use chat.permission / tools)",
		"tools":              "top-level tools map is not imported",
		"instructions":       "instructions are not imported",
		"username":           "username is not imported",
		"share":              "share is not imported",
		"autoshare":          "autoshare is not imported",
		"watcher":            "watcher is not imported",
		"server":             "server is not imported",
		"experimental":       "experimental is not imported",
		"enabled_providers":  "enabled_providers is not imported",
		"disabled_providers": "disabled_providers is not imported",
		"default_agent":      "default_agent is not imported",
		"snapshot":           "snapshot is not imported",
		"layout":             "layout is not imported",
		"small_model":        "small_model is not imported (use models.compact)",
	}
	knownKeep := map[string]struct{}{
		"$schema": {}, "model": {}, "provider": {}, "mcp": {}, "compaction": {},
		"command": {}, "models": {}, // models → YMZ role map; command → chat.commands
	}
	for key := range root {
		if _, ok := knownKeep[key]; ok {
			continue
		}
		if msg, ok := dropTop[key]; ok {
			warn("dropped %q: %s", key, msg)
			continue
		}
		warn("dropped unknown top-level field %q", key)
	}

	out := providerconfig.File{
		Provider: make(map[string]providerconfig.Provider),
	}

	// model
	if rawModel, ok := root["model"]; ok {
		var model string
		if err := json.Unmarshal(rawModel, &model); err != nil {
			return Result{}, fmt.Errorf("model: %w", err)
		}
		out.Model = strings.TrimSpace(model)
	}

	// provider map
	if rawProv, ok := root["provider"]; ok {
		var providers map[string]ocProvider
		if err := json.Unmarshal(rawProv, &providers); err != nil {
			return Result{}, fmt.Errorf("provider: %w", err)
		}
		ids := make([]string, 0, len(providers))
		for id := range providers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			p, w := mapProvider(id, providers[id])
			warnings = append(warnings, w...)
			out.Provider[id] = p
		}
	}

	if len(out.Provider) == 0 {
		return Result{}, errors.New("opencode config has no provider entries to import")
	}

	// Default model if missing: first provider + first model key
	if out.Model == "" {
		if ref := firstModelRef(out.Provider); ref != "" {
			out.Model = ref
			warn("model was empty; defaulted to %q", ref)
		} else {
			return Result{}, errors.New("model is required and no catalog entry found to default")
		}
	}

	// top-level models → YMZ role map (subagent/compact/web/vision/speech)
	if rawRoles, ok := root["models"]; ok {
		roles, w := mapRoleModels(rawRoles)
		warnings = append(warnings, w...)
		if len(roles) > 0 {
			out.Models = roles
		}
	}

	// mcp
	if rawMCP, ok := root["mcp"]; ok {
		mcp, w := mapMCP(rawMCP)
		warnings = append(warnings, w...)
		if mcp != nil && len(mcp.Servers) > 0 {
			out.MCP = mcp
		}
	}

	// compaction (top-level in OC) → chat.compaction
	if rawComp, ok := root["compaction"]; ok {
		enabled, w := mapCompaction(rawComp)
		warnings = append(warnings, w...)
		if enabled != nil {
			if out.Chat == nil {
				out.Chat = &providerconfig.ChatConfig{}
			}
			out.Chat.Compaction = &providerconfig.ChatCompactionConfig{Enabled: enabled}
		}
	}

	// command map → chat.commands (O3)
	if rawCmd, ok := root["command"]; ok {
		cmds, w := mapCommands(rawCmd)
		warnings = append(warnings, w...)
		if len(cmds) > 0 {
			if out.Chat == nil {
				out.Chat = &providerconfig.ChatConfig{}
			}
			out.Chat.Commands = cmds
		}
	}

	return Result{File: out, Warnings: warnings}, nil
}

func mapRoleModels(raw json.RawMessage) (map[string]string, []string) {
	var warnings []string
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, []string{fmt.Sprintf("models: decode failed: %v", err)}
	}
	out := make(map[string]string)
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		role := strings.TrimSpace(key)
		if role == "" {
			warnings = append(warnings, "models: skipped empty role key")
			continue
		}
		if role == providerconfig.RoleMain {
			warnings = append(warnings, `dropped models.main: use top-level "model"`)
			continue
		}
		if _, ok := providerconfig.AllowedModelRoles[role]; !ok {
			warnings = append(warnings, fmt.Sprintf("dropped models.%s: not a supported role (allowed: %s)", role, providerconfig.AllowedModelRoleList()))
			continue
		}
		var ref string
		if err := json.Unmarshal(root[key], &ref); err != nil {
			warnings = append(warnings, fmt.Sprintf("models.%s: skipped (want provider/model string)", role))
			continue
		}
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		providerID, modelID, cut := strings.Cut(ref, "/")
		if !cut || strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
			warnings = append(warnings, fmt.Sprintf("models.%s: skipped (must use provider/model format)", role))
			continue
		}
		out[role] = ref
	}
	if len(out) == 0 {
		return nil, warnings
	}
	return out, warnings
}

func mapCommands(raw json.RawMessage) (map[string]providerconfig.ChatCommandConfig, []string) {
	var warnings []string
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, []string{fmt.Sprintf("command: decode failed: %v", err)}
	}
	out := make(map[string]providerconfig.ChatCommandConfig)
	names := make([]string, 0, len(root))
	for name := range root {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !providerconfig.ValidChatCommandName(name) {
			warnings = append(warnings, fmt.Sprintf("command %q: skipped (name must match [a-zA-Z0-9_-]+)", name))
			continue
		}
		// string template
		var asString string
		if err := json.Unmarshal(root[name], &asString); err == nil && strings.TrimSpace(asString) != "" {
			out[name] = providerconfig.ChatCommandConfig{Template: asString}
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(root[name], &entry); err != nil {
			warnings = append(warnings, fmt.Sprintf("command %q: skipped (invalid object)", name))
			continue
		}
		var template, desc string
		if v, ok := entry["template"]; ok {
			_ = json.Unmarshal(v, &template)
		}
		if v, ok := entry["description"]; ok {
			_ = json.Unmarshal(v, &desc)
		}
		// OC sometimes uses "prompt" or "message"
		if strings.TrimSpace(template) == "" {
			if v, ok := entry["prompt"]; ok {
				_ = json.Unmarshal(v, &template)
			}
		}
		if strings.TrimSpace(template) == "" {
			if v, ok := entry["message"]; ok {
				_ = json.Unmarshal(v, &template)
			}
		}
		template = strings.TrimSpace(template)
		if template == "" {
			warnings = append(warnings, fmt.Sprintf("command %q: skipped (no template)", name))
			continue
		}
		out[name] = providerconfig.ChatCommandConfig{
			Description: strings.TrimSpace(desc),
			Template:    template,
		}
	}
	if len(out) == 0 {
		return nil, warnings
	}
	return out, warnings
}

// WriteLocal writes the mapped file to configDir/agent.local.json (atomic, 0600).
func WriteLocal(configDir string, file providerconfig.File) (string, error) {
	configDir = filepath.Clean(strings.TrimSpace(configDir))
	if configDir == "" {
		return "", errors.New("config directory is required")
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	path := filepath.Join(configDir, providerconfig.LocalFilename)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(configDir, ".yunmengze-import-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("replace %s: %w", path, err)
	}
	cleanup = false
	return path, nil
}

type ocProvider struct {
	Name    string                     `json:"name"`
	NPM     string                     `json:"npm"`
	API     string                     `json:"api"`
	Env     []string                   `json:"env"`
	Options map[string]json.RawMessage `json:"options"`
	Models  map[string]ocModel         `json:"models"`
	// whitelist extras we ignore with warning via raw re-parse if needed
}

type ocModel struct {
	Name            string   `json:"name"`
	ID              string   `json:"id"`
	Temperature     *float64 `json:"temperature"`
	MaxTokens       int64    `json:"maxTokens"`
	ContextWindow   int64    `json:"contextWindow"`
	Limit           *ocLimit `json:"limit"`
	ReasoningEffort string   `json:"reasoningEffort"`
	// provider-specific blobs ignored
}

type ocLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

func mapProvider(id string, p ocProvider) (providerconfig.Provider, []string) {
	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(p.NPM) != "" {
		// npm package is OpenCode adapter hint; map via type when possible
	}
	protocol, typeHint := protocolFromNPM(p.NPM)
	opts := providerconfig.ProviderOptions{}
	if p.Options != nil {
		if v, ok := p.Options["baseURL"]; ok {
			_ = json.Unmarshal(v, &opts.BaseURL)
		}
		if v, ok := p.Options["apiKey"]; ok {
			_ = json.Unmarshal(v, &opts.APIKey)
		}
		if v, ok := p.Options["headers"]; ok {
			_ = json.Unmarshal(v, &opts.Headers)
		}
		if v, ok := p.Options["completionPath"]; ok {
			_ = json.Unmarshal(v, &opts.CompletionPath)
		}
		if v, ok := p.Options["modelsPath"]; ok {
			_ = json.Unmarshal(v, &opts.ModelsPath)
		}
		// Drop OpenCode-only option keys with a single summary warning.
		skip := map[string]struct{}{
			"baseURL": {}, "apiKey": {}, "headers": {}, "completionPath": {}, "modelsPath": {},
		}
		var dropped []string
		for k := range p.Options {
			if _, ok := skip[k]; ok {
				continue
			}
			dropped = append(dropped, k)
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			warn("provider %q: dropped options %s", id, strings.Join(dropped, ", "))
		}
	}
	if opts.BaseURL == "" && strings.TrimSpace(p.API) != "" {
		opts.BaseURL = strings.TrimSpace(p.API)
	}
	if opts.APIKey == "" && len(p.Env) > 0 {
		// Prefer first env name as {env:VAR}
		envName := strings.TrimSpace(p.Env[0])
		if envName != "" {
			opts.APIKey = "{env:" + envName + "}"
			warn("provider %q: apiKey set from env list as {env:%s}", id, envName)
		}
	}
	// Prefer env placeholder when literal key looks like a secret we should not rewrite — keep as-is
	// (user may want local import). Document that import preserves literal keys.

	models := make(map[string]providerconfig.Model, len(p.Models))
	for key, m := range p.Models {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		ym := providerconfig.Model{
			Name:            strings.TrimSpace(m.Name),
			ID:              strings.TrimSpace(m.ID),
			Temperature:     m.Temperature,
			MaxTokens:       m.MaxTokens,
			ContextWindow:   m.ContextWindow,
			ReasoningEffort: strings.TrimSpace(m.ReasoningEffort),
		}
		if m.Limit != nil {
			if ym.ContextWindow == 0 && m.Limit.Context > 0 {
				ym.ContextWindow = m.Limit.Context
			}
			if ym.MaxTokens == 0 && m.Limit.Output > 0 {
				ym.MaxTokens = m.Limit.Output
			}
		}
		models[key] = ym
	}

	out := providerconfig.Provider{
		Protocol: protocol,
		Type:     typeHint,
		Options:  opts,
		Models:   models,
	}
	if out.Type == "" && out.Protocol == "" {
		// Default openai-compatible for custom npm packages
		out.Type = "openai-compatible"
		if strings.TrimSpace(p.NPM) != "" {
			warn("provider %q: npm %q mapped to type openai-compatible", id, p.NPM)
		}
	}
	return out, warnings
}

func protocolFromNPM(npm string) (protocol, typeHint string) {
	npm = strings.TrimSpace(strings.ToLower(npm))
	switch {
	case npm == "" || strings.Contains(npm, "openai-compatible"):
		return "", "openai-compatible"
	case strings.Contains(npm, "openai") && !strings.Contains(npm, "compatible"):
		// @ai-sdk/openai → responses-style often; YMZ uses type openai → openai-responses
		return "", "openai"
	case strings.Contains(npm, "anthropic"):
		return "", "anthropic"
	case strings.Contains(npm, "google") || strings.Contains(npm, "gemini"):
		return "", "gemini"
	default:
		return "", ""
	}
}

func mapMCP(raw json.RawMessage) (*providerconfig.MCPConfig, []string) {
	var warnings []string
	// OC shapes:
	// "mcp": { "name": { "type":"local", "command":["npx","-y","..."], "environment":{} } }
	// or { "command": "...", "args": [] } style
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, []string{fmt.Sprintf("mcp: decode failed: %v", err)}
	}
	servers := make(map[string]providerconfig.MCPServer)
	names := make([]string, 0, len(root))
	for name := range root {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !providerconfig.ValidMCPServerName(name) {
			warnings = append(warnings, fmt.Sprintf("mcp server %q: skipped (name must match [a-zA-Z0-9_-]+)", name))
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(root[name], &entry); err != nil {
			warnings = append(warnings, fmt.Sprintf("mcp server %q: skipped (invalid object)", name))
			continue
		}
		var typ string
		if v, ok := entry["type"]; ok {
			_ = json.Unmarshal(v, &typ)
		}
		typ = strings.ToLower(strings.TrimSpace(typ))
		// Remote / url-based MCP (O2)
		if typ == "remote" || typ == "sse" || typ == "http" || hasMCPURL(entry) {
			urlStr, headers, okR, w := parseMCPRemote(entry, typ)
			warnings = append(warnings, w...)
			if !okR {
				warnings = append(warnings, fmt.Sprintf("mcp server %q: skipped (remote MCP missing url)", name))
				continue
			}
			outType := typ
			if outType == "" || outType == "remote" {
				outType = providerconfig.MCPTypeRemote
			}
			servers[name] = providerconfig.MCPServer{Type: outType, URL: urlStr, Headers: headers}
			continue
		}
		cmd, args, env, ok, w := parseMCPCommand(entry)
		warnings = append(warnings, w...)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("mcp server %q: skipped (no local command)", name))
			continue
		}
		servers[name] = providerconfig.MCPServer{Type: providerconfig.MCPTypeStdio, Command: cmd, Args: args, Env: env}
	}
	if len(servers) == 0 {
		return nil, warnings
	}
	return &providerconfig.MCPConfig{Servers: servers}, warnings
}

func hasMCPURL(entry map[string]json.RawMessage) bool {
	_, ok := entry["url"]
	return ok
}

func parseMCPRemote(entry map[string]json.RawMessage, typ string) (urlStr string, headers map[string]string, ok bool, warnings []string) {
	if v, okU := entry["url"]; okU {
		_ = json.Unmarshal(v, &urlStr)
	}
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return "", nil, false, warnings
	}
	// headers / headers map
	if v, okH := entry["headers"]; okH {
		_ = json.Unmarshal(v, &headers)
	}
	// OpenCode sometimes uses authorization / oauth token fields as opaque headers — leave unmapped.
	if _, okA := entry["oauth"]; okA {
		warnings = append(warnings, "mcp remote: oauth block not imported (use headers with {env:} secrets)")
	}
	_ = typ
	return urlStr, headers, true, warnings
}

func parseMCPCommand(entry map[string]json.RawMessage) (cmd string, args []string, env map[string]string, ok bool, warnings []string) {
	// command as string
	if v, okRaw := entry["command"]; okRaw {
		var asString string
		if err := json.Unmarshal(v, &asString); err == nil && strings.TrimSpace(asString) != "" {
			cmd = strings.TrimSpace(asString)
			if a, okA := entry["args"]; okA {
				_ = json.Unmarshal(a, &args)
			}
			ok = true
		} else {
			var asArr []string
			if err := json.Unmarshal(v, &asArr); err == nil && len(asArr) > 0 {
				cmd = strings.TrimSpace(asArr[0])
				if len(asArr) > 1 {
					args = asArr[1:]
				}
				ok = cmd != ""
			}
		}
	}
	// environment / env
	if v, okE := entry["environment"]; okE {
		_ = json.Unmarshal(v, &env)
	}
	if v, okE := entry["env"]; okE {
		var e2 map[string]string
		if err := json.Unmarshal(v, &e2); err == nil {
			if env == nil {
				env = e2
			} else {
				for k, val := range e2 {
					env[k] = val
				}
			}
		}
	}
	return cmd, args, env, ok, warnings
}

func mapCompaction(raw json.RawMessage) (*bool, []string) {
	var warnings []string
	// bool
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return &b, nil
	}
	// object: { "auto": true, "prune": ... }
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, []string{fmt.Sprintf("compaction: skipped (%v)", err)}
	}
	// Prefer auto → enabled; if auto false → enabled false
	if v, ok := obj["auto"]; ok {
		var auto bool
		if err := json.Unmarshal(v, &auto); err == nil {
			enabled := auto
			for k := range obj {
				if k != "auto" {
					warnings = append(warnings, fmt.Sprintf("compaction: dropped field %q", k))
				}
			}
			return &enabled, warnings
		}
	}
	if v, ok := obj["enabled"]; ok {
		var enabled bool
		if err := json.Unmarshal(v, &enabled); err == nil {
			return &enabled, warnings
		}
	}
	warnings = append(warnings, "compaction: no mappable enabled/auto flag; skipped")
	return nil, warnings
}

func firstModelRef(providers map[string]providerconfig.Provider) string {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := providers[id]
		if len(p.Models) == 0 {
			continue
		}
		keys := make([]string, 0, len(p.Models))
		for k := range p.Models {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return id + "/" + keys[0]
	}
	return ""
}

// stripJSONC removes // line comments and /* */ block comments outside strings.
func stripJSONC(in []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(in))
	i := 0
	inString := false
	escape := false
	for i < len(in) {
		c := in[i]
		if inString {
			out.WriteByte(c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			i++
			continue
		}
		if c == '/' && i+1 < len(in) {
			if in[i+1] == '/' {
				i += 2
				for i < len(in) && in[i] != '\n' {
					i++
				}
				continue
			}
			if in[i+1] == '*' {
				i += 2
				for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
					i++
				}
				if i+1 < len(in) {
					i += 2
				}
				continue
			}
		}
		out.WriteByte(c)
		i++
	}
	return out.Bytes()
}
