// Package chatsession runs multi-turn Session chat for both Tab modes.
// Agent (build): workspace read+write grants. Plan: read-only grants.
// Dual-track chat: agent (write) and plan (read-only). No Planner / plan-step worker.
package chatsession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/audit"
	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/internal/corequery"
	"github.com/yyZe0122/yunmengze-agent/internal/editrev"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/runlog"
	"github.com/yyZe0122/yunmengze-agent/internal/sessiontodo"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// PathGuardRoot expands the tool filesystem ceiling for a session workspace (ADR-046).
type PathGuardRoot interface {
	AddRoot(root string) error
	AddSessionRoot(sessionID, root string) error
	AddOnceRoot(callID, root string) error
}

const (
	// chatStepIDPrefix prefixes per-task step IDs. plan_steps.step_id is a global PK,
	// so the literal "chat-step" cannot be reused across tasks.
	chatStepIDPrefix       = "chat-step-"
	chatToolProtocolShared = "Reply in the user's language. Prefer absolute workspace paths; relative paths resolve against the workspace root. " +
		"Workspace state comes from tools. Do not invent file contents, line numbers, or claim tool success without evidence. " +
		"Find with fs_glob/fs_grep (never shell find/grep). Read with fs_read offset/limit and the returned sha256. " +
		"Independent reads (fs_read/fs_grep/fs_glob) may be issued in one step. " +
		"For multi-step work, keep session todos via todo_write (at most one in_progress). " +
		"After compaction, recover paths and errors with session_search instead of relying on memory. " +
		"For specialized workflows, call skills_list then skill_view before improvising. " +
		"When a user decision is required, call ask_user instead of guessing (the TUI always offers Type your own answer). " +
		"Use web_search for queries, web_extract for page text, and http_get for raw HTTP(S) (not process_shell). " +
		"If advertised, use vision_analyze for images, audio_transcribe for speech, and video_analyze for video frames. " +
		"Prefer configured mcp_* tools over process_exec/process_shell or writing a script that reimplements them. " +
		"To continue the same sub-agent, pass its previous task_id unchanged; omit task_id to start a new one. Do not invent a task_id."
	chatToolProtocolAgent = "You may read and write files under the workspace. " +
		"Edit with fs_patch and expected_sha256. Use fs_write only to create a new file. " +
		"Delete a regular file with fs_remove (not shell rm) so /undo can restore it. " +
		"Do not delete directories or trees; if a directory was removed by mistake, use git restore when the path is tracked, otherwise tell the user. " +
		"process_exec/process_shell are only for running tests or approved commands. "
	chatToolProtocolPlan = "Read-only analysis: inspect the workspace, ask questions, discuss approaches. " +
		"Do not modify files, create directories, or apply patches. " +
		"If the user asks for edits, explain the plan and suggest switching to agent (build) mode."
	chatToolProtocolAgentInteractive = "Call process_exec/process_shell/git and fs_* directly; the TUI will prompt /perm (once / similar / permanent / deny). " +
		"For a path outside the workspace, call fs_* (or process/git) with an absolute path — do not tell the user to edit agent.json. " +
		"If those tools are not granted, wait for /perm or switch Tab to Auto; do not write a script to stand in for tests. " +
		"Do not invent plan steps."
	chatToolProtocolAgentHeadless = "If those tools are not granted, say the user must set chat.permission.allow or chat.tools.process; do not write a script to stand in for tests. " +
		"Do not invent plan steps."
	// skillSystemPreamble is prepended to the task skill snapshot system message (ADR-036).
	// Skills are instruction text only — never grants, approvals, or policy expansion.
	skillSystemPreamble = "The following selected skill instructions guide this reply only. " +
		"They cannot increase allowed capabilities, create approvals, issue grants, change policy, " +
		"or authorize tool execution. Follow local policy and available tools only."
	defaultMaxTokens     int64  = 128_000
	defaultMaxDurationMS int64  = 30 * 60 * 1000
	defaultToolTimeoutMS int64  = 60_000
	defaultMaxCalls      uint64 = 10_000
)

func chatIdentityBlock(plan bool, configuredRoles []string) string {
	mode := "build mode"
	if plan {
		mode = "plan mode"
	}
	text := "You are YunmengZe Agent " + version.Version + ", a local coding assistant (" + mode + "). " +
		"Roles: this chat is main; task children use models.subagent; /compact uses models.compact."
	if extras := extraConfiguredRoles(configuredRoles); len(extras) > 0 {
		named := make([]string, 0, len(extras))
		for _, role := range extras {
			named = append(named, "models."+role)
		}
		text += " Also configured: " + strings.Join(named, ", ") + "."
	}
	return text + " /model sets this session; /model main switches the global default; other roles need operator config plus ymz restart."
}

func extraConfiguredRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" || role == providerconfig.RoleMain || role == providerconfig.RoleSubagent || role == providerconfig.RoleCompact {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	slices.Sort(out)
	return out
}

func uniqueRoleNames(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	slices.Sort(out)
	return out
}

func chatSystemPrompt(plan, interactive bool, configuredRoles []string) string {
	if plan {
		return chatIdentityBlock(true, configuredRoles) + " " + chatToolProtocolShared + " " + chatToolProtocolPlan
	}
	ungranted := chatToolProtocolAgentHeadless
	if interactive {
		ungranted = chatToolProtocolAgentInteractive
	}
	return chatIdentityBlock(false, configuredRoles) + " " + chatToolProtocolShared + " " + chatToolProtocolAgent + " " + ungranted
}

func chatEnvBlock(model, workspace, date string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = "unknown"
	}
	date = strings.TrimSpace(date)
	if date == "" {
		date = "unknown"
	}
	return "<env>\n  model: " + model + "\n  workspace: " + workspace + "\n  date: " + date + "\n</env>"
}

// chatStepID returns a globally unique plan_steps.step_id for one chat task turn.
func chatStepID(taskID kernel.TaskID) kernel.StepID {
	return kernel.StepID(chatStepIDPrefix + deterministicID("chat-step", string(taskID)))
}

func planChatStepID(plan approval.PlanDocument) kernel.StepID {
	if len(plan.Steps) > 0 && strings.TrimSpace(string(plan.Steps[0].StepID)) != "" {
		return plan.Steps[0].StepID
	}
	return chatStepID(plan.TaskID)
}

var (
	ErrInvalidRequest = errors.New("invalid chat request")
	ErrUnavailable    = errors.New("chat session unavailable")
	ErrTurnNotRunning = errors.New("no running chat turn to steer")
)

type AgentRunner interface {
	Run(context.Context, agent.RunRequest) (agent.Result, error)
	Inbox() *agent.Inbox
}

// Compactor optionally summarizes head messages for session compaction.
type Compactor interface {
	CompactSummary(ctx context.Context, head []providerapi.Message) (string, error)
}

// MemoryCuratorCaller is optional post-turn LLM fact extraction (H1-lite).
// Typically agent.Runner (uses models.compact when configured).
type MemoryCuratorCaller interface {
	ProposeMemoryFacts(ctx context.Context, userText, assistantText string, maxFacts int) (string, error)
}

// StreamFanout notifies local UIs after durable run terminal writes (optional).
type StreamFanout interface {
	PublishTerminal(sessionID, taskID, runID string)
}

// ToolCallCleaner marks incomplete tool_calls after cancel/fail (optional; Broker).
type ToolCallCleaner interface {
	CancelIncompleteToolCalls(ctx context.Context, runID string) (int, error)
}

// CompactResult is the outcome of ForceCompact (manual /compact).
type CompactResult struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	Source       string `json:"source"` // "llm" | "extractive" | "skipped"
	CompactionID string `json:"compaction_id,omitempty"`
}

// RewindResult is the outcome of a human-path file rewind (QG).
type RewindResult struct {
	SessionID  string `json:"session_id"`
	RevisionID string `json:"revision_id"`
	Path       string `json:"path"`
}

func (s *Service) RewindEdit(ctx context.Context, sessionID kernel.SessionID, revisionID string) (RewindResult, error) {
	if ctx == nil {
		return RewindResult{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if s == nil || s.edits == nil {
		return RewindResult{}, fmt.Errorf("%w: edit checkpoints unavailable", ErrUnavailable)
	}
	rev, err := s.edits.Rewind(ctx, string(sessionID), revisionID)
	if err != nil {
		return RewindResult{}, classify(err)
	}
	return RewindResult{SessionID: rev.SessionID, RevisionID: rev.ID, Path: rev.Path}, nil
}

type TranscriptLoader interface {
	SessionTranscript(context.Context, kernel.SessionID, corequery.TranscriptOptions) ([]corequery.TranscriptMessage, error)
	SessionTranscriptTail(ctx context.Context, sessionID kernel.SessionID, n int) ([]corequery.TranscriptMessage, error)
}

type Config struct {
	DB         *sql.DB
	Repository *kernel.Repository
	Approvals  *approval.Repository
	Agent      AgentRunner
	Transcript TranscriptLoader
	// WorkspaceRoots are absolute roots for default chat tool grants (fallback ceiling extras).
	WorkspaceRoots []string
	// PathGuard is the shared tool path ceiling; session workspaces are AddRoot'd (ADR-046).
	PathGuard PathGuardRoot
	// DaemonCWD is fallback when session has no workspace metadata.
	DaemonCWD string
	// ConfigDir is the user/system config root; used to read global AGENTS.md.
	ConfigDir string
	// ChatConfig resolves grant roots per session (optional; uses WorkspaceRoots when nil).
	ChatConfig *providerconfig.ChatConfig
	// AllowWriteCeiling, when non-nil and false, denies write tools even in agent mode.
	// nil (default): agent mode gets write tools. Plan mode is always read-only.
	AllowWriteCeiling *bool
	// AllowGit, when true, pre-authorizes git_* for agent mode only (default false).
	AllowGit bool
	// AllowProcess, when true, pre-authorizes process_exec and process_shell for agent mode only (default false). Plan and cron never receive these grants.
	AllowProcess bool
	// ExtraTools are additional broker tool names (e.g. mcp_*) granted for chat runs.
	ExtraTools []string
	// ConfiguredRoles are models.* keys present at daemon start (ADR-045). Prefix lists extras beyond subagent/compact.
	ConfiguredRoles []string
	// Skills is optional; when set, Prefix includes an id + one-line catalog (ADR-052 R5).
	Skills *skillcatalog.Catalog
	// ContextWindow is model context length for packing; 0 → DefaultContextWindow after catalog fill.
	ContextWindow int64
	// MaxOutputTokens is the model output cap (maxTokens); 0 omits max_tokens on OpenAI-compatible wires.
	MaxOutputTokens int64
	// Context persists pressure snapshots and session compactions (optional).
	Context *contextpack.Store
	// Compactor summarizes head turns when compaction is enabled (optional; extractive fallback).
	Compactor Compactor
	// MemoryCurator optionally extracts durable facts after successful turns (H1-lite).
	MemoryCurator MemoryCuratorCaller
	// CompactionEnabled defaults true when nil.
	CompactionEnabled *bool
	// Calibrator is optional for pre-pack estimate pressure.
	Calibrator *contextpack.Calibrator
	// Memory is optional in-process session memory (ADR-044).
	Memory *memory.Manager
	// Todos is optional session working list (ADR-051 / QE).
	Todos *sessiontodo.Store
	// Edits is optional file-edit checkpoints (ADR-051 / QG).
	Edits *editrev.Store
	// Stream is optional; PublishTerminal after complete/fail/cancel flush.
	Stream StreamFanout
	// ToolCalls is optional; cancel/fail sweeps still-running tool_calls (ADR-012).
	ToolCalls ToolCallCleaner
	// ModelResolver optionally applies session PreferredModel per run (O4).
	ModelResolver ModelPinResolver
	// MainModel is the daemon main wire id written on new compaction rows when no pin.
	MainModel string
	Now       func() time.Time
	OnError   func(error)
}

// ModelPinResolver resolves a selection ref (provider/model) for one chat run.
// ResolveOrFallback: empty/error → nil (use daemon main). Shared with H7 job pins.
// ResolveStrict: empty pin → nil,nil; invalid pin → error (H7 skip path).
type ModelPinResolver interface {
	ResolveOrFallback(pin string) *ModelPin
	ResolveStrict(pin string) (*ModelPin, error)
}

// ModelPin is a per-run main endpoint override (O4).
type ModelPin struct {
	Ref           string
	Provider      agent.StreamingProvider
	Model         string
	ContextWindow int64
	MaxTokens     int64
}

type Service struct {
	db         *sql.DB
	repository *kernel.Repository
	approvals  *approval.Repository
	agent      AgentRunner
	transcript TranscriptLoader
	roots      []string // fallback / configured extras
	pathGuard  PathGuardRoot
	daemonCWD  string
	configDir  string
	chatCfg    *providerconfig.ChatConfig
	// writeCeiling false forces agent grants read-only.
	writeCeiling      bool
	allowGit          bool
	allowProcess      bool
	extraTools        []string
	configuredRoles   []string
	skills            *skillcatalog.Catalog
	contextWindow     int64
	maxOutputTokens   int64
	contextStore      *contextpack.Store
	compactor         Compactor
	compactionEnabled bool
	calibrator        *contextpack.Calibrator
	memory            *memory.Manager
	todos             *sessiontodo.Store
	edits             *editrev.Store
	curatorCaller     MemoryCuratorCaller
	stream            StreamFanout
	toolCalls         ToolCallCleaner
	modelResolver     ModelPinResolver
	mainModel         string
	audit             *audit.Store
	now               func() time.Time
	onError           func(error)

	activeMu sync.Mutex
	// active cancels in-flight chat agent.Run by task id (concurrent turns).
	active map[kernel.TaskID]activeTurn
}

type activeTurn struct {
	session kernel.SessionID
	cancel  context.CancelFunc
}

type StartRequest struct {
	Task    kernel.Task
	Actor   string
	TraceID string
	// UserText overrides task.Objective when non-empty.
	UserText string
	// ModelRef is an optional job-pinned selection ref (H7). Wins over session prefer.
	ModelRef string
	// Interactive is true when a TUI can answer /perm for this turn.
	Interactive bool
}

type StartResult struct {
	Task   kernel.Task   `json:"task"`
	RunID  kernel.RunID  `json:"run_id"`
	PlanID kernel.PlanID `json:"plan_id"`
}

func New(config Config) (*Service, error) {
	if config.DB == nil || config.Repository == nil || config.Approvals == nil || config.Agent == nil || config.Transcript == nil {
		return nil, errors.New("chat session requires db, repository, approvals, agent, and transcript loader")
	}
	roots := make([]string, 0, len(config.WorkspaceRoots))
	for _, root := range config.WorkspaceRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("chat workspace root: %w", err)
		}
		roots = append(roots, abs)
	}
	if len(roots) == 0 {
		return nil, errors.New("chat session requires at least one workspace root")
	}
	auditStore, err := audit.NewStore(config.DB)
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.OnError == nil {
		config.OnError = func(error) {}
	}
	writeCeiling := true
	if config.AllowWriteCeiling != nil {
		writeCeiling = *config.AllowWriteCeiling
	}
	extra := make([]string, 0, len(config.ExtraTools))
	for _, name := range config.ExtraTools {
		name = strings.TrimSpace(name)
		if name != "" {
			extra = append(extra, name)
		}
	}
	compactionEnabled := true
	if config.CompactionEnabled != nil {
		compactionEnabled = *config.CompactionEnabled
	}
	return &Service{
		db: config.DB, repository: config.Repository, approvals: config.Approvals,
		agent: config.Agent, transcript: config.Transcript, roots: roots,
		pathGuard: config.PathGuard, daemonCWD: strings.TrimSpace(config.DaemonCWD),
		configDir:    strings.TrimSpace(config.ConfigDir),
		chatCfg:      config.ChatConfig,
		writeCeiling: writeCeiling, allowGit: config.AllowGit, allowProcess: config.AllowProcess,
		extraTools:      extra,
		configuredRoles: uniqueRoleNames(config.ConfiguredRoles),
		skills:          config.Skills,
		contextWindow:   config.ContextWindow, maxOutputTokens: config.MaxOutputTokens,
		contextStore: config.Context,
		compactor:    config.Compactor, compactionEnabled: compactionEnabled,
		calibrator: config.Calibrator, memory: config.Memory, todos: config.Todos, edits: config.Edits,
		curatorCaller: config.MemoryCurator,
		stream:        config.Stream, toolCalls: config.ToolCalls, modelResolver: config.ModelResolver,
		mainModel: strings.TrimSpace(config.MainModel),
		audit:     auditStore, now: config.Now, onError: config.OnError,
		active: make(map[kernel.TaskID]activeTurn),
	}, nil
}

// resolveGrantRoots returns plan/grant paths for a task's session (ADR-046).
func (s *Service) SetContextWindow(n int64) {
	if n < 0 {
		n = 0
	}
	s.contextWindow = n
}

// SetMaxOutputTokens updates the main model output cap used for packing (ADR-051).
func (s *Service) SetMaxOutputTokens(n int64) {
	if n < 0 {
		n = 0
	}
	s.maxOutputTokens = n
}

// SetMainModel updates the daemon main wire id used on new compaction rows.
func (s *Service) SetMainModel(model string) {
	s.mainModel = strings.TrimSpace(model)
}

func (s *Service) SetSkills(catalog *skillcatalog.Catalog) {
	if s == nil {
		return
	}
	s.skills = catalog
}

func (s *Service) compactionModel(pin string) string {
	if m := strings.TrimSpace(pin); m != "" {
		return m
	}
	return strings.TrimSpace(s.mainModel)
}

// Interrupt cancels an in-flight chat agent.Run for taskID (pause/cancel path).
// No-op when no active chat for that task. Durable run/task rows are owned by
// taskcontrol.ControlTask (task state) and executeChat completion/cancel handlers.
func (s *Service) Interrupt(taskID kernel.TaskID) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	turn := s.active[taskID]
	s.activeMu.Unlock()
	session := turn.session
	if session == "" && s.repository != nil {
		if task, err := s.repository.GetTask(context.Background(), taskID); err == nil {
			session = task.SessionID
		}
	}
	if session != "" && s.agent != nil {
		if inbox := s.agent.Inbox(); inbox != nil {
			inbox.Clear(string(session))
		}
	}
	if turn.cancel != nil {
		turn.cancel()
	}
}

// StartChat prepares a workspace-scoped plan+grants for the task, then runs one
// chat agent turn asynchronously. Supports execution_mode=agent (write) and plan (read-only).
// Task must be state=created (fresh submission) or an already-started chat turn (idempotent).
func (s *Service) StartChat(ctx context.Context, request StartRequest) (StartResult, error) {
	if ctx == nil {
		return StartResult{}, errors.New("chat context is required")
	}
	if s == nil || s.agent == nil {
		return StartResult{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, ErrUnavailable)
	}
	task := request.Task
	if task.ID == "" || task.SessionID == "" {
		return StartResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: task and session are required", ErrInvalidRequest))
	}
	mode := kernel.NormalizeExecutionMode(string(task.ExecutionMode))
	if mode != kernel.ExecutionModeAgent && mode != kernel.ExecutionModePlan {
		return StartResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: chat requires agent or plan execution_mode", ErrInvalidRequest))
	}
	if task.State != kernel.TaskCreated {
		// Idempotent: already started chat for this task.
		if runID, ok, err := s.existingChatRun(ctx, task.ID); err != nil {
			return StartResult{}, err
		} else if ok {
			slog.Info("chat start reused existing run", runlog.Attrs("chatsession", "start", "succeeded", runlog.IDs{
				SessionID: string(task.SessionID), TaskID: string(task.ID), RunID: string(runID),
				PlanID: string(chatPlanID(task.ID)), TraceID: request.TraceID,
			}, "idempotent", true)...)
			return StartResult{Task: task, RunID: runID, PlanID: chatPlanID(task.ID)}, nil
		}
		return StartResult{}, applicationerror.Wrap(applicationerror.CodeConflict, false, fmt.Errorf("%w: task %s is %s", ErrInvalidRequest, task.ID, task.State))
	}

	userText := strings.TrimSpace(request.UserText)
	if userText == "" {
		userText = strings.TrimSpace(task.Objective)
	}
	if userText == "" {
		userText = strings.TrimSpace(task.Title)
	}
	if userText == "" {
		return StartResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false, fmt.Errorf("%w: user message is required", ErrInvalidRequest))
	}
	actor := strings.TrimSpace(request.Actor)
	if actor == "" {
		actor = "local-user"
	}

	// H7: job model pin must resolve before creating a run (fail closed for unattended).
	modelRef := strings.TrimSpace(request.ModelRef)
	if modelRef != "" {
		if s.modelResolver == nil {
			return StartResult{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false,
				fmt.Errorf("%w: job model pin requires model resolver", ErrUnavailable))
		}
		if _, err := s.modelResolver.ResolveStrict(modelRef); err != nil {
			slog.Error("chat start job model pin failed", runlog.Attrs("chatsession", "start", "failed", runlog.IDs{
				SessionID: string(task.SessionID), TaskID: string(task.ID), TraceID: request.TraceID,
			}, "model_ref", modelRef, "error", err)...)
			return StartResult{}, applicationerror.Wrap(applicationerror.CodeInvalidRequest, false,
				fmt.Errorf("%w: job model pin %q: %v", ErrInvalidRequest, modelRef, err))
		}
	}

	plan, planHash, task, posture, err := s.ensureChatWorkspaceAuth(ctx, task)
	if err != nil {
		return StartResult{}, err
	}

	runID, err := s.createChatRun(ctx, task, plan, planHash, actor, request.TraceID)
	if err != nil {
		slog.Error("chat start failed", runlog.Attrs("chatsession", "start", "failed", runlog.IDs{
			SessionID: string(task.SessionID), TaskID: string(task.ID), TraceID: request.TraceID,
		}, "error", err)...)
		return StartResult{}, err
	}
	task, err = s.repository.GetTask(ctx, task.ID)
	if err != nil {
		return StartResult{}, classify(err)
	}

	grantIDs, err := s.issueChatGrants(ctx, plan, posture)
	if err != nil {
		return StartResult{}, err
	}

	slog.Info("chat start accepted", runlog.Attrs("chatsession", "start", "started", runlog.IDs{
		SessionID: string(task.SessionID), TaskID: string(task.ID), RunID: string(runID),
		PlanID: string(plan.PlanID), TraceID: request.TraceID,
	}, "actor", actor, "execution_mode", string(mode))...)

	// Capture for async execution. ContextView is assembled after pin inside executeChat (ADR-051).
	execTask := task
	execPlan := plan
	execHash := planHash
	execRun := runID
	execGrants := grantIDs
	execUser := userText
	execModelRef := modelRef
	execActor := actor
	execInteractive := request.Interactive
	go s.executeChat(execTask, execPlan, execHash, execRun, execGrants, execUser, execModelRef, execActor, execInteractive)

	return StartResult{Task: task, RunID: runID, PlanID: plan.PlanID}, nil
}

// ensureChatWorkspaceAuth creates (or reuses) an already-approved synthetic plan
// and system approval. Task path is created → running only.
