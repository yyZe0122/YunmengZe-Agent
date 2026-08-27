// Package kernel contains the Session/Task/Plan/Run state machines.
package kernel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/coreidentity"
)

type SessionID = coreidentity.SessionID
type TaskID = coreidentity.TaskID
type PlanID = coreidentity.PlanID
type StepID = coreidentity.StepID
type RunID = coreidentity.RunID

var (
	ErrInvalidAggregate  = errors.New("invalid aggregate")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrVersionConflict   = errors.New("aggregate version conflict")
	ErrNotFound          = errors.New("aggregate not found")
)

type TransitionError struct {
	Aggregate string
	From      string
	To        string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s -> %s: %v", e.Aggregate, e.From, e.To, ErrInvalidTransition)
}

func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }

type SessionState string

const (
	SessionActive SessionState = "active"
	SessionClosed SessionState = "closed"
)

type Session struct {
	ID        SessionID
	State     SessionState
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
	// Workspace is the absolute client launch directory for this session (ADR-046).
	// Empty until set from task submit.
	Workspace string
	// PreferredModel is an optional session model preference (provider/model).
	// O4: chat runs resolve job pin → prefer → main via modelresolve; does not rewrite global config.
	PreferredModel string
	// PermissionStance is the Tab posture for this session: agent | auto | plan.
	// Empty means agent. Independent of task execution_mode (still only agent|plan).
	PermissionStance string
	// HiddenTaskIDs are retracted turns (soft-hide). Transcript and packing skip these tasks.
	HiddenTaskIDs []string
}

const (
	PermissionStanceAgent = "agent"
	PermissionStanceAuto  = "auto"
	PermissionStancePlan  = "plan"
)

func NormalizePermissionStance(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PermissionStanceAgent:
		return PermissionStanceAgent, nil
	case PermissionStanceAuto:
		return PermissionStanceAuto, nil
	case PermissionStancePlan:
		return PermissionStancePlan, nil
	default:
		return "", fmt.Errorf("%w: permission_stance must be agent, auto, or plan", ErrInvalidAggregate)
	}
}

func NewSession(id SessionID, now time.Time) (Session, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Session{}, fmt.Errorf("%w: session ID is required", ErrInvalidAggregate)
	}
	now = normalizedTime(now)
	return Session{ID: id, State: SessionActive, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Session) Close(now time.Time) error {
	if s.State != SessionActive {
		return &TransitionError{Aggregate: "session", From: string(s.State), To: string(SessionClosed)}
	}
	s.State = SessionClosed
	s.Version++
	s.UpdatedAt = normalizedTime(now)
	return nil
}

type TaskState string

const (
	TaskCreated   TaskState = "created"
	TaskRunning   TaskState = "running"
	TaskPaused    TaskState = "paused"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
)

var taskTransitions = map[TaskState]map[TaskState]struct{}{
	TaskCreated: {
		TaskRunning:   {},
		TaskCancelled: {},
	},
	TaskRunning: {
		TaskPaused:    {},
		TaskCompleted: {},
		TaskFailed:    {},
		TaskCancelled: {},
	},
	TaskPaused: {
		TaskRunning:   {},
		TaskCancelled: {},
	},
	TaskFailed: {
		TaskCancelled: {},
	},
}

// ExecutionMode is the task-level posture set at create time.
// agent: build (workspace write grants via chatsession).
// plan: read-only chat (same chatsession path; no write grants).
type ExecutionMode string

const (
	ExecutionModeAgent ExecutionMode = "agent"
	ExecutionModePlan  ExecutionMode = "plan"
)

func (m ExecutionMode) Valid() bool {
	return m == ExecutionModeAgent || m == ExecutionModePlan
}

func NormalizeExecutionMode(value string) ExecutionMode {
	switch ExecutionMode(strings.TrimSpace(value)) {
	case ExecutionModePlan:
		return ExecutionModePlan
	default:
		return ExecutionModeAgent
	}
}

type Task struct {
	ID            TaskID
	SessionID     SessionID
	Title         string
	Objective     string
	State         TaskState
	ExecutionMode ExecutionMode
	Version       uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TaskSkillSnapshot is the immutable, task-bound planning input selected from
// file-based Skills. Instructions are a content snapshot, not an authority or grant.
type TaskSkillSnapshot struct {
	TaskID       TaskID
	SkillIDs     []string
	Instructions string
	ContentHash  string
	CreatedAt    time.Time
}

func NewTask(id TaskID, sessionID SessionID, title, objective string, now time.Time) (Task, error) {
	return NewTaskWithMode(id, sessionID, title, objective, ExecutionModeAgent, now)
}

func NewTaskWithMode(id TaskID, sessionID SessionID, title, objective string, mode ExecutionMode, now time.Time) (Task, error) {
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(string(sessionID)) == "" {
		return Task{}, fmt.Errorf("%w: task and session IDs are required", ErrInvalidAggregate)
	}
	title = strings.TrimSpace(title)
	objective = strings.TrimSpace(objective)
	if title == "" || objective == "" {
		return Task{}, fmt.Errorf("%w: task title and objective are required", ErrInvalidAggregate)
	}
	if mode == "" {
		mode = ExecutionModeAgent
	}
	if !mode.Valid() {
		return Task{}, fmt.Errorf("%w: execution_mode must be plan or agent", ErrInvalidAggregate)
	}
	now = normalizedTime(now)
	return Task{
		ID: id, SessionID: sessionID, Title: title, Objective: objective,
		State: TaskCreated, ExecutionMode: mode, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (t *Task) Transition(to TaskState, now time.Time) error {
	allowed := taskTransitions[t.State]
	if _, ok := allowed[to]; !ok {
		return &TransitionError{Aggregate: "task", From: string(t.State), To: string(to)}
	}
	t.State = to
	t.Version++
	t.UpdatedAt = normalizedTime(now)
	return nil
}

func (t *Task) Cancel(now time.Time) error {
	return t.Transition(TaskCancelled, now)
}

type PlanState string

const (
	PlanDraft      PlanState = "draft"
	PlanApproved   PlanState = "approved"
	PlanSuperseded PlanState = "superseded"
)

var planTransitions = map[PlanState]map[PlanState]struct{}{
	PlanDraft: {
		PlanApproved:   {}, // session-chat synthetic workspace plan
		PlanSuperseded: {},
	},
	PlanApproved: {
		PlanSuperseded: {},
	},
}

type Plan struct {
	ID        PlanID
	TaskID    TaskID
	Revision  uint64
	State     PlanState
	ScopeHash string
	Steps     []PlanStep
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewPlan(id PlanID, taskID TaskID, revision uint64, scopeHash string, now time.Time) (Plan, error) {
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(string(taskID)) == "" || revision == 0 {
		return Plan{}, fmt.Errorf("%w: plan ID, task ID, and revision are required", ErrInvalidAggregate)
	}
	if strings.TrimSpace(scopeHash) == "" {
		return Plan{}, fmt.Errorf("%w: plan scope hash is required", ErrInvalidAggregate)
	}
	now = normalizedTime(now)
	return Plan{
		ID: id, TaskID: taskID, Revision: revision, State: PlanDraft,
		ScopeHash: scopeHash, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (p *Plan) Transition(to PlanState, now time.Time) error {
	if _, ok := planTransitions[p.State][to]; !ok {
		return &TransitionError{Aggregate: "plan", From: string(p.State), To: string(to)}
	}
	p.State = to
	p.Version++
	p.UpdatedAt = normalizedTime(now)
	return nil
}

func (p *Plan) AddStep(step PlanStep, now time.Time) error {
	if p.State != PlanDraft {
		return &TransitionError{Aggregate: "plan", From: string(p.State), To: "add_step"}
	}
	if step.PlanID != p.ID {
		return fmt.Errorf("%w: step belongs to another plan", ErrInvalidAggregate)
	}
	for _, existing := range p.Steps {
		if existing.ID == step.ID || existing.Position == step.Position {
			return fmt.Errorf("%w: duplicate plan step ID or position", ErrInvalidAggregate)
		}
	}
	p.Steps = append(p.Steps, step)
	p.Version++
	p.UpdatedAt = normalizedTime(now)
	return nil
}

type StepState string

const (
	StepPending   StepState = "pending"
	StepApproved  StepState = "approved"
	StepRunning   StepState = "running"
	StepCompleted StepState = "completed"
	StepFailed    StepState = "failed"
	StepSkipped   StepState = "skipped"
)

var stepTransitions = map[StepState]map[StepState]struct{}{
	StepPending: {
		StepApproved: {},
		StepSkipped:  {},
	},
	StepApproved: {
		StepRunning: {},
		StepSkipped: {},
	},
	StepRunning: {
		StepCompleted: {},
		StepFailed:    {},
	},
}

type PlanStepDraft struct {
	ID          StepID
	Position    int
	Title       string
	EffectLevel string
}

type PlanStep struct {
	ID          StepID
	PlanID      PlanID
	Position    int
	Title       string
	State       StepState
	EffectLevel string
	Version     uint64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func NewPlanStep(id StepID, planID PlanID, position int, title, effectLevel string, now time.Time) (PlanStep, error) {
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(string(planID)) == "" || position < 0 {
		return PlanStep{}, fmt.Errorf("%w: step ID, plan ID, and non-negative position are required", ErrInvalidAggregate)
	}
	title = strings.TrimSpace(title)
	effectLevel = strings.TrimSpace(effectLevel)
	if title == "" || effectLevel == "" {
		return PlanStep{}, fmt.Errorf("%w: step title and effect level are required", ErrInvalidAggregate)
	}
	now = normalizedTime(now)
	return PlanStep{
		ID: id, PlanID: planID, Position: position, Title: title,
		State: StepPending, EffectLevel: effectLevel, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *PlanStep) Transition(to StepState, now time.Time) error {
	if _, ok := stepTransitions[s.State][to]; !ok {
		return &TransitionError{Aggregate: "plan_step", From: string(s.State), To: string(to)}
	}
	s.State = to
	s.Version++
	s.UpdatedAt = normalizedTime(now)
	return nil
}

type RunState string

const (
	RunCreated   RunState = "created"
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

var runTransitions = map[RunState]map[RunState]struct{}{
	RunCreated: {
		RunRunning:   {},
		RunCancelled: {},
	},
	RunRunning: {
		RunCompleted: {},
		RunFailed:    {},
		RunCancelled: {},
	},
}

type Run struct {
	ID         RunID
	TaskID     TaskID
	PlanID     PlanID
	State      RunState
	Version    uint64
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
	Error      string
}

func NewRun(id RunID, taskID TaskID, planID PlanID, now time.Time) (Run, error) {
	if strings.TrimSpace(string(id)) == "" || strings.TrimSpace(string(taskID)) == "" || strings.TrimSpace(string(planID)) == "" {
		return Run{}, fmt.Errorf("%w: run, task, and plan IDs are required", ErrInvalidAggregate)
	}
	now = normalizedTime(now)
	return Run{ID: id, TaskID: taskID, PlanID: planID, State: RunCreated, Version: 1, StartedAt: now, UpdatedAt: now}, nil
}

func (r *Run) Transition(to RunState, failure string, now time.Time) error {
	if _, ok := runTransitions[r.State][to]; !ok {
		return &TransitionError{Aggregate: "run", From: string(r.State), To: string(to)}
	}
	now = normalizedTime(now)
	r.State = to
	r.Version++
	r.UpdatedAt = now
	if to == RunCompleted || to == RunFailed || to == RunCancelled {
		finished := now
		r.FinishedAt = &finished
	}
	if to == RunFailed {
		r.Error = strings.TrimSpace(failure)
	}
	return nil
}

func normalizedTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
