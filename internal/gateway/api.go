package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/app"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/events"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/taskcontrol"
	"github.com/yyZe0122/yunmengze-agent/internal/tasksubmission"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

const (
	defaultListLimit = 100
	maximumListLimit = corequery.MaxPageSize
)

type QueryService interface {
	Check(context.Context) error
	ListSessions(context.Context, corequery.SessionListOptions) ([]corequery.Session, error)
	GetSession(context.Context, kernel.SessionID) (corequery.Session, error)
	SessionTranscript(context.Context, kernel.SessionID, corequery.TranscriptOptions) ([]corequery.TranscriptMessage, error)
	ListSessionTodos(context.Context, kernel.SessionID) ([]corequery.SessionTodo, error)
	TaskTranscript(context.Context, kernel.TaskID, corequery.TranscriptOptions) ([]corequery.TranscriptMessage, error)
	ListTasks(context.Context, corequery.TaskListOptions) ([]corequery.Task, error)
	GetTask(context.Context, kernel.TaskID) (corequery.Task, error)
	TaskUsage(context.Context, kernel.TaskID) (corequery.TaskUsage, error)
	RunUsage(context.Context, kernel.RunID) (corequery.RunUsage, error)
	TaskContext(context.Context, kernel.TaskID) (corequery.TaskContext, error)
	SessionContext(context.Context, kernel.SessionID) (corequery.TaskContext, error)
	ListPlans(context.Context, corequery.PlanListOptions) ([]corequery.Plan, error)
	GetPlan(context.Context, kernel.PlanID) (corequery.Plan, error)
	ListApprovals(context.Context, corequery.ApprovalListOptions) ([]corequery.Approval, error)
	ListRuns(context.Context, corequery.RunListOptions) ([]corequery.Run, error)
	GetRun(context.Context, kernel.RunID) (corequery.Run, error)
	ListMemory(context.Context, corequery.MemoryListOptions) ([]corequery.MemoryEntry, error)
	ListSkillEvents(context.Context, corequery.SkillEventListOptions) ([]corequery.SkillEvent, error)
}

type TaskSubmitter interface {
	Submit(context.Context, tasksubmission.Request) (tasksubmission.Result, error)
}

type TaskController interface {
	ControlTask(context.Context, taskcontrol.TaskActionRequest) (kernel.Task, error)
}

type JobService interface {
	Create(context.Context, schedulerapi.CreateRequest) (schedulerapi.Job, error)
	Get(context.Context, string) (schedulerapi.Job, error)
	List(context.Context, bool) ([]schedulerapi.Job, error)
	ChangeState(context.Context, schedulerapi.StateRequest, string) (schedulerapi.Job, error)
}

// ModelConfig is the model snapshot exposed by GET/PUT /v1/config/model.
// It must never include API keys or other secrets.
type ModelConfig struct {
	Model  string   `json:"model"`
	Models []string `json:"models"`
	// ContextWindow is the selected model's context length in tokens; 0 = unknown.
	ContextWindow int64 `json:"context_window,omitempty"`
	// Ready is true when the main model stack is usable for chat and /model switch
	// (config load OK and agent bound at daemon start; see ADR-048).
	Ready bool `json:"ready"`
	// Error is a secret-free explanation when Ready is false (load failure or restart required).
	Error string `json:"error,omitempty"`
}

// ModelSwitcher applies a provider/model selection at runtime.
type ModelSwitcher interface {
	SelectModel(ctx context.Context, ref string) (ModelConfig, error)
}

// MCPStatus is a secret-free MCP snapshot for GET /v1/config/mcp.
type MCPStatus struct {
	Enabled bool `json:"enabled"`
	Total   int  `json:"total"`
	OK      int  `json:"ok"`
	Error   int  `json:"error"`
	Tools   int  `json:"tools"`
}

// MCPStatusProvider supplies live MCP status (optional).
type MCPStatusProvider interface {
	MCPStatus() MCPStatus
}

// ChatCommandItem is one configured slash template (instruction text only; not a secret).
type ChatCommandItem struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	// Template is the user-message body; may contain $ARGUMENTS.
	Template string `json:"template"`
}

// ChatCommandsProvider lists chat.commands metadata (O3).
type ChatCommandsProvider interface {
	ChatCommands() []ChatCommandItem
}

// SessionCompactor forces durable session head summarization (manual /compact).
// Optional until chat is configured.
type SessionCompactor interface {
	ForceCompact(ctx context.Context, sessionID kernel.SessionID, focus string) (SessionCompactResult, error)
}

// SessionCompactResult is the JSON body for POST /v1/sessions/{id}/compact.
type SessionCompactResult struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	Source       string `json:"source"`
	CompactionID string `json:"compaction_id,omitempty"`
}

// SessionSteerer queues next-step user text on a running turn (ADR-052 R3).
type SessionSteerer interface {
	Steer(ctx context.Context, sessionID kernel.SessionID, text string) (SessionSteerResult, error)
}

type SessionSteerResult struct {
	SessionID string        `json:"session_id"`
	TaskID    kernel.TaskID `json:"task_id"`
	RunID     kernel.RunID  `json:"run_id"`
	ItemID    string        `json:"item_id"`
}

type SessionSteerFunc func(ctx context.Context, sessionID kernel.SessionID, text string) (SessionSteerResult, error)

func (f SessionSteerFunc) Steer(ctx context.Context, sessionID kernel.SessionID, text string) (SessionSteerResult, error) {
	return f(ctx, sessionID, text)
}

// SessionCompactFunc adapts a function to SessionCompactor.
type SessionCompactFunc func(ctx context.Context, sessionID kernel.SessionID, focus string) (SessionCompactResult, error)

func (f SessionCompactFunc) ForceCompact(ctx context.Context, sessionID kernel.SessionID, focus string) (SessionCompactResult, error) {
	return f(ctx, sessionID, focus)
}

// SessionRewinder restores the last (or specified) agent file edit.
type SessionRewinder interface {
	RewindEdit(ctx context.Context, sessionID kernel.SessionID, revisionID string) (SessionRewindResult, error)
}

type SessionRewindResult struct {
	SessionID  string `json:"session_id"`
	RevisionID string `json:"revision_id"`
	Path       string `json:"path"`
}

type SessionRewindFunc func(ctx context.Context, sessionID kernel.SessionID, revisionID string) (SessionRewindResult, error)

func (f SessionRewindFunc) RewindEdit(ctx context.Context, sessionID kernel.SessionID, revisionID string) (SessionRewindResult, error) {
	return f(ctx, sessionID, revisionID)
}

type APIConfig struct {
	Queries         QueryService
	TaskSubmissions TaskSubmitter
	TaskControls    TaskController
	Jobs            JobService
	Core            interface{ Status() app.Status }
	Events          *events.Store
	Skills          *skillcatalog.Catalog
	ModelConfig     ModelConfig
	ModelSwitcher   ModelSwitcher
	// ModelConfigError is set when provider config failed to load at daemon start.
	// Exposed on GET /v1/config/model as error + ready=false.
	ModelConfigError string
	// ModelStream is optional; when set, exposes GET /v1/model-stream.
	ModelStream *modelstream.Hub
	// MCP is optional; when set, exposes GET /v1/config/mcp.
	MCP MCPStatusProvider
	// ChatCommands is optional; when set, exposes GET /v1/config/commands (O3; metadata only).
	ChatCommands ChatCommandsProvider
	// SessionCompact is optional; when set, exposes POST /v1/sessions/{id}/compact.
	SessionCompact SessionCompactor
	// SessionRewind is optional; when set, exposes POST /v1/sessions/{id}/rewind (QG).
	SessionRewind SessionRewinder
	// SessionSteer is optional; when set, exposes POST /v1/sessions/{id}/steer (ADR-052 R3).
	SessionSteer SessionSteerer
	// UserQuestions is optional; when set, exposes GET /v1/questions and POST /v1/questions/{id}/answer (ADR-052 R4).
	UserQuestions UserQuestionService
	// ToolPermissions is optional; when set, exposes GET /v1/permissions and POST /v1/permissions/{id}/decide (ADR-043).
	ToolPermissions ToolPermissionService
	// MemoryControl is optional; when set, exposes POST /v1/memory/actions (refresh/forget/promote).
	MemoryControl MemoryControlService
	// SkillControl is optional; when set, exposes POST /v1/skills/actions (apply/reject).
	SkillControl SkillControlService
	// SessionPrefs is optional; PATCH session preferred_model / permission_stance.
	SessionPrefs SessionPreferenceService
}

// SessionPreferenceService updates session metadata preferences (not global model).
type SessionPreferenceService interface {
	SetPreferredModel(ctx context.Context, sessionID kernel.SessionID, model string) error
	SetPermissionStance(ctx context.Context, sessionID kernel.SessionID, stance string) error
}

// MemoryControlService is the narrow write surface for memory productization (not raw SQL).
type MemoryControlService interface {
	RefreshMemory(sessionID string)
	ForgetMemory(ctx context.Context, entryID string) error
	PromoteMemory(ctx context.Context, entryID string) (corequery.MemoryEntry, error)
}

// SkillControlService is the narrow write surface for skill draft apply/reject (ADR-050).
type SkillControlService interface {
	ApplySkillDraft(ctx context.Context, skillID, actor string) error
	RejectSkillDraft(ctx context.Context, skillID, actor string) error
	SkillUsage(ctx context.Context) (map[string]SkillUsageView, error)
}

// SkillUsageView is last-used / archive metadata for GET /v1/skills.
type SkillUsageView struct {
	LastUsedAt string `json:"last_used_at,omitempty"`
	ArchivedAt string `json:"archived_at,omitempty"`
}

// UserQuestionService lists and answers model-facing ask_user questions (ADR-052 R4).
type UserQuestionService interface {
	ListPending(ctx context.Context, sessionID string, limit int) ([]UserQuestionView, error)
	Answer(ctx context.Context, id, actor string, answers map[string][]string) (UserQuestionView, error)
}

type UserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type UserQuestionItem struct {
	ID          string               `json:"id"`
	Question    string               `json:"question"`
	Header      string               `json:"header,omitempty"`
	Options     []UserQuestionOption `json:"options,omitempty"`
	MultiSelect bool                 `json:"multi_select,omitempty"`
}

type UserQuestionView struct {
	ID         string              `json:"question_id"`
	SessionID  string              `json:"session_id,omitempty"`
	TaskID     string              `json:"task_id,omitempty"`
	RunID      string              `json:"run_id,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Questions  []UserQuestionItem  `json:"questions"`
	State      string              `json:"state"`
	Answers    map[string][]string `json:"answers,omitempty"`
	CreatedAt  string              `json:"created_at"`
	DecidedAt  string              `json:"decided_at,omitempty"`
}

// ToolPermissionService lists and decides interactive tool permissions.
type ToolPermissionService interface {
	ListPending(ctx context.Context, sessionID string, limit int) ([]ToolPermissionView, error)
	Decide(ctx context.Context, permissionID, decision, actor string) (ToolPermissionView, error)
	// DecideWithConfirm supports allow_permanent second confirmation (ADR-046).
	DecideWithConfirm(ctx context.Context, permissionID, decision, actor string, confirm bool) (ToolPermissionView, error)
}

// ToolPermissionView is the JSON shape for permission rows.
type ToolPermissionView struct {
	ID                string `json:"permission_id"`
	SessionID         string `json:"session_id,omitempty"`
	TaskID            string `json:"task_id"`
	RunID             string `json:"run_id"`
	ToolCallID        string `json:"tool_call_id"`
	ToolName          string `json:"tool_name"`
	Capability        string `json:"capability,omitempty"`
	Path              string `json:"path,omitempty"`
	Risk              string `json:"risk,omitempty"`
	State             string `json:"state"`
	GrantID           string `json:"grant_id,omitempty"`
	Decision          string `json:"decision,omitempty"`
	CreatedAt         string `json:"created_at"`
	DecidedAt         string `json:"decided_at,omitempty"`
	SuggestedDecision string `json:"suggested_decision,omitempty"`
	SuggestedReason   string `json:"suggested_reason,omitempty"`
}

type API struct {
	queries          QueryService
	taskSubmissions  TaskSubmitter
	taskControls     TaskController
	jobs             JobService
	core             interface{ Status() app.Status }
	events           *events.Store
	skills           *skillcatalog.Catalog
	modelMu          sync.RWMutex
	modelConfig      ModelConfig
	modelSwitcher    ModelSwitcher
	modelConfigError string
	modelStream      *modelstream.Hub
	mcp              MCPStatusProvider
	chatCommands     ChatCommandsProvider
	sessionCompact   SessionCompactor
	sessionRewind    SessionRewinder
	sessionSteer     SessionSteerer
	userQuestions    UserQuestionService
	toolPermissions  ToolPermissionService
	memoryControl    MemoryControlService
	skillControl     SkillControlService
	sessionPrefs     SessionPreferenceService
}

func NewAPI(config APIConfig) (*API, error) {
	if config.Queries == nil || config.TaskSubmissions == nil || config.Core == nil || config.Events == nil {
		return nil, errors.New("gateway queries, task submission service, core, and event store are required")
	}
	if config.ModelConfig.Models == nil {
		config.ModelConfig.Models = []string{}
	}
	errMsg := strings.TrimSpace(config.ModelConfigError)
	if errMsg == "" {
		errMsg = strings.TrimSpace(config.ModelConfig.Error)
	}
	config.ModelConfig.Error = errMsg
	// ready = switcher present and no error (load failure or chat not bound).
	config.ModelConfig.Ready = config.ModelSwitcher != nil && errMsg == ""
	return &API{
		queries: config.Queries, taskSubmissions: config.TaskSubmissions,
		taskControls: config.TaskControls, jobs: config.Jobs, core: config.Core, events: config.Events, skills: config.Skills,
		modelConfig: config.ModelConfig, modelSwitcher: config.ModelSwitcher, modelConfigError: strings.TrimSpace(config.ModelConfigError),
		modelStream: config.ModelStream,
		mcp:         config.MCP, chatCommands: config.ChatCommands,
		sessionCompact: config.SessionCompact, sessionRewind: config.SessionRewind, sessionSteer: config.SessionSteer, userQuestions: config.UserQuestions,
		toolPermissions: config.ToolPermissions,
		memoryControl:   config.MemoryControl, skillControl: config.SkillControl, sessionPrefs: config.SessionPrefs,
	}, nil
}

// UpdateModelSnapshot replaces the GET /v1/config/model view after hot-reload or /model.
// loadError non-empty marks ready=false (secrets never included). Empty loadError keeps
// config.Ready/Error from the caller when Error is already set (e.g. chat not bound).
func (a *API) UpdateModelSnapshot(config ModelConfig, loadError string) {
	if a == nil {
		return
	}
	if config.Models == nil {
		config.Models = []string{}
	}
	loadError = strings.TrimSpace(loadError)
	if loadError != "" {
		config.Error = loadError
		config.Ready = false
	} else if strings.TrimSpace(config.Error) != "" {
		config.Ready = false
		loadError = strings.TrimSpace(config.Error)
	} else {
		config.Error = ""
		config.Ready = a.modelSwitcher != nil
	}
	a.modelMu.Lock()
	a.modelConfig = config
	a.modelConfigError = loadError
	a.modelMu.Unlock()
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == "/v1/permissions":
		a.handlePermissions(w, r)
	case strings.HasSuffix(r.URL.Path, "/decide") && strings.HasPrefix(r.URL.Path, "/v1/permissions/"):
		a.handlePermissionDecide(w, r)
	case r.URL.Path == "/v1/questions":
		a.handleQuestions(w, r)
	case strings.HasSuffix(r.URL.Path, "/answer") && strings.HasPrefix(r.URL.Path, "/v1/questions/"):
		a.handleQuestionAnswer(w, r)
	case r.URL.Path == "/v1/health":
		a.handleHealth(w, r)
	case r.URL.Path == "/v1/config/model":
		a.handleConfigModel(w, r)
	case r.URL.Path == "/v1/config/mcp":
		a.handleConfigMCP(w, r)
	case r.URL.Path == "/v1/config/commands":
		a.handleConfigCommands(w, r)
	case r.URL.Path == "/v1/skills":
		a.handleSkills(w, r)
	case r.URL.Path == "/v1/skills/events":
		a.handleSkillEvents(w, r)
	case r.URL.Path == "/v1/skills/actions":
		a.handleSkillActions(w, r)
	case r.URL.Path == "/v1/memory":
		a.handleMemory(w, r)
	case r.URL.Path == "/v1/memory/actions":
		a.handleMemoryActions(w, r)
	case r.URL.Path == "/v1/sessions":
		a.handleSessions(w, r)
	case strings.HasSuffix(r.URL.Path, "/messages") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionMessages(w, r)
	case strings.HasSuffix(r.URL.Path, "/todos") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionTodos(w, r)
	case strings.HasSuffix(r.URL.Path, "/context") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionContext(w, r)
	case strings.HasSuffix(r.URL.Path, "/compact") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionCompact(w, r)
	case strings.HasSuffix(r.URL.Path, "/rewind") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionRewind(w, r)
	case strings.HasSuffix(r.URL.Path, "/steer") && strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSessionSteer(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		a.handleSession(w, r)
	case r.URL.Path == "/v1/tasks":
		a.handleTasks(w, r)
	case strings.HasSuffix(r.URL.Path, "/actions") && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		a.handleTaskAction(w, r)
	case strings.HasSuffix(r.URL.Path, "/usage") && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		a.handleTaskUsage(w, r)
	case strings.HasSuffix(r.URL.Path, "/context") && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		a.handleTaskContext(w, r)
	case strings.HasSuffix(r.URL.Path, "/messages") && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		a.handleTaskMessages(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
		a.handleTask(w, r)
	case r.URL.Path == "/v1/jobs":
		a.handleJobs(w, r)
	case strings.HasSuffix(r.URL.Path, "/actions") && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
		a.handleJobAction(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
		a.handleJob(w, r)
	case r.URL.Path == "/v1/plans":
		a.handlePlans(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/plans/"):
		a.handlePlan(w, r)
	case r.URL.Path == "/v1/approvals":
		a.handleApprovals(w, r)
	case r.URL.Path == "/v1/runs":
		a.handleRuns(w, r)
	case strings.HasSuffix(r.URL.Path, "/usage") && strings.HasPrefix(r.URL.Path, "/v1/runs/"):
		a.handleRunUsage(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/runs/"):
		a.handleRun(w, r)
	case r.URL.Path == "/v1/events":
		a.handleEvents(w, r)
	case r.URL.Path == "/v1/events/stream":
		a.handleEventStream(w, r)
	case r.URL.Path == "/v1/model-stream":
		a.handleModelStream(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	}
}

type healthResponse struct {
	OK   bool       `json:"ok"`
	Core app.Status `json:"core"`
}
