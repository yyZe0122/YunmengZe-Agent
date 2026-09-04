package tools

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

const (
	// DefaultMaxTaskDepth: top-level is 0; spawn denied when parent Depth >= max.
	DefaultMaxTaskDepth = 2

	childAnswerStyle = "Reply with a short final conclusion plus paths or evidence. Do not narrate tool calls, thinking, or intermediate steps."

	taskSystemPromptBody = "Complete the delegated task. " +
		"Reply helpfully in the user's language. Prefer absolute paths under the workspace. " +
		"Do not claim tool success without evidence. You cannot spawn nested task runs, call ask_user, write memory, or draft skills. " +
		childAnswerStyle
)

func childSystemPrompt(kindPrompt string) string {
	kindPrompt = strings.TrimSpace(kindPrompt)
	if kindPrompt == "" {
		kindPrompt = taskSystemPromptBody
	}
	return "You are a sub-agent of YunmengZe Agent " + version.Version + ". " + kindPrompt
}

func childPrefixMessages(configDir, workspace, prompt, kindPrompt string) []providerapi.Message {
	msgs := []providerapi.Message{{Role: providerapi.RoleSystem, Content: childSystemPrompt(kindPrompt)}}
	if overlay := providerconfig.OverlayAgents(configDir, workspace); overlay != "" {
		msgs = append(msgs, providerapi.Message{Role: providerapi.RoleSystem, Content: overlay})
	}
	return append(msgs, providerapi.Message{Role: providerapi.RoleUser, Content: prompt})
}

// SubagentRunner runs a child agent loop. Implemented by *agent.Runner.
type SubagentRunner interface {
	Run(context.Context, agent.RunRequest) (agent.Result, error)
}

// TaskToolConfig wires the ADR-039 task builtin.
type TaskToolConfig struct {
	DB        *sql.DB
	Runner    SubagentRunner
	MaxDepth  int
	Now       func() time.Time
	ConfigDir string
	Workspace string
}

type taskTool struct {
	db         *sql.DB
	mu         sync.RWMutex
	runner     SubagentRunner
	maxDepth   int
	now        func() time.Time
	configDir  string
	workspace  string
	configured map[string]struct{}
	inflight   map[string]struct{}
}

// NewTaskTool builds the task tool. Call SetRunner after agent construction if needed.
func NewTaskTool(config TaskToolConfig) (*taskTool, error) {
	if config.DB == nil {
		return nil, errors.New("task tool database is required")
	}
	maxDepth := config.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxTaskDepth
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &taskTool{
		db: config.DB, runner: config.Runner, maxDepth: maxDepth, now: now,
		configDir: strings.TrimSpace(config.ConfigDir), workspace: strings.TrimSpace(config.Workspace),
		inflight: make(map[string]struct{}),
	}, nil
}

// SetRunner attaches the agent runner (composition root may set after agent.New).
func (t *taskTool) SetRunner(runner SubagentRunner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runner = runner
}

func (t *taskTool) SetAgentsOverlay(configDir, workspace string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.configDir = strings.TrimSpace(configDir)
	t.workspace = strings.TrimSpace(workspace)
}

// SetConfiguredRoles records models.* keys present at daemon start (ADR-045). Not hot-reloaded.
func (t *taskTool) SetConfiguredRoles(roles []string) {
	if t == nil {
		return
	}
	configured := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" || role == "main" {
			continue
		}
		configured[role] = struct{}{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.configured = configured
}

func (t *taskTool) Definition() toolapi.Definition {
	t.mu.RLock()
	configured := t.configured
	t.mu.RUnlock()
	kinds := AdvertisedKindsFor(configured)
	enumJSON, err := json.Marshal(kinds)
	if err != nil {
		enumJSON = []byte(`["general","explore"]`)
	}
	schema := fmt.Sprintf(`{
			"type":"object",
			"additionalProperties":false,
			"required":["prompt"],
			"properties":{
				"prompt":{"type":"string","description":"Instructions for the sub-agent"},
				"kind":{"type":"string","enum":%s,"description":"Sub-agent kind. Omit for general. explore is read-only search. web searches and extracts URLs. On resume must match the original kind or be omitted."},
				"tools":{"type":"array","items":{"type":"string"},"description":"Optional subset of parent allowed tools; must not include kind-forbidden names. On resume must match the original set or be omitted."},
				"task_id":{"type":"string","description":"Resume handle from a previous task result (same value as run_id). Omit to start a new sub-agent. Do not invent an id."}
			}
		}`, enumJSON)
	return toolapi.Definition{
		Name:                 "task",
		Description:          "Delegate work to a synchronous sub-agent (inherited tools/grants; child cannot spawn task). Omit task_id to start a new sub-agent; pass the previous task_id to continue that sub-agent's memory. kind general (default) completes the prompt; explore is read-only search; web uses web_search/web_extract; vision/speech/video require configured models.*. Returns the sub-agent final reply plus task_id for optional reuse.",
		Risk:                 string(policy.RiskR1),
		DefaultTimeoutMillis: 30 * 60 * 1000,
		InputSchema:          json.RawMessage(schema),
	}
}

type taskInput struct {
	Prompt string   `json:"prompt"`
	Kind   string   `json:"kind,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	TaskID string   `json:"task_id,omitempty"`
}

func (t *taskTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	var input taskInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Authorization{}, errors.New("prompt is required")
	}
	return Authorization{Capability: "task"}, nil
}

func (t *taskTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	parent, ok := runmeta.From(ctx)
	if !ok || strings.TrimSpace(parent.RunID) == "" {
		return nil, fmt.Errorf("%w: task requires parent run context", ErrToolDenied)
	}
	t.mu.RLock()
	runner := t.runner
	t.mu.RUnlock()
	if runner == nil {
		return nil, fmt.Errorf("%w: task sub-agent runner is not configured", ErrToolDenied)
	}
	if parent.Depth >= t.maxDepth {
		return encodeResult(map[string]any{
			"error":   "max_depth_exceeded",
			"depth":   parent.Depth,
			"max":     t.maxDepth,
			"message": "cannot spawn nested task: maximum depth reached",
		})
	}

	var input taskInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}

	t.mu.RLock()
	configDir, workspace, configured := t.configDir, t.workspace, t.configured
	t.mu.RUnlock()

	handle := strings.TrimSpace(input.TaskID)
	if handle != "" {
		return t.resumeChild(ctx, runner, parent, prompt, input, handle, configured)
	}

	child, err := ResolveChildTools(input.Kind, parent.AllowedTools, input.Tools, configured)
	if err != nil {
		return encodeKindError(err)
	}
	childRunID := childRunID(parent.RunID, prompt)
	if callID := strings.TrimSpace(parent.CallID); callID != "" {
		childRunID = childRunIDFromCall(parent.RunID, callID)
	}

	if !t.tryBegin(childRunID) {
		return encodeHandleError("handle_busy", childRunID, "sub-agent is still running")
	}
	defer t.end(childRunID)

	if err := t.insertChildRun(ctx, childRunID, parent, child); err != nil {
		return nil, err
	}

	req := agent.RunRequest{
		RunID: childRunID, TaskID: parent.TaskID, SessionID: parent.SessionID,
		PlanID: parent.PlanID, PlanHash: parent.PlanHash, StepID: parent.StepID,
		Actor: parent.Actor, TraceID: parent.TraceID, Interactive: parent.Interactive,
		Messages:           childPrefixMessages(configDir, workspace, prompt, child.Prompt),
		AllowedTools:       child.Tools,
		CapabilityGrantIDs: parent.CapabilityGrantIDs,
		MaxOutputTokens:    parent.MaxOutputTokens,
		MaxTotalTokens:     parent.MaxTotalTokens,
		MaxCostMicros:      parent.MaxCostMicros,
		ToolTimeoutMillis:  parent.ToolTimeoutMillis,
		Depth:              parent.Depth + 1,
		Role:               child.Role,
		ParentRunID:        parent.RunID,
	}
	return t.runChild(ctx, runner, req, child.Kind, false)
}

func encodeKindError(err error) (json.RawMessage, error) {
	var kr *kindResolveError
	if !errors.As(err, &kr) {
		return nil, err
	}
	payload := map[string]any{
		"error":   kr.Code,
		"kind":    kr.Kind,
		"message": kr.Message,
	}
	if len(kr.Tools) > 0 {
		payload["tools"] = kr.Tools
	}
	return encodeResult(payload)
}

func (t *taskTool) runChild(ctx context.Context, runner SubagentRunner, req agent.RunRequest, kind string, reused bool) (json.RawMessage, error) {
	result, err := runner.Run(ctx, req)
	finished := t.now().UTC()
	if err != nil {
		_ = t.finishChildRun(context.WithoutCancel(ctx), req.RunID, kernel.RunFailed, err.Error(), finished)
		return encodeResult(map[string]any{
			"task_id": req.RunID,
			"run_id":  req.RunID,
			"kind":    kind,
			"state":   "failed",
			"error":   err.Error(),
			"content": result.Content,
			"reused":  reused,
		})
	}
	_ = t.finishChildRun(context.WithoutCancel(ctx), req.RunID, kernel.RunCompleted, "", finished)
	return encodeResult(map[string]any{
		"task_id":    req.RunID,
		"run_id":     req.RunID,
		"kind":       kind,
		"state":      "completed",
		"content":    result.Content,
		"reused":     reused,
		"iterations": result.Iterations,
		"usage": map[string]any{
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
			"total_tokens":  result.Usage.TotalTokens,
		},
	})
}

func (t *taskTool) resumeChild(ctx context.Context, runner SubagentRunner, parent runmeta.Context, prompt string, input taskInput, handle string, configured map[string]struct{}) (json.RawMessage, error) {
	row, err := t.loadChildHandle(ctx, handle)
	if errors.Is(err, sql.ErrNoRows) {
		return encodeHandleError("handle_not_found", handle, "unknown task_id")
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.sessionID) != strings.TrimSpace(parent.SessionID) {
		return encodeHandleError("handle_wrong_session", handle, "task_id is not from this session")
	}
	if strings.TrimSpace(row.parentRunID) == "" {
		return encodeHandleError("handle_not_child", handle, "task_id is not a sub-agent run")
	}
	switch row.state {
	case string(kernel.RunCompleted), string(kernel.RunFailed), string(kernel.RunRunning):
		// running + not inflight = orphan after crash; takeover instead of handle_busy.
	case string(kernel.RunCancelled):
		return encodeHandleError("handle_cancelled", handle, "cancelled sub-agent cannot be resumed")
	default:
		return encodeHandleError("handle_not_resumable", handle, "sub-agent state "+row.state+" cannot be resumed")
	}
	if !t.tryBegin(handle) {
		return encodeHandleError("handle_busy", handle, "sub-agent is still running")
	}
	defer t.end(handle)
	if row.kind == "" {
		return encodeHandleError("handle_kind_missing", handle, "sub-agent has no stored kind")
	}
	if kind := strings.TrimSpace(input.Kind); kind != "" && kind != row.kind {
		return encodeHandleError("handle_kind_mismatch", handle, "kind must match the original sub-agent or be omitted")
	}
	storedTools := decodeChildTools(row.toolsJSON)
	if len(storedTools) == 0 {
		return encodeHandleError("handle_tools_missing", handle, "sub-agent has no stored tool set")
	}
	if len(input.Tools) > 0 && !sameToolSet(input.Tools, storedTools) {
		return encodeHandleError("handle_tools_mismatch", handle, "tools must match the original sub-agent or be omitted")
	}
	allowed := intersectTools(storedTools, parent.AllowedTools)
	if len(allowed) == 0 {
		return encodeHandleError("handle_tools_empty", handle, "stored tools do not intersect the current parent allow-list")
	}

	records, err := agent.NewRecordStore(t.db)
	if err != nil {
		return nil, err
	}
	prefix, err := records.InitialPrefix(ctx, handle)
	if err != nil {
		return nil, err
	}
	if len(prefix) == 0 {
		return encodeHandleError("handle_no_history", handle, "sub-agent has no persisted prefix")
	}

	if err := t.attachChildParent(ctx, handle, parent); err != nil {
		return encodeHandleError("handle_not_resumable", handle, err.Error())
	}

	spec, ok := lookupKind(row.kind)
	role := "subagent"
	if ok {
		if resolved, err := resolveChildRole(spec, configured); err == nil {
			role = resolved
		}
	}

	req := agent.RunRequest{
		RunID: handle, TaskID: parent.TaskID, SessionID: parent.SessionID,
		PlanID: parent.PlanID, PlanHash: parent.PlanHash, StepID: parent.StepID,
		Actor: parent.Actor, TraceID: parent.TraceID, Interactive: parent.Interactive,
		Messages:           prefix,
		ResumePrompt:       prompt,
		AllowedTools:       allowed,
		CapabilityGrantIDs: parent.CapabilityGrantIDs,
		MaxOutputTokens:    parent.MaxOutputTokens,
		MaxTotalTokens:     parent.MaxTotalTokens,
		MaxCostMicros:      parent.MaxCostMicros,
		ToolTimeoutMillis:  parent.ToolTimeoutMillis,
		Depth:              parent.Depth + 1,
		Role:               role,
		ParentRunID:        parent.RunID,
	}
	return t.runChild(ctx, runner, req, row.kind, true)
}

type childHandleRow struct {
	sessionID   string
	parentRunID string
	state       string
	kind        string
	toolsJSON   string
}

func (t *taskTool) loadChildHandle(ctx context.Context, runID string) (childHandleRow, error) {
	var row childHandleRow
	err := t.db.QueryRowContext(ctx, `
		SELECT t.session_id, COALESCE(r.parent_run_id, ''), r.state, COALESCE(r.child_kind, ''), COALESCE(r.child_tools, '')
		FROM runs r
		INNER JOIN tasks t ON t.task_id = r.task_id
		WHERE r.run_id = ?`, runID).Scan(&row.sessionID, &row.parentRunID, &row.state, &row.kind, &row.toolsJSON)
	if err != nil {
		return childHandleRow{}, err
	}
	return row, nil
}

func encodeHandleError(code, handle, message string) (json.RawMessage, error) {
	return encodeResult(map[string]any{
		"error":   code,
		"task_id": handle,
		"message": message,
	})
}

func (t *taskTool) insertChildRun(ctx context.Context, childRunID string, parent runmeta.Context, child ChildSpec) error {
	now := t.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	toolsJSON, err := json.Marshal(child.Tools)
	if err != nil {
		return fmt.Errorf("marshal child tools: %w", err)
	}
	_, err = t.db.ExecContext(ctx, `
		INSERT INTO runs (run_id, task_id, plan_id, state, started_at, finished_at, error, version, updated_at, step_id, parent_run_id, child_kind, child_tools)
		VALUES (?, ?, ?, ?, ?, NULL, NULL, 1, ?, NULL, ?, ?, ?)`,
		childRunID, parent.TaskID, parent.PlanID, kernel.RunRunning, stamp, stamp, parent.RunID, child.Kind, string(toolsJSON),
	)
	if err != nil {
		return fmt.Errorf("insert child run: %w", err)
	}
	return nil
}

func (t *taskTool) tryBegin(runID string) bool {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == nil {
		t.inflight = make(map[string]struct{})
	}
	if _, ok := t.inflight[runID]; ok {
		return false
	}
	t.inflight[runID] = struct{}{}
	return true
}

func (t *taskTool) end(runID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inflight, strings.TrimSpace(runID))
}

// attachChildParent retargets a child handle onto the current parent task/run
// without reversing runs.state (ADR-039). Busy is process-local inflight.
func (t *taskTool) attachChildParent(ctx context.Context, runID string, parent runmeta.Context) error {
	now := t.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	result, err := t.db.ExecContext(ctx, `
		UPDATE runs SET version = version + 1, updated_at = ?,
			task_id = ?, plan_id = ?, parent_run_id = ?
		WHERE run_id = ? AND parent_run_id IS NOT NULL AND state != ?`,
		stamp, parent.TaskID, parent.PlanID, parent.RunID, runID, kernel.RunCancelled,
	)
	if err != nil {
		return fmt.Errorf("attach child parent: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("attach child parent: not resumable")
	}
	return nil
}

func decodeChildTools(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var tools []string
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		return nil
	}
	return tools
}

func sameToolSet(left, right []string) bool {
	a, _ := uniqueToolSet(left)
	b, _ := uniqueToolSet(right)
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, name := range a {
		seen[name] = struct{}{}
	}
	for _, name := range b {
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

func intersectTools(stored, parent []string) []string {
	_, parentSet := uniqueToolSet(parent)
	out := make([]string, 0, len(stored))
	seen := make(map[string]struct{}, len(stored))
	for _, name := range stored {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := parentSet[name]; !ok {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (t *taskTool) finishChildRun(ctx context.Context, runID string, state kernel.RunState, failure string, finished time.Time) error {
	stamp := finished.UTC().Format(time.RFC3339Nano)
	if len(failure) > 2048 {
		failure = failure[:2048]
	}
	var errCol any
	if failure != "" {
		errCol = failure
	}
	_, err := t.db.ExecContext(ctx, `
		UPDATE runs SET state = ?, version = version + 1, updated_at = ?, finished_at = ?, error = ?
		WHERE run_id = ?`,
		state, stamp, stamp, errCol, runID,
	)
	return err
}

func childRunID(parentRunID, prompt string) string {
	return "c" + shortHash("child-run", parentRunID, prompt)[:31]
}

func childRunIDFromCall(parentRunID, callID string) string {
	return "c" + shortHash("child-run", parentRunID, callID)[:31]
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
