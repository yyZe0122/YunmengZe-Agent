package tui

import (
	"strings"
)

// completer filters slash commands as the user types "/…".
// After the first space it may offer argument completions (e.g. /perm decisions).
type completer struct {
	active  bool
	query   string
	items   []slashCommand
	cursor  int
	visible bool
	// argMode is true when completing after the command token (first space).
	argMode bool
}

func (c *completer) update(input string) {
	c.updateWith(input, nil, nil, nil)
}

// updateWith allows argument completion using model/permission lists and skill slashes.
func (c *completer) updateWith(input string, models []string, permIDs []string, extraSlashes []slashCommand) {
	input = strings.TrimRight(input, "\t")
	if !strings.HasPrefix(input, "/") {
		c.active = false
		c.visible = false
		c.items = nil
		c.cursor = 0
		c.query = ""
		c.argMode = false
		return
	}
	space := strings.IndexByte(input, ' ')
	if space < 0 {
		c.argMode = false
		c.active = true
		c.query = strings.ToLower(input)
		c.items = filterCommands(c.query, extraSlashes)
		if c.cursor >= len(c.items) {
			c.cursor = 0
		}
		c.visible = len(c.items) > 0
		return
	}
	// Argument completion.
	cmd := canonicalSlash(input[:space])
	arg := strings.TrimSpace(input[space+1:])
	c.argMode = true
	c.active = true
	c.query = strings.ToLower(arg)
	c.items = filterArgCompletions(cmd, arg, models, permIDs)
	if c.cursor >= len(c.items) {
		c.cursor = 0
	}
	c.visible = len(c.items) > 0
}

func filterArgCompletions(cmd, arg string, models, permIDs []string) []slashCommand {
	argLower := strings.ToLower(strings.TrimSpace(arg))
	var out []slashCommand
	switch cmd {
	case "/perm":
		fields := strings.Fields(arg)
		if len(fields) == 0 || (len(fields) == 1 && !strings.HasSuffix(arg, " ") && !strings.Contains(arg, " ")) {
			// Completing decision token.
			for _, d := range []string{"allow_once", "allow_similar", "allow_permanent", "deny"} {
				if argLower == "" || strings.HasPrefix(d, argLower) {
					out = append(out, slashCommand{Name: d, Desc: "permission decision"})
				}
			}
			return out
		}
		// Completing id prefix after decision.
		decision := ""
		prefix := ""
		if len(fields) >= 1 {
			decision = fields[0]
		}
		if len(fields) >= 2 {
			prefix = strings.ToLower(fields[1])
		} else if strings.HasSuffix(arg, " ") {
			prefix = ""
		}
		for _, id := range permIDs {
			idLower := strings.ToLower(id)
			short := id
			if len(short) > 8 {
				short = short[:8]
			}
			if prefix == "" || strings.HasPrefix(idLower, prefix) || strings.Contains(idLower, prefix) {
				out = append(out, slashCommand{
					Name: decision + " " + short,
					Desc: "pending " + shortID(id),
					Help: decision + " " + id,
				})
			}
		}
		return out
	case "/model":
		fields := strings.Fields(arg)
		if len(fields) == 0 || (len(fields) == 1 && fields[0] != "main" && !strings.HasSuffix(arg, " ")) {
			if argLower == "" || strings.HasPrefix("main", argLower) {
				out = append(out, slashCommand{Name: "main", Desc: "global daemon main"})
			}
			for _, name := range models {
				if argLower == "" || strings.Contains(strings.ToLower(name), argLower) {
					out = append(out, slashCommand{Name: name, Desc: "this session"})
				}
			}
			return out
		}
		prefix := argLower
		if len(fields) >= 1 && fields[0] == "main" {
			if len(fields) >= 2 {
				prefix = strings.ToLower(strings.Join(fields[1:], " "))
			} else {
				prefix = ""
			}
			for _, name := range models {
				if prefix == "" || strings.Contains(strings.ToLower(name), prefix) {
					out = append(out, slashCommand{Name: "main " + name, Desc: "global daemon main"})
				}
			}
			return out
		}
		for _, name := range models {
			if prefix == "" || strings.Contains(strings.ToLower(name), prefix) {
				out = append(out, slashCommand{Name: name, Desc: "this session"})
			}
		}
		return out
	default:
		return nil
	}
}

func filterCommands(query string, extraSlashes []slashCommand) []slashCommand {
	builtin := slashCommands
	if query == "" || query == "/" {
		out := make([]slashCommand, 0, len(builtin)+len(extraSlashes))
		out = append(out, builtin...)
		out = append(out, extraSlashes...)
		return out
	}
	var out []slashCommand
	for _, cmd := range builtin {
		name := strings.ToLower(cmd.Name)
		if strings.HasPrefix(name, query) {
			out = append(out, cmd)
		}
	}
	for _, cmd := range extraSlashes {
		name := strings.ToLower(cmd.Name)
		if strings.HasPrefix(name, query) {
			out = append(out, cmd)
		}
	}
	// Also match aliases listed in Help when name doesn't prefix-match.
	if len(out) == 0 {
		for _, cmd := range builtin {
			if strings.Contains(strings.ToLower(cmd.Help), query[1:]) {
				out = append(out, cmd)
			}
		}
	}
	return out
}

func (c *completer) move(delta int) {
	if !c.visible || len(c.items) == 0 {
		return
	}
	c.cursor += delta
	if c.cursor < 0 {
		c.cursor = len(c.items) - 1
	}
	if c.cursor >= len(c.items) {
		c.cursor = 0
	}
}

// selectedName returns the highlighted command name without dismissing.
func (c *completer) selectedName() string {
	if !c.visible || len(c.items) == 0 {
		return ""
	}
	if c.cursor < 0 || c.cursor >= len(c.items) {
		return c.items[0].Name
	}
	return c.items[c.cursor].Name
}

// accept returns the selected command name (e.g. "/new") and clears the popup.
func (c *completer) accept() string {
	name := c.selectedName()
	if name == "" {
		return ""
	}
	c.active = false
	c.visible = false
	c.items = nil
	c.argMode = false
	return name
}

// inputIsCompleteCommand reports whether trim(input) already names the selected
// slash command (or an alias that canonicalizes to it).
func inputIsCompleteCommand(input, selectedName string) bool {
	input = strings.TrimSpace(input)
	selectedName = strings.TrimSpace(selectedName)
	if input == "" || selectedName == "" {
		return false
	}
	return canonicalSlash(input) == canonicalSlash(selectedName)
}

func (c *completer) dismiss() {
	c.active = false
	c.visible = false
	c.items = nil
	c.cursor = 0
	c.argMode = false
}

func (c *completer) render(max int) string {
	if !c.visible || len(c.items) == 0 {
		return ""
	}
	if max <= 0 {
		max = 6
	}
	start := 0
	if c.cursor >= max {
		start = c.cursor - max + 1
	}
	end := start + max
	if end > len(c.items) {
		end = len(c.items)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		cmd := c.items[i]
		name := styleKeyword.Render(cmd.Name)
		desc := styleDim.Render(cmd.Desc)
		if i == c.cursor {
			b.WriteString(styleCompSel.Render("› ") + styleKeyword.Render(cmd.Name) + "  " + desc)
		} else {
			b.WriteString("  " + name + "  " + desc)
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
