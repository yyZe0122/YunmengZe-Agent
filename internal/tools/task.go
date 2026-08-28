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

	taskSystemPromptBody = "Complete the delegated task. " +
		"Reply helpfully in the user's language. Prefer absolute paths under the workspace. " +
		"Do not claim tool success without evidence. You cannot spawn nested task runs, call ask_user, write memory, or draft skills."
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
				"kind":{"type":"string","enum":%s,"description":"Sub-agent kind. Omit for general. explore is read-only search. web searches and extracts URLs."},
				"tools":{"type":"array","items":{"type":"string"},"description":"Optional subset of parent allowed tools; must not include kind-forbidden names"}
			}
		}`, enumJSON)
	return toolapi.Definition{
		Name:                 "task",
		Description:          "Delegate work to a synchronous sub-agent run (same task, inherited tools/grants; child cannot spawn task). kind general (default) completes the prompt; explore is read-only search; web uses web_search/web_extract; vision/speech/video require configured models.*. Returns the sub-agent final reply.",
		Risk:                 string(policy.RiskR1),
		DefaultTimeoutMillis: 30 * 60 * 1000,
		InputSchema:          json.RawMessage(schema),
	}
}

type taskInput struct {
	Prompt string   `json:"prompt"`
	Kind   string   `json:"kind,omitempty"`
	Tools  []string `json:"tools,omitempty"`
}

func (t *taskTool) Authorization(raw json.RawMessage) (Authorization, error) {
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
	child, err := ResolveChildTools(input.Kind, parent.AllowedTools, input.Tools, configured)
	if err != nil {
		return encodeKindError(err)
	}
	childRunID := childRunID(parent.RunID, prompt)
	if callID := strings.TrimSpace(parent.CallID); callID != "" {
		childRunID = childRunIDFromCall(parent.RunID, callID)
	}

	if err := t.insertChildRun(ctx, childRunID, parent); err != nil {
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
	}

	result, err := runner.Run(ctx, req)
	finished := t.now().UTC()
	if err != nil {
		_ = t.finishChildRun(context.WithoutCancel(ctx), childRunID, kernel.RunFailed, err.Error(), finished)
		return encodeResult(map[string]any{
			"run_id":  childRunID,
			"kind":    child.Kind,
			"state":   "failed",
			"error":   err.Error(),
			"content": result.Content,
		})
	}
	_ = t.finishChildRun(context.WithoutCancel(ctx), childRunID, kernel.RunCompleted, "", finished)
	return encodeResult(map[string]any{
		"run_id":     childRunID,
		"kind":       child.Kind,
		"state":      "completed",
		"content":    result.Content,
		"iterations": result.Iterations,
		"usage": map[string]any{
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
			"total_tokens":  result.Usage.TotalTokens,
		},
	})
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

func (t *taskTool) insertChildRun(ctx context.Context, childRunID string, parent runmeta.Context) error {
	now := t.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO runs (run_id, task_id, plan_id, state, started_at, finished_at, error, version, updated_at, step_id, parent_run_id)
		VALUES (?, ?, ?, ?, ?, NULL, NULL, 1, ?, NULL, ?)`,
		childRunID, parent.TaskID, parent.PlanID, kernel.RunRunning, stamp, stamp, parent.RunID,
	)
	if err != nil {
		return fmt.Errorf("insert child run: %w", err)
	}
	return nil
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
