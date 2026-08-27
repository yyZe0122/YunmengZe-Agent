package chatsession

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
)

// RetractResult is the outcome of hiding the last visible chat turn.
type RetractResult struct {
	SessionID     string   `json:"session_id"`
	TaskID        string   `json:"task_id"`
	UserText      string   `json:"user_text"`
	RewindFiles   bool     `json:"rewind_files"`
	RewoundPaths  []string `json:"rewound_paths,omitempty"`
	FailedPath    string   `json:"failed_path,omitempty"`
	FailedReason  string   `json:"failed_reason,omitempty"`
	CancelledTurn bool     `json:"cancelled_turn,omitempty"`
}

func (s *Service) RetractLastTurn(ctx context.Context, sessionID kernel.SessionID, rewindFiles bool) (RetractResult, error) {
	if ctx == nil {
		return RetractResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: context is required", ErrInvalidRequest))
	}
	if s == nil || s.repository == nil {
		return RetractResult{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, ErrUnavailable)
	}
	sessionID = kernel.SessionID(strings.TrimSpace(string(sessionID)))
	if sessionID == "" {
		return RetractResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: session id is required", ErrInvalidRequest))
	}
	session, err := s.repository.GetSession(ctx, sessionID)
	if err != nil {
		return RetractResult{}, classify(err)
	}
	hidden := make(map[string]struct{}, len(session.HiddenTaskIDs))
	for _, id := range session.HiddenTaskIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			hidden[id] = struct{}{}
		}
	}
	tasks, err := s.repository.ListSessionTasks(ctx, sessionID)
	if err != nil {
		return RetractResult{}, classify(err)
	}
	var last kernel.Task
	for i := len(tasks) - 1; i >= 0; i-- {
		if _, skip := hidden[string(tasks[i].ID)]; skip {
			continue
		}
		last = tasks[i]
		break
	}
	if last.ID == "" {
		return RetractResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
			fmt.Errorf("%w: no turn to retract", ErrInvalidRequest))
	}
	ids := runlog.IDs{SessionID: string(sessionID), TaskID: string(last.ID)}
	userText := strings.TrimSpace(last.Objective)
	if userText == "" {
		userText = strings.TrimSpace(last.Title)
	}
	result := RetractResult{
		SessionID:   string(sessionID),
		TaskID:      string(last.ID),
		UserText:    userText,
		RewindFiles: rewindFiles,
	}

	cancelled := false
	if last.State == kernel.TaskRunning || last.State == kernel.TaskPaused {
		if _, err := s.repository.CancelTask(ctx, last.ID, last.Version, "retract", s.now()); err != nil {
			slog.Warn("session retract cancel failed", runlog.Attrs("chatsession", "retract", "failed", ids, "error", err, "reason", "cancel")...)
			return RetractResult{}, classify(err)
		}
		s.Interrupt(last.ID)
		if s.approvals != nil {
			if err := s.approvals.RevokeTaskGrants(ctx, last.ID, s.now()); err != nil {
				slog.Error("session retract revoke grants failed", runlog.Attrs("chatsession", "retract", "failed", ids, "error", err, "reason", "revoke_grants")...)
				return RetractResult{}, classify(err)
			}
		}
		cancelled = true
		result.CancelledTurn = true
	}

	runIDs, err := s.repository.ListRunIDsForTask(ctx, last.ID)
	if err != nil {
		slog.Error("session retract list runs failed", runlog.Attrs("chatsession", "retract", "failed", ids, "error", err, "reason", "list_runs")...)
		return result, classify(err)
	}
	runStr := make([]string, 0, len(runIDs))
	for _, id := range runIDs {
		if s := strings.TrimSpace(string(id)); s != "" {
			runStr = append(runStr, s)
		}
	}

	if rewindFiles {
		if s.edits == nil {
			return result, applicationerror.Wrap(applicationerror.CodeUnavailable, false,
				fmt.Errorf("%w: edit checkpoints unavailable", ErrUnavailable))
		}
		revs, listErr := s.edits.ListByRunIDs(ctx, string(sessionID), runStr)
		if listErr != nil {
			slog.Error("session retract list revisions failed", runlog.Attrs("chatsession", "retract_rewind", "failed", ids, "error", listErr, "reason", "list_revisions")...)
			return result, classify(listErr)
		}
		for _, rev := range revs {
			revIDs := ids
			revIDs.RunID = rev.RunID
			got, rewindErr := s.edits.Rewind(ctx, string(sessionID), rev.ID)
			if rewindErr != nil {
				reason := rewindErr.Error()
				result.FailedPath = rev.Path
				result.FailedReason = reason
				slog.Error("session retract rewind failed", runlog.Attrs("chatsession", "retract_rewind", "failed", revIDs,
					"path", rev.Path, "revision_id", rev.ID, "reason", reason, "error", rewindErr)...)
				return result, applicationerror.Wrap(applicationerror.CodeConflict, false, rewindErr)
			}
			result.RewoundPaths = append(result.RewoundPaths, got.Path)
			slog.Info("session retract rewind succeeded", runlog.Attrs("chatsession", "retract_rewind", "succeeded", revIDs,
				"path", got.Path, "revision_id", got.ID)...)
		}
	}

	hiddenIDs := append(append([]string(nil), session.HiddenTaskIDs...), string(last.ID))
	if err := s.repository.SetSessionHiddenTaskIDs(ctx, sessionID, hiddenIDs); err != nil {
		slog.Error("session retract hide failed", runlog.Attrs("chatsession", "retract", "failed", ids, "error", err, "reason", "hide")...)
		return result, classify(err)
	}
	slog.Info("session retract hid turn", runlog.Attrs("chatsession", "retract", "succeeded", ids,
		"rewind_files", rewindFiles, "cancelled_turn", cancelled)...)

	s.clearRetractedContext(ctx, sessionID, last.ID, runStr, ids)
	return result, nil
}

func (s *Service) clearRetractedContext(ctx context.Context, sessionID kernel.SessionID, taskID kernel.TaskID, runIDs []string, ids runlog.IDs) {
	if s.memory != nil {
		if err := s.memory.DeleteTranscriptByRunIDs(ctx, runIDs); err != nil {
			slog.Error("session retract drop transcript search failed", runlog.Attrs("chatsession", "retract_clear", "failed", ids, "error", err, "reason", "transcript_search")...)
		}
	}
	if s.todos != nil {
		if err := s.todos.Replace(ctx, string(sessionID), nil); err != nil {
			slog.Error("session retract drop todos failed", runlog.Attrs("chatsession", "retract_clear", "failed", ids, "error", err, "reason", "todos")...)
		}
	}
	if s.contextStore == nil {
		return
	}
	c, err := s.contextStore.LatestCompaction(ctx, string(sessionID))
	if err != nil || strings.TrimSpace(c.ID) == "" {
		return
	}
	if !compactionCoversTask(c.ThroughMessageID, taskID, runIDs) {
		return
	}
	if err := s.contextStore.DeleteCompaction(ctx, c.ID); err != nil {
		slog.Error("session retract drop compaction failed", runlog.Attrs("chatsession", "retract_clear", "failed", ids, "error", err, "reason", "compaction")...)
	}
}

func compactionCoversTask(through string, taskID kernel.TaskID, runIDs []string) bool {
	through = strings.TrimSpace(through)
	if through == "" {
		return false
	}
	if through == "task-user:"+string(taskID) {
		return true
	}
	for _, id := range runIDs {
		if through == id || strings.HasPrefix(through, id+":") {
			return true
		}
	}
	return false
}
