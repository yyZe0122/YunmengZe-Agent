package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (r *Runner) Run(ctx context.Context, request RunRequest) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := validateRunRequest(request); err != nil {
		return Result{}, err
	}
	startedAt := time.Now()
	slog.Info("agent run started", "component", "agent", "operation", "run", "result", "started",
		"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
		"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
		"allowed_tools", len(request.AllowedTools))
	definitions, advertised, err := selectDefinitions(r.broker.Definitions(), request.AllowedTools)
	if err != nil {
		slog.Error("agent tool selection failed", "component", "agent", "operation", "select_tools", "result", "failed",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "error", err)
		return Result{}, err
	}
	records, err := r.records.Prepare(ctx, request.RunID, request.Messages)
	if err != nil {
		slog.Error("agent history preparation failed", "component", "agent", "operation", "prepare_history", "result", "failed",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID, "error", err)
		return Result{}, err
	}
	messages, seenCallIDs, result, completed, err := r.restore(
		ctx, request.RunID, records, advertised, request.MaxTotalTokens, request.MaxCostMicros,
	)
	if err != nil {
		slog.Error("agent history recovery failed", "component", "agent", "operation", "restore", "result", "failed",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
			"record_count", len(records), "error", err)
		return result, err
	}
	// Provider view is Prefix+Summary+Tail+Ephemeral (ADR-051). Records stay Prefix+current user.
	if len(request.ProviderMessages) > 0 {
		messages = append([]providerapi.Message(nil), request.ProviderMessages...)
		if !completed {
			// Recovery may have appended this-run assistant/tool after the persisted prefix.
			persistLen := len(request.Messages)
			if persistLen < len(records) {
				extra := messagesFromRecords(records[persistLen:])
				messages = append(messages, extra...)
			}
		}
	}
	slog.Info("agent history restored", "component", "agent", "operation", "restore", "result", "succeeded",
		"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
		"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
		"record_count", len(records), "completed", completed)
	resumePrompt := strings.TrimSpace(request.ResumePrompt)
	if completed && resumePrompt == "" {
		slog.Info("agent run completed from recovery", "component", "agent", "operation", "run", "result", "succeeded",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
			"iterations", result.Iterations, "tool_calls", len(result.ToolCalls),
			"duration_ms", time.Since(startedAt).Milliseconds())
		return result, nil
	}
	if resumePrompt != "" {
		msg := providerapi.Message{Role: providerapi.RoleUser, Content: resumePrompt}
		if _, err := r.records.AppendUser(ctx, request.RunID, msg); err != nil {
			return result, err
		}
		messages = append(messages, msg)
		completed = false
		slog.Info("agent run resumed", "component", "agent", "operation", "resume", "result", "continued",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID)
	}

	provider, model, roleWindow := r.snapshotForRole(request.Role)
	// O4: session/job pin overrides main only (not configured subagent/compact roles).
	if r.useModelOverride(request) {
		provider = request.OverrideProvider
		model = strings.TrimSpace(request.ModelOverride)
		if request.OverrideContextWindow > 0 {
			roleWindow = request.OverrideContextWindow
		}
		slog.Info("agent run model override", "component", "agent", "operation", "run", "result", "override",
			"session_id", request.SessionID, "run_id", request.RunID, "model", model)
	}
	if request.ContextWindow <= 0 && roleWindow > 0 {
		request.ContextWindow = roleWindow
	}
	if r.useModelOverride(request) && request.OverrideMaxOutputTokens > 0 {
		request.MaxOutputTokens = request.OverrideMaxOutputTokens
	}
	var stepSigs []string
	toolsDisabled := false
	compactedTurn := request.Compacted
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := budgetExhausted(result.Usage, request.MaxTotalTokens, request.MaxCostMicros); err != nil {
			return result, err
		}
		maxOutputTokens := request.MaxOutputTokens
		if request.MaxTotalTokens > 0 && maxOutputTokens > 0 {
			remaining := request.MaxTotalTokens - int64(result.Usage.TotalTokens)
			if remaining < 0 {
				remaining = 0
			}
			if maxOutputTokens > remaining {
				maxOutputTokens = remaining
			}
		}
		// Soft landing: configured last step advertises no tools so the model must answer in text.
		iterTools := definitions
		loopMsgs := messages
		capped := r.maxIterations > 0 && result.Iterations+1 >= r.maxIterations
		if toolsDisabled || capped {
			iterTools = nil
			if capped && !toolsDisabled {
				loopMsgs = append(append([]providerapi.Message(nil), messages...), providerapi.Message{
					Role: providerapi.RoleSystem, Content: maxStepsPrompt,
				})
			}
		}
		packed, packRaw, packEst, usable := r.packForProvider(loopMsgs, iterTools, request, model, maxOutputTokens)
		providerStartedAt := time.Now()
		response, attempts, err := r.collectProviderResponse(ctx, provider, providerapi.CompletionRequest{
			Model:           model,
			Messages:        packed,
			Tools:           iterTools,
			MaxOutputTokens: maxOutputTokens,
			Temperature:     request.Temperature,
		}, request)
		// Overflow → aggressive repack (and optional head summary) → single retry.
		if err != nil && providerapi.IsContextOverflow(err) {
			slog.Warn("provider context overflow; compacting provider view and retrying once",
				"component", "agent", "operation", "overflow_retry", "result", "warning",
				"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID, "error", err,
			)
			messages = r.rebuildProviderView(ctx, messages, request, model)
			compactedTurn = true
			loopMsgs = messages
			if toolsDisabled || capped {
				loopMsgs = append(append([]providerapi.Message(nil), messages...), providerapi.Message{
					Role: providerapi.RoleSystem, Content: maxStepsPrompt,
				})
			}
			packed, packRaw, packEst, usable = r.packForProvider(loopMsgs, iterTools, request, model, maxOutputTokens)
			response, attempts, err = r.collectProviderResponse(ctx, provider, providerapi.CompletionRequest{
				Model:           model,
				Messages:        packed,
				Tools:           iterTools,
				MaxOutputTokens: maxOutputTokens,
				Temperature:     request.Temperature,
			}, request)
		}
		result.Iterations++
		addUsage(&result.Usage, response.Usage)
		durationMillis := time.Since(providerStartedAt).Milliseconds()
		if err != nil {
			logArgs := []any{
				"component", "provider", "operation", "stream", "result", "failed",
				"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
				"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
				"iteration", result.Iterations, "provider_attempts", attempts, "model", model, "duration_ms", durationMillis,
			}
			var providerErr *providerapi.ProviderError
			if errors.As(err, &providerErr) {
				logArgs = append(logArgs,
					"provider", providerErr.Provider, "error_kind", providerErr.Kind, "status_code", providerErr.StatusCode,
					"retryable", providerErr.Retryable, "retry_after_ms", providerErr.RetryAfter.Milliseconds(),
				)
			}
			logArgs = append(logArgs, "error", err)
			slog.Error("provider iteration failed", logArgs...)
			return result, err
		}
		r.observeUsage(ctx, request, model, response.Usage, packRaw, packEst, usable, len(packed), compactedTurn)
		slog.Info("provider iteration completed",
			"component", "provider", "operation", "stream", "result", "succeeded",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
			"iteration", result.Iterations, "provider_attempts", attempts, "model", model, "duration_ms", durationMillis,
			"input_tokens", response.Usage.InputTokens, "output_tokens", response.Usage.OutputTokens, "total_tokens", response.Usage.TotalTokens,
			"prompt_tokens", response.Usage.PromptTokens(), "estimate_tokens", packEst, "usable_tokens", usable,
			"tool_calls", len(response.ToolCalls), "finish_reason", response.FinishReason,
		)

		assistant := providerapi.Message{
			Role: providerapi.RoleAssistant, Content: response.Content,
			Thinking:  response.Thinking,
			ToolCalls: append([]providerapi.ToolCall(nil), response.ToolCalls...),
		}
		if len(response.ToolCalls) == 0 || toolsDisabled || iterTools == nil {
			// Ignore tool calls when tools were not advertised (soft landing / loop stop).
			if len(response.ToolCalls) > 0 {
				assistant.ToolCalls = nil
				if strings.TrimSpace(assistant.Content) == "" {
					assistant.Content = "Stopped tool use: maximum steps reached or tool loop detected. Please continue with a new user message."
				}
			}
			if strings.TrimSpace(assistant.Content) == "" && strings.TrimSpace(response.Thinking) == "" {
				slog.Warn("provider returned empty final response", "component", "provider", "operation", "stream", "result", "failed",
					"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
					"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
					"iteration", result.Iterations, "model", model, "error", ErrEmptyResponse)
				return result, ErrEmptyResponse
			}
			if strings.TrimSpace(assistant.Content) == "" {
				slog.Warn("provider returned empty final response", "component", "provider", "operation", "stream", "result", "failed",
					"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
					"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
					"iteration", result.Iterations, "model", model, "error", ErrEmptyResponse)
				return result, ErrEmptyResponse
			}
			if _, err := r.records.AppendAssistant(ctx, request.RunID, assistant, response.Usage, response.FinishReason); err != nil {
				return result, err
			}
			if err := budgetExceeded(result.Usage, request.MaxTotalTokens, request.MaxCostMicros); err != nil {
				return result, err
			}
			messages = append(messages, assistant)
			steered, err := r.claimAndAppendStepInbox(ctx, request, &messages)
			if err != nil {
				return result, err
			}
			if steered && !capped {
				result.Content = assistant.Content
				continue
			}
			result.Content = assistant.Content
			slog.Info("agent run completed", "component", "agent", "operation", "run", "result", "succeeded",
				"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
				"plan_id", request.PlanID, "step_id", request.StepID, "trace_id", request.TraceID,
				"iterations", result.Iterations, "tool_calls", len(result.ToolCalls),
				"duration_ms", time.Since(startedAt).Milliseconds())
			return result, nil
		}

		observations, _ := classifyToolCalls(response.ToolCalls, advertised, seenCallIDs)
		executable := make([]providerapi.ToolCall, 0, len(response.ToolCalls))
		rejected := make([]toolCallObservation, 0)
		for _, obs := range observations {
			if obs.Kind == "" {
				executable = append(executable, obs.Call)
			} else {
				rejected = append(rejected, obs)
			}
		}
		sig := toolStepSignature(executable)
		if sig != "" {
			stepSigs = append(stepSigs, sig)
			if hasRepeatedToolSteps(stepSigs, loopDetectionWindowSize, loopDetectionMaxRepeats) {
				slog.Warn("agent tool loop detected; disabling tools",
					"component", "agent", "operation", "loop_detection", "result", "warning",
					"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
					"iterations", result.Iterations,
				)
				// Do not execute this batch; force a text-only follow-up iteration.
				toolsDisabled = true
				messages = append(messages, providerapi.Message{Role: providerapi.RoleSystem, Content: loopDetectedPrompt})
				continue
			}
		}
		if _, err := r.records.AppendAssistant(ctx, request.RunID, assistant, response.Usage, response.FinishReason); err != nil {
			return result, err
		}
		if err := budgetExceeded(result.Usage, request.MaxTotalTokens, request.MaxCostMicros); err != nil {
			return result, err
		}
		messages = append(messages, assistant)
		toolMsgs, toolResps, err := r.executeToolCalls(ctx, request, executable)
		if err != nil {
			return result, err
		}
		rejectMsgs, rejectResps, err := r.appendRejectedToolCalls(ctx, request, rejected)
		if err != nil {
			return result, err
		}
		toolMsgs = append(toolMsgs, rejectMsgs...)
		toolResps = append(toolResps, rejectResps...)
		result.ToolCalls = append(result.ToolCalls, toolResps...)
		from := len(messages)
		messages = append(messages, toolMsgs...)
		// Mid-turn: incremental L1/L2 on new tool bodies; rebuild via Build if still over.
		var midCompact bool
		messages, midCompact = r.maybeCompactMidTurn(ctx, messages, request, model, from)
		if midCompact {
			compactedTurn = true
		}
		if _, err := r.claimAndAppendStepInbox(ctx, request, &messages); err != nil {
			return result, err
		}
	}
}

func (r *Runner) claimAndAppendStepInbox(ctx context.Context, request RunRequest, messages *[]providerapi.Message) (bool, error) {
	if r == nil || r.inbox == nil || strings.TrimSpace(request.SessionID) == "" {
		return false, nil
	}
	claimed := r.inbox.ClaimStep(request.SessionID, request.RunID)
	if len(claimed) == 0 {
		return false, nil
	}
	for _, item := range claimed {
		msg := providerapi.Message{Role: providerapi.RoleUser, Content: item.Text}
		if !item.Persisted {
			if _, err := r.records.AppendUser(ctx, request.RunID, msg); err != nil {
				return false, err
			}
		}
		*messages = append(*messages, msg)
		slog.Info("agent claimed next-step inbox",
			"component", "agent", "operation", "inbox_claim", "result", "continued",
			"session_id", request.SessionID, "run_id", request.RunID, "task_id", request.TaskID,
			"inbox_id", item.ID,
		)
	}
	return true, nil
}
