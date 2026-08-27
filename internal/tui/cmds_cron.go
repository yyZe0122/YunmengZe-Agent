package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

func (m model) cronCmd(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			defer cancel()
			jobs, err := m.gateway.ListJobs(ctx, false)
			if err != nil {
				return commandDoneMsg{err: err}
			}
			return commandDoneMsg{openList: listJobs, jobs: jobs, status: "scheduled jobs · /cron <every> <objective> to create"}
		}
	}
	return m.cronCreateCmd(arg)
}

// cronCreateCmd: /cron <every> <objective> on the current session (TUI primary path).
// Execution mode and skills follow the Tab draft (agent|plan|auto → agent|plan) and /skills.
func (m model) cronCreateCmd(arg string) tea.Cmd {
	return func() tea.Msg {
		everyRaw, objective, ok := splitCronCreateArg(arg)
		if !ok {
			return commandDoneMsg{err: fmt.Errorf("usage: /cron <every> <objective>  (e.g. /cron 15m check status)")}
		}
		every, err := parseCronEvery(everyRaw)
		if err != nil {
			return commandDoneMsg{err: err}
		}
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "" || sessionID == "…" {
			return commandDoneMsg{err: fmt.Errorf("focus a session first (send a message or /sessions), then /cron")}
		}
		execMode := m.submitExecutionMode()
		key, err := gatewayclient.RandomID("job-")
		if err != nil {
			return commandDoneMsg{err: err}
		}
		title := gatewayclient.TaskTitle(objective)
		name := title
		if len(name) > 40 {
			name = name[:40]
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		modelRef := ""
		if cfg, err := m.gateway.ModelConfig(ctx); err == nil {
			modelRef = strings.TrimSpace(cfg.Model)
		}
		job, err := m.gateway.CreateJob(ctx, schedulerapi.CreateRequest{
			Name: name, SessionID: sessionID, TaskTitle: title, TaskObjective: objective,
			ExecutionMode: execMode, SkillIDs: append([]string(nil), m.selectedSkillIDs...),
			ModelRef: modelRef, IntervalSeconds: int64(every.Seconds()), IdempotencyKey: key,
		})
		if err != nil {
			return commandDoneMsg{err: err}
		}
		pinNote := modelRef
		if pinNote == "" {
			pinNote = "main"
		}
		jobs, listErr := m.gateway.ListJobs(ctx, false)
		if listErr != nil {
			return commandDoneMsg{
				status: fmt.Sprintf("created job %s every %s (%s, pin %s)", shortID(job.ID), every, execMode, pinNote),
			}
		}
		return commandDoneMsg{
			openList: listJobs, jobs: jobs,
			status: fmt.Sprintf("created job %s every %s (%s, pin %s)", shortID(job.ID), every, execMode, pinNote),
		}
	}
}

func splitCronCreateArg(arg string) (every, objective string, ok bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", false
	}
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	every = strings.TrimSpace(parts[0])
	objective = strings.TrimSpace(parts[1])
	if every == "" || objective == "" {
		return "", "", false
	}
	return every, objective, true
}

func parseCronEvery(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q (use Go duration, e.g. 15m, 1h)", raw)
	}
	if d < time.Second {
		return 0, fmt.Errorf("interval must be at least 1s")
	}
	return d, nil
}
