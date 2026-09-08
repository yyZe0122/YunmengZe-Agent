package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

// readOnlyParallelTools may run in the same provider step concurrently.
// Broker dbMu serializes grant consume + tool_calls writes; tool bodies overlap.
var readOnlyParallelTools = map[string]struct{}{
	"fs_read": {}, "fs_list": {}, "fs_stat": {}, "fs_glob": {}, "fs_grep": {},
}

func allReadOnlyParallel(calls []providerapi.ToolCall) bool {
	if len(calls) <= 1 {
		return false
	}
	for _, call := range calls {
		if _, ok := readOnlyParallelTools[call.Name]; !ok {
			return false
		}
	}
	return true
}

func (r *Runner) executeToolCalls(ctx context.Context, request RunRequest, calls []providerapi.ToolCall) ([]providerapi.Message, []toolapi.Response, error) {
	if len(calls) == 0 {
		return nil, nil, nil
	}
	if allReadOnlyParallel(calls) {
		return r.executeToolCallsParallel(ctx, request, calls)
	}
	return r.executeToolCallsSerial(ctx, request, calls)
}

func (r *Runner) executeOneToolCall(ctx context.Context, request RunRequest, call providerapi.ToolCall) (providerapi.Message, toolapi.Response, error) {
	toolCtx := runmeta.With(ctx, runmeta.Context{
		RunID: request.RunID, TaskID: request.TaskID, SessionID: request.SessionID,
		PlanID: request.PlanID, PlanHash: request.PlanHash, StepID: request.StepID,
		Actor: request.Actor, TraceID: request.TraceID,
		AllowedTools:       append([]string(nil), request.AllowedTools...),
		CapabilityGrantIDs: cloneGrantMap(request.CapabilityGrantIDs),
		MaxOutputTokens:    request.MaxOutputTokens, MaxTotalTokens: request.MaxTotalTokens,
		MaxCostMicros: request.MaxCostMicros, ToolTimeoutMillis: request.ToolTimeoutMillis,
		Depth: request.Depth, CallID: call.ID, Interactive: request.Interactive,
	})
	toolResponse, err := r.broker.Execute(toolCtx, toolapi.Request{
		CallID: call.ID, RunID: request.RunID, TaskID: request.TaskID,
		PlanID: request.PlanID, PlanHash: request.PlanHash, StepID: request.StepID,
		CapabilityGrantID: request.CapabilityGrantID, CapabilityGrantIDs: append([]string(nil), request.CapabilityGrantIDs[call.Name]...),
		Actor: request.Actor, TraceID: request.TraceID, Interactive: request.Interactive,
		Tool: call.Name, Arguments: json.RawMessage(call.Arguments), TimeoutMillis: request.ToolTimeoutMillis,
	})
	if err != nil {
		if isInfrastructureToolError(ctx, err) {
			return providerapi.Message{}, toolapi.Response{}, err
		}
		content := toolDeniedContent(call, err)
		if !errors.Is(err, toolapi.ErrDenied) && !errors.Is(err, toolapi.ErrPermissionRequired) {
			content = toolFailedContent(call, err)
			if len(toolResponse.Output) > 0 {
				if merged := mergeToolFailureOutput(toolResponse.Output, call, err); merged != "" {
					content = merged
				}
			}
			slog.Info("tool call failed; feeding result back to provider",
				"component", "agent", "operation", "tool_failed", "result", "continued",
				"run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID,
				"step_id", request.StepID, "trace_id", request.TraceID,
				"tool", call.Name, "tool_call_id", call.ID, "error", err,
			)
		} else {
			slog.Info("tool call denied; feeding result back to provider",
				"component", "agent", "operation", "tool_denied", "result", "continued",
				"run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID,
				"step_id", request.StepID, "trace_id", request.TraceID,
				"tool", call.Name, "tool_call_id", call.ID, "error", err,
			)
		}
		toolResponse.CallID = call.ID
		toolResponse.Tool = call.Name
		toolResponse.Output = json.RawMessage(content)
		toolMessage := providerapi.Message{Role: providerapi.RoleTool, ToolCallID: call.ID, Content: content}
		return toolMessage, toolResponse, nil
	}
	content, err := toolResultContent(toolResponse)
	if err != nil {
		if isInfrastructureToolError(ctx, err) {
			return providerapi.Message{}, toolapi.Response{}, err
		}
		content = toolFailedContent(call, err)
		toolResponse.Output = json.RawMessage(content)
	}
	toolMessage := providerapi.Message{Role: providerapi.RoleTool, ToolCallID: call.ID, Content: content}
	return toolMessage, toolResponse, nil
}

func (r *Runner) appendRejectedToolCalls(ctx context.Context, request RunRequest, rejected []toolCallObservation) ([]providerapi.Message, []toolapi.Response, error) {
	if len(rejected) == 0 {
		return nil, nil, nil
	}
	outMsgs := make([]providerapi.Message, 0, len(rejected))
	outResps := make([]toolapi.Response, 0, len(rejected))
	for _, obs := range rejected {
		content := toolCallRejectedContent(obs)
		toolMessage := providerapi.Message{Role: providerapi.RoleTool, ToolCallID: obs.Call.ID, Content: content}
		toolResponse := toolapi.Response{CallID: obs.Call.ID, Tool: obs.Call.Name, Output: json.RawMessage(content)}
		rec, err := r.records.AppendToolResult(ctx, request.RunID, toolMessage)
		if err != nil {
			return nil, nil, err
		}
		r.indexToolResult(ctx, request, obs.Call.Name, rec)
		outMsgs = append(outMsgs, toolMessage)
		outResps = append(outResps, toolResponse)
		slog.Info("invalid tool call; feeding result back to provider",
			"component", "agent", "operation", "tool_rejected", "result", "continued",
			"run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID,
			"step_id", request.StepID, "trace_id", request.TraceID,
			"tool", obs.Call.Name, "tool_call_id", obs.Call.ID, "kind", obs.Kind,
		)
	}
	return outMsgs, outResps, nil
}

func isInfrastructureToolError(ctx context.Context, err error) bool {
	if err == nil || ctx == nil || ctx.Err() == nil {
		return false
	}
	return errors.Is(err, ctx.Err())
}

func mergeToolFailureOutput(output json.RawMessage, call providerapi.ToolCall, err error) string {
	var payload map[string]any
	if json.Unmarshal(output, &payload) != nil || payload == nil {
		return ""
	}
	if _, ok := payload["error"]; !ok {
		payload["error"] = "tool_failed"
	}
	if _, ok := payload["tool"]; !ok {
		payload["tool"] = call.Name
	}
	if _, ok := payload["message"]; !ok && err != nil {
		payload["message"] = err.Error()
	}
	if _, ok := payload["hint"]; !ok {
		payload["hint"] = "Read the error and tool output. Do not claim the tool succeeded."
	}
	encoded, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return ""
	}
	return string(encoded)
}

func (r *Runner) executeToolCallsSerial(ctx context.Context, request RunRequest, calls []providerapi.ToolCall) ([]providerapi.Message, []toolapi.Response, error) {
	outMsgs := make([]providerapi.Message, 0, len(calls))
	outResps := make([]toolapi.Response, 0, len(calls))
	for i, call := range calls {
		toolMessage, toolResponse, err := r.executeOneToolCall(ctx, request, call)
		if err != nil {
			if persistErr := r.persistInterruptedToolCalls(ctx, request, calls[i:], err); persistErr != nil {
				return nil, nil, persistErr
			}
			return nil, nil, err
		}
		rec, err := r.records.AppendToolResult(ctx, request.RunID, toolMessage)
		if err != nil {
			return nil, nil, err
		}
		r.indexToolResult(ctx, request, call.Name, rec)
		outMsgs = append(outMsgs, toolMessage)
		outResps = append(outResps, toolResponse)
	}
	return outMsgs, outResps, nil
}

// executeToolCallsParallel runs read-only tools concurrently. Broker dbMu serializes
// grant TX / tool_calls writes; tool bodies (disk IO) overlap. Results keep call order.
func (r *Runner) executeToolCallsParallel(ctx context.Context, request RunRequest, calls []providerapi.ToolCall) ([]providerapi.Message, []toolapi.Response, error) {
	type result struct {
		msg  providerapi.Message
		resp toolapi.Response
		err  error
	}
	results := make([]result, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call providerapi.ToolCall) {
			defer wg.Done()
			msg, resp, err := r.executeOneToolCall(ctx, request, call)
			results[i] = result{msg: msg, resp: resp, err: err}
		}(i, call)
	}
	wg.Wait()
	outMsgs := make([]providerapi.Message, 0, len(calls))
	outResps := make([]toolapi.Response, 0, len(calls))
	var firstErr error
	for i := range results {
		if results[i].err != nil {
			if firstErr == nil {
				firstErr = results[i].err
			}
			if persistErr := r.persistInterruptedToolCall(ctx, request, calls[i], results[i].err); persistErr != nil && firstErr == nil {
				firstErr = persistErr
			}
			continue
		}
		rec, err := r.records.AppendToolResult(ctx, request.RunID, results[i].msg)
		if err != nil {
			return nil, nil, err
		}
		r.indexToolResult(ctx, request, calls[i].Name, rec)
		outMsgs = append(outMsgs, results[i].msg)
		outResps = append(outResps, results[i].resp)
	}
	if firstErr != nil {
		return outMsgs, outResps, firstErr
	}
	return outMsgs, outResps, nil
}

func (r *Runner) persistInterruptedToolCalls(ctx context.Context, request RunRequest, calls []providerapi.ToolCall, cause error) error {
	for _, call := range calls {
		if err := r.persistInterruptedToolCall(ctx, request, call, cause); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) persistInterruptedToolCall(ctx context.Context, request RunRequest, call providerapi.ToolCall, cause error) error {
	if r == nil || r.records == nil || strings.TrimSpace(call.ID) == "" {
		return nil
	}
	content := toolInterruptedContent(call, cause)
	msg := providerapi.Message{Role: providerapi.RoleTool, ToolCallID: call.ID, Content: content}
	persistCtx := context.WithoutCancel(ctx)
	if persistCtx.Err() != nil {
		persistCtx = context.Background()
	}
	rec, err := r.records.AppendToolResult(persistCtx, request.RunID, msg)
	if err != nil {
		return err
	}
	r.indexToolResult(persistCtx, request, call.Name, rec)
	slog.Info("interrupted tool call; persisted synthetic result",
		"component", "agent", "operation", "tool_interrupted", "result", "cancelled",
		"run_id", request.RunID, "task_id", request.TaskID, "plan_id", request.PlanID,
		"step_id", request.StepID, "trace_id", request.TraceID,
		"tool", call.Name, "tool_call_id", call.ID, "error", cause,
	)
	return nil
}

func (r *Runner) indexToolResult(ctx context.Context, request RunRequest, toolName string, rec RunRecord) {
	if r == nil || r.transcript == nil || strings.TrimSpace(request.SessionID) == "" {
		return
	}
	if request.Depth > 0 || strings.TrimSpace(request.ParentRunID) != "" {
		return
	}
	body := projectToolResult(toolName, rec.Message)
	if body == "" {
		return
	}
	created := ""
	if !rec.CreatedAt.IsZero() {
		created = rec.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	_ = r.transcript.IndexTranscriptRecord(ctx, request.SessionID, request.RunID, rec.Position, "tool", body, created)
}

func projectToolResult(toolName string, msg providerapi.Message) string {
	toolName = strings.TrimSpace(toolName)
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	path := toolResultPath(content)
	var b strings.Builder
	if toolName != "" {
		b.WriteString(toolName)
		b.WriteByte(' ')
	}
	if path != "" {
		b.WriteString(path)
		b.WriteByte(' ')
	}
	b.WriteString(content)
	return b.String()
}

func toolResultPath(content string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(content), &payload) == nil {
		return strings.TrimSpace(payload.Path)
	}
	return ""
}
