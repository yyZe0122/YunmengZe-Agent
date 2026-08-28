// Package agent runs the provider tool loop through the mandatory Tool Broker.
// Tool business failures are observations (ADR-052), not turn-ending errors.
// Chat dual-track orchestration lives in chatsession (ADR-038); child Runs via
// the task tool are ADR-039.
package agent

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

var (
	ErrInvalidRequest      = errors.New("invalid agent request")
	ErrInvalidToolCall     = errors.New("invalid provider tool call")
	ErrUnadvertisedTool    = errors.New("provider requested an unadvertised tool")
	ErrEmptyResponse       = errors.New("provider returned no final response")
	ErrTokenBudgetExceeded = errors.New("agent token budget exceeded")
	ErrCostBudgetExceeded  = errors.New("agent cost budget exceeded")
)

type StreamingProvider interface {
	Stream(context.Context, providerapi.CompletionRequest, providerapi.StreamHandler) error
}

type ToolBroker interface {
	Definitions() []toolapi.Definition
	Execute(context.Context, toolapi.Request) (toolapi.Response, error)
}

// StreamObserver receives provider stream events for UI fan-out (optional).
type StreamObserver interface {
	Publish(sessionID, taskID, runID string, event providerapi.StreamEvent)
}

// RoleEndpoint is a provider+model pair for a model role (ADR-045).
type RoleEndpoint struct {
	Provider      StreamingProvider
	Model         string
	ContextWindow int64
}

type Config struct {
	Provider StreamingProvider
	Broker   ToolBroker
	Records  *RecordStore
	Model    string
	// MaxIterations caps tool-loop steps per turn. 0 = no hard cap (ADR-052 R2).
	// When set, must be 1–256; the last allowed step is a text-only soft landing.
	MaxIterations int
	// MaxToolResultRunes caps tool/assistant Content length on provider
	// requests only. Zero uses DefaultMaxToolResultRunes. Records stay full.
	MaxToolResultRunes int
	// ContextWindow is the model context length in tokens; 0 → DefaultContextWindow for packing.
	ContextWindow int64
	// Roles maps optional role names (subagent, compact) to endpoints.
	// Unset roles fall back to main Provider/Model/ContextWindow.
	Roles map[string]RoleEndpoint
	// Stream is optional; when set, CollectStream events are teed for local UI.
	Stream StreamObserver
	// Context is optional; when set, each successful provider iteration updates pressure snapshot.
	Context *contextpack.Store
	// Calibrator is optional; when set, post-flight usage calibrates estimates.
	Calibrator *contextpack.Calibrator
	// Transcript indexes tool results into L3 as they append (ADR-051 / QF).
	Transcript TranscriptIndexer
}

// TranscriptIndexer projects run records into session_search (optional).
type TranscriptIndexer interface {
	IndexTranscriptRecord(ctx context.Context, sessionID, runID string, position int, recordType, content, createdAt string) error
}

type Runner struct {
	mu                 sync.RWMutex
	provider           StreamingProvider
	broker             ToolBroker
	records            *RecordStore
	model              string
	maxIterations      int
	maxToolResultRunes int
	contextWindow      int64
	roles              map[string]RoleEndpoint
	stream             StreamObserver
	contextStore       *contextpack.Store
	calibrator         *contextpack.Calibrator
	transcript         TranscriptIndexer
	inbox              *Inbox
}

const MaxIterationsLimit = 256

type RunRequest struct {
	RunID              string
	TaskID             string
	SessionID          string
	PlanID             string
	PlanHash           string
	StepID             string
	CapabilityGrantID  string
	CapabilityGrantIDs map[string][]string
	Actor              string
	TraceID            string
	// Interactive is true when a TUI can answer /perm for this run (ADR-043).
	Interactive bool
	// Messages are persisted as this run's input_message prefix (Prepare): Prefix + current user.
	Messages []providerapi.Message
	// ProviderMessages is the assembled ContextView for the provider (ADR-051).
	// Empty → Messages. Never written to run records.
	ProviderMessages  []providerapi.Message
	AllowedTools      []string
	MaxOutputTokens   int64
	MaxTotalTokens    int64
	MaxCostMicros     int64
	Temperature       *float64
	ToolTimeoutMillis int64
	// ContextWindow overrides runner default when > 0.
	ContextWindow int64
	// Depth is 0 for top-level chat runs; child task tools increment (ADR-039).
	Depth int
	// Role selects model endpoint (ADR-045): empty/main → main; subagent/compact when configured.
	Role string
	// ModelOverride is optional per-run main endpoint (O4). When set with OverrideProvider,
	// used instead of Role/main for this run only (does not mutate global main).
	// Ignored when Role is a non-main configured role (subagent/compact still win).
	ModelOverride           string
	OverrideProvider        StreamingProvider
	OverrideContextWindow   int64
	OverrideMaxOutputTokens int64
	// Compacted is set when the turn-start ContextView already summarized or dropped turns.
	Compacted bool
}

type Result struct {
	Content    string
	ToolCalls  []toolapi.Response
	Usage      providerapi.Usage
	Iterations int
}

func New(config Config) (*Runner, error) {
	if config.Provider == nil || config.Broker == nil || config.Records == nil {
		return nil, errors.New("agent provider, tool broker, and record store are required")
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		return nil, errors.New("agent model is required")
	}
	maxIterations := config.MaxIterations
	if maxIterations < 0 || maxIterations > MaxIterationsLimit {
		return nil, errors.New("agent maximum iterations must be between 0 and 256 (0 = no hard cap)")
	}
	maxToolResultRunes := config.MaxToolResultRunes
	if maxToolResultRunes <= 0 {
		maxToolResultRunes = DefaultMaxToolResultRunes
	}
	cal := config.Calibrator
	if cal == nil {
		cal = contextpack.NewCalibrator()
	}
	roles := config.Roles
	if len(roles) > 0 {
		roles = make(map[string]RoleEndpoint, len(config.Roles))
		for k, v := range config.Roles {
			roles[k] = v
		}
	}
	return &Runner{
		provider: config.Provider, broker: config.Broker, records: config.Records,
		model: model, maxIterations: maxIterations, maxToolResultRunes: maxToolResultRunes,
		contextWindow: config.ContextWindow, roles: roles, stream: config.Stream,
		contextStore: config.Context, calibrator: cal, transcript: config.Transcript,
		inbox: NewInbox(),
	}, nil
}

// Inbox is the process-local next-step queue (ADR-052).
func (r *Runner) Inbox() *Inbox {
	if r == nil {
		return nil
	}
	return r.inbox
}

// SetContextWindow updates the model context length used for packing.
func (r *Runner) SetContextWindow(n int64) {
	if n < 0 {
		n = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contextWindow = n
}

// ContextWindow returns the configured model context length (0 = use packing default).
func (r *Runner) ContextWindow() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contextWindow
}

// SetProvider replaces the main streaming provider used by subsequent runs.
// Role endpoints (subagent/compact) are unchanged; reload config + restart to update them.
func (r *Runner) SetProvider(provider StreamingProvider) error {
	if provider == nil {
		return errors.New("agent provider is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provider = provider
	return nil
}

// SetModel replaces the main model id used by subsequent provider requests.
func (r *Runner) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("agent model is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.model = model
	return nil
}

// Model returns the currently selected main model id.
func (r *Runner) Model() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.model
}

// SetRoleEndpoint sets or replaces a non-main role endpoint (ADR-045). Empty role or main is rejected.
func (r *Runner) SetRoleEndpoint(role string, ep RoleEndpoint) error {
	role = strings.TrimSpace(role)
	if role == "" || role == "main" {
		return errors.New("role endpoint must be a non-main role name")
	}
	if ep.Provider == nil || strings.TrimSpace(ep.Model) == "" {
		return errors.New("role endpoint provider and model are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roles == nil {
		r.roles = make(map[string]RoleEndpoint)
	}
	r.roles[role] = ep
	return nil
}

func (r *Runner) snapshot() (StreamingProvider, string) {
	p, m, _ := r.snapshotForRole("")
	return p, m
}

// snapshotForRole returns provider, model id, and context window for the role.
// Empty role, "main", or unconfigured role falls back to main.
func (r *Runner) snapshotForRole(role string) (StreamingProvider, string, int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	role = strings.TrimSpace(role)
	if role != "" && role != "main" {
		if ep, ok := r.roles[role]; ok && ep.Provider != nil && strings.TrimSpace(ep.Model) != "" {
			cw := ep.ContextWindow
			if cw < 0 {
				cw = 0
			}
			return ep.Provider, ep.Model, cw
		}
	}
	return r.provider, r.model, r.contextWindow
}

// isConfiguredRole reports whether role has a dedicated endpoint (not main fallback).
func (r *Runner) isConfiguredRole(role string) bool {
	role = strings.TrimSpace(role)
	if role == "" || role == "main" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ep, ok := r.roles[role]
	return ok && ep.Provider != nil && strings.TrimSpace(ep.Model) != ""
}

// useModelOverride reports whether RunRequest carries a usable main-path override (O4).
// Configured subagent/compact roles win over session prefer.
func (r *Runner) useModelOverride(request RunRequest) bool {
	if request.OverrideProvider == nil || strings.TrimSpace(request.ModelOverride) == "" {
		return false
	}
	return !r.isConfiguredRole(request.Role)
}
