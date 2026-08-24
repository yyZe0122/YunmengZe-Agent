// Package corequery provides read-only projections over the Core database.
// It is the only application-facing package that owns SQL for Gateway queries.
package corequery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/coreidentity"
)

var ErrNotFound = errors.New("core query resource not found")

type Task struct {
	ID            coreidentity.TaskID     `json:"task_id"`
	SessionID     *coreidentity.SessionID `json:"session_id,omitempty"`
	Title         string                  `json:"title"`
	Objective     string                  `json:"objective"`
	State         string                  `json:"state"`
	ExecutionMode string                  `json:"execution_mode"`
	Version       uint64                  `json:"version"`
	CreatedAt     string                  `json:"created_at"`
	UpdatedAt     string                  `json:"updated_at"`
}

type Step struct {
	ID          coreidentity.StepID `json:"step_id"`
	Position    int                 `json:"position"`
	Title       string              `json:"title"`
	State       string              `json:"state"`
	EffectLevel string              `json:"effect_level"`
	Version     uint64              `json:"version"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
}

type Plan struct {
	ID        coreidentity.PlanID `json:"plan_id"`
	TaskID    coreidentity.TaskID `json:"task_id"`
	Revision  uint64              `json:"revision"`
	State     string              `json:"state"`
	ScopeHash string              `json:"scope_hash"`
	Document  json.RawMessage     `json:"document"`
	Version   uint64              `json:"version"`
	CreatedAt string              `json:"created_at"`
	UpdatedAt string              `json:"updated_at"`
	Steps     []Step              `json:"steps"`
}

type Approval struct {
	ID            coreidentity.ApprovalID `json:"approval_id"`
	PlanID        coreidentity.PlanID     `json:"plan_id"`
	PlanRevision  uint64                  `json:"plan_revision"`
	Decision      string                  `json:"decision"`
	ScopeHash     string                  `json:"scope_hash"`
	DecidedBy     string                  `json:"decided_by"`
	DecidedAt     string                  `json:"decided_at"`
	ExpiresAt     *string                 `json:"expires_at,omitempty"`
	Scope         string                  `json:"scope"`
	StepID        *coreidentity.StepID    `json:"step_id,omitempty"`
	Reason        string                  `json:"reason"`
	InvalidatedAt *string                 `json:"invalidated_at,omitempty"`
}

type Run struct {
	ID          coreidentity.RunID   `json:"run_id"`
	TaskID      coreidentity.TaskID  `json:"task_id"`
	PlanID      coreidentity.PlanID  `json:"plan_id"`
	StepID      *coreidentity.StepID `json:"step_id,omitempty"`
	ParentRunID *coreidentity.RunID  `json:"parent_run_id,omitempty"`
	State       string               `json:"state"`
	StartedAt   string               `json:"started_at"`
	FinishedAt  *string              `json:"finished_at,omitempty"`
	Error       *string              `json:"error,omitempty"`
	Result      *string              `json:"result,omitempty"`
}

// TaskUsage is the aggregated provider usage for all runs of a task.
// Source: agent_run_records.usage JSON on assistant_message rows.
// InputTokens is uncached input; cache fields are optional provider metrics.
// Cost is optional (vendor billing is authoritative; often zero).
type TaskUsage struct {
	TaskID           coreidentity.TaskID `json:"task_id"`
	InputTokens      int64               `json:"input_tokens"`
	OutputTokens     int64               `json:"output_tokens"`
	TotalTokens      int64               `json:"total_tokens"`
	CacheReadTokens  int64               `json:"cache_read_tokens"`
	CacheWriteTokens int64               `json:"cache_write_tokens"`
	CostMicros       int64               `json:"cost_micros"`
}

// RunUsage is provider usage for one run, with optional direct-child rollup (ADR-039).
// Self is this run only; Children sums one-level parent_run_id children; Total = Self+Children.
// Observability only — does not change budget policy.
type RunUsage struct {
	RunID            coreidentity.RunID `json:"run_id"`
	Self             UsageTotals        `json:"self"`
	Children         UsageTotals        `json:"children"`
	Total            UsageTotals        `json:"total"`
	ChildRunCount    int                `json:"child_run_count"`
	InputTokens      int64              `json:"input_tokens"`
	OutputTokens     int64              `json:"output_tokens"`
	TotalTokens      int64              `json:"total_tokens"`
	CacheReadTokens  int64              `json:"cache_read_tokens"`
	CacheWriteTokens int64              `json:"cache_write_tokens"`
	CostMicros       int64              `json:"cost_micros"`
}

// UsageTotals is a token/cost aggregate slice (self, children, or combined).
type UsageTotals struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CostMicros       int64 `json:"cost_micros"`
}

// CacheHitRate is cache_read / (cache_read + uncached input). ok=false when unknown.
func (u TaskUsage) CacheHitRate() (rate float64, ok bool) {
	if u.CacheReadTokens <= 0 && u.CacheWriteTokens <= 0 {
		return 0, false
	}
	den := u.CacheReadTokens + u.InputTokens
	if den <= 0 {
		return 0, false
	}
	return float64(u.CacheReadTokens) / float64(den), true
}

// CacheHitRate uses the rolled-up total (self+children).
func (u RunUsage) CacheHitRate() (rate float64, ok bool) {
	return TaskUsage{
		InputTokens: u.InputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
	}.CacheHitRate()
}

// TaskContext is the last context-pressure snapshot for a task (window fill, not lifetime spend).
type TaskContext struct {
	TaskID           coreidentity.TaskID    `json:"task_id"`
	SessionID        coreidentity.SessionID `json:"session_id"`
	Model            string                 `json:"model"`
	ContextWindow    int64                  `json:"context_window"`
	MaxOutputTokens  int64                  `json:"max_output_tokens"`
	UsableTokens     int64                  `json:"usable_tokens"`
	LastPromptTokens int64                  `json:"last_prompt_tokens"`
	LastOutputTokens int64                  `json:"last_output_tokens"`
	EstimateTokens   int64                  `json:"estimate_tokens"`
	CacheReadTokens  int64                  `json:"cache_read_tokens"`
	CacheWriteTokens int64                  `json:"cache_write_tokens"`
	Source           string                 `json:"source"`
	EstimateSource   string                 `json:"estimate_source"`
	Ratio            float64                `json:"ratio"`
	Calibrated       bool                   `json:"calibrated"`
	Compacted        bool                   `json:"compacted"`
	HistoryMessages  int                    `json:"history_messages"`
	Pressure         float64                `json:"pressure"`
	UpdatedAt        string                 `json:"updated_at"`
}

// Session is a chat container; tasks and runs hang off it.
type Session struct {
	ID           coreidentity.SessionID `json:"session_id"`
	State        string                 `json:"state"`
	Version      uint64                 `json:"version"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
	Title        string                 `json:"title,omitempty"`
	LatestTaskID *coreidentity.TaskID   `json:"latest_task_id,omitempty"`
	LatestState  string                 `json:"latest_task_state,omitempty"`
	TaskCount    int                    `json:"task_count"`
	// PreferredModel is optional session model preference (O4; chat run job pin → prefer → main).
	PreferredModel string `json:"preferred_model,omitempty"`
	// Workspace is session-bound client cwd when set (ADR-046).
	Workspace string `json:"workspace,omitempty"`
	// PermissionStance is the Tab posture: agent | auto | plan (empty = agent).
	PermissionStance string `json:"permission_stance,omitempty"`
}

// TranscriptMessage is a chat-facing projection of agent_run_records
// (plus synthetic user lines from task objectives when records are empty).
type TranscriptMessage struct {
	ID         string                 `json:"id"`
	SessionID  coreidentity.SessionID `json:"session_id,omitempty"`
	TaskID     coreidentity.TaskID    `json:"task_id,omitempty"`
	RunID      coreidentity.RunID     `json:"run_id,omitempty"`
	Position   int                    `json:"position"`
	Role       string                 `json:"role"` // user | assistant | tool | system
	Content    string                 `json:"content"`
	Thinking   string                 `json:"thinking,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	ToolCalls  []TranscriptToolCall   `json:"tool_calls,omitempty"`
	RecordType string                 `json:"record_type,omitempty"`
	CreatedAt  string                 `json:"created_at"`
}

type TranscriptToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// SessionTodo is a read projection of session_todos (ADR-053 pills).
type SessionTodo struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	Position  int    `json:"position"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type StoredPlanDocument struct {
	Revision  uint64
	Hash      string
	Document  []byte
	PlanState string
	TaskState string
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("core query database is required")
	}
	return &Store{db: db}, nil
}

func (s *Store) Check(ctx context.Context) error {
	if ctx == nil {
		return errors.New("core query health context is required")
	}
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("core database quick check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("core database quick check failed: %s", result)
	}
	return nil
}

func (s *Store) ListTasks(ctx context.Context, options TaskListOptions) ([]Task, error) {
	if err := validateList(ctx, options.Page, options.Sort); err != nil {
		return nil, err
	}
	query := `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks`
	args := make([]any, 0, 3)
	if state := strings.TrimSpace(options.State); state != "" {
		query += " WHERE state = ?"
		args = append(args, state)
	}
	direction := sqlSortDirection(options.Sort)
	query += " ORDER BY created_at " + direction + ", task_id " + direction + " LIMIT ? OFFSET ?"
	args = append(args, options.Page.Limit, options.Page.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
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

func (s *Store) GetTask(ctx context.Context, id coreidentity.TaskID) (Task, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return Task{}, err
	}
	item, err := scanTask(s.db.QueryRowContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE task_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListPlans(ctx context.Context, options PlanListOptions) ([]Plan, error) {
	if err := validateList(ctx, options.Page, options.Sort); err != nil {
		return nil, err
	}
	query := `
        SELECT plan_id, task_id, revision, state, scope_hash, document, version, created_at, updated_at
        FROM plans`
	args := make([]any, 0, 3)
	if state := strings.TrimSpace(options.State); state != "" {
		query += " WHERE state = ?"
		args = append(args, state)
	}
	direction := sqlSortDirection(options.Sort)
	query += " ORDER BY created_at " + direction + ", plan_id " + direction + " LIMIT ? OFFSET ?"
	args = append(args, options.Page.Limit, options.Page.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]Plan, 0)
	for rows.Next() {
		item, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Steps, err = s.listPlanSteps(ctx, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) GetPlan(ctx context.Context, id coreidentity.PlanID) (Plan, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return Plan{}, err
	}
	item, err := scanPlan(s.db.QueryRowContext(ctx, `
        SELECT plan_id, task_id, revision, state, scope_hash, document, version, created_at, updated_at
        FROM plans WHERE plan_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	if err != nil {
		return Plan{}, err
	}
	item.Steps, err = s.listPlanSteps(ctx, id)
	return item, err
}

func (s *Store) ListApprovals(ctx context.Context, options ApprovalListOptions) ([]Approval, error) {
	if err := validateList(ctx, options.Page, options.Sort); err != nil {
		return nil, err
	}
	query := `
        SELECT approval_id, plan_id, plan_revision, decision, scope_hash, decided_by, decided_at,
               expires_at, scope_type, step_id, reason, invalidated_at
        FROM approvals`
	args := make([]any, 0, 3)
	if decision := strings.TrimSpace(options.Decision); decision != "" {
		query += " WHERE decision = ?"
		args = append(args, decision)
	}
	direction := sqlSortDirection(options.Sort)
	query += " ORDER BY decided_at " + direction + ", approval_id " + direction + " LIMIT ? OFFSET ?"
	args = append(args, options.Page.Limit, options.Page.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Approval, 0)
	for rows.Next() {
		var item Approval
		var expires, step, invalidated sql.NullString
		if err := rows.Scan(&item.ID, &item.PlanID, &item.PlanRevision, &item.Decision, &item.ScopeHash, &item.DecidedBy, &item.DecidedAt, &expires, &item.Scope, &step, &item.Reason, &invalidated); err != nil {
			return nil, err
		}
		if err := normalizeTimeFields(&item.DecidedAt); err != nil {
			return nil, err
		}
		item.ExpiresAt, err = normalizedOptionalTime(expires)
		if err != nil {
			return nil, err
		}
		if step.Valid {
			stepID := coreidentity.StepID(step.String)
			item.StepID = &stepID
		}
		item.InvalidatedAt, err = normalizedOptionalTime(invalidated)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) LoadPlanDocument(ctx context.Context, id coreidentity.PlanID) (StoredPlanDocument, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return StoredPlanDocument{}, err
	}
	var item StoredPlanDocument
	var raw string
	err := s.db.QueryRowContext(ctx, `
        SELECT p.revision, p.scope_hash, p.document, p.state, t.state
        FROM plans p
        JOIN tasks t ON t.task_id = p.task_id
        WHERE p.plan_id = ?`, id,
	).Scan(&item.Revision, &item.Hash, &raw, &item.PlanState, &item.TaskState)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredPlanDocument{}, ErrNotFound
	}
	if err != nil {
		return StoredPlanDocument{}, err
	}
	item.Document = []byte(raw)
	return item, nil
}

func (s *Store) ListRuns(ctx context.Context, options RunListOptions) ([]Run, error) {
	if err := validateList(ctx, options.Page, options.Sort); err != nil {
		return nil, err
	}
	query := `
        SELECT r.run_id, r.task_id, r.plan_id, r.step_id, r.parent_run_id, r.state, r.started_at, r.finished_at, r.error,
               (SELECT json_extract(records.message, '$.content')
                FROM agent_run_records records
                WHERE records.run_id = r.run_id AND records.record_type = 'assistant_message'
                ORDER BY records.position DESC LIMIT 1)
        FROM runs r`
	args := make([]any, 0, 3)
	if state := strings.TrimSpace(options.State); state != "" {
		query += " WHERE r.state = ?"
		args = append(args, state)
	}
	direction := sqlSortDirection(options.Sort)
	query += " ORDER BY r.started_at " + direction + ", r.run_id " + direction + " LIMIT ? OFFSET ?"
	args = append(args, options.Page.Limit, options.Page.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Run, 0)
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetRun(ctx context.Context, id coreidentity.RunID) (Run, error) {
	if err := validateGet(ctx, string(id)); err != nil {
		return Run{}, err
	}
	return scanRun(s.db.QueryRowContext(ctx, `
        SELECT r.run_id, r.task_id, r.plan_id, r.step_id, r.parent_run_id, r.state, r.started_at, r.finished_at, r.error,
               (SELECT json_extract(records.message, '$.content')
                FROM agent_run_records records
                WHERE records.run_id = r.run_id AND records.record_type = 'assistant_message'
                ORDER BY records.position DESC LIMIT 1)
        FROM runs r WHERE r.run_id = ?`, id))
}

// TaskUsage sums assistant_message usage for every run belonging to taskID.
// Missing usage fields count as zero; unknown task still returns zeros (no ErrNotFound).
func (s *Store) TaskUsage(ctx context.Context, taskID coreidentity.TaskID) (TaskUsage, error) {
	if err := validateGet(ctx, string(taskID)); err != nil {
		return TaskUsage{}, err
	}
	var usage TaskUsage
	usage.TaskID = taskID
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.input_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.output_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.total_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cache_read_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cache_write_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cost.micros'), 0) AS INTEGER)), 0)
		FROM agent_run_records a
		JOIN runs r ON r.run_id = a.run_id
		WHERE r.task_id = ? AND a.record_type = 'assistant_message'`,
		taskID,
	).Scan(&usage.InputTokens, &usage.OutputTokens, &usage.TotalTokens,
		&usage.CacheReadTokens, &usage.CacheWriteTokens, &usage.CostMicros)
	if err != nil {
		return TaskUsage{}, fmt.Errorf("task usage: %w", err)
	}
	return usage, nil
}

// RunUsage returns self usage for runID plus one-level child rollup via parent_run_id.
// Unknown run still returns zeros with RunID set (no ErrNotFound), matching TaskUsage.
func (s *Store) RunUsage(ctx context.Context, runID coreidentity.RunID) (RunUsage, error) {
	if err := validateGet(ctx, string(runID)); err != nil {
		return RunUsage{}, err
	}
	out := RunUsage{RunID: runID}
	self, err := s.sumUsageForRuns(ctx, `a.run_id = ?`, runID)
	if err != nil {
		return RunUsage{}, fmt.Errorf("run usage self: %w", err)
	}
	children, err := s.sumUsageForRuns(ctx, `
		a.run_id IN (SELECT run_id FROM runs WHERE parent_run_id = ?)`, runID)
	if err != nil {
		return RunUsage{}, fmt.Errorf("run usage children: %w", err)
	}
	var childCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE parent_run_id = ?`, runID,
	).Scan(&childCount); err != nil {
		return RunUsage{}, fmt.Errorf("run usage child count: %w", err)
	}
	out.Self = self
	out.Children = children
	out.ChildRunCount = childCount
	out.Total = addUsageTotals(self, children)
	out.InputTokens = out.Total.InputTokens
	out.OutputTokens = out.Total.OutputTokens
	out.TotalTokens = out.Total.TotalTokens
	out.CacheReadTokens = out.Total.CacheReadTokens
	out.CacheWriteTokens = out.Total.CacheWriteTokens
	out.CostMicros = out.Total.CostMicros
	return out, nil
}

func (s *Store) sumUsageForRuns(ctx context.Context, whereSQL string, args ...any) (UsageTotals, error) {
	var u UsageTotals
	q := `
		SELECT
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.input_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.output_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.total_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cache_read_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cache_write_tokens'), 0) AS INTEGER)), 0),
			COALESCE(SUM(CAST(COALESCE(json_extract(a.usage, '$.cost.micros'), 0) AS INTEGER)), 0)
		FROM agent_run_records a
		WHERE a.record_type = 'assistant_message' AND (` + whereSQL + `)`
	err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&u.InputTokens, &u.OutputTokens, &u.TotalTokens,
		&u.CacheReadTokens, &u.CacheWriteTokens, &u.CostMicros,
	)
	return u, err
}

func addUsageTotals(a, b UsageTotals) UsageTotals {
	return UsageTotals{
		InputTokens:      a.InputTokens + b.InputTokens,
		OutputTokens:     a.OutputTokens + b.OutputTokens,
		TotalTokens:      a.TotalTokens + b.TotalTokens,
		CacheReadTokens:  a.CacheReadTokens + b.CacheReadTokens,
		CacheWriteTokens: a.CacheWriteTokens + b.CacheWriteTokens,
		CostMicros:       a.CostMicros + b.CostMicros,
	}
}

// TaskContext returns the latest context pressure snapshot for taskID.
// Missing snapshot returns zero values with Source "none" (no ErrNotFound).
func (s *Store) TaskContext(ctx context.Context, taskID coreidentity.TaskID) (TaskContext, error) {
	if err := validateGet(ctx, string(taskID)); err != nil {
		return TaskContext{}, err
	}
	var item TaskContext
	var calibrated, compacted int
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id, session_id, model, context_window, max_output_tokens, usable_tokens,
			last_prompt_tokens, last_output_tokens, estimate_tokens,
			cache_read_tokens, cache_write_tokens, source, estimate_source,
			ratio, calibrated, compacted, history_messages, updated_at
		FROM context_snapshots WHERE task_id = ?`, taskID,
	).Scan(
		&item.TaskID, &item.SessionID, &item.Model, &item.ContextWindow, &item.MaxOutputTokens, &item.UsableTokens,
		&item.LastPromptTokens, &item.LastOutputTokens, &item.EstimateTokens,
		&item.CacheReadTokens, &item.CacheWriteTokens, &item.Source, &item.EstimateSource,
		&item.Ratio, &calibrated, &compacted, &item.HistoryMessages, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskContext{TaskID: taskID, Source: "none", EstimateSource: "local_estimate", Ratio: 1}, nil
	}
	if err != nil {
		return TaskContext{}, fmt.Errorf("task context: %w", err)
	}
	item.Calibrated = calibrated != 0
	item.Compacted = compacted != 0
	if item.UsableTokens > 0 {
		fill := item.LastPromptTokens
		if fill <= 0 {
			fill = item.EstimateTokens
		}
		if fill > 0 {
			item.Pressure = float64(fill) / float64(item.UsableTokens)
		}
	}
	return item, nil
}

// SessionContext returns the most recent context snapshot for sessionID.
func (s *Store) SessionContext(ctx context.Context, sessionID coreidentity.SessionID) (TaskContext, error) {
	if err := validateGet(ctx, string(sessionID)); err != nil {
		return TaskContext{}, err
	}
	var item TaskContext
	var calibrated, compacted int
	err := s.db.QueryRowContext(ctx, `
		SELECT task_id, session_id, model, context_window, max_output_tokens, usable_tokens,
			last_prompt_tokens, last_output_tokens, estimate_tokens,
			cache_read_tokens, cache_write_tokens, source, estimate_source,
			ratio, calibrated, compacted, history_messages, updated_at
		FROM context_snapshots WHERE session_id = ?
		ORDER BY updated_at DESC LIMIT 1`, sessionID,
	).Scan(
		&item.TaskID, &item.SessionID, &item.Model, &item.ContextWindow, &item.MaxOutputTokens, &item.UsableTokens,
		&item.LastPromptTokens, &item.LastOutputTokens, &item.EstimateTokens,
		&item.CacheReadTokens, &item.CacheWriteTokens, &item.Source, &item.EstimateSource,
		&item.Ratio, &calibrated, &compacted, &item.HistoryMessages, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskContext{SessionID: sessionID, Source: "none", EstimateSource: "local_estimate", Ratio: 1}, nil
	}
	if err != nil {
		return TaskContext{}, fmt.Errorf("session context: %w", err)
	}
	item.Calibrated = calibrated != 0
	item.Compacted = compacted != 0
	if item.UsableTokens > 0 {
		fill := item.LastPromptTokens
		if fill <= 0 {
			fill = item.EstimateTokens
		}
		if fill > 0 {
			item.Pressure = float64(fill) / float64(item.UsableTokens)
		}
	}
	return item, nil
}

func scanRun(row scanner) (Run, error) {
	var item Run
	var step, parent, finished, failure, result sql.NullString
	if err := row.Scan(&item.ID, &item.TaskID, &item.PlanID, &step, &parent, &item.State, &item.StartedAt, &finished, &failure, &result); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	if err := normalizeTimeFields(&item.StartedAt); err != nil {
		return Run{}, err
	}
	var err error
	item.FinishedAt, err = normalizedOptionalTime(finished)
	if err != nil {
		return Run{}, err
	}
	if step.Valid {
		value := coreidentity.StepID(step.String)
		item.StepID = &value
	}
	if parent.Valid && strings.TrimSpace(parent.String) != "" {
		value := coreidentity.RunID(parent.String)
		item.ParentRunID = &value
	}
	if failure.Valid {
		item.Error = &failure.String
	}
	if result.Valid && strings.TrimSpace(result.String) != "" {
		item.Result = &result.String
	}
	return item, nil
}

func (s *Store) listPlanSteps(ctx context.Context, planID coreidentity.PlanID) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT step_id, position, title, state, effect_level, version, created_at, updated_at
        FROM plan_steps WHERE plan_id = ? ORDER BY position, step_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Step, 0)
	for rows.Next() {
		var item Step
		if err := rows.Scan(&item.ID, &item.Position, &item.Title, &item.State, &item.EffectLevel, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := normalizeTimeFields(&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func scanTask(row scanner) (Task, error) {
	var item Task
	var session sql.NullString
	if err := row.Scan(&item.ID, &session, &item.Title, &item.Objective, &item.State, &item.ExecutionMode, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Task{}, err
	}
	if session.Valid {
		sessionID := coreidentity.SessionID(session.String)
		item.SessionID = &sessionID
	}
	if item.ExecutionMode == "" {
		item.ExecutionMode = "agent"
	}
	if err := normalizeTimeFields(&item.CreatedAt, &item.UpdatedAt); err != nil {
		return Task{}, err
	}
	return item, nil
}

func scanPlan(row scanner) (Plan, error) {
	var item Plan
	var document string
	if err := row.Scan(&item.ID, &item.TaskID, &item.Revision, &item.State, &item.ScopeHash, &document, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Plan{}, err
	}
	item.Document = json.RawMessage(document)
	if err := normalizeTimeFields(&item.CreatedAt, &item.UpdatedAt); err != nil {
		return Plan{}, err
	}
	return item, nil
}

func normalizeTimeFields(values ...*string) error {
	for _, value := range values {
		parsed, err := time.Parse(time.RFC3339Nano, *value)
		if err != nil {
			return fmt.Errorf("parse core query time: %w", err)
		}
		*value = parsed.UTC().Format(time.RFC3339Nano)
	}
	return nil
}

func normalizedOptionalTime(value sql.NullString) (*string, error) {
	if !value.Valid {
		return nil, nil
	}
	if err := normalizeTimeFields(&value.String); err != nil {
		return nil, err
	}
	return &value.String, nil
}

func validateList(ctx context.Context, page Page, sort SortDirection) error {
	if ctx == nil {
		return errors.New("core query context is required")
	}
	return validateListOptions(page, sort)
}

func validateGet(ctx context.Context, id string) error {
	if ctx == nil {
		return errors.New("core query context is required")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("core query ID is required")
	}
	return nil
}
