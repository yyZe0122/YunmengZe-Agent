// Package tools contains the mandatory Tool Broker and built-in typed tools.
// Executors are reachable only through registered Tool handlers.
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/artifacts"
	"github.com/yyZe0122/yunmengze-agent/internal/audit"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/internal/tools/internal/executor"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

var (
	ErrUnknownTool        = errors.New("unknown tool")
	ErrDuplicateTool      = errors.New("duplicate tool")
	ErrInvalidTool        = errors.New("invalid tool")
	ErrToolDenied         = toolapi.ErrDenied
	ErrToolTimeout        = errors.New("tool call timed out")
	ErrToolOutputTooLarge = errors.New("tool output too large")
	// ErrPermissionRequired aliases toolapi for callers that import tools only.
	ErrPermissionRequired = toolapi.ErrPermissionRequired
)

// PermissionGate is the interactive /perm gate. Implemented by toolpermission.
type PermissionGate interface {
	// CreatePending records a pending permission and returns its id.
	CreatePending(ctx context.Context, req PermissionPending) (permissionID string, err error)
	// Wait blocks until the permission is decided or ctx ends.
	Wait(ctx context.Context, permissionID string) (PermissionDecision, error)
}

// PermissionPending is input for CreatePending.
type PermissionPending struct {
	SessionID     string
	TaskID        string
	RunID         string
	PlanID        string
	PlanHash      string
	StepID        string
	ToolCallID    string
	ToolName      string
	Arguments     json.RawMessage
	Capability    string
	Path          string
	Command       string
	CommandArgs   []string
	NetworkDomain string
	Risk          string
	ExtraRoot     bool
}

// PermissionDecision is the outcome after user decide.
type PermissionDecision struct {
	Decision string // allow_once | allow_similar | allow_permanent | deny
	GrantID  string
	State    string
}

type Authorization struct {
	Capability    string
	Path          string
	Command       string
	Arguments     []string
	NetworkDomain string
	// ExtraRoot is set when Authorization accepted a path outside PathGuard roots
	// so interactive TUI can /perm-expand instead of denying at Resolve.
	ExtraRoot bool
}

type Tool interface {
	Definition() toolapi.Definition
	Authorization(context.Context, json.RawMessage) (Authorization, error)
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

type Config struct {
	DB                *sql.DB
	Approvals         *approval.Repository
	Policy            *policy.Evaluator
	Artifacts         *artifacts.Store
	ArtifactThreshold int
	MaximumTimeout    time.Duration
	Now               func() time.Time
	// Permission is the interactive /perm gate. TUI agent turns wait when this is set.
	Permission PermissionGate
}

type Broker struct {
	db                *sql.DB
	approvals         *approval.Repository
	policy            *policy.Evaluator
	audit             *audit.Store
	artifacts         *artifacts.Store
	artifactThreshold int
	maximumTimeout    time.Duration
	now               func() time.Time
	permission        PermissionGate
	mu                sync.RWMutex
	// dbMu serializes SQLite grant consume + tool_calls writes so same-step
	// parallel tool bodies (read-only) can run without concurrent TX races.
	dbMu     sync.Mutex
	registry map[string]Tool
}

func NewBroker(config Config) (*Broker, error) {
	if config.DB == nil || config.Approvals == nil || config.Policy == nil || config.Artifacts == nil {
		return nil, errors.New("tool broker database, approval, policy, and artifact stores are required")
	}
	auditStore, err := audit.NewStore(config.DB)
	if err != nil {
		return nil, err
	}
	if config.ArtifactThreshold <= 0 {
		config.ArtifactThreshold = 64 * 1024
	}
	if config.MaximumTimeout <= 0 {
		config.MaximumTimeout = 10 * time.Minute
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Broker{
		db: config.DB, approvals: config.Approvals, policy: config.Policy,
		audit: auditStore, artifacts: config.Artifacts,
		artifactThreshold: config.ArtifactThreshold, maximumTimeout: config.MaximumTimeout,
		now: config.Now, permission: config.Permission,
		registry: make(map[string]Tool),
	}, nil
}

// SetPermission attaches or replaces the interactive permission gate.
func (b *Broker) SetPermission(gate PermissionGate) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.permission = gate
}

// SetEditCheckpointer attaches QG snapshots to registered fs_write/fs_patch/fs_mkdir/fs_remove tools.
func (b *Broker) SetEditCheckpointer(cp EditCheckpointer) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tool := range b.registry {
		if ft, ok := tool.(*fileTool); ok {
			ft.SetCheckpointer(cp)
		}
	}
}

func (b *Broker) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("%w: nil handler", ErrInvalidTool)
	}
	definition := tool.Definition()
	definition.Name = strings.TrimSpace(definition.Name)
	if definition.Name == "" || strings.TrimSpace(definition.Description) == "" {
		return fmt.Errorf("%w: name and description are required", ErrInvalidTool)
	}
	if !toolapi.ValidName(definition.Name) {
		return fmt.Errorf("%w: name %q must match ^[a-zA-Z0-9_-]+$", ErrInvalidTool, definition.Name)
	}
	if definition.DefaultTimeoutMillis <= 0 {
		return fmt.Errorf("%w: default timeout must be positive", ErrInvalidTool)
	}
	if !policy.RiskLevel(definition.Risk).Valid() {
		return fmt.Errorf("%w: invalid risk %q", ErrInvalidTool, definition.Risk)
	}
	if err := validateInputSchema(definition.InputSchema); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTool, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.registry[definition.Name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, definition.Name)
	}
	b.registry[definition.Name] = tool
	return nil
}

func validateInputSchema(raw json.RawMessage) error {
	if len(raw) == 0 || !json.Valid(raw) {
		return errors.New("input schema must be valid JSON")
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return errors.New("input schema must be a JSON object")
	}
	kind, ok := schema["type"].(string)
	if !ok || kind != "object" {
		return errors.New("input schema type must be object")
	}
	if properties, exists := schema["properties"]; exists {
		if _, ok := properties.(map[string]any); !ok {
			return errors.New("input schema properties must be an object")
		}
	}
	if required, exists := schema["required"]; exists {
		items, ok := required.([]any)
		if !ok {
			return errors.New("input schema required must be an array")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return errors.New("input schema required entries must be strings")
			}
		}
	}
	return nil
}
func (b *Broker) Definitions() []toolapi.Definition {
	b.mu.RLock()
	definitions := make([]toolapi.Definition, 0, len(b.registry))
	for _, tool := range b.registry {
		definitions = append(definitions, tool.Definition())
	}
	b.mu.RUnlock()
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}

func (b *Broker) Execute(ctx context.Context, request toolapi.Request) (toolapi.Response, error) {
	if ctx == nil {
		return toolapi.Response{}, errors.New("tool context is required")
	}
	startedAt := b.now().UTC()
	actor := strings.TrimSpace(request.Actor)
	if actor == "" {
		actor = "core"
	}
	if err := validateRequest(request); err != nil {
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	b.mu.RLock()
	tool, exists := b.registry[request.Tool]
	b.mu.RUnlock()
	if !exists {
		err := fmt.Errorf("%w: %s", ErrUnknownTool, request.Tool)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	definition := tool.Definition()
	timeout, err := b.resolveTimeout(request.TimeoutMillis, definition.DefaultTimeoutMillis)
	if err != nil {
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	authorization, err := tool.Authorization(ctx, request.Arguments)
	if err != nil {
		err = fmt.Errorf("%w: invalid %s arguments: %v", ErrToolDenied, request.Tool, err)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	if strings.TrimSpace(authorization.Capability) == "" {
		authorization.Capability = request.Tool
	}
	// Serialize grant consume + tool_calls insert (SQLite). tool.Execute runs unlocked.
	b.dbMu.Lock()
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		b.dbMu.Unlock()
		return toolapi.Response{}, fmt.Errorf("begin tool call: %w", err)
	}
	grantID, err := b.authorize(ctx, tx, request, definition, authorization, timeout)
	if errors.Is(err, errPermissionWait) {
		_ = tx.Rollback()
		b.dbMu.Unlock()
		// Interactive permission: create pending, wait, then re-execute with grant.
		return b.executeAfterPermission(ctx, request, definition, authorization, timeout, actor, startedAt)
	}
	if err != nil {
		_ = tx.Rollback()
		b.dbMu.Unlock()
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	request.CapabilityGrantID = grantID
	if err := b.audit.RecordTx(ctx, tx, audit.Entry{
		OccurredAt: startedAt, Actor: actor, Action: "tool.execute", ResourceType: "tool_call",
		ResourceID: request.CallID, Outcome: "started", TraceID: request.TraceID,
		Details: map[string]any{"tool": request.Tool, "run_id": request.RunID, "step_id": request.StepID},
	}); err != nil {
		_ = tx.Rollback()
		b.dbMu.Unlock()
		return toolapi.Response{}, fmt.Errorf("write pre-execution audit: %w", err)
	}
	if err := b.insertToolCall(ctx, tx, request, startedAt); err != nil {
		_ = tx.Rollback()
		b.dbMu.Unlock()
		return toolapi.Response{}, err
	}
	if err := tx.Commit(); err != nil {
		b.dbMu.Unlock()
		return toolapi.Response{}, fmt.Errorf("commit tool call start: %w", err)
	}
	b.dbMu.Unlock()
	slog.Info("tool execution started", "component", "tool_broker", "operation", "execute", "result", "started", "tool", request.Tool, "tool_call_id", request.CallID, "run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "timeout_ms", timeout.Milliseconds())

	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executionContext = WithToolCallID(executionContext, request.CallID)
	output, executeErr := tool.Execute(executionContext, request.Arguments)
	finishedAt := b.now().UTC()
	response := toolapi.Response{CallID: request.CallID, Tool: request.Tool, StartedAt: startedAt, FinishedAt: finishedAt}
	if len(output) > 0 {
		if !json.Valid(output) {
			executeErr = errors.Join(executeErr, errors.New("tool returned invalid JSON output"))
		} else if len(output) > b.artifactThreshold {
			artifactContext, cancelArtifact := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			artifact, artifactErr := b.artifacts.Put(artifactContext, "application/json", output, map[string]any{
				"tool_call_id": request.CallID, "tool": request.Tool,
			})
			cancelArtifact()
			if artifactErr != nil {
				executeErr = errors.Join(executeErr, artifactErr)
			} else {
				response.Artifact = &artifact
				response.Output = truncateToolOutput(output, b.artifactThreshold, artifact.ID)
			}
		} else {
			response.Output = output
		}
	}
	state := "succeeded"
	if executeErr != nil {
		state = "failed"
		if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
			state = "timed_out"
			executeErr = fmt.Errorf("%w: %s", ErrToolTimeout, request.Tool)
		} else if errors.Is(executionContext.Err(), context.Canceled) || errors.Is(executeErr, context.Canceled) {
			state = "cancelled"
		}
	}
	b.dbMu.Lock()
	if err := b.finishToolCall(request.CallID, state, response, executeErr, finishedAt); err != nil && executeErr == nil {
		executeErr = err
	}
	if err := b.recordFinished(request, actor, state, executeErr, finishedAt); err != nil && executeErr == nil {
		executeErr = err
	}
	b.dbMu.Unlock()
	if executeErr != nil {
		slog.Error("tool execution finished", "component", "tool_broker", "operation", "execute", "result", state, "tool", request.Tool, "tool_call_id", request.CallID, "run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "duration_ms", finishedAt.Sub(startedAt).Milliseconds(), "error", executeErr)
	} else {
		slog.Info("tool execution finished", "component", "tool_broker", "operation", "execute", "result", state, "tool", request.Tool, "tool_call_id", request.CallID, "run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "duration_ms", finishedAt.Sub(startedAt).Milliseconds())
	}
	return response, executeErr
}

func (b *Broker) authorize(ctx context.Context, tx *sql.Tx, request toolapi.Request, definition toolapi.Definition, scope Authorization, timeout time.Duration) (string, error) {
	risk := policy.RiskLevel(definition.Risk)
	// Authority is Policy + Capability Grant, not task execution_mode.
	// Plan mode only changes when Start is allowed (after human approval); grants still fail closed.
	result := b.policy.Evaluate(risk)
	if result.Action == policy.ActionDeny {
		return "", fmt.Errorf("%w: %s", ErrToolDenied, result.Reason)
	}
	candidates := grantCandidates(request)
	if scope.ExtraRoot && !b.canWaitPermission(ctx, request) {
		return "", fmt.Errorf("%w: extra filesystem root is not granted", ErrToolDenied)
	}
	if result.RequiresApproval && len(candidates) == 0 {
		// Interactive TUI: leave authorize to wait path outside the TX.
		// Job/cron and nested retries never hang (ADR-043).
		if b.canWaitPermission(ctx, request) {
			return "", errPermissionWait
		}
		return "", fmt.Errorf("%w: capability grant is required for %s", ErrToolDenied, definition.Risk)
	}
	if len(candidates) == 0 {
		if scope.ExtraRoot && b.canWaitPermission(ctx, request) {
			return "", errPermissionWait
		}
		return "", nil
	}
	var lastErr error
	for _, candidate := range candidates {
		err := b.approvals.AuthorizeAndConsumeTx(ctx, tx, approval.GrantRequest{
			GrantID: approval.GrantID(candidate),
			TaskID:  kernel.TaskID(request.TaskID), PlanID: kernel.PlanID(request.PlanID),
			StepID: kernel.StepID(request.StepID), PlanHash: request.PlanHash,
			Capability: scope.Capability, Path: scope.Path, Command: scope.Command,
			Arguments: scope.Arguments, NetworkDomain: scope.NetworkDomain,
			Duration: timeout, Now: b.now().UTC(),
		})
		if err == nil {
			return candidate, nil
		}
		lastErr = err
	}
	// No matching grant: interactive TUI may wait for /perm (once).
	// Extra-root / once-then-retry: PathGuard may already contain the path while
	// grants do not — still wait instead of denying R0.
	if b.canWaitPermission(ctx, request) && (result.RequiresApproval || scope.ExtraRoot || grantPathDenied(lastErr)) {
		return "", errPermissionWait
	}
	return "", fmt.Errorf("%w: no candidate capability grant matched: %v", ErrToolDenied, lastErr)
}

func grantPathDenied(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path is outside grant")
}

// errPermissionWait is an internal signal from authorize to Execute (not exported).
var errPermissionWait = errors.New("permission wait required")

type permissionWaitKey struct{}

func permissionWaitDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(permissionWaitKey{}).(bool)
	return v
}

func (b *Broker) canWaitPermission(ctx context.Context, request toolapi.Request) bool {
	if b == nil || permissionWaitDisabled(ctx) {
		return false
	}
	b.mu.RLock()
	gate := b.permission
	b.mu.RUnlock()
	if gate == nil {
		return false
	}
	if isNonInteractiveToolCaller(request) {
		return false
	}
	return request.Interactive
}

func isNonInteractiveToolCaller(request toolapi.Request) bool {
	taskID := strings.TrimSpace(request.TaskID)
	if strings.HasPrefix(taskID, "scheduled_") {
		return true
	}
	actor := strings.ToLower(strings.TrimSpace(request.Actor))
	switch actor {
	case "scheduler", "job", "cron", "scheduledtasks", "system-job":
		return true
	default:
		return false
	}
}

func grantCandidates(request toolapi.Request) []string {
	values := make([]string, 0, len(request.CapabilityGrantIDs)+1)
	seen := make(map[string]struct{}, len(request.CapabilityGrantIDs)+1)
	for _, value := range append([]string{request.CapabilityGrantID}, request.CapabilityGrantIDs...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func validateRequest(request toolapi.Request) error {
	if strings.TrimSpace(request.CallID) == "" || strings.TrimSpace(request.RunID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.PlanID) == "" ||
		strings.TrimSpace(request.StepID) == "" || strings.TrimSpace(request.Tool) == "" {
		return fmt.Errorf("%w: call, run, task, plan, step, and tool IDs are required", ErrInvalidTool)
	}
	if len(request.Arguments) == 0 || !json.Valid(request.Arguments) {
		return fmt.Errorf("%w: arguments must be valid JSON", ErrInvalidTool)
	}
	return nil
}

// executeAfterPermission creates a pending permission, waits for decide, then re-runs Execute with grant.
func (b *Broker) executeAfterPermission(
	ctx context.Context,
	request toolapi.Request,
	definition toolapi.Definition,
	authorization Authorization,
	timeout time.Duration,
	actor string,
	startedAt time.Time,
) (toolapi.Response, error) {
	b.mu.RLock()
	gate := b.permission
	b.mu.RUnlock()
	if gate == nil {
		err := fmt.Errorf("%w: capability grant is required for %s", ErrToolDenied, definition.Risk)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	sessionID := ""
	if meta, ok := runmeta.From(ctx); ok {
		sessionID = meta.SessionID
	}
	capName := strings.TrimSpace(authorization.Capability)
	if capName == "" {
		capName = request.Tool
	}
	permID, err := gate.CreatePending(ctx, PermissionPending{
		SessionID: sessionID, TaskID: request.TaskID, RunID: request.RunID,
		PlanID: request.PlanID, PlanHash: request.PlanHash, StepID: request.StepID,
		ToolCallID: request.CallID, ToolName: request.Tool, Arguments: request.Arguments,
		Capability: capName, Path: authorization.Path, Command: authorization.Command,
		CommandArgs: authorization.Arguments, NetworkDomain: authorization.NetworkDomain,
		Risk: definition.Risk, ExtraRoot: authorization.ExtraRoot,
	})
	if err != nil {
		err = fmt.Errorf("%w: create permission: %v", ErrToolDenied, err)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	slog.Info("tool permission pending",
		"component", "tool_broker", "operation", "permission_wait", "result", "pending",
		"tool", request.Tool, "tool_call_id", request.CallID, "permission_id", permID,
		"run_id", request.RunID, "task_id", request.TaskID, "trace_id", request.TraceID)
	_ = b.audit.Record(ctx, audit.Entry{
		OccurredAt: startedAt, Actor: actor, Action: "tool.permission.pending",
		ResourceType: "tool_permission", ResourceID: permID, Outcome: "pending", TraceID: request.TraceID,
		Details: map[string]any{"tool": request.Tool, "tool_call_id": request.CallID, "run_id": request.RunID},
	})

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	decision, err := gate.Wait(waitCtx, permID)
	if err != nil {
		err = fmt.Errorf("%w: permission wait: %v", ErrToolDenied, err)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	if decision.Decision == "deny" || strings.TrimSpace(decision.GrantID) == "" {
		err := fmt.Errorf("%w: user denied tool permission for %s", ErrToolDenied, request.Tool)
		b.recordDenied(request, actor, err)
		return toolapi.Response{}, err
	}
	// Retry once with the newly issued grant; disable nested permission wait.
	retry := request
	retry.CapabilityGrantID = decision.GrantID
	retry.CapabilityGrantIDs = append([]string{decision.GrantID}, request.CapabilityGrantIDs...)
	retryCtx := context.WithValue(ctx, permissionWaitKey{}, true)
	return b.Execute(retryCtx, retry)
}

func (b *Broker) resolveTimeout(requestedMillis, defaultMillis int64) (time.Duration, error) {
	millis := requestedMillis
	if millis <= 0 {
		millis = defaultMillis
	}
	timeout := time.Duration(millis) * time.Millisecond
	if timeout <= 0 || timeout > b.maximumTimeout {
		return 0, fmt.Errorf("%w: timeout must be between 1ms and %s", ErrToolDenied, b.maximumTimeout)
	}
	return timeout, nil
}

func (b *Broker) insertToolCall(ctx context.Context, tx *sql.Tx, request toolapi.Request, startedAt time.Time) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal tool call request: %w", err)
	}
	var grant any
	if strings.TrimSpace(request.CapabilityGrantID) != "" {
		grant = request.CapabilityGrantID
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tool_calls (
			tool_call_id, run_id, step_id, grant_id, tool_name, state, request, started_at
		) VALUES (?, ?, ?, ?, ?, 'running', ?, ?)`,
		request.CallID, request.RunID, request.StepID, grant, request.Tool, string(encoded), startedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert tool call: %w", err)
	}
	return nil
}

func (b *Broker) finishToolCall(callID, state string, response toolapi.Response, executeErr error, finishedAt time.Time) error {
	payload := map[string]any{"response": response}
	if executeErr != nil {
		payload["error"] = executeErr.Error()
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal tool response: %w", err)
	}
	result, err := b.db.Exec(`
		UPDATE tool_calls SET state = ?, response = ?, finished_at = ? WHERE tool_call_id = ?`,
		state, string(encoded), finishedAt.Format(time.RFC3339Nano), callID,
	)
	if err != nil {
		return fmt.Errorf("finish tool call: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("finish tool call: call %s not found", callID)
	}
	return nil
}

// CancelIncompleteToolCalls marks still-running tool_calls for runID as cancelled.
// Used after chat cancel/fail so durable rows do not stay running forever.
// Fail-closed: never invents success; short timeout + WithoutCancel for durable write.
// Returns the number of rows updated.
func (b *Broker) CancelIncompleteToolCalls(ctx context.Context, runID string) (int, error) {
	if b == nil || b.db == nil {
		return 0, nil
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return 0, fmt.Errorf("%w: run_id is required", ErrInvalidTool)
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finishedAt := b.now().UTC()
	payload, err := json.Marshal(map[string]any{
		"error": "tool call incomplete: run cancelled or failed",
	})
	if err != nil {
		return 0, fmt.Errorf("marshal incomplete tool response: %w", err)
	}
	b.dbMu.Lock()
	defer b.dbMu.Unlock()
	result, err := b.db.ExecContext(writeCtx, `
		UPDATE tool_calls
		SET state = 'cancelled', response = ?, finished_at = ?
		WHERE run_id = ? AND state = 'running'`,
		string(payload), finishedAt.Format(time.RFC3339Nano), runID,
	)
	if err != nil {
		return 0, fmt.Errorf("cancel incomplete tool calls: %w", err)
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		slog.Info("incomplete tool calls cancelled",
			"component", "tool_broker", "operation", "cancel_incomplete", "result", "cancelled",
			"run_id", runID, "count", n)
		auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = b.audit.Record(auditCtx, audit.Entry{
			OccurredAt: finishedAt, Actor: "system", Action: "tool.execute",
			ResourceType: "run", ResourceID: runID, Outcome: "cancelled",
			Details: map[string]any{"incomplete_tool_calls": n},
		})
		auditCancel()
	}
	return int(n), nil
}

func (b *Broker) recordDenied(request toolapi.Request, actor string, cause error) {
	resourceID := strings.TrimSpace(request.CallID)
	if resourceID == "" {
		resourceID = "unidentified"
	}
	slog.Warn("tool execution denied", "component", "tool_broker", "operation", "authorize", "result", "denied", "tool", request.Tool, "tool_call_id", resourceID, "run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "error", cause)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.audit.Record(ctx, audit.Entry{
		Actor: actor, Action: "tool.execute", ResourceType: "tool_call", ResourceID: resourceID,
		Outcome: "denied", TraceID: request.TraceID,
		Details: map[string]any{"tool": request.Tool, "error": cause.Error()},
	})
}

func (b *Broker) recordFinished(request toolapi.Request, actor, outcome string, executeErr error, occurredAt time.Time) error {
	details := map[string]any{"tool": request.Tool, "run_id": request.RunID, "step_id": request.StepID}
	if executeErr != nil {
		details["error"] = executeErr.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.audit.Record(ctx, audit.Entry{
		OccurredAt: occurredAt, Actor: actor, Action: "tool.execute", ResourceType: "tool_call",
		ResourceID: request.CallID, Outcome: outcome, TraceID: request.TraceID, Details: details,
	})
}

// recordIsolationProbe writes a durable audit for process isolation baseline probe results.
func (b *Broker) recordIsolationProbe(status IsolationStatus) {
	if b == nil || b.audit == nil {
		return
	}
	outcome := "degraded"
	switch status.Mode {
	case executor.StatusSystemdScope:
		outcome = "enabled"
	case executor.StatusUnsupported:
		outcome = "unsupported"
	}
	details := map[string]any{
		"mode":       status.Mode,
		"reason":     status.Reason,
		"user_scope": status.UserScope,
		"label":      "process isolation baseline",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.audit.Record(ctx, audit.Entry{
		Actor: "system", Action: "process.isolation", ResourceType: "executor",
		ResourceID: "process_isolation", Outcome: outcome, Details: details,
	})
	slog.Info("process isolation baseline",
		"component", "tool_broker",
		"operation", "isolation_probe",
		"result", outcome,
		"mode", status.Mode,
		"reason", status.Reason,
		"user_scope", status.UserScope,
	)
}

const artifactPreviewRunes = 2_000

func truncateToolOutput(output []byte, _ int, artifactID string) json.RawMessage {
	preview := string(output)
	runes := []rune(preview)
	truncated := false
	if len(runes) > artifactPreviewRunes {
		preview = string(runes[:artifactPreviewRunes])
		truncated = true
	}
	body, err := json.Marshal(map[string]any{
		"truncated":   truncated,
		"preview":     preview,
		"artifact_id": artifactID,
	})
	if err != nil {
		return json.RawMessage(`{"truncated":true,"artifact_id":"` + artifactID + `"}`)
	}
	return body
}
