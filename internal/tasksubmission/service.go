// Package tasksubmission coordinates the user-facing task creation use case.
// Domain state changes remain owned by Kernel. Both agent and plan modes start session chat.
package tasksubmission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
)

const defaultMaxSkillContextBytes = 64 * 1024

var (
	ErrConflict = errors.New("task submission conflicts with an existing task")
	// ErrPlanning marks missing chat configuration (provider/agent required).
	ErrPlanning = errors.New("task submission chat is not configured")
)

type Repository interface {
	CreateSession(context.Context, kernel.SessionID, time.Time) (kernel.Session, error)
	CreateSessionWithWorkspace(context.Context, kernel.SessionID, string, string, time.Time) (kernel.Session, error)
	EnsureSessionWorkspace(context.Context, kernel.SessionID, string) error
	SetSessionPermissionStance(context.Context, kernel.SessionID, string) error
	SetSessionPreferredModel(context.Context, kernel.SessionID, string) error
	GetSession(context.Context, kernel.SessionID) (kernel.Session, error)
	CreateTaskWithSkillSnapshot(context.Context, kernel.TaskID, kernel.SessionID, string, string, []string, string, kernel.ExecutionMode, time.Time) (kernel.Task, error)
	GetTask(context.Context, kernel.TaskID) (kernel.Task, error)
	GetTaskSkillSnapshot(context.Context, kernel.TaskID) (kernel.TaskSkillSnapshot, error)
}

// ChatStarter starts multi-turn session chat for agent (build) or plan (read-only) modes.
type ChatStarter interface {
	StartChat(context.Context, ChatStartRequest) (ChatStartResult, error)
}

// ChatStartRequest/Result avoid importing chatsession into this package's public graph.
type ChatStartRequest struct {
	Task     kernel.Task
	Actor    string
	TraceID  string
	UserText string
	// ModelRef is an optional job-pinned selection ref (H7). Empty → session prefer → main.
	ModelRef string
	// Interactive is true when the caller can decide /perm (TUI).
	Interactive bool
}

type ChatStartResult struct {
	Task   kernel.Task
	RunID  kernel.RunID
	PlanID kernel.PlanID
}

type Config struct {
	Repository           Repository
	Chat                 ChatStarter
	Skills               *skillcatalog.Catalog
	SkillUsage           SkillUsageRecorder
	MaxSkillContextBytes int
	Now                  func() time.Time
	NewID                func(string) (string, error)
}

// SkillUsageRecorder records skill_ids as last-used (ADR-050).
type SkillUsageRecorder interface {
	RecordUsed(ctx context.Context, skillIDs []string, actor string) error
}

type Service struct {
	repository           Repository
	chat                 ChatStarter
	skills               *skillcatalog.Catalog
	skillUsage           SkillUsageRecorder
	maxSkillContextBytes int
	now                  func() time.Time
	newID                func(string) (string, error)
}

type Request struct {
	TaskID        kernel.TaskID
	SessionID     kernel.SessionID
	PlanID        kernel.PlanID
	Title         string
	Objective     string
	SkillIDs      []string
	ExecutionMode kernel.ExecutionMode
	// ModelRef is an optional job-pinned selection ref (H7). Empty → session prefer → main.
	ModelRef string
	// Workspace is the client launch directory (absolute); bound to session on create (ADR-046).
	Workspace string
	// PermissionStance is the Tab posture written onto the session (agent|auto|plan).
	// Empty on an existing session leaves the stored stance unchanged.
	PermissionStance string
	// PreferredModel is an optional session model preference (provider/model).
	// Empty leaves the stored preference unchanged. Applied before StartChat so the first turn sees it.
	PreferredModel string
	// Interactive is true when a TUI can answer pending /perm.
	Interactive   bool
	EnsureSession bool
	AllowExisting bool
	// TraceID is optional; empty means chatsession will use run_id after create (ADR-047).
	TraceID string
}

type Result struct {
	Task   kernel.Task
	Plan   *approval.PlanDocument
	PlanID kernel.PlanID
	RunID  kernel.RunID
}

func New(config Config) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("task submission repository is required")
	}
	if config.MaxSkillContextBytes < 0 {
		return nil, errors.New("task submission skill context byte budget cannot be negative")
	}
	if config.MaxSkillContextBytes == 0 {
		config.MaxSkillContextBytes = defaultMaxSkillContextBytes
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.NewID == nil {
		config.NewID = randomID
	}
	return &Service{
		repository:           config.Repository,
		chat:                 config.Chat,
		skills:               config.Skills,
		skillUsage:           config.SkillUsage,
		maxSkillContextBytes: config.MaxSkillContextBytes,
		now:                  config.Now,
		newID:                config.NewID,
	}, nil
}

func (s *Service) Submit(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("task submission context is required")
	}
	request.Title = strings.TrimSpace(request.Title)
	request.Objective = strings.TrimSpace(request.Objective)
	if request.Title == "" || request.Objective == "" {
		return Result{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: task title and objective are required", kernel.ErrInvalidAggregate))
	}
	request.ExecutionMode = kernel.NormalizeExecutionMode(string(request.ExecutionMode))
	if !request.ExecutionMode.Valid() {
		return Result{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: execution_mode must be plan or agent", kernel.ErrInvalidAggregate))
	}

	var err error
	if request.SessionID == "" {
		if !request.EnsureSession {
			return Result{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: session ID is required", kernel.ErrInvalidAggregate))
		}
		request.SessionID, err = s.nextSessionID()
		if err != nil {
			return Result{}, err
		}
	}
	if request.TaskID == "" {
		request.TaskID, err = s.nextTaskID()
		if err != nil {
			return Result{}, err
		}
	}
	if request.EnsureSession {
		if err := s.ensureSession(ctx, request.SessionID, request.Workspace, request.PermissionStance, request.PreferredModel); err != nil {
			return Result{}, classifyError(err)
		}
	}

	skillIDs, skillContext, err := s.resolveSkills(request.SkillIDs)
	if err != nil {
		return Result{}, classifyError(err)
	}
	request.SkillIDs = skillIDs

	var task kernel.Task
	if request.AllowExisting {
		task, err = s.repository.GetTask(ctx, request.TaskID)
		if err == nil {
			return s.continueSubmission(ctx, task, request)
		}
		if !errors.Is(err, kernel.ErrNotFound) {
			return Result{}, classifyError(err)
		}
	}

	task, err = s.repository.CreateTaskWithSkillSnapshot(
		ctx, request.TaskID, request.SessionID, request.Title, request.Objective,
		skillIDs, skillContext, request.ExecutionMode, s.now(),
	)
	if errors.Is(err, kernel.ErrAlreadyExists) && request.AllowExisting {
		task, err = s.repository.GetTask(ctx, request.TaskID)
		if err == nil {
			return s.continueSubmission(ctx, task, request)
		}
	}
	if err != nil {
		return Result{}, classifyError(err)
	}
	if s.skillUsage != nil && len(skillIDs) > 0 {
		actor := "user"
		if strings.HasPrefix(string(request.TaskID), "scheduled_") {
			actor = "scheduler"
		}
		_ = s.skillUsage.RecordUsed(ctx, skillIDs, actor)
	}
	return s.planTask(ctx, task, request)
}

func (s *Service) continueSubmission(ctx context.Context, task kernel.Task, request Request) (Result, error) {
	snapshot, err := s.repository.GetTaskSkillSnapshot(ctx, task.ID)
	if err != nil {
		return Result{}, classifyError(err)
	}
	if !sameTask(task, snapshot, request) {
		return Result{}, applicationerror.Wrap(applicationerror.CodeConflict, false, fmt.Errorf("%w: task %s", ErrConflict, request.TaskID))
	}
	return s.planTask(ctx, task, request)
}

func (s *Service) planTask(ctx context.Context, task kernel.Task, request Request) (Result, error) {
	// Both agent (build) and plan (read-only) use session chat — no Planner.
	return s.startChat(ctx, task, request)
}

func (s *Service) startChat(ctx context.Context, task kernel.Task, request Request) (Result, error) {
	result := Result{Task: task}
	if s.chat == nil {
		return result, applicationerror.Wrap(applicationerror.CodeUnavailable, false,
			fmt.Errorf("%w: chat session is not configured (provider/agent required)", ErrPlanning))
	}
	switch task.State {
	case kernel.TaskCreated, kernel.TaskRunning, kernel.TaskCompleted, kernel.TaskFailed:
		// StartChat is idempotent for already-started turns.
	default:
		return result, applicationerror.Wrap(applicationerror.CodeConflict, false, fmt.Errorf("%w: task %s is already %s", ErrConflict, task.ID, task.State))
	}
	actor := "local-user"
	// Deterministic job tasks (scheduledtasks) are non-interactive (ADR-043).
	if strings.HasPrefix(string(task.ID), "scheduled_") {
		actor = "scheduler"
	}
	traceID := strings.TrimSpace(request.TraceID)
	slog.Info("tasksubmission start chat", runlog.Attrs("tasksubmission", "start_chat", "started", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(task.ID), TraceID: traceID,
	}, "actor", actor, "execution_mode", string(task.ExecutionMode))...)
	chatResult, err := s.chat.StartChat(ctx, ChatStartRequest{
		Task: task, Actor: actor, TraceID: traceID, UserText: request.Objective,
		ModelRef: strings.TrimSpace(request.ModelRef), Interactive: request.Interactive,
	})
	if err != nil {
		slog.Error("tasksubmission start chat failed", runlog.Attrs("tasksubmission", "start_chat", "failed", runlog.IDs{
			SessionID: string(task.SessionID), TaskID: string(task.ID), TraceID: traceID,
		}, "error", err)...)
		return Result{Task: task}, err
	}
	slog.Info("tasksubmission start chat completed", runlog.Attrs("tasksubmission", "start_chat", "succeeded", runlog.IDs{
		SessionID: string(chatResult.Task.SessionID), TaskID: string(chatResult.Task.ID),
		RunID: string(chatResult.RunID), PlanID: string(chatResult.PlanID), TraceID: traceID,
	})...)
	return Result{Task: chatResult.Task, PlanID: chatResult.PlanID, RunID: chatResult.RunID}, nil
}

func (s *Service) resolveSkills(requested []string) ([]string, string, error) {
	if s.skills == nil {
		if len(requested) == 0 {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("%w: skill catalog is unavailable", kernel.ErrInvalidAggregate)
	}
	explicit := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		explicit = append(explicit, id)
	}
	if len(explicit) == 0 {
		return nil, "", nil
	}
	selected, err := s.skills.Select(explicit)
	if err != nil {
		return nil, "", fmt.Errorf("%w: select skills: %w", kernel.ErrInvalidAggregate, err)
	}
	contextText, err := s.skills.Context(selected, s.maxSkillContextBytes)
	if err != nil {
		return nil, "", fmt.Errorf("%w: render skill context: %w", kernel.ErrInvalidAggregate, err)
	}
	ids := make([]string, len(selected))
	for index, skill := range selected {
		ids[index] = skill.ID
	}
	return ids, contextText, nil
}

func classifyError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, kernel.ErrInvalidAggregate):
		return applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, err)
	case errors.Is(err, ErrConflict), errors.Is(err, kernel.ErrAlreadyExists), errors.Is(err, kernel.ErrSessionClosed):
		return applicationerror.Wrap(applicationerror.CodeConflict, false, err)
	case errors.Is(err, kernel.ErrNotFound):
		return applicationerror.Wrap(applicationerror.CodeNotFound, false, err)
	default:
		return err
	}
}

func (s *Service) ensureSession(ctx context.Context, id kernel.SessionID, workspace, stance, preferredModel string) error {
	workspace = strings.TrimSpace(workspace)
	stance = strings.TrimSpace(stance)
	preferredModel = strings.TrimSpace(preferredModel)
	// Empty stance means "leave existing" on update; create still defaults to agent.
	var normalized string
	if stance != "" {
		var err error
		normalized, err = kernel.NormalizePermissionStance(stance)
		if err != nil {
			return err
		}
	}
	session, err := s.repository.GetSession(ctx, id)
	if err == nil {
		if session.State != kernel.SessionActive {
			return fmt.Errorf("%w: %s", kernel.ErrSessionClosed, id)
		}
		if workspace != "" {
			_ = s.repository.EnsureSessionWorkspace(ctx, id, workspace)
		}
		if normalized != "" && normalized != session.PermissionStance {
			if err := s.repository.SetSessionPermissionStance(ctx, id, normalized); err != nil {
				return err
			}
		}
		return s.applyPreferredModel(ctx, id, preferredModel)
	}
	if !errors.Is(err, kernel.ErrNotFound) {
		return err
	}
	_, err = s.repository.CreateSessionWithWorkspace(ctx, id, workspace, normalized, s.now())
	if errors.Is(err, kernel.ErrAlreadyExists) {
		session, err = s.repository.GetSession(ctx, id)
		if err == nil && session.State != kernel.SessionActive {
			return fmt.Errorf("%w: %s", kernel.ErrSessionClosed, id)
		}
		if err == nil && workspace != "" {
			_ = s.repository.EnsureSessionWorkspace(ctx, id, workspace)
		}
		if err == nil && normalized != "" && normalized != session.PermissionStance {
			if setErr := s.repository.SetSessionPermissionStance(ctx, id, normalized); setErr != nil {
				return setErr
			}
		}
		if err != nil {
			return err
		}
		return s.applyPreferredModel(ctx, id, preferredModel)
	}
	if err != nil {
		return err
	}
	return s.applyPreferredModel(ctx, id, preferredModel)
}

func (s *Service) applyPreferredModel(ctx context.Context, id kernel.SessionID, preferredModel string) error {
	if strings.TrimSpace(preferredModel) == "" {
		return nil
	}
	return s.repository.SetSessionPreferredModel(ctx, id, preferredModel)
}

func (s *Service) nextSessionID() (kernel.SessionID, error) {
	value, err := s.newID("session-")
	return kernel.SessionID(value), err
}

func (s *Service) nextTaskID() (kernel.TaskID, error) {
	value, err := s.newID("task-")
	return kernel.TaskID(value), err
}

func (s *Service) nextPlanID() (kernel.PlanID, error) {
	value, err := s.newID("plan-")
	return kernel.PlanID(value), err
}

func sameTask(task kernel.Task, snapshot kernel.TaskSkillSnapshot, request Request) bool {
	mode := request.ExecutionMode
	if mode == "" {
		mode = kernel.ExecutionModeAgent
	}
	taskMode := task.ExecutionMode
	if taskMode == "" {
		taskMode = kernel.ExecutionModeAgent
	}
	return task.SessionID == request.SessionID &&
		task.Title == request.Title &&
		task.Objective == request.Objective &&
		taskMode == mode &&
		slices.Equal(snapshot.SkillIDs, request.SkillIDs)
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s ID: %w", strings.TrimSuffix(prefix, "-"), err)
	}
	return prefix + hex.EncodeToString(value), nil
}
