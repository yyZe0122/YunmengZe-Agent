package providerconfig

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
)

func (c ChatConfig) EffectiveRoots(fallback string) []string {
	out := c.configuredRoots()
	if len(out) > 0 {
		return out
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return nil
	}
	return []string{fallback}
}

// WorkspaceAllowAll reports chat.workspace.allow_all.
func (c ChatConfig) WorkspaceAllowAll() bool {
	return c.Workspace != nil && c.Workspace.AllowAll
}

// configuredRoots returns chat.workspace.allow (absolute paths only).
func (c ChatConfig) configuredRoots() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	if c.Workspace != nil {
		for _, root := range c.Workspace.Allow {
			add(root)
		}
	}
	return out
}

// PathCeilingRoots returns roots for PathGuard at daemon start (no client cwd).
// When allow_all, returns nil (caller uses unrestricted guard).
// Otherwise: configured roots, else daemonFallback.
func (c ChatConfig) PathCeilingRoots(daemonFallback string) []string {
	if c.WorkspaceAllowAll() {
		return nil
	}
	out := c.configuredRoots()
	if len(out) > 0 {
		return out
	}
	daemonFallback = strings.TrimSpace(daemonFallback)
	if daemonFallback == "" {
		return nil
	}
	return []string{daemonFallback}
}

// ResolveSessionWorkspace picks the workspace root for a session/task (ADR-046).
// clientWorkspace is the absolute path from the client (usually Getwd).
// daemonFallback is the daemon process cwd.
func (c ChatConfig) ResolveSessionWorkspace(clientWorkspace, daemonFallback string) string {
	clientWorkspace = strings.TrimSpace(clientWorkspace)
	daemonFallback = strings.TrimSpace(daemonFallback)
	if c.Workspace != nil {
		def := strings.TrimSpace(c.Workspace.Default)
		switch {
		case def == "" || strings.EqualFold(def, WorkspaceDefaultClientCWD):
			if clientWorkspace != "" {
				return clientWorkspace
			}
			if daemonFallback != "" {
				return daemonFallback
			}
		case strings.EqualFold(def, WorkspaceDefaultDaemonCWD):
			if daemonFallback != "" {
				return daemonFallback
			}
			return clientWorkspace
		case filepath.IsAbs(def):
			return def
		}
	}
	if clientWorkspace != "" {
		return clientWorkspace
	}
	return daemonFallback
}

// GrantRootsForSession returns plan/grant path list: session workspace + configured extras.
func (c ChatConfig) GrantRootsForSession(sessionWorkspace string) []string {
	if c.WorkspaceAllowAll() {
		sessionWorkspace = strings.TrimSpace(sessionWorkspace)
		if sessionWorkspace == "" {
			return nil
		}
		return []string{sessionWorkspace}
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	add(sessionWorkspace)
	for _, root := range c.configuredRoots() {
		add(root)
	}
	return out
}

// AcceptsWorkspace reports whether client workspace is allowed under ceiling policy.
func (c ChatConfig) AcceptsWorkspace(workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return false
	}
	if c.WorkspaceAllowAll() {
		return true
	}
	// client_cwd default: any absolute client path is accepted (session-bound).
	def := WorkspaceDefaultClientCWD
	if c.Workspace != nil && strings.TrimSpace(c.Workspace.Default) != "" {
		def = strings.TrimSpace(c.Workspace.Default)
	}
	if strings.EqualFold(def, WorkspaceDefaultClientCWD) || def == "" {
		return true
	}
	if strings.EqualFold(def, WorkspaceDefaultDaemonCWD) {
		return true // resolved to daemon path by ResolveSessionWorkspace
	}
	if filepath.IsAbs(def) {
		return workspace == def || pathUnder(def, workspace)
	}
	// Must be under configured roots.
	for _, root := range c.configuredRoots() {
		if workspace == root || pathUnder(root, workspace) {
			return true
		}
	}
	return false
}

func pathUnder(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}

func (c ChatConfig) validate() error {
	if c.Workspace != nil {
		def := strings.TrimSpace(c.Workspace.Default)
		if def != "" && !strings.EqualFold(def, WorkspaceDefaultClientCWD) &&
			!strings.EqualFold(def, WorkspaceDefaultDaemonCWD) && !filepath.IsAbs(def) {
			return fmt.Errorf("chat.workspace.default must be client_cwd, daemon_cwd, or an absolute path")
		}
		for i, root := range c.Workspace.Allow {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			if !filepath.IsAbs(root) {
				return fmt.Errorf("chat.workspace.allow[%d] must be an absolute path", i)
			}
		}
	}
	if c.MaxIterations != 0 && (c.MaxIterations < 1 || c.MaxIterations > 256) {
		return fmt.Errorf("chat.max_iterations must be between 1 and 256 (or omit / 0 for no hard cap)")
	}
	if c.Permission != nil {
		mode := strings.ToLower(strings.TrimSpace(c.Permission.Mode))
		if mode != "" && mode != PermissionModePreauth && mode != PermissionModeAsk {
			return fmt.Errorf("chat.permission.mode must be preauth or ask")
		}
		for i, item := range c.Permission.Allow {
			switch strings.ToLower(strings.TrimSpace(item)) {
			case "process", "git":
			case "":
				return fmt.Errorf("chat.permission.allow[%d] is empty", i)
			default:
				return fmt.Errorf("chat.permission.allow[%d] must be process or git", i)
			}
		}
	}
	if c.Memory != nil {
		if c.Memory.MaxInjectRunes != 0 && (c.Memory.MaxInjectRunes < 200 || c.Memory.MaxInjectRunes > 16_000) {
			return fmt.Errorf("chat.memory.max_inject_runes must be between 200 and 16000 (or omit)")
		}
		mode := strings.ToLower(strings.TrimSpace(c.Memory.InjectMode))
		if mode != "" && mode != "session_start" {
			return fmt.Errorf("chat.memory.inject_mode must be session_start (or omit)")
		}
		if ttl := strings.TrimSpace(c.Memory.DefaultTTL); ttl != "" {
			d, err := time.ParseDuration(ttl)
			if err != nil || d <= 0 || d > 8760*time.Hour {
				return fmt.Errorf("chat.memory.default_ttl must be a positive Go duration up to 8760h (or omit)")
			}
		}
		if c.Memory.Curator != nil {
			if c.Memory.Curator.MaxFacts != 0 && (c.Memory.Curator.MaxFacts < 1 || c.Memory.Curator.MaxFacts > 8) {
				return fmt.Errorf("chat.memory.curator.max_facts must be between 1 and 8 (or omit)")
			}
			if c.Memory.Curator.TimeoutMS != 0 && (c.Memory.Curator.TimeoutMS < 1_000 || c.Memory.Curator.TimeoutMS > 120_000) {
				return fmt.Errorf("chat.memory.curator.timeout_ms must be between 1000 and 120000 (or omit)")
			}
		}
	}
	if c.Skills != nil {
		if ttl := strings.TrimSpace(c.Skills.UnusedTTL); ttl != "" {
			d, err := time.ParseDuration(ttl)
			if err != nil || d <= 0 || d > 8760*time.Hour {
				return fmt.Errorf("chat.skills.unused_ttl must be a positive Go duration up to 8760h (or omit)")
			}
		}
	}
	if err := validateChatCommands(c.Commands); err != nil {
		return err
	}
	if err := c.validateWeb(); err != nil {
		return err
	}
	return nil
}

func (c ChatConfig) validateWeb() error {
	if c.Web == nil {
		return nil
	}
	search := strings.ToLower(strings.TrimSpace(c.Web.Search))
	switch search {
	case "", WebSearchDDG:
		return nil
	case WebSearchSearxNG:
		raw := strings.TrimSpace(c.Web.SearxNGURL)
		if raw == "" {
			return fmt.Errorf("chat.web.searxng_url is required when search is searxng")
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("chat.web.searxng_url must be an absolute http(s) URL")
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("chat.web.searxng_url scheme must be http or https")
		}
		if parsed.User != nil {
			return fmt.Errorf("chat.web.searxng_url must not include userinfo")
		}
		return nil
	case WebSearchTavily:
		if strings.TrimSpace(c.Web.TavilyKey) == "" {
			return fmt.Errorf("chat.web.tavily_key is required when search is tavily")
		}
		return nil
	default:
		return fmt.Errorf("chat.web.search must be ddg, searxng, or tavily")
	}
}

// MaxChatCommandTemplateRunes caps one command template at load time.
const MaxChatCommandTemplateRunes = 8_000

// reservedChatCommandNames are TUI builtin slash names (without /) that chat.commands may not claim.
var reservedChatCommandNames = map[string]struct{}{
	"new": {}, "sessions": {}, "tasks": {}, "back": {}, "clear": {},
	"pause": {}, "resume": {}, "cancel": {}, "stop": {}, "retry": {},
	"model": {}, "skills": {}, "theme": {}, "cron": {}, "compact": {},
	"perm": {}, "memory": {}, "refresh-memory": {}, "status": {}, "help": {},
	"edit": {}, "editundo": {}, "undo": {},
	"quit": {}, "q": {}, "exit": {}, "resume-list": {},
}

func validateChatCommands(commands map[string]ChatCommandConfig) error {
	if len(commands) == 0 {
		return nil
	}
	for name, cmd := range commands {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("chat.commands: empty command name")
		}
		if strings.HasPrefix(name, "/") {
			return fmt.Errorf("chat.commands %q: name must not include leading slash", name)
		}
		if !ValidChatCommandName(name) {
			return fmt.Errorf("chat.commands %q: name must match [a-zA-Z0-9_-]+", name)
		}
		if _, reserved := reservedChatCommandNames[strings.ToLower(name)]; reserved {
			return fmt.Errorf("chat.commands %q: name conflicts with built-in slash command", name)
		}
		template := strings.TrimSpace(cmd.Template)
		if template == "" {
			return fmt.Errorf("chat.commands %q: template is required", name)
		}
		if utf8.RuneCountInString(template) > MaxChatCommandTemplateRunes {
			return fmt.Errorf("chat.commands %q: template exceeds %d runes", name, MaxChatCommandTemplateRunes)
		}
		if err := injectscan.Scan(template); err != nil {
			return fmt.Errorf("chat.commands %q: template rejected: %w", name, err)
		}
		if err := injectscan.Scan(cmd.Description); err != nil {
			return fmt.Errorf("chat.commands %q: description rejected: %w", name, err)
		}
	}
	return nil
}

// ValidChatCommandName matches slash fragment for /name.
func ValidChatCommandName(name string) bool {
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

// ExpandChatCommandTemplate replaces $ARGUMENTS (and $0) with args; if no placeholder, appends args.
func ExpandChatCommandTemplate(template, args string) string {
	template = strings.TrimRight(template, " \t")
	args = strings.TrimSpace(args)
	if strings.Contains(template, "$ARGUMENTS") {
		return strings.ReplaceAll(template, "$ARGUMENTS", args)
	}
	if strings.Contains(template, "$0") {
		return strings.ReplaceAll(template, "$0", args)
	}
	if args == "" {
		return strings.TrimSpace(template)
	}
	if template == "" {
		return args
	}
	return strings.TrimSpace(template) + "\n\n" + args
}

// ChatCommandListItem is one command for Gateway/TUI (instruction template; not secrets).
type ChatCommandListItem struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template"`
}

// CommandList returns sorted commands for Gateway.
func (c ChatConfig) CommandList() []ChatCommandListItem {
	if len(c.Commands) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Commands))
	for name := range c.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ChatCommandListItem, 0, len(names))
	for _, name := range names {
		cmd := c.Commands[name]
		out = append(out, ChatCommandListItem{
			ID:          name,
			Description: strings.TrimSpace(cmd.Description),
			Template:    cmd.Template,
		})
	}
	return out
}

// LookupCommand returns a command by id (case-sensitive key; falls back to case-insensitive).
func (c ChatConfig) LookupCommand(id string) (ChatCommandConfig, bool) {
	id = strings.TrimSpace(id)
	if id == "" || len(c.Commands) == 0 {
		return ChatCommandConfig{}, false
	}
	if cmd, ok := c.Commands[id]; ok {
		return cmd, true
	}
	lower := strings.ToLower(id)
	for name, cmd := range c.Commands {
		if strings.ToLower(name) == lower {
			return cmd, true
		}
	}
	return ChatCommandConfig{}, false
}

// LoadModelRoles returns the main model ref and optional role overrides from config.
// roles keys are only configured non-empty overrides (subagent, compact).
// Unset or empty values fall back to main at call sites.
