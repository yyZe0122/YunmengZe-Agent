package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) applyRefresh(msg refreshDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != 0 && msg.gen != m.refreshGen {
		// Stale response from a superseded refresh.
		return m, nil
	}
	m.refreshing = false
	m.busy = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
	} else {
		m.errMsg = ""
		if msg.tasks != nil {
			m.tasks = msg.tasks
		}
		if msg.sessions != nil {
			m.sessions = msg.sessions
		}
		if msg.task != nil {
			m.task = msg.task
			if msg.task.SessionID != nil && *msg.task.SessionID != "" {
				m.sessionID = *msg.task.SessionID
			}
		}
		// Full refresh replaces optional slices (including nil = none).
		// Partial kinds only patch when the fan-out produced a value.
		switch msg.kind {
		case refreshFull:
			m.plan = msg.plan
			if msg.plan != nil {
				m.planID = msg.plan.ID
			}
			m.runs = msg.runs
			if msg.messages != nil {
				m.messages = msg.messages
			}
		default:
			if msg.plan != nil {
				m.plan = msg.plan
				m.planID = msg.plan.ID
			}
			if msg.runs != nil {
				m.runs = msg.runs
			}
			if msg.messages != nil {
				m.messages = msg.messages
			}
		}
		if msg.usageOK {
			m.usage = msg.usage
			m.usageOK = true
		}
		if msg.runUsageOK {
			m.runUsage = msg.runUsage
			m.runUsageOK = true
		}
		if msg.contextOK {
			m.taskContext = msg.taskContext
			m.contextOK = true
		}
		if msg.messages != nil && !m.keepLiveDraft() {
			m.resetLiveStream()
		}
		if msg.todosOK {
			m.todos = msg.todos
			if len(m.todos) == 0 {
				m.pillsExpanded = false
			}
		}
		m.timeline = buildChatTimeline(m.messages, m.task, m.plan, m.runs)
		if len(m.journeyRows) > 0 {
			m.timeline = append(append([]timelineItem(nil), m.journeyRows...), m.timeline...)
		}
		if m.pendingPermCount > 0 {
			m.timeline = patchRunningStatus(m.timeline, runningStatusTitle(m.task, m.pendingPermCount))
		}
		if m.liveContent != "" || m.liveThinking != "" || len(m.liveTools) > 0 {
			m.timeline = upsertLiveDraft(m.timeline, m.liveThinking, m.liveContent, m.liveTools)
		}
		m.lastRunPoll = time.Now()
		if n := m.listLen(); n > 0 && m.selectedIdx >= n {
			m.selectedIdx = n - 1
		}
	}
	m.layout()
	m.syncViewport(false)

	var cmds []tea.Cmd
	if m.pendingRefresh || m.dirty {
		m.pendingRefresh = false
		m.dirty = false
		cmds = append(cmds, m.scheduleRefresh(refreshFull))
	}
	if m.shouldPollPermissions() && time.Since(m.lastPermPoll) >= permPollInterval {
		m.lastPermPoll = time.Now()
		autoOpen := !m.autoOpenedPermList && m.list == listNone
		cmds = append(cmds, m.pollPermissionsCmd(autoOpen))
	}
	return m, tea.Batch(cmds...)
}

func (m model) keepLiveDraft() bool {
	if m.liveContent == "" && m.liveThinking == "" && len(m.liveTools) == 0 {
		return false
	}
	if m.runActivity() != activityActive {
		return false
	}
	return !assistantTranscriptCoversLive(m.messages, m.liveContent, m.liveRunID)
}

func assistantTranscriptCoversLive(messages []gatewayclient.TranscriptMessage, live string, liveRun gatewayclient.RunID) bool {
	live = strings.TrimRight(live, "\n")
	if live == "" {
		return false
	}
	start := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(messages[i].Role) == "user" {
			start = i + 1
			break
		}
	}
	for i := len(messages) - 1; i >= start; i-- {
		if strings.ToLower(messages[i].Role) != "assistant" {
			continue
		}
		if liveRun != "" && messages[i].RunID != "" && messages[i].RunID != liveRun {
			continue
		}
		got := strings.TrimRight(messages[i].Content, "\n")
		return got != "" && (got == live || strings.HasPrefix(got, live))
	}
	return false
}

func (m model) applyCommand(msg commandDoneMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.quit {
		m.quitting = true
		return m, tea.Quit
	}
	if msg.help {
		m.helpOpen = true
		m.statusMsg = ""
		m.errMsg = ""
		m.layout()
		return m, nil
	}
	if msg.toggleTheme {
		next := toggleTheme(m.theme)
		if err := saveTheme(m.mode, next); err != nil {
			m.errMsg = err.Error()
		} else {
			m.theme = next
			applyTheme(themeByName(next))
			m.statusMsg = "theme " + string(next)
			m.errMsg = ""
		}
		m.syncViewport(true)
		return m, nil
	}
	if msg.modelName != "" {
		m.modelName = msg.modelName
		m.contextWindow = msg.contextWindow
	}
	if msg.models != nil {
		m.models = msg.models
	}
	if msg.jobs != nil {
		m.jobs = msg.jobs
	}
	if msg.skills != nil {
		m.skills = msg.skills
	}
	if msg.permissions != nil {
		m.permissions = msg.permissions
		m.pendingPermCount = len(msg.permissions)
		if m.pendingPermCount == 0 {
			m.autoOpenedPermList = false
		}
	}
	if msg.questions != nil {
		m.questions = msg.questions
		if len(m.questions) == 0 {
			m.autoOpenedQList = false
		}
	}
	if msg.skillIDs != nil {
		m.selectedSkillIDs = append([]string(nil), msg.skillIDs...)
	}
	if msg.draftInput != "" {
		m.input.SetValue(msg.draftInput)
		m.input.MoveToEnd()
		m.historyIdx = -1
		m.completer.update(m.input.Value())
	}
	if msg.expandMode != "" {
		switch msg.expandMode {
		case "all":
			m.expand.setAll(true)
		case "none":
			m.expand.setAll(false)
		case "last":
			keys := collectExpandKeys(m.timeline)
			if len(keys) > 0 {
				m.expand.toggle(keys[len(keys)-1])
			}
		}
		m.tlCache = timelineRenderCache{} // expand changes render output
	}
	if msg.setJourney {
		m.journeyRows = msg.journeyRows
		// Rebuild timeline so journey rows appear without waiting for refresh.
		m.timeline = buildChatTimeline(m.messages, m.task, m.plan, m.runs)
		if len(m.journeyRows) > 0 {
			m.timeline = append(append([]timelineItem(nil), m.journeyRows...), m.timeline...)
		}
		if m.liveContent != "" || m.liveThinking != "" || len(m.liveTools) > 0 {
			m.timeline = upsertLiveDraft(m.timeline, m.liveThinking, m.liveContent, m.liveTools)
		}
	}
	submitAfter := strings.TrimSpace(msg.submitAfter)
	if msg.dropTaskFocus {
		m.task = nil
		m.plan = nil
		m.planID = ""
		m.runs = nil
		m.resetLiveStream()
	}
	if msg.clearTask {
		m.sessionID = ""
		m.task = nil
		m.plan = nil
		m.planID = ""
		m.runs = nil
		m.messages = nil
		m.timeline = nil
		m.journeyRows = nil
		m.tlCache = timelineRenderCache{}
		m.resetLiveStream()
		m.usage = gatewayclient.TaskUsage{}
		m.usageOK = false
		m.runUsage = gatewayclient.RunUsage{}
		m.runUsageOK = false
		m.taskContext = gatewayclient.TaskContext{}
		m.contextOK = false
		m.viewportContent = ""
		m.refreshGen++
		m.refreshing = false
		m.pendingRefresh = false
		m.dirty = false
		m.permissions = nil
		m.pendingPermCount = 0
		m.autoOpenedPermList = false
		m.questions = nil
		m.autoOpenedQList = false
		m.todos = nil
		m.pillsExpanded = false
		if m.list == listPermissions {
			m.closeList()
		}
	}
	if msg.sessions != nil {
		m.sessions = msg.sessions
	}
	if msg.closeList {
		m.closeList()
	}
	if msg.openList != listNone {
		m.openList(msg.openList)
	}
	needRefresh := false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		if msg.status != "" {
			m.statusMsg = msg.status
		} else {
			m.statusMsg = "request failed"
		}
		// Keep optimistic transcript so the user sees what failed; clear only the
		// placeholder task id so refresh does not focus a non-existent task.
		if m.task != nil && m.task.ID == "…" {
			m.task = nil
			m.timeline = buildChatTimeline(m.messages, nil, m.plan, m.runs)
		}
		if msg.sessionID != "" {
			needRefresh = true
		}
	} else {
		m.errMsg = ""
		if msg.status != "" {
			m.statusMsg = msg.status
		}
		if msg.sessionID != "" {
			m.sessionID = msg.sessionID
			needRefresh = true
		}
		if msg.taskID != "" {
			id := msg.taskID
			// Keep optimistic objective if we already focused this task/placeholder.
			if m.task == nil || (m.task.ID != id && m.task.ID != "…") {
				m.task = &gatewayclient.Task{ID: id, State: gatewayclient.TaskStateRunning, ExecutionMode: m.submitExecutionMode()}
			} else {
				m.task.ID = id
				if m.task.State == "" {
					m.task.State = gatewayclient.TaskStateRunning
				}
			}
			if msg.sessionID != "" {
				sid := msg.sessionID
				m.task.SessionID = &sid
			}
			m.closeList()
			needRefresh = true
		}
		if msg.planID != "" {
			m.planID = msg.planID
			needRefresh = true
		}
		if msg.openList == listSessions || msg.openList == listTasks {
			needRefresh = true
		}
	}
	m.layout()
	m.syncViewport(false)
	var cmds []tea.Cmd
	if needRefresh {
		cmds = append(cmds, m.scheduleRefresh(refreshFull))
	}
	if submitAfter != "" && msg.err == nil {
		cmds = append(cmds, m.newTaskCmd(submitAfter))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	if len(cmds) == 1 {
		return m, cmds[0]
	}
	return m, tea.Batch(cmds...)
}
