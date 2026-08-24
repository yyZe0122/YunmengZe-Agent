package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) lookupChatCommand(name string) *gatewayclient.ChatCommand {
	id := strings.TrimPrefix(strings.TrimSpace(name), "/")
	if id == "" {
		return nil
	}
	idLower := strings.ToLower(id)
	for i := range m.commands {
		if m.commands[i].ID == id || strings.ToLower(m.commands[i].ID) == idLower {
			c := m.commands[i]
			return &c
		}
	}
	return nil
}

// chatCommandSlashCmd expands chat.commands template into a user message and submits.
func (m model) chatCommandSlashCmd(cmd gatewayclient.ChatCommand, arg string) tea.Cmd {
	expanded := expandChatCommandTemplate(cmd.Template, arg)
	if strings.TrimSpace(expanded) == "" {
		return func() tea.Msg {
			return commandDoneMsg{err: fmt.Errorf("command /%s produced empty message", cmd.ID)}
		}
	}
	return m.newTaskCmd(expanded)
}

func expandChatCommandTemplate(template, args string) string {
	// Local copy of providerconfig.ExpandChatCommandTemplate (TUI must not import providerconfig).
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

// commandSlashItems builds completer entries for chat.commands.
func commandSlashItems(commands []gatewayclient.ChatCommand) []slashCommand {
	if len(commands) == 0 {
		return nil
	}
	out := make([]slashCommand, 0, len(commands))
	for _, cmd := range commands {
		name := skillSlashName(cmd.ID)
		if name == "" || isBuiltinSlash(name) {
			continue
		}
		desc := strings.TrimSpace(cmd.Description)
		if desc == "" {
			desc = "command"
		} else {
			desc = "cmd · " + desc
		}
		out = append(out, slashCommand{
			Name: name, Desc: desc,
			Help: name + " [args]  template slash " + cmd.ID,
		})
	}
	return out
}

// skillSlashCmd handles /<skill-id> [message] — instruction-only, no grant expansion.
// Empty arg: toggle skill on the next-submit selection.
// Non-empty arg: ensure skill selected, then submit the message.
// Also refreshes chat.commands when the slash was not cached yet.
func (m model) skillSlashCmd(name, arg string) tea.Cmd {
	id := strings.TrimPrefix(strings.TrimSpace(name), "/")
	id = strings.TrimSpace(id)
	if id == "" {
		return func() tea.Msg {
			return commandDoneMsg{err: fmt.Errorf("unknown command %s (try /help)", name)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		// Prefer chat.commands when catalog was empty at parse time.
		if commands, err := m.gateway.ListChatCommands(ctx); err == nil {
			idLower := strings.ToLower(id)
			for _, cmd := range commands {
				if cmd.ID == id || strings.ToLower(cmd.ID) == idLower {
					expanded := expandChatCommandTemplate(cmd.Template, arg)
					if strings.TrimSpace(expanded) == "" {
						return commandDoneMsg{err: fmt.Errorf("command /%s produced empty message", cmd.ID)}
					}
					// Chain: cache commands on model via status field, submit via submitAfter on empty skill path —
					// submit directly by returning a synthetic path: use status + re-enter is awkward;
					// call gateway submit below via nested — simplest: return submitAfter with empty skill change.
					return commandDoneMsg{
						status:      fmt.Sprintf("command /%s", cmd.ID),
						submitAfter: expanded,
						closeList:   true,
					}
				}
			}
		}
		skills, err := m.gateway.ListSkills(ctx)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		var match *gatewayclient.Skill
		idLower := strings.ToLower(id)
		for i := range skills {
			if skills[i].ID == id || strings.ToLower(skills[i].ID) == idLower {
				sk := skills[i]
				match = &sk
				break
			}
		}
		if match == nil {
			return commandDoneMsg{err: fmt.Errorf("unknown command %s (try /help or /skills)", name)}
		}
		// Prefer catalog id casing.
		skillID := match.ID
		selected := append([]string(nil), m.selectedSkillIDs...)
		arg = strings.TrimSpace(arg)
		if arg == "" {
			selected = toggleSkillID(selected, skillID)
			status := "no skills selected for next submit"
			if n := len(selected); n > 0 {
				status = fmt.Sprintf("%d skill(s) for next submit: %s", n, strings.Join(selected, ", "))
			}
			return commandDoneMsg{skillIDs: selected, skills: skills, status: status}
		}
		// Select (add if missing) then submit message with those skills.
		if !skillSelected(selected, skillID) {
			selected = append(selected, skillID)
		}
		// Apply selection on model via skillIDs, then chain submit in update — use status + skillIDs
		// and let caller submit: return skillIDs then new task from a combined path.
		return commandDoneMsg{
			skillIDs:    selected,
			skills:      skills,
			status:      fmt.Sprintf("skill %s · submitting", skillID),
			submitAfter: arg,
			closeList:   true,
		}
	}
}

// skillSlashItems builds completer entries for discovered skills (built-ins win on name clash).
func skillSlashItems(skills []gatewayclient.Skill) []slashCommand {
	if len(skills) == 0 {
		return nil
	}
	out := make([]slashCommand, 0, len(skills))
	for _, sk := range skills {
		name := skillSlashName(sk.ID)
		if name == "" || isBuiltinSlash(name) {
			continue
		}
		desc := strings.TrimSpace(sk.Name)
		if desc == "" {
			desc = "skill"
		} else {
			desc = "skill · " + desc
		}
		out = append(out, slashCommand{Name: name, Desc: desc, Help: name + "  toggle skill " + sk.ID})
	}
	return out
}
