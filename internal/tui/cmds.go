package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) handleLineCmd(line string) tea.Cmd {
	name, arg := parseSlash(line)
	if name == "" {
		text := strings.TrimSpace(line)
		if m.canSteer() {
			return m.steerCmd(text)
		}
		return m.newTaskCmd(text)
	}
	switch name {
	case "/quit":
		return func() tea.Msg { return commandDoneMsg{quit: true} }
	case "/help":
		return func() tea.Msg { return commandDoneMsg{help: true, status: strings.TrimSpace(helpText())} }
	case "/status":
		return m.statusCommandCmd()
	case "/model":
		return m.modelCommandCmd(arg)
	case "/skills":
		return m.skillsCmd(arg)
	case "/theme":
		return m.themeCommandCmd(arg)
	case "/cron":
		return m.cronCmd(arg)
	case "/compact":
		return m.compactCmd(arg)
	case "/undo":
		return m.undoCmd()
	case "/edit":
		return m.editCmd(false)
	case "/editundo":
		return m.editCmd(true)
	case "/perm":
		return m.permCmd(arg)
	case "/expand":
		return m.expandCmd(arg)
	case "/journey":
		return m.journeyCmd(arg)
	case "/memory":
		return m.memoryCmd(arg)
	case "/refresh-memory":
		return m.refreshMemoryCmd()
	case "/new":
		if strings.TrimSpace(arg) != "" {
			return func() tea.Msg {
				return commandDoneMsg{err: fmt.Errorf("usage: /new")}
			}
		}
		return m.leaveSessionCmd()
	case "/pause", "/resume", "/cancel":
		action, _ := gatewayclient.ParseTaskAction(name)
		return m.taskActionCmd(action, arg)
	case "/retry":
		return m.retryCmd()
	case "/back", "/sessions":
		return func() tea.Msg {
			return commandDoneMsg{openList: listSessions, status: "sessions"}
		}
	case "/tasks":
		return m.tasksCmd(arg)
	default:
		if !isBuiltinSlash(name) {
			if cmd := m.lookupChatCommand(name); cmd != nil {
				return m.chatCommandSlashCmd(*cmd, arg)
			}
			return m.skillSlashCmd(name, arg)
		}
		return func() tea.Msg {
			return commandDoneMsg{err: fmt.Errorf("unknown command %s (try /help)", name)}
		}
	}
}
