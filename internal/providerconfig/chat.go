package providerconfig

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
)

func (c ChatConfig) validateWeb() error {
	if c.Web == nil || !c.WebEnabled() {
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
	"new": {}, "sessions": {}, "resume": {}, "model": {}, "skills": {}, "compact": {}, "memory": {}, "mode": {}, "status": {}, "help": {}, "quit": {}, "q": {}, "exit": {},
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
