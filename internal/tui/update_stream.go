package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (m model) applyModelStream(env modelstream.Envelope) (tea.Model, tea.Cmd) {
	if m.sessionID == "" || m.sessionID == "…" {
		return m, nil
	}
	if env.SessionID != "" && gatewayclient.SessionID(env.SessionID) != m.sessionID {
		return m, nil
	}
	if strings.TrimSpace(env.ParentRunID) != "" {
		return m, nil
	}
	if env.RunID != "" {
		m.liveRunID = gatewayclient.RunID(env.RunID)
	}
	switch env.Event.Type {
	case providerapi.StreamDelta:
		m.liveContent += env.Event.ContentDelta
		m.streamDirty = true
		return m, m.ensureStreamPaint()
	case providerapi.StreamThinking:
		m.liveThinking += env.Event.ThinkingDelta
		m.streamDirty = true
		return m, m.ensureStreamPaint()
	case providerapi.StreamToolCall:
		if env.Event.ToolCall != nil {
			preview := toolCallPreview(env.Event.ToolCall.Name, env.Event.ToolCall.Arguments)
			m.liveTools = append(m.liveTools, contentBlock{
				Kind:     blockToolCall,
				Text:     preview,
				ToolName: env.Event.ToolCall.Name,
				ToolID:   env.Event.ToolCall.ID,
				Key:      "live-tc:" + env.Event.ToolCall.ID,
				Live:     true,
			})
		}
		m.streamDirty = false
		m.paintLiveDraft()
		return m, nil
	case providerapi.StreamComplete:
		m.resetLiveStream()
		m.timeline = dropLiveDraft(m.timeline)
		return m, m.scheduleRefresh(refreshFull)
	default:
		if env.Event.ContentDelta != "" {
			m.liveContent += env.Event.ContentDelta
			m.streamDirty = true
		}
		if env.Event.ThinkingDelta != "" {
			m.liveThinking += env.Event.ThinkingDelta
			m.streamDirty = true
		}
		if m.streamDirty {
			return m, m.ensureStreamPaint()
		}
		return m, nil
	}
}

type streamPaintMsg struct{}

func streamPaintCmd() tea.Cmd {
	return tea.Tick(streamPaintInterval, func(time.Time) tea.Msg { return streamPaintMsg{} })
}

func (m *model) ensureStreamPaint() tea.Cmd {
	if m.streamPaintOn {
		return nil
	}
	m.streamPaintOn = true
	return streamPaintCmd()
}

func (m model) applyStreamPaint() (tea.Model, tea.Cmd) {
	m.streamPaintOn = false
	if !m.streamDirty {
		return m, nil
	}
	m.streamDirty = false
	m.paintLiveDraft()
	return m, nil
}

func (m *model) paintLiveDraft() {
	m.timeline = upsertLiveDraft(m.timeline, m.liveThinking, m.liveContent, m.liveTools)
	m.syncViewport(false)
}

func (m *model) resetLiveStream() {
	m.liveContent = ""
	m.liveThinking = ""
	m.liveTools = nil
	m.liveRunID = ""
	m.streamDirty = false
	m.streamPaintOn = false
	m.streamMD.reset()
}

func dropLiveDraft(items []timelineItem) []timelineItem {
	if n := len(items); n > 0 && items[n-1].Key == "live" {
		return items[:n-1]
	}
	return items
}

func (m model) applyPermPoll(msg permPollDoneMsg) (tea.Model, tea.Cmd) {
	if m.sessionID == "" || m.sessionID == "…" {
		return m, nil
	}
	if msg.err != nil {
		return m, nil
	}
	prev := m.pendingPermCount
	m.permissions = msg.permissions
	m.pendingPermCount = len(msg.permissions)
	if m.pendingPermCount == 0 {
		m.autoOpenedPermList = false
		if m.list == listPermissions {
			m.closeList()
		}
	} else if msg.openList && prev == 0 && m.list == listNone {
		m.openPermissionsWithGrace()
		m.statusMsg = fmt.Sprintf("%d tool permission(s) pending · 1 once · 2 similar · 3 permanent · 4 deny", m.pendingPermCount)
	} else if m.list == listPermissions {
		// Keep picker in sync with latest rows.
		if m.selectedIdx >= len(m.permissions) && len(m.permissions) > 0 {
			m.selectedIdx = len(m.permissions) - 1
		}
	}
	if m.task != nil && m.task.State == gatewayclient.TaskStateRunning {
		m.timeline = patchRunningStatus(m.timeline, runningStatusTitle(m.task, m.pendingPermCount))
		m.syncViewport(false)
	}
	m.layout()
	return m, nil
}

func (m *model) shouldPollPermissions() bool {
	if m.sessionID == "" || m.sessionID == "…" {
		return false
	}
	if m.needsRunPoll() {
		return true
	}
	return m.pendingPermCount > 0 || len(m.questions) > 0
}

func (m model) applyQuestionPoll(msg questionPollDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	prev := len(m.questions)
	m.questions = msg.questions
	if len(m.questions) == 0 {
		m.autoOpenedQList = false
		if m.list == listQuestions {
			m.closeList()
		}
	} else if msg.openList && prev == 0 && m.list == listNone && m.pendingPermCount == 0 {
		m.openList(listQuestions)
		m.autoOpenedQList = true
		m.statusMsg = fmt.Sprintf("%d question(s) · 1–9 · Enter · Type your own · Esc Esc dismiss", len(m.questions))
	} else if m.list == listQuestions && m.selectedIdx >= len(m.questions) && len(m.questions) > 0 {
		m.selectedIdx = len(m.questions) - 1
	}
	m.layout()
	return m, nil
}

func (m model) applySSE(envelope eventapi.Envelope) (tea.Model, tea.Cmd) {
	if envelope.Sequence > m.sseAfter {
		m.sseAfter = envelope.Sequence
	}
	typ := envelope.Type
	// C1: permission.pending / permission.decided → refresh perm queue (still DecidePermission*).
	if strings.HasPrefix(typ, "permission.") {
		if m.sessionID == "" || m.sessionID == "…" {
			return m, nil
		}
		autoOpen := !m.autoOpenedPermList && m.list == listNone && !m.completer.visible
		return m, m.pollPermissionsCmd(autoOpen)
	}
	if strings.HasPrefix(typ, "question.") {
		if m.sessionID == "" || m.sessionID == "…" {
			return m, nil
		}
		if m.pendingPermCount > 0 || m.list == listPermissions {
			return m, nil
		}
		autoOpen := !m.autoOpenedQList && m.list == listNone && !m.completer.visible
		return m, m.pollQuestionsCmd(autoOpen)
	}
	var kind refreshKind
	switch {
	case strings.HasPrefix(typ, "run."):
		kind = refreshRuns
	case strings.HasPrefix(typ, "plan."), strings.HasPrefix(typ, "approval."):
		kind = refreshPlan
	case strings.HasPrefix(typ, "task."):
		kind = refreshTask
	default:
		return m, nil
	}
	// Prefer immediate coalesced refresh over waiting for tick.
	return m, m.scheduleRefresh(kind)
}
