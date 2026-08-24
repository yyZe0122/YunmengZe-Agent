package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) permCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		arg = strings.TrimSpace(arg)
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		if arg == "" {
			items, err := m.gateway.ListPermissions(ctx, sessionID, 20)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			if len(items) == 0 {
				return commandDoneMsg{status: "no pending tool permissions", permissions: items}
			}
			return commandDoneMsg{
				openList:    listPermissions,
				permissions: items,
				status:      fmt.Sprintf("%d pending · Enter once · /perm once|similar|permanent|deny <id>", len(items)),
			}
		}
		parts := strings.Fields(arg)
		if len(parts) != 2 {
			return commandDoneMsg{err: fmt.Errorf("usage: /perm [allow_once|allow_similar|allow_permanent|deny <id-prefix>]")}
		}
		decision := strings.ToLower(strings.TrimSpace(parts[0]))
		prefix := strings.ToLower(strings.TrimSpace(parts[1]))
		switch decision {
		case "once", "allow_once":
			decision = "allow_once"
		case "similar", "allow_similar":
			decision = "allow_similar"
		case "permanent", "allow_permanent", "always":
			decision = "allow_permanent"
		case "deny":
		default:
			return commandDoneMsg{err: fmt.Errorf("decision must be allow_once, allow_similar, allow_permanent, or deny")}
		}
		items, err := m.gateway.ListPermissions(ctx, sessionID, 50)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		var match *gatewayclient.Permission
		for i := range items {
			id := strings.ToLower(items[i].ID)
			if strings.HasPrefix(id, prefix) || strings.Contains(id, prefix) {
				if match != nil {
					return commandDoneMsg{err: fmt.Errorf("ambiguous permission prefix %q", prefix)}
				}
				t := items[i]
				match = &t
			}
		}
		if match == nil {
			return commandDoneMsg{err: fmt.Errorf("no pending permission matching %q", prefix)}
		}
		var updated gatewayclient.Permission
		var decideErr error
		if decision == "allow_permanent" {
			// Typed /perm permanent assumes explicit user intent (= second confirm).
			updated, decideErr = m.gateway.DecidePermissionConfirm(ctx, match.ID, decision, true)
		} else {
			updated, decideErr = m.gateway.DecidePermission(ctx, match.ID, decision)
		}
		if decideErr != nil {
			return commandDoneMsg{err: decideErr}
		}
		remaining, _ := m.gateway.ListPermissions(ctx, sessionID, 20)
		msg := commandDoneMsg{
			status:      fmt.Sprintf("permission %s → %s (%s)", shortID(updated.ID), decision, updated.ToolName),
			permissions: remaining,
		}
		if len(remaining) == 0 {
			msg.closeList = true
		}
		return msg
	}
}

func (m model) permDecideCmd(permissionID, decision string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		var updated gatewayclient.Permission
		var err error
		if decision == "allow_permanent" {
			// Hotkey / Enter cycle for permanent requires confirm (same as typed /perm).
			updated, err = m.gateway.DecidePermissionConfirm(ctx, permissionID, decision, true)
		} else {
			updated, err = m.gateway.DecidePermission(ctx, permissionID, decision)
		}
		if err != nil {
			return commandDoneMsg{err: err}
		}
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		remaining, _ := m.gateway.ListPermissions(ctx, sessionID, 20)
		msg := commandDoneMsg{
			status:      fmt.Sprintf("permission %s → %s (%s)", shortID(updated.ID), decision, updated.ToolName),
			permissions: remaining,
		}
		if len(remaining) == 0 {
			msg.closeList = true
		} else {
			msg.openList = listPermissions
		}
		return msg
	}
}

func (m model) pollPermissionsCmd(autoOpen bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		items, err := m.gateway.ListPermissions(ctx, sessionID, 20)
		if err != nil {
			return permPollDoneMsg{err: err}
		}
		return permPollDoneMsg{permissions: items, openList: autoOpen && len(items) > 0}
	}
}

func (m model) pollQuestionsCmd(autoOpen bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		items, err := m.gateway.ListQuestions(ctx, sessionID, 20)
		if err != nil {
			return questionPollDoneMsg{err: err}
		}
		return questionPollDoneMsg{questions: items, openList: autoOpen && len(items) > 0}
	}
}

func (m model) answerQuestionCmd(id string, answers map[string][]string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		_, err := m.gateway.AnswerQuestion(ctx, id, "local-user", answers)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		remaining, _ := m.gateway.ListQuestions(ctx, sessionID, 20)
		msg := commandDoneMsg{status: "answered " + shortID(id), questions: remaining}
		if len(remaining) == 0 {
			msg.closeList = true
		} else {
			msg.openList = listQuestions
		}
		return msg
	}
}
