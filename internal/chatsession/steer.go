package chatsession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const maxSteerRunes = 64 * 1024

// SteerResult is the JSON body for POST /v1/sessions/{id}/steer.
type SteerResult struct {
	SessionID string        `json:"session_id"`
	TaskID    kernel.TaskID `json:"task_id"`
	RunID     kernel.RunID  `json:"run_id"`
	ItemID    string        `json:"item_id"`
}

// Steer queues user text for the next step of the session's running turn (ADR-052 R3).
// Persist first, then enqueue so a crash can rebuild from agent_run_records.
func (s *Service) Steer(ctx context.Context, sessionID kernel.SessionID, text string) (SteerResult, error) {
	if ctx == nil {
		return SteerResult{}, errors.New("steer context is required")
	}
	if s == nil || s.agent == nil {
		return SteerResult{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, ErrUnavailable)
	}
	sessionID = kernel.SessionID(strings.TrimSpace(string(sessionID)))
	text = strings.TrimSpace(text)
	if sessionID == "" || text == "" {
		return SteerResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: session id and text are required", ErrInvalidRequest))
	}
	if len([]rune(text)) > maxSteerRunes {
		return SteerResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: steer text exceeds %d runes", ErrInvalidRequest, maxSteerRunes))
	}
	if _, err := s.repository.GetSession(ctx, sessionID); err != nil {
		return SteerResult{}, classify(err)
	}
	taskID, err := s.runningTaskID(ctx, sessionID)
	if err != nil {
		return SteerResult{}, err
	}
	runID := chatRunID(taskID)
	var existing string
	if err := s.db.QueryRowContext(ctx, "SELECT run_id FROM runs WHERE run_id = ?", runID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SteerResult{}, applicationerror.Wrap(applicationerror.CodeConflict, true,
				fmt.Errorf("%w: chat run not started", ErrTurnNotRunning))
		}
		return SteerResult{}, fmt.Errorf("lookup chat run: %w", err)
	}
	inbox := s.agent.Inbox()
	if inbox == nil {
		return SteerResult{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, ErrUnavailable)
	}
	records, err := agent.NewRecordStore(s.db)
	if err != nil {
		return SteerResult{}, err
	}
	itemID := "steer-" + deterministicID(string(sessionID), string(runID), text, s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	if _, err := records.AppendUser(ctx, string(runID), providerapi.Message{
		Role: providerapi.RoleUser, Content: text,
	}); err != nil {
		return SteerResult{}, fmt.Errorf("persist steer: %w", err)
	}
	inbox.Enqueue(agent.InboxItem{
		ID: itemID, Session: string(sessionID), RunID: string(runID), Text: text, Persisted: true,
	})
	slog.Info("chat turn steered", runlog.Attrs("chatsession", "steer", "accepted", runlog.IDs{
		SessionID: string(sessionID), TaskID: string(taskID), RunID: string(runID),
	}, "item_id", itemID)...)
	return SteerResult{SessionID: string(sessionID), TaskID: taskID, RunID: runID, ItemID: itemID}, nil
}

func (s *Service) runningTaskID(ctx context.Context, sessionID kernel.SessionID) (kernel.TaskID, error) {
	var taskID string
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id FROM tasks
		WHERE session_id = ? AND state = ?
		ORDER BY updated_at DESC, task_id DESC LIMIT 1`,
		sessionID, kernel.TaskRunning,
	).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", applicationerror.Wrap(applicationerror.CodeConflict, true,
			fmt.Errorf("%w", ErrTurnNotRunning))
	}
	if err != nil {
		return "", fmt.Errorf("lookup running chat task: %w", err)
	}
	return kernel.TaskID(taskID), nil
}
