package tui

import (
	"context"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

// Gateway is the narrow local-gateway surface used by the TUI.
// Production uses *gatewayclient.Client; tests may inject a fake.
type Gateway interface {
	StreamEvents(ctx context.Context, after uint64, emit func(eventapi.Envelope) error) error
	StreamModelEvents(ctx context.Context, sessionID gatewayclient.SessionID, runID gatewayclient.RunID, emit func(modelstream.Envelope) error) error

	Health(ctx context.Context) (gatewayclient.Health, error)
	ModelConfig(ctx context.Context) (gatewayclient.ModelConfig, error)
	SetModelConfig(ctx context.Context, model string) (gatewayclient.ModelConfig, error)
	MCPStatus(ctx context.Context) (gatewayclient.MCPStatus, error)
	ListSkills(ctx context.Context) ([]gatewayclient.Skill, error)
	ListSkillsFilter(ctx context.Context, includeArchived bool) ([]gatewayclient.Skill, error)
	ListSkillEvents(ctx context.Context, skillID string, limit int) ([]gatewayclient.SkillEvent, error)
	ApplySkillDraft(ctx context.Context, skillID string) error
	RejectSkillDraft(ctx context.Context, skillID string) error
	ListChatCommands(ctx context.Context) ([]gatewayclient.ChatCommand, error)

	ListSessions(ctx context.Context, limit int) ([]gatewayclient.Session, error)
	GetSession(ctx context.Context, id gatewayclient.SessionID) (gatewayclient.Session, error)
	SetSessionPreferredModel(ctx context.Context, id gatewayclient.SessionID, model string) (gatewayclient.Session, error)
	SetSessionPermissionStance(ctx context.Context, id gatewayclient.SessionID, stance string) (gatewayclient.Session, error)
	SessionMessages(ctx context.Context, id gatewayclient.SessionID, limit int) ([]gatewayclient.TranscriptMessage, error)
	ListSessionTodos(ctx context.Context, id gatewayclient.SessionID) ([]gatewayclient.SessionTodo, error)
	TaskMessages(ctx context.Context, id gatewayclient.TaskID, limit int) ([]gatewayclient.TranscriptMessage, error)
	CompactSession(ctx context.Context, id gatewayclient.SessionID, focus string) (gatewayclient.CompactResult, error)
	SteerSession(ctx context.Context, id gatewayclient.SessionID, text string) (gatewayclient.SteerResult, error)
	RewindSession(ctx context.Context, id gatewayclient.SessionID, revisionID string) (gatewayclient.RewindResult, error)

	ListTasks(ctx context.Context, limit int) ([]gatewayclient.Task, error)
	GetTask(ctx context.Context, id gatewayclient.TaskID) (gatewayclient.Task, error)
	TaskUsage(ctx context.Context, id gatewayclient.TaskID) (gatewayclient.TaskUsage, error)
	TaskContext(ctx context.Context, id gatewayclient.TaskID) (gatewayclient.TaskContext, error)
	SubmitTask(ctx context.Context, req gatewayclient.TaskSubmissionRequest) (gatewayclient.TaskSubmissionResponse, error)
	ControlTask(ctx context.Context, id gatewayclient.TaskID, action gatewayclient.TaskAction, expectedVersion uint64, reason string) (gatewayclient.Task, error)

	GetPlan(ctx context.Context, id gatewayclient.PlanID) (gatewayclient.Plan, error)
	FindPlanForTask(ctx context.Context, taskID gatewayclient.TaskID) (gatewayclient.Plan, error)

	ListRuns(ctx context.Context, taskID gatewayclient.TaskID, limit int) ([]gatewayclient.Run, error)
	RunUsage(ctx context.Context, id gatewayclient.RunID) (gatewayclient.RunUsage, error)

	ListJobs(ctx context.Context, includeArchived bool) ([]schedulerapi.Job, error)
	CreateJob(ctx context.Context, request schedulerapi.CreateRequest) (schedulerapi.Job, error)

	ListPermissions(ctx context.Context, sessionID string, limit int) ([]gatewayclient.Permission, error)
	DecidePermission(ctx context.Context, permissionID, decision string) (gatewayclient.Permission, error)
	DecidePermissionConfirm(ctx context.Context, permissionID, decision string, confirm bool) (gatewayclient.Permission, error)
	ListQuestions(ctx context.Context, sessionID string, limit int) ([]gatewayclient.UserQuestion, error)
	AnswerQuestion(ctx context.Context, id, actor string, answers map[string][]string) (gatewayclient.UserQuestion, error)

	ListMemory(ctx context.Context, sessionID, query, kind string, limit int) ([]gatewayclient.MemoryEntry, error)
	ListMemoryFilter(ctx context.Context, sessionID, query, kind string, limit int, includeArchived bool) ([]gatewayclient.MemoryEntry, error)
	RefreshMemory(ctx context.Context, sessionID string) error
	ForgetMemory(ctx context.Context, entryID string) error
	PromoteMemory(ctx context.Context, entryID string) (gatewayclient.MemoryEntry, error)
}

// Compile-time check: production client satisfies the TUI surface.
var _ Gateway = (*gatewayclient.Client)(nil)
