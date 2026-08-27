package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

// leaveSessionCmd unfocuses to the ready page. A running turn is cancelled first.
func (m model) leaveSessionCmd() tea.Cmd {
	task := m.task
	poll := m.needsRunPoll()
	return func() tea.Msg {
		done := commandDoneMsg{clearTask: true, status: "new session"}
		if task == nil || task.ID == "" || task.ID == "…" || !poll {
			return done
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		current, err := m.gateway.GetTask(ctx, task.ID)
		if err != nil {
			done.err = err
			return done
		}
		if _, err := m.gateway.ControlTask(ctx, current.ID, gatewayclient.TaskActionCancel, current.Version, "new"); err != nil {
			done.err = err
		}
		return done
	}
}

func (m model) tasksCmd(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	return func() tea.Msg {
		if arg == "" {
			return commandDoneMsg{openList: listTasks, status: "tasks"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		tasks, err := m.gateway.ListTasks(ctx, 50)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		arg = strings.ToLower(arg)
		var match *gatewayclient.Task
		for i := range tasks {
			id := strings.ToLower(string(tasks[i].ID))
			if strings.HasPrefix(id, arg) || strings.Contains(id, arg) {
				if match != nil {
					return commandDoneMsg{openList: listSessions, err: fmt.Errorf("ambiguous task prefix %q", arg)}
				}
				t := tasks[i]
				match = &t
			}
		}
		if match == nil {
			return commandDoneMsg{err: fmt.Errorf("no task matching %q", arg)}
		}
		return commandDoneMsg{taskID: match.ID, closeList: true, status: fmt.Sprintf("focused %s", match.ID)}
	}
}

func (m model) retryCmd() tea.Cmd {
	return func() tea.Msg {
		objective := lastUserMessage(m.messages)
		if objective == "" {
			return commandDoneMsg{err: fmt.Errorf("no user message to retry on focused session")}
		}
		if m.sessionID == "" || m.sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first")}
		}
		return m.newTaskCmd(objective)()
	}
}

func lastUserMessage(messages []gatewayclient.TranscriptMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func (m model) patchStanceCmd() tea.Cmd {
	sessionID := m.sessionID
	if sessionID == "" || sessionID == "…" {
		return nil
	}
	stance := m.submitPermissionStance()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		if _, err := m.gateway.SetSessionPermissionStance(ctx, sessionID, stance); err != nil {
			return commandDoneMsg{err: err}
		}
		return commandDoneMsg{}
	}
}

func (m model) canSteer() bool {
	if m.sessionID == "" || m.sessionID == "…" {
		return false
	}
	if m.task == nil || m.task.ID == "" || m.task.ID == "…" {
		return false
	}
	return m.task.State == gatewayclient.TaskStateRunning
}

func isSteerConflict(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "409") && strings.Contains(s, "conflict")
}

func (m model) steerCmd(text string) tea.Cmd {
	text = strings.TrimSpace(text)
	sessionID := m.sessionID
	return func() tea.Msg {
		if text == "" {
			return commandDoneMsg{err: fmt.Errorf("empty message")}
		}
		if sessionID == "" || sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		result, err := m.gateway.SteerSession(ctx, sessionID, text)
		if err != nil {
			if isSteerConflict(err) {
				done := m.newTaskCmd(text)()
				if msg, ok := done.(commandDoneMsg); ok && msg.err == nil {
					msg.status = "turn ended · sent as new message"
					return msg
				}
				return done
			}
			return commandDoneMsg{err: err}
		}
		return commandDoneMsg{
			status:    fmt.Sprintf("steering · next step · %s", shortID(result.ItemID)),
			sessionID: sessionID,
			taskID:    gatewayclient.TaskID(result.TaskID),
		}
	}
}

func (m model) newTaskCmd(objective string) tea.Cmd {
	objective = strings.TrimSpace(objective)
	execMode := m.submitExecutionMode()
	stance := m.submitPermissionStance()
	sessionID := m.sessionID
	if sessionID == "…" {
		sessionID = ""
	}
	skillIDs := append([]string(nil), m.selectedSkillIDs...)
	return func() tea.Msg {
		if objective == "" {
			return commandDoneMsg{err: fmt.Errorf("empty message")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		req := gatewayclient.TaskSubmissionRequest{
			Title: gatewayclient.TaskTitle(objective), Objective: objective,
			ExecutionMode: execMode, Workspace: m.cwd,
			PermissionStance: stance, Interactive: true,
		}
		if sessionID != "" {
			req.SessionID = sessionID
		}
		if len(skillIDs) > 0 {
			req.SkillIDs = skillIDs
		}
		submitted, err := m.gateway.SubmitTask(ctx, req)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		sid := sessionID
		if submitted.Task.SessionID != nil && *submitted.Task.SessionID != "" {
			sid = *submitted.Task.SessionID
		}
		label := "build"
		switch stance {
		case "plan":
			label = "plan (read-only)"
		case "auto":
			label = "auto (session pre-grant)"
		}
		status := fmt.Sprintf("%s · task %s", label, shortID(string(submitted.Task.ID)))
		if sid != "" {
			status = fmt.Sprintf("%s · session %s · task %s", label, shortID(string(sid)), shortID(string(submitted.Task.ID)))
		}
		return commandDoneMsg{
			status:    status,
			taskID:    submitted.Task.ID,
			planID:    "",
			sessionID: sid,
		}
	}
}

func (m model) taskActionCmd(action gatewayclient.TaskAction, reason string) tea.Cmd {
	return func() tea.Msg {
		if m.task == nil {
			return commandDoneMsg{err: fmt.Errorf("no current task")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		current, err := m.gateway.GetTask(ctx, m.task.ID)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		updated, err := m.gateway.ControlTask(ctx, current.ID, action, current.Version, reason)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		return commandDoneMsg{status: fmt.Sprintf("task %s → %s", updated.ID, updated.State), taskID: updated.ID}
	}
}
