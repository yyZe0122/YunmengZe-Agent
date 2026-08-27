package kernel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (r *Repository) CreateTask(
	ctx context.Context,
	id TaskID,
	sessionID SessionID,
	title string,
	objective string,
	now time.Time,
) (Task, error) {
	return r.CreateTaskWithSkillSnapshot(ctx, id, sessionID, title, objective, nil, "", ExecutionModeAgent, now)
}

func (r *Repository) CreateTaskWithSkillSnapshot(
	ctx context.Context,
	id TaskID,
	sessionID SessionID,
	title string,
	objective string,
	skillIDs []string,
	instructions string,
	executionMode ExecutionMode,
	now time.Time,
) (Task, error) {
	if ctx == nil {
		return Task{}, errors.New("create task context is required")
	}
	task, err := NewTaskWithMode(id, sessionID, title, objective, executionMode, now)
	if err != nil {
		return Task{}, err
	}
	snapshot, encodedSkillIDs, err := newTaskSkillSnapshot(task.ID, skillIDs, instructions, task.CreatedAt)
	if err != nil {
		return Task{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin create task: %w", err)
	}
	defer tx.Rollback()

	session, err := scanSession(tx.QueryRowContext(ctx, `
        SELECT session_id, state, version, created_at, updated_at, metadata
        FROM sessions WHERE session_id = ?`, sessionID))
	if err != nil {
		return Task{}, err
	}
	if session.State != SessionActive {
		return Task{}, fmt.Errorf("%w: %s", ErrSessionClosed, sessionID)
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO tasks (
            task_id, session_id, title, objective, state, execution_mode, created_at, updated_at, version
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		task.SessionID,
		task.Title,
		task.Objective,
		task.State,
		task.ExecutionMode,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		task.Version,
	)
	if err != nil {
		if existsByID(ctx, tx, "tasks", "task_id", string(task.ID)) {
			return Task{}, fmt.Errorf("%w: task %s", ErrAlreadyExists, task.ID)
		}
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO task_skill_snapshots (
            task_id, skill_ids, instructions, content_hash, created_at
        ) VALUES (?, ?, ?, ?, ?)`,
		snapshot.TaskID, encodedSkillIDs, snapshot.Instructions, snapshot.ContentHash, formatTime(snapshot.CreatedAt),
	); err != nil {
		return Task{}, fmt.Errorf("insert task skill snapshot: %w", err)
	}
	ev, err := aggregateEvent(
		"task", string(task.ID), task.Version, "task.created", task.CreatedAt,
		map[string]any{
			"session_id":         task.SessionID,
			"title":              task.Title,
			"objective":          task.Objective,
			"state":              task.State,
			"execution_mode":     task.ExecutionMode,
			"skill_ids":          snapshot.SkillIDs,
			"skill_content_hash": snapshot.ContentHash,
		},
	)
	if err != nil {
		return Task{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Task{}, fmt.Errorf("append task created event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit create task: %w", err)
	}
	return task, nil
}

// CreateApprovedWorkspacePlan persists a synthetic workspace plan already approved
// for session chat (agent or plan mode). Task goes created → running only.
// Plan is draft → approved in memory and stored as approved.
func (r *Repository) CreateApprovedWorkspacePlan(
	ctx context.Context,
	id PlanID,
	taskID TaskID,
	expectedTaskVersion uint64,
	revision uint64,
	scopeHash string,
	document []byte,
	drafts []PlanStepDraft,
	reason string,
	now time.Time,
) (Plan, Task, error) {
	if ctx == nil {
		return Plan{}, Task{}, errors.New("create approved workspace plan context is required")
	}
	if len(drafts) == 0 {
		return Plan{}, Task{}, fmt.Errorf("%w: plan requires at least one step", ErrInvalidAggregate)
	}
	if len(document) == 0 || !json.Valid(document) {
		return Plan{}, Task{}, fmt.Errorf("%w: canonical plan document must be valid JSON", ErrInvalidAggregate)
	}
	digest := sha256.Sum256(document)
	if hex.EncodeToString(digest[:]) != strings.TrimSpace(scopeHash) {
		return Plan{}, Task{}, fmt.Errorf("%w: canonical plan document hash differs from scope hash", ErrInvalidAggregate)
	}
	plan, err := NewPlan(id, taskID, revision, scopeHash, now)
	if err != nil {
		return Plan{}, Task{}, err
	}
	for _, draft := range drafts {
		step, err := NewPlanStep(draft.ID, id, draft.Position, draft.Title, draft.EffectLevel, now)
		if err != nil {
			return Plan{}, Task{}, err
		}
		for _, existing := range plan.Steps {
			if existing.ID == step.ID || existing.Position == step.Position {
				return Plan{}, Task{}, fmt.Errorf("%w: duplicate plan step ID or position", ErrInvalidAggregate)
			}
		}
		if err := step.Transition(StepApproved, now); err != nil {
			return Plan{}, Task{}, err
		}
		plan.Steps = append(plan.Steps, step)
	}
	if err := plan.Transition(PlanApproved, now); err != nil {
		return Plan{}, Task{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, Task{}, fmt.Errorf("begin create approved workspace plan: %w", err)
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRowContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE task_id = ?`, taskID))
	if err != nil {
		return Plan{}, Task{}, err
	}
	if task.Version != expectedTaskVersion {
		return Plan{}, Task{}, versionConflict("task", string(taskID), expectedTaskVersion, task.Version)
	}
	mode := NormalizeExecutionMode(string(task.ExecutionMode))
	if mode != ExecutionModeAgent && mode != ExecutionModePlan {
		return Plan{}, Task{}, fmt.Errorf("%w: approved workspace plan requires agent or plan execution_mode", ErrInvalidAggregate)
	}
	if task.State != TaskCreated {
		return Plan{}, Task{}, fmt.Errorf("%w: approved workspace plan requires task state created (got %s)", ErrInvalidAggregate, task.State)
	}
	previousTaskState := task.State
	if err := task.Transition(TaskRunning, now); err != nil {
		return Plan{}, Task{}, err
	}

	_, err = tx.ExecContext(ctx, `
        INSERT INTO plans (plan_id, task_id, revision, state, scope_hash, created_at, version, updated_at, document)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.TaskID, plan.Revision, plan.State, plan.ScopeHash,
		formatTime(plan.CreatedAt), plan.Version, formatTime(plan.UpdatedAt), string(document),
	)
	if err != nil {
		if existsByID(ctx, tx, "plans", "plan_id", string(plan.ID)) {
			return Plan{}, Task{}, fmt.Errorf("%w: plan %s", ErrAlreadyExists, plan.ID)
		}
		return Plan{}, Task{}, fmt.Errorf("insert workspace plan: %w", err)
	}
	for _, step := range plan.Steps {
		_, err := tx.ExecContext(ctx, `
            INSERT INTO plan_steps (
                step_id, plan_id, position, title, state, effect_level, created_at, version, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			step.ID, step.PlanID, step.Position, step.Title, step.State, step.EffectLevel,
			formatTime(step.CreatedAt), step.Version, formatTime(step.UpdatedAt),
		)
		if err != nil {
			return Plan{}, Task{}, fmt.Errorf("insert workspace plan step %s: %w", step.ID, err)
		}
	}
	ev, err := aggregateEvent(
		"plan", string(plan.ID), plan.Version, "plan.workspace_auth", plan.UpdatedAt,
		map[string]any{
			"task_id": plan.TaskID, "revision": plan.Revision, "state": plan.State,
			"scope_hash": plan.ScopeHash, "step_count": len(plan.Steps),
			"reason": strings.TrimSpace(reason),
		},
	)
	if err != nil {
		return Plan{}, Task{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Plan{}, Task{}, fmt.Errorf("append workspace plan event: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
        UPDATE tasks SET state = ?, version = ?, updated_at = ?
        WHERE task_id = ? AND version = ?`,
		task.State, task.Version, formatTime(task.UpdatedAt), task.ID, expectedTaskVersion,
	)
	if err != nil {
		return Plan{}, Task{}, fmt.Errorf("update task for workspace plan: %w", err)
	}
	if err := requireOneVersionedRow(result, "task", string(task.ID), expectedTaskVersion); err != nil {
		return Plan{}, Task{}, err
	}
	ev, err = aggregateEvent(
		"task", string(task.ID), task.Version, "task.state_changed", task.UpdatedAt,
		map[string]any{
			"from_state": previousTaskState,
			"to_state":   task.State,
			"reason":     strings.TrimSpace(reason),
		},
	)
	if err != nil {
		return Plan{}, Task{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Plan{}, Task{}, fmt.Errorf("append task transition event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, Task{}, fmt.Errorf("commit approved workspace plan: %w", err)
	}
	return plan, task, nil
}

func (r *Repository) GetTask(ctx context.Context, id TaskID) (Task, error) {
	if ctx == nil {
		return Task{}, errors.New("get task context is required")
	}
	return scanTask(r.db.QueryRowContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE task_id = ?`, id))
}
func (r *Repository) GetTaskSkillSnapshot(ctx context.Context, id TaskID) (TaskSkillSnapshot, error) {
	if ctx == nil {
		return TaskSkillSnapshot{}, errors.New("get task skill snapshot context is required")
	}
	return scanTaskSkillSnapshot(r.db.QueryRowContext(ctx, `
        SELECT task_id, skill_ids, instructions, content_hash, created_at
        FROM task_skill_snapshots WHERE task_id = ?`, id))
}

func (r *Repository) TransitionTask(
	ctx context.Context,
	id TaskID,
	expectedVersion uint64,
	to TaskState,
	reason string,
	now time.Time,
) (Task, error) {
	return r.changeTask(ctx, id, expectedVersion, reason, now, func(task *Task) error {
		return task.Transition(to, now)
	})
}

func (r *Repository) CancelTask(
	ctx context.Context,
	id TaskID,
	expectedVersion uint64,
	reason string,
	now time.Time,
) (Task, error) {
	return r.changeTask(ctx, id, expectedVersion, reason, now, func(task *Task) error {
		return task.Cancel(now)
	})
}

func (r *Repository) changeTask(
	ctx context.Context,
	id TaskID,
	expectedVersion uint64,
	reason string,
	now time.Time,
	change func(*Task) error,
) (Task, error) {
	if ctx == nil {
		return Task{}, errors.New("transition task context is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, fmt.Errorf("begin task transition: %w", err)
	}
	defer tx.Rollback()

	task, err := scanTask(tx.QueryRowContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE task_id = ?`, id))
	if err != nil {
		return Task{}, err
	}
	if task.Version != expectedVersion {
		return Task{}, versionConflict("task", string(id), expectedVersion, task.Version)
	}
	previous := task.State
	if err := change(&task); err != nil {
		return Task{}, err
	}

	result, err := tx.ExecContext(ctx, `
        UPDATE tasks SET state = ?, version = ?, updated_at = ?
        WHERE task_id = ? AND version = ?`,
		task.State, task.Version, formatTime(task.UpdatedAt), task.ID, expectedVersion,
	)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	if err := requireOneVersionedRow(result, "task", string(id), expectedVersion); err != nil {
		return Task{}, err
	}
	ev, err := aggregateEvent(
		"task", string(task.ID), task.Version, "task.state_changed", task.UpdatedAt,
		map[string]any{
			"from_state": previous,
			"to_state":   task.State,
			"reason":     strings.TrimSpace(reason),
		},
	)
	if err != nil {
		return Task{}, err
	}
	if _, err := r.events.AppendTx(ctx, tx, ev); err != nil {
		return Task{}, fmt.Errorf("append task transition event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Task{}, fmt.Errorf("commit task transition: %w", err)
	}
	return task, nil
}

// RecoverableTasks returns non-terminal tasks that Kernel must restore after a
// process restart.
func (r *Repository) RecoverableTasks(ctx context.Context, limit int) ([]Task, error) {
	if ctx == nil {
		return nil, errors.New("recover tasks context is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("recover task limit must be between 1 and 1000")
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks
        WHERE state NOT IN (?, ?)
        ORDER BY created_at, task_id
        LIMIT ?`, TaskCompleted, TaskCancelled, limit)
	if err != nil {
		return nil, fmt.Errorf("query recoverable tasks: %w", err)
	}
	defer rows.Close()

	result := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable tasks: %w", err)
	}
	return result, nil
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var id, sessionID, state, executionMode, createdAt, updatedAt string
	var version int64
	if err := row.Scan(
		&id, &sessionID, &task.Title, &task.Objective, &state, &executionMode,
		&version, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, fmt.Errorf("scan task: %w", err)
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return Task{}, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return Task{}, err
	}
	task.ID = TaskID(id)
	task.SessionID = SessionID(sessionID)
	task.State = TaskState(state)
	task.ExecutionMode = NormalizeExecutionMode(executionMode)
	task.Version = uint64(version)
	task.CreatedAt = created
	task.UpdatedAt = updated
	return task, nil
}

func newTaskSkillSnapshot(taskID TaskID, skillIDs []string, instructions string, now time.Time) (TaskSkillSnapshot, string, error) {
	normalized := make([]string, 0, len(skillIDs))
	seen := make(map[string]struct{}, len(skillIDs))
	for _, rawID := range skillIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return TaskSkillSnapshot{}, "", fmt.Errorf("%w: skill ID is required", ErrInvalidAggregate)
		}
		if _, exists := seen[id]; exists {
			return TaskSkillSnapshot{}, "", fmt.Errorf("%w: duplicate skill ID %q", ErrInvalidAggregate, id)
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 && instructions != "" {
		return TaskSkillSnapshot{}, "", fmt.Errorf("%w: skill instructions require at least one skill ID", ErrInvalidAggregate)
	}
	if len(normalized) != 0 && strings.TrimSpace(instructions) == "" {
		return TaskSkillSnapshot{}, "", fmt.Errorf("%w: selected skills require instructions", ErrInvalidAggregate)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return TaskSkillSnapshot{}, "", fmt.Errorf("encode task skill IDs: %w", err)
	}
	digest := sha256.Sum256([]byte(instructions))
	return TaskSkillSnapshot{
		TaskID: taskID, SkillIDs: normalized, Instructions: instructions,
		ContentHash: hex.EncodeToString(digest[:]), CreatedAt: normalizedTime(now),
	}, string(encoded), nil
}

func scanTaskSkillSnapshot(row scanner) (TaskSkillSnapshot, error) {
	var snapshot TaskSkillSnapshot
	var taskID, encodedSkillIDs, createdAt string
	if err := row.Scan(&taskID, &encodedSkillIDs, &snapshot.Instructions, &snapshot.ContentHash, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskSkillSnapshot{}, ErrNotFound
		}
		return TaskSkillSnapshot{}, fmt.Errorf("scan task skill snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(encodedSkillIDs), &snapshot.SkillIDs); err != nil {
		return TaskSkillSnapshot{}, fmt.Errorf("decode task skill IDs: %w", err)
	}
	validated, _, err := newTaskSkillSnapshot(TaskID(taskID), snapshot.SkillIDs, snapshot.Instructions, time.Time{})
	if err != nil {
		return TaskSkillSnapshot{}, fmt.Errorf("validate task skill snapshot: %w", err)
	}
	if validated.ContentHash != snapshot.ContentHash {
		return TaskSkillSnapshot{}, errors.New("task skill snapshot content hash mismatch")
	}
	created, err := parseTime(createdAt)
	if err != nil {
		return TaskSkillSnapshot{}, err
	}
	snapshot.TaskID = TaskID(taskID)
	snapshot.SkillIDs = validated.SkillIDs
	snapshot.CreatedAt = created
	return snapshot, nil
}

func (r *Repository) ListSessionTasks(ctx context.Context, sessionID SessionID) ([]Task, error) {
	if ctx == nil {
		return nil, errors.New("list session tasks context is required")
	}
	if strings.TrimSpace(string(sessionID)) == "" {
		return nil, fmt.Errorf("%w: session ID is required", ErrInvalidAggregate)
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT task_id, session_id, title, objective, state, execution_mode, version, created_at, updated_at
        FROM tasks WHERE session_id = ?
        ORDER BY created_at ASC, task_id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session tasks: %w", err)
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (r *Repository) ListRunIDsForTask(ctx context.Context, taskID TaskID) ([]RunID, error) {
	if ctx == nil {
		return nil, errors.New("list run ids context is required")
	}
	if strings.TrimSpace(string(taskID)) == "" {
		return nil, fmt.Errorf("%w: task ID is required", ErrInvalidAggregate)
	}
	rows, err := r.db.QueryContext(ctx, `
        SELECT run_id FROM runs WHERE task_id = ? ORDER BY started_at ASC, run_id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list run ids: %w", err)
	}
	defer rows.Close()
	out := make([]RunID, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if s := strings.TrimSpace(id); s != "" {
			out = append(out, RunID(s))
		}
	}
	return out, rows.Err()
}
