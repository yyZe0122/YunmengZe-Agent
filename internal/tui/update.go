package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.syncViewport(true)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case streamPaintMsg:
		return m.applyStreamPaint()

	case tickMsg:
		if m.isLanding() {
			m.animFrame++
			m.syncViewport(true)
		}
		var cmds []tea.Cmd
		cmds = append(cmds, tickCmd())
		if m.dirty && !m.refreshing {
			cmds = append(cmds, m.scheduleRefresh(refreshFull))
		} else if !m.refreshing && m.needsRunPoll() && time.Since(m.lastRunPoll) >= runPollInterval {
			cmds = append(cmds, m.scheduleRefresh(refreshFull))
		}
		if m.shouldPollPermissions() && time.Since(m.lastPermPoll) >= permPollInterval {
			m.lastPermPoll = time.Now()
			autoOpen := !m.autoOpenedPermList && m.list == listNone && !m.completer.visible
			cmds = append(cmds, m.pollPermissionsCmd(autoOpen))
			if m.pendingPermCount == 0 {
				qOpen := !m.autoOpenedQList && m.list == listNone && !m.completer.visible
				cmds = append(cmds, m.pollQuestionsCmd(qOpen))
			}
		}
		return m, tea.Batch(cmds...)

	case permPollDoneMsg:
		return m.applyPermPoll(msg)

	case questionPollDoneMsg:
		return m.applyQuestionPoll(msg)

	case refreshDoneMsg:
		return m.applyRefresh(msg)

	case statusDoneMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			if msg.model.Model != "" {
				m.modelName = msg.model.Model
			}
			m.models = msg.model.Models
			m.contextWindow = msg.model.ContextWindow
			if msg.mcpOK {
				m.mcpStatus = msg.mcp
				m.mcpOK = true
			}
			if msg.skills != nil {
				m.skills = msg.skills
			}
			if msg.commands != nil {
				m.commands = msg.commands
			}
			if msg.health.Core.Runtime.DataDir != "" {
				m.dataDir = msg.health.Core.Runtime.DataDir
			}
			if msg.health.OK && m.statusMsg == "daemon ok" {
				m.statusMsg = ""
			}
		}
		return m, nil

	case commandDoneMsg:
		return m.applyCommand(msg)

	case sseEventMsg:
		return m.applySSE(msg.envelope)

	case modelStreamMsg:
		return m.applyModelStream(msg.env)

	case sseStateMsg:
		var cmd tea.Cmd
		if msg.state != "" {
			prev := m.sseState
			m.sseState = msg.state
			if msg.state == "ok" && prev == "reconnecting" {
				cmd = m.scheduleRefresh(refreshFull)
			}
		}
		return m, cmd

	}

	var cmd tea.Cmd
	prevH := m.input.Height()
	prevVisible := m.completer.visible
	m.input, cmd = m.input.Update(msg)
	m.refreshCompleter()
	if m.input.Height() != prevH || m.completer.visible != prevVisible {
		m.layout()
		m.syncViewport(false)
	}
	return m, cmd
}

func (m *model) refreshCompleter() {
	permIDs := make([]string, 0, len(m.permissions))
	for _, p := range m.permissions {
		permIDs = append(permIDs, p.ID)
	}
	m.completer.updateWith(m.input.Value(), m.models, permIDs, m.extraSlashItems())
}

// extraSlashItems: chat.commands then skills (builtins win via isBuiltinSlash filters).
func (m model) extraSlashItems() []slashCommand {
	cmds := commandSlashItems(m.commands)
	skills := skillSlashItems(m.skills)
	// Drop skills that collide with command ids (commands win).
	if len(cmds) == 0 {
		return skills
	}
	cmdNames := make(map[string]struct{}, len(cmds))
	for _, c := range cmds {
		cmdNames[strings.ToLower(c.Name)] = struct{}{}
	}
	out := make([]slashCommand, 0, len(cmds)+len(skills))
	out = append(out, cmds...)
	for _, s := range skills {
		if _, ok := cmdNames[strings.ToLower(s.Name)]; ok {
			continue
		}
		out = append(out, s)
	}
	return out
}
