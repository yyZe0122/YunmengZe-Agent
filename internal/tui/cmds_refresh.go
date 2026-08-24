package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func (m model) compactCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "" || sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first (send a message or /sessions), then /compact")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		result, err := m.gateway.CompactSession(ctx, gatewayclient.SessionID(sessionID), arg)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		src := result.Source
		if src == "" {
			src = "ok"
		}
		status := fmt.Sprintf("compacted session (%s)", src)
		if f := strings.TrimSpace(arg); f != "" {
			status = fmt.Sprintf("compacted session (%s, focus)", src)
		}
		return commandDoneMsg{status: status}
	}
}

func (m model) undoCmd() tea.Cmd {
	return func() tea.Msg {
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "" || sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first, then /undo")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		result, err := m.gateway.RewindSession(ctx, gatewayclient.SessionID(sessionID), "")
		if err != nil {
			return commandDoneMsg{err: err}
		}
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = result.RevisionID
		}
		return commandDoneMsg{status: "restored " + path}
	}
}

func (m model) expandCmd(arg string) tea.Cmd {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg == "" {
		arg = "last"
	}
	switch arg {
	case "all", "none", "last":
		status := "expand " + arg
		if arg == "last" {
			status = "expand toggled last foldable · e / E / c"
		}
		return func() tea.Msg {
			return commandDoneMsg{expandMode: arg, status: status}
		}
	default:
		return func() tea.Msg {
			return commandDoneMsg{err: fmt.Errorf("usage: /expand [all|none|last]")}
		}
	}
}

func (m model) refreshCmd(gen uint64, kind refreshKind) tea.Cmd {
	gw := m.gateway
	taskID := gatewayclient.TaskID("")
	if m.task != nil && m.task.ID != "" && m.task.ID != "…" {
		taskID = m.task.ID
	}
	sessionID := m.sessionID
	if sessionID == "…" {
		sessionID = ""
	}
	planID := m.planID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()

		msg := refreshDoneMsg{gen: gen, kind: kind}

		if kind == refreshFull {
			if sessions, err := gw.ListSessions(ctx, 30); err == nil {
				msg.sessions = sessions
			}
			if tasks, err := gw.ListTasks(ctx, 20); err == nil {
				msg.tasks = tasks
			} else if msg.sessions == nil {
				msg.err = err
				return msg
			}
		}

		// Transcript: prefer session, else task.
		if sessionID != "" {
			if messages, err := gw.SessionMessages(ctx, sessionID, 200); err == nil {
				msg.messages = messages
			}
			if todos, err := gw.ListSessionTodos(ctx, sessionID); err == nil {
				msg.todos = todos
				msg.todosOK = true
			}
		} else if taskID != "" {
			if messages, err := gw.TaskMessages(ctx, taskID, 200); err == nil {
				msg.messages = messages
			}
		}

		if taskID == "" {
			return msg
		}

		var (
			wg          sync.WaitGroup
			taskMu      sync.Mutex
			taskErr     error
			task        gatewayclient.Task
			plan        *gatewayclient.Plan
			runs        []gatewayclient.Run
			usage       gatewayclient.TaskUsage
			usageOK     bool
			taskContext gatewayclient.TaskContext
			contextOK   bool
		)

		needTask := kind == refreshFull || kind == refreshTask || kind == refreshPlan
		needPlan := kind == refreshFull || kind == refreshPlan || kind == refreshTask
		needRuns := kind == refreshFull || kind == refreshRuns || kind == refreshTask
		needUsage := kind == refreshFull || kind == refreshRuns

		if needTask {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t, err := gw.GetTask(ctx, taskID)
				taskMu.Lock()
				defer taskMu.Unlock()
				if err != nil {
					taskErr = err
					return
				}
				task = t
			}()
		}

		if needPlan {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var p gatewayclient.Plan
				var err error
				if planID != "" {
					p, err = gw.GetPlan(ctx, planID)
				} else {
					p, err = gw.FindPlanForTask(ctx, taskID)
				}
				if err != nil {
					return
				}
				taskMu.Lock()
				plan = &p
				taskMu.Unlock()
			}()
		}

		if needRuns {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := gw.ListRuns(ctx, taskID, 50)
				if err != nil {
					return
				}
				taskMu.Lock()
				runs = r
				taskMu.Unlock()
			}()
		}

		if needUsage {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u, err := gw.TaskUsage(ctx, taskID)
				if err != nil {
					return
				}
				taskMu.Lock()
				usage = u
				usageOK = true
				taskMu.Unlock()
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, err := gw.TaskContext(ctx, taskID)
				if err != nil {
					return
				}
				taskMu.Lock()
				taskContext = c
				contextOK = true
				taskMu.Unlock()
			}()
		}

		wg.Wait()
		if taskErr != nil && kind != refreshRuns {
			if kind == refreshFull || kind == refreshTask {
				msg.err = taskErr
				return msg
			}
		}
		if task.ID != "" {
			msg.task = &task
		}
		msg.plan = plan
		msg.runs = runs
		msg.usage = usage
		msg.usageOK = usageOK
		msg.taskContext = taskContext
		msg.contextOK = contextOK

		// Parent run usage rollup (self + children); pick root parent when present.
		if needUsage && len(runs) > 0 {
			parentID := pickParentRunID(runs)
			if parentID != "" {
				if ru, err := gw.RunUsage(ctx, parentID); err == nil {
					msg.runUsage = ru
					msg.runUsageOK = true
				}
			}
		}
		return msg
	}
}

// pickParentRunID chooses a top-level run (no parent) that has children, else the first root run.
func pickParentRunID(runs []gatewayclient.Run) gatewayclient.RunID {
	hasChild := make(map[string]bool, len(runs))
	for _, r := range runs {
		if r.ParentRunID != nil && strings.TrimSpace(string(*r.ParentRunID)) != "" {
			hasChild[string(*r.ParentRunID)] = true
		}
	}
	var firstRoot gatewayclient.RunID
	for _, r := range runs {
		if r.ParentRunID != nil && strings.TrimSpace(string(*r.ParentRunID)) != "" {
			continue
		}
		if firstRoot == "" {
			firstRoot = r.ID
		}
		if hasChild[string(r.ID)] {
			return r.ID
		}
	}
	return firstRoot
}
