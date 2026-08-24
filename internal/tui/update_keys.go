package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Permission modal hotkeys (four tiers) while listPermissions is open.
	if m.list == listPermissions && len(key) == 1 {
		if decision, ok := permHotkeyDecision(key); ok {
			if m.permInGrace() {
				m.statusMsg = "permission grace · 1–4 after a moment"
				return m, nil
			}
			if m.selectedIdx < 0 || m.selectedIdx >= len(m.permissions) {
				return m, nil
			}
			p := m.permissions[m.selectedIdx]
			m.busy = true
			return m, m.permDecideCmd(p.ID, decision)
		}
	}
	if m.list == listQuestions && len(key) == 1 {
		r := key[0]
		if r >= '1' && r <= '9' {
			return m, m.answerSelectedQuestion(int(r - '1'))
		}
	}

	switch key {
	case "ctrl+c":
		m.input.SetValue("")
		m.errMsg = ""
		m.statusMsg = "use /quit to exit"
		m.historyIdx = -1
		m.completer.update("")
		return m, nil

	case "ctrl+p":
		m.input.SetValue("/")
		m.input.MoveToEnd()
		m.refreshCompleter()
		m.layout()
		return m, nil
	case "ctrl+l":
		return m, m.handleLineCmd("/model")
	case "ctrl+s":
		return m, m.handleLineCmd("/sessions")
	case "ctrl+t":
		if len(m.todos) == 0 {
			m.statusMsg = "no session todos"
			return m, nil
		}
		m.pillsExpanded = !m.pillsExpanded
		m.layout()
		m.syncViewport(false)
		return m, nil

	case "esc":
		if m.helpOpen {
			m.helpOpen = false
			m.layout()
			m.syncViewport(true)
			return m, nil
		}
		if m.completer.visible {
			m.completer.dismiss()
			m.layout()
			return m, nil
		}
		if m.list != listNone {
			m.closeList()
			m.layout()
			return m, nil
		}
		if m.runActivity() == activityActive && m.task != nil {
			m.statusMsg = "cancelling…"
			return m, m.taskActionCmd(gatewayclient.TaskActionCancel, "esc")
		}
		if m.escUndoPending() {
			m.lastEscAt = time.Time{}
			return m, m.undoCmd()
		}
		m.lastEscAt = time.Now()
		m.input.SetValue("")
		m.errMsg = ""
		m.historyIdx = -1
		m.completer.update("")
		m.statusMsg = "esc again to undo last file edit"
		return m, nil

	case "tab":
		if m.completer.visible {
			if name := m.completer.accept(); name != "" {
				if strings.HasPrefix(name, "/") {
					m.input.SetValue(name + " ")
				} else {
					// Argument completion: replace from first space.
					cur := m.input.Value()
					if i := strings.IndexByte(cur, ' '); i >= 0 {
						cmd := cur[:i]
						m.input.SetValue(cmd + " " + name + " ")
					} else {
						m.input.SetValue(name + " ")
					}
				}
				m.input.MoveToEnd()
				m.refreshCompleter()
			}
			m.layout()
			return m, nil
		}
		m.cycleDraftMode(1)
		m.statusMsg = "mode " + string(m.draftMode)
		return m, m.patchStanceCmd()

	case "shift+tab":
		m.cycleDraftMode(-1)
		m.statusMsg = "mode " + string(m.draftMode)
		return m, m.patchStanceCmd()

	case "ctrl+pgup":
		return m, m.cycleSession(1)
	case "ctrl+pgdown":
		return m, m.cycleSession(-1)

	case "pgup":
		// Always scroll conversation — pickers do not steal this.
		m.viewport.ScrollUp(5)
		m.stickBottom = m.viewport.AtBottom()
		return m, nil

	case "pgdown":
		m.viewport.ScrollDown(5)
		m.stickBottom = m.viewport.AtBottom()
		return m, nil

	case "e", "E", "shift+e":
		if m.list == listNone && !m.completer.visible && !m.helpOpen &&
			strings.TrimSpace(m.input.Value()) == "" {
			if key == "e" {
				keys := collectExpandKeys(m.timeline)
				if len(keys) > 0 {
					m.expand.toggle(keys[len(keys)-1])
					m.statusMsg = "expand toggled · /expand all|none"
					m.syncViewport(true)
				}
			} else {
				m.expand.setAll(true)
				m.statusMsg = "expanded all · c or /expand none to collapse"
				m.syncViewport(true)
			}
			return m, nil
		}

	case "c":
		if m.list == listNone && !m.completer.visible && !m.helpOpen &&
			strings.TrimSpace(m.input.Value()) == "" {
			m.expand.setAll(false)
			m.statusMsg = "collapsed"
			m.syncViewport(true)
			return m, nil
		}

	case "up":
		if m.completer.visible {
			m.completer.move(-1)
			return m, nil
		}
		if m.inListMode() {
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
			return m, nil
		}
		if strings.TrimSpace(m.input.Value()) == "" || m.historyIdx >= 0 {
			m.historyPrev()
			return m, nil
		}
		m.viewport.ScrollUp(1)
		m.stickBottom = false
		return m, nil

	case "down":
		if m.completer.visible {
			m.completer.move(1)
			return m, nil
		}
		if m.inListMode() {
			if m.selectedIdx < m.listLen()-1 {
				m.selectedIdx++
			}
			return m, nil
		}
		if m.historyIdx >= 0 {
			m.historyNext()
			return m, nil
		}
		m.viewport.ScrollDown(1)
		m.stickBottom = m.viewport.AtBottom()
		return m, nil

	case "enter":
		// Two-stage slash completer: first Enter completes prefix → full name;
		// second Enter (or Enter when already complete) executes.
		// In arg mode, first Enter inserts the arg into the line.
		if m.completer.visible {
			name := m.completer.selectedName()
			if name != "" {
				typed := strings.TrimSpace(m.input.Value())
				if m.completer.argMode {
					// Build full command line from cmd + selected arg.
					space := strings.IndexByte(typed, ' ')
					cmd := typed
					if space >= 0 {
						cmd = typed[:space]
					}
					line := strings.TrimSpace(cmd + " " + name)
					m.completer.dismiss()
					m.input.SetValue("")
					m.historyIdx = -1
					m.pushHistory(line)
					m.helpOpen = false
					m.busy = true
					m.layout()
					return m, m.handleLineCmd(line)
				}
				if !inputIsCompleteCommand(typed, name) {
					m.completer.dismiss()
					m.input.SetValue(name)
					m.input.MoveToEnd()
					m.historyIdx = -1
					m.refreshCompleter()
					m.layout()
					return m, nil
				}
				m.completer.dismiss()
				m.input.SetValue("")
				m.historyIdx = -1
				m.pushHistory(name)
				m.helpOpen = false
				m.busy = true
				m.layout()
				return m, m.handleLineCmd(name)
			}
		}
		line := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		m.completer.dismiss()
		m.historyIdx = -1
		if line == "" {
			if m.inListMode() && m.listLen() > 0 {
				cmd := m.listEnter()
				m.layout()
				m.syncViewport(false)
				return m, cmd
			}
			return m, nil
		}
		m.pushHistory(line)
		m.helpOpen = false
		m.busy = true
		if cmd, ok := m.optimisticNew(line); ok {
			m.layout()
			m.syncViewport(true)
			return m, cmd
		}
		m.layout()
		return m, m.handleLineCmd(line)
	}

	prevVisible := m.completer.visible
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.completer.update(m.input.Value())
	m.historyIdx = -1
	if m.completer.visible != prevVisible {
		m.layout()
	}
	return m, cmd
}

func (m model) escUndoPending() bool {
	return !m.lastEscAt.IsZero() && time.Since(m.lastEscAt) <= 800*time.Millisecond
}

// optimisticNew paints a local user message before Submit returns.
func (m *model) optimisticNew(line string) (tea.Cmd, bool) {
	name, _ := parseSlash(line)
	if name != "" {
		return nil, false
	}
	objective := strings.TrimSpace(line)
	if objective == "" {
		return nil, false
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	execMode := m.submitExecutionMode()
	// Both agent (build) and plan (read-only) are chat turns.
	m.task = &gatewayclient.Task{
		ID:            "…",
		Title:         gatewayclient.TaskTitle(objective),
		Objective:     objective,
		State:         gatewayclient.TaskStateRunning,
		ExecutionMode: execMode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if m.sessionID != "" {
		sid := m.sessionID
		m.task.SessionID = &sid
	}
	// Append optimistic user bubble to chat transcript.
	m.messages = append(m.messages, gatewayclient.TranscriptMessage{
		ID:        "local-user:" + now,
		SessionID: m.sessionID,
		Role:      "user",
		Content:   objective,
		CreatedAt: now,
	})
	m.timeline = buildChatTimeline(m.messages, m.task, m.plan, m.runs)
	if m.canSteer() {
		m.statusMsg = "steering…"
	} else if m.sessionID != "" {
		m.statusMsg = "sending…"
	} else {
		m.statusMsg = "starting session…"
	}
	m.errMsg = ""
	m.stickBottom = true
	return m.handleLineCmd(line), true
}

func (m *model) pushHistory(line string) {
	if line == "" {
		return
	}
	if n := len(m.history); n > 0 && m.history[n-1] == line {
		return
	}
	m.history = append(m.history, line)
	if len(m.history) > historyLimit {
		m.history = m.history[len(m.history)-historyLimit:]
	}
}

func (m *model) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.historyIdx < 0 {
		m.historyIdx = len(m.history) - 1
	} else if m.historyIdx > 0 {
		m.historyIdx--
	}
	m.input.SetValue(m.history[m.historyIdx])
	m.input.MoveToEnd()
	m.completer.update(m.input.Value())
}

func (m *model) historyNext() {
	if m.historyIdx < 0 {
		return
	}
	if m.historyIdx >= len(m.history)-1 {
		m.historyIdx = -1
		m.input.SetValue("")
		m.completer.update("")
		return
	}
	m.historyIdx++
	m.input.SetValue(m.history[m.historyIdx])
	m.input.MoveToEnd()
	m.completer.update(m.input.Value())
}

func (m *model) needsRunPoll() bool {
	for _, run := range m.runs {
		switch run.State {
		case gatewayclient.RunStateCompleted, gatewayclient.RunStateFailed, gatewayclient.RunStateCancelled:
		default:
			return true
		}
	}
	if m.task != nil {
		switch m.task.State {
		case gatewayclient.TaskStateCompleted, gatewayclient.TaskStateFailed, gatewayclient.TaskStateCancelled:
			return false
		case gatewayclient.TaskStateRunning:
			return true
		}
	}
	return false
}
