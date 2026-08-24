package corequery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/coreidentity"
)

func (s *Store) ListSessions(ctx context.Context, options SessionListOptions) ([]Session, error) {
	if err := validateList(ctx, options.Page, options.Sort); err != nil {
		return nil, err
	}
	direction := sqlSortDirection(options.Sort)
	// One row per session with latest task summary.
	query := `
        SELECT s.session_id, s.state, s.version, s.created_at, s.updated_at, s.metadata,
               COALESCE(stats.task_count, 0) AS task_count,
               latest.task_id, latest.title, latest.state AS task_state
        FROM sessions s
        LEFT JOIN (
            SELECT session_id, COUNT(*) AS task_count
            FROM tasks
            WHERE session_id IS NOT NULL AND session_id != ''
            GROUP BY session_id
        ) stats ON stats.session_id = s.session_id
        LEFT JOIN (
            SELECT t.session_id, t.task_id, t.title, t.state
            FROM tasks t
            INNER JOIN (
                SELECT session_id, MAX(created_at) AS max_created
                FROM tasks
                WHERE session_id IS NOT NULL AND session_id != ''
                GROUP BY session_id
            ) m ON m.session_id = t.session_id AND m.max_created = t.created_at
        ) latest ON latest.session_id = s.session_id
        ORDER BY s.updated_at ` + direction + `, s.session_id ` + direction + `
        LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, options.Page.Limit, options.Page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Session, 0)
	for rows.Next() {
		var item Session
		var metadata string
		var taskID, title, taskState sql.NullString
		if err := rows.Scan(
			&item.ID, &item.State, &item.Version, &item.CreatedAt, &item.UpdatedAt, &metadata,
			&item.TaskCount, &taskID, &title, &taskState,
		); err != nil {
			return nil, err
		}
		if err := normalizeTimeFields(&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		applySessionMetadata(&item, metadata)
		if taskID.Valid && strings.TrimSpace(taskID.String) != "" {
			id := coreidentity.TaskID(taskID.String)
			item.LatestTaskID = &id
		}
		item.Title = strings.TrimSpace(title.String)
		item.LatestState = strings.TrimSpace(taskState.String)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, id coreidentity.SessionID) (Session, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return Session{}, err
	}
	var item Session
	var metadata string
	var taskID, title, taskState sql.NullString
	err := s.db.QueryRowContext(ctx, `
        SELECT s.session_id, s.state, s.version, s.created_at, s.updated_at, s.metadata,
               COALESCE((SELECT COUNT(*) FROM tasks t WHERE t.session_id = s.session_id), 0),
               (SELECT t.task_id FROM tasks t WHERE t.session_id = s.session_id ORDER BY t.created_at DESC LIMIT 1),
               (SELECT t.title FROM tasks t WHERE t.session_id = s.session_id ORDER BY t.created_at DESC LIMIT 1),
               (SELECT t.state FROM tasks t WHERE t.session_id = s.session_id ORDER BY t.created_at DESC LIMIT 1)
        FROM sessions s WHERE s.session_id = ?`, id).Scan(
		&item.ID, &item.State, &item.Version, &item.CreatedAt, &item.UpdatedAt, &metadata,
		&item.TaskCount, &taskID, &title, &taskState,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if err := normalizeTimeFields(&item.CreatedAt, &item.UpdatedAt); err != nil {
		return Session{}, err
	}
	applySessionMetadata(&item, metadata)
	if taskID.Valid && strings.TrimSpace(taskID.String) != "" {
		tid := coreidentity.TaskID(taskID.String)
		item.LatestTaskID = &tid
	}
	item.Title = strings.TrimSpace(title.String)
	item.LatestState = strings.TrimSpace(taskState.String)
	return item, nil
}

func applySessionMetadata(item *Session, raw string) {
	raw = strings.TrimSpace(raw)
	if item == nil || raw == "" || raw == "{}" {
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return
	}
	if v, ok := meta["workspace"].(string); ok {
		item.Workspace = strings.TrimSpace(v)
	}
	if v, ok := meta["model"].(string); ok {
		item.PreferredModel = strings.TrimSpace(v)
	}
	if v, ok := meta["permission_stance"].(string); ok {
		item.PermissionStance = strings.TrimSpace(v)
	}
}

// SessionTranscript returns chat messages for a session: task objectives as
// user turns, then agent_run_records (user/assistant/tool) in time order.
func (s *Store) SessionTranscript(ctx context.Context, sessionID coreidentity.SessionID, options TranscriptOptions) ([]TranscriptMessage, error) {
	if err := validateGet(ctx, string(sessionID)); err != nil {
		return nil, err
	}
	if options.Page.Limit <= 0 {
		options.Page.Limit = 200
	}
	if options.Page.Limit > MaxPageSize {
		options.Page.Limit = MaxPageSize
	}
	if options.Page.Offset < 0 {
		return nil, errors.New("core query offset must not be negative")
	}

	// Ensure session exists.
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}

	tasks, err := s.listSessionTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Synthetic user messages from each task objective (chat turn anchors).
	out := make([]TranscriptMessage, 0, 32)
	for _, task := range tasks {
		body := strings.TrimSpace(task.Objective)
		if body == "" {
			body = task.Title
		}
		out = append(out, TranscriptMessage{
			ID:        fmt.Sprintf("task-user:%s", task.ID),
			SessionID: sessionID,
			TaskID:    task.ID,
			Role:      "user",
			Content:   body,
			CreatedAt: task.CreatedAt,
		})
	}

	rows, err := s.db.QueryContext(ctx, `
        SELECT a.run_id, a.position, a.record_type, a.message, a.tool_call_id, a.created_at,
               r.task_id
        FROM agent_run_records a
        INNER JOIN runs r ON r.run_id = a.run_id
        INNER JOIN tasks t ON t.task_id = r.task_id
        WHERE t.session_id = ?
        ORDER BY a.created_at ASC, a.run_id ASC, a.position ASC
        LIMIT ? OFFSET ?`, sessionID, options.Page.Limit, options.Page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		msg, err := scanTranscriptRow(rows, sessionID)
		if err != nil {
			return nil, err
		}
		if skipTranscriptRecord(msg, out) {
			continue
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Stable chronological order: task user anchors then records may interleave wrong
	// if we only appended records after all tasks. Re-sort by CreatedAt + id.
	sortTranscript(out)
	return out, nil
}

// SessionTranscriptTail returns the newest n transcript messages in chronological
// order for packing (ADR-051 / QB2). TUI/API pagination stays on SessionTranscript (ASC).
func (s *Store) SessionTranscriptTail(ctx context.Context, sessionID coreidentity.SessionID, n int) ([]TranscriptMessage, error) {
	if err := validateGet(ctx, string(sessionID)); err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 200
	}
	if n > MaxPageSize {
		n = MaxPageSize
	}
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	tasks, err := s.listSessionTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]TranscriptMessage, 0, n+len(tasks))
	for _, task := range tasks {
		body := strings.TrimSpace(task.Objective)
		if body == "" {
			body = task.Title
		}
		out = append(out, TranscriptMessage{
			ID:        fmt.Sprintf("task-user:%s", task.ID),
			SessionID: sessionID,
			TaskID:    task.ID,
			Role:      "user",
			Content:   body,
			CreatedAt: task.CreatedAt,
		})
	}
	rows, err := s.db.QueryContext(ctx, `
        SELECT a.run_id, a.position, a.record_type, a.message, a.tool_call_id, a.created_at,
               r.task_id
        FROM agent_run_records a
        INNER JOIN runs r ON r.run_id = a.run_id
        INNER JOIN tasks t ON t.task_id = r.task_id
        WHERE t.session_id = ?
        ORDER BY a.created_at DESC, a.run_id DESC, a.position DESC
        LIMIT ?`, sessionID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		msg, err := scanTranscriptRow(rows, sessionID)
		if err != nil {
			return nil, err
		}
		if skipTranscriptRecord(msg, out) {
			continue
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortTranscript(out)
	if len(out) > n {
		out = out[alignTranscriptTailCut(out, len(out)-n):]
	}
	return out, nil
}

// alignTranscriptTailCut moves cut left so a tool result is not split from
// its owning assistant tool_calls (ADR-051 pair-safe tail).
func alignTranscriptTailCut(msgs []TranscriptMessage, cut int) int {
	if cut <= 0 || cut >= len(msgs) {
		return cut
	}
	if strings.EqualFold(msgs[cut].Role, "tool") {
		for cut > 0 && strings.EqualFold(msgs[cut-1].Role, "tool") {
			cut--
		}
		if cut > 0 && strings.EqualFold(msgs[cut-1].Role, "assistant") && len(msgs[cut-1].ToolCalls) > 0 {
			cut--
		}
		return cut
	}
	i := cut
	for i > 0 && strings.EqualFold(msgs[i-1].Role, "tool") {
		i--
	}
	if i < cut && i > 0 && strings.EqualFold(msgs[i-1].Role, "assistant") && len(msgs[i-1].ToolCalls) > 0 {
		return i - 1
	}
	return cut
}

// TaskTranscript is the same projection scoped to one task (for focused views).
func (s *Store) TaskTranscript(ctx context.Context, taskID coreidentity.TaskID, options TranscriptOptions) ([]TranscriptMessage, error) {
	if err := validateGet(ctx, string(taskID)); err != nil {
		return nil, err
	}
	if options.Page.Limit <= 0 {
		options.Page.Limit = 200
	}
	if options.Page.Limit > MaxPageSize {
		options.Page.Limit = MaxPageSize
	}
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var sessionID coreidentity.SessionID
	if task.SessionID != nil {
		sessionID = *task.SessionID
	}
	out := make([]TranscriptMessage, 0, 16)
	body := strings.TrimSpace(task.Objective)
	if body == "" {
		body = task.Title
	}
	out = append(out, TranscriptMessage{
		ID:        fmt.Sprintf("task-user:%s", task.ID),
		SessionID: sessionID,
		TaskID:    task.ID,
		Role:      "user",
		Content:   body,
		CreatedAt: task.CreatedAt,
	})
	rows, err := s.db.QueryContext(ctx, `
        SELECT a.run_id, a.position, a.record_type, a.message, a.tool_call_id, a.created_at,
               r.task_id
        FROM agent_run_records a
        INNER JOIN runs r ON r.run_id = a.run_id
        WHERE r.task_id = ?
        ORDER BY a.created_at ASC, a.run_id ASC, a.position ASC
        LIMIT ? OFFSET ?`, taskID, options.Page.Limit, options.Page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		msg, err := scanTranscriptRow(rows, sessionID)
		if err != nil {
			return nil, err
		}
		if skipTranscriptRecord(msg, out) {
			continue
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortTranscript(out)
	return out, nil
}

// skipTranscriptRecord drops internal agent/planner prompts that must not appear
// as chat bubbles (OpenCode/Crush-style Session transcript).
func skipTranscriptRecord(msg TranscriptMessage, existing []TranscriptMessage) bool {
	if msg.Role == "system" {
		return true
	}
	if msg.RecordType != "input_message" || msg.Role != "user" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return true
	}
	if isInternalStepPrompt(content) {
		return true
	}
	// Avoid double-counting: keep task-user anchors, drop matching input_message copies.
	for _, item := range existing {
		if item.TaskID == msg.TaskID && item.Role == "user" &&
			strings.HasPrefix(item.ID, "task-user:") &&
			strings.TrimSpace(item.Content) == content {
			return true
		}
	}
	return false
}

// isInternalStepPrompt detects legacy plan-step user templates and
// similar internal scaffolding that leaked into agent_run_records as role=user.
func isInternalStepPrompt(content string) bool {
	if !strings.Contains(content, "Task objective:") {
		return false
	}
	return strings.Contains(content, "Current step:") ||
		strings.Contains(content, "Approved plan objective:") ||
		strings.Contains(content, "Approved capabilities:") ||
		strings.Contains(content, "Approved plan")
}

func (s *Store) listSessionTasks(ctx context.Context, sessionID coreidentity.SessionID) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE session_id = ?
        ORDER BY created_at ASC, task_id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Task, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type transcriptScanner interface {
	Scan(dest ...any) error
}

func scanTranscriptRow(row transcriptScanner, sessionID coreidentity.SessionID) (TranscriptMessage, error) {
	var (
		runID, recordType, messageJSON, toolCallID, createdAt, taskID string
		position                                                      int
	)
	if err := row.Scan(&runID, &position, &recordType, &messageJSON, &toolCallID, &createdAt, &taskID); err != nil {
		return TranscriptMessage{}, err
	}
	var payload struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		Thinking   string `json:"thinking"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(messageJSON), &payload); err != nil {
		return TranscriptMessage{}, fmt.Errorf("decode transcript message: %w", err)
	}
	msg := TranscriptMessage{
		ID:         fmt.Sprintf("%s:%d", runID, position),
		SessionID:  sessionID,
		TaskID:     coreidentity.TaskID(taskID),
		RunID:      coreidentity.RunID(runID),
		Position:   position,
		Role:       strings.TrimSpace(payload.Role),
		Content:    payload.Content,
		Thinking:   payload.Thinking,
		ToolCallID: firstNonEmpty(strings.TrimSpace(toolCallID), strings.TrimSpace(payload.ToolCallID)),
		RecordType: recordType,
		CreatedAt:  createdAt,
	}
	if err := normalizeTimeFields(&msg.CreatedAt); err != nil {
		return TranscriptMessage{}, err
	}
	for _, tc := range payload.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, TranscriptToolCall{
			ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
		})
	}
	return msg, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Store) ListSessionTodos(ctx context.Context, id coreidentity.SessionID) ([]SessionTodo, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return nil, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE session_id = ?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_id, content, status, position, updated_at
		FROM session_todos WHERE session_id = ?
		ORDER BY position ASC, item_id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("list session todos: %w", err)
	}
	defer rows.Close()
	out := make([]SessionTodo, 0)
	for rows.Next() {
		var item SessionTodo
		if err := rows.Scan(&item.ID, &item.Content, &item.Status, &item.Position, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func sortTranscript(items []TranscriptMessage) {
	// Simple insertion sort — transcripts are small (page-limited).
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 {
			if items[j-1].CreatedAt < items[j].CreatedAt ||
				(items[j-1].CreatedAt == items[j].CreatedAt && items[j-1].ID <= items[j].ID) {
				break
			}
			items[j-1], items[j] = items[j], items[j-1]
			j--
		}
	}
}
