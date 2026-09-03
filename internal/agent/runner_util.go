package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

func validateRunRequest(request RunRequest) error {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.TaskID) == "" ||
		strings.TrimSpace(request.PlanID) == "" || strings.TrimSpace(request.PlanHash) == "" ||
		strings.TrimSpace(request.StepID) == "" || len(request.Messages) == 0 {
		return fmt.Errorf("%w: run, task, plan, plan hash, step, and messages are required", ErrInvalidRequest)
	}
	if request.MaxTotalTokens < 0 || request.MaxCostMicros < 0 {
		return fmt.Errorf("%w: token and cost budgets cannot be negative", ErrInvalidRequest)
	}
	return nil
}
func selectDefinitions(all []toolapi.Definition, allowed []string) ([]providerapi.ToolDefinition, map[string]struct{}, error) {
	available := make(map[string]toolapi.Definition, len(all))
	for _, definition := range all {
		available[definition.Name] = definition
	}
	advertised := make(map[string]struct{}, len(allowed))
	definitions := make([]providerapi.ToolDefinition, 0, len(allowed))
	for _, rawName := range allowed {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		definition, exists := available[name]
		if !exists {
			return nil, nil, fmt.Errorf("%w: unknown allowed tool %q", ErrInvalidRequest, rawName)
		}
		if _, duplicate := advertised[name]; duplicate {
			continue
		}
		advertised[name] = struct{}{}
		definitions = append(definitions, providerapi.ToolDefinition{
			Name: definition.Name, Description: definition.Description, InputSchema: append(json.RawMessage(nil), definition.InputSchema...),
		})
	}
	return definitions, advertised, nil
}

// classifyToolCalls marks call IDs as seen and reports the first protocol error.
// Agent execution treats those errors as observations (ADR-052); this helper stays
// for tests that still assert the classification.
func classifyToolCalls(calls []providerapi.ToolCall, advertised, seen map[string]struct{}) ([]toolCallObservation, error) {
	batch := make(map[string]struct{}, len(calls))
	out := make([]toolCallObservation, 0, len(calls))
	var first error
	for _, call := range calls {
		obs := inspectToolCall(call, advertised, seen, batch)
		out = append(out, obs)
		if obs.Kind != "" && first == nil {
			first = obs.err()
		}
		if id := strings.TrimSpace(call.ID); id != "" {
			batch[id] = struct{}{}
			seen[id] = struct{}{}
		}
	}
	return out, first
}

type toolCallObservation struct {
	Call providerapi.ToolCall
	Kind string
}

func (o toolCallObservation) err() error {
	switch o.Kind {
	case "unadvertised_tool":
		return fmt.Errorf("%w: %s", ErrUnadvertisedTool, o.Call.Name)
	case "invalid_tool_call":
		return ErrInvalidToolCall
	default:
		return nil
	}
}

func inspectToolCall(call providerapi.ToolCall, advertised, seen, batch map[string]struct{}) toolCallObservation {
	id := strings.TrimSpace(call.ID)
	name := strings.TrimSpace(call.Name)
	if id == "" || name == "" || !json.Valid([]byte(call.Arguments)) {
		return toolCallObservation{Call: call, Kind: "invalid_tool_call"}
	}
	if _, exists := advertised[name]; !exists {
		return toolCallObservation{Call: call, Kind: "unadvertised_tool"}
	}
	if _, duplicate := seen[id]; duplicate {
		return toolCallObservation{Call: call, Kind: "invalid_tool_call"}
	}
	if _, duplicate := batch[id]; duplicate {
		return toolCallObservation{Call: call, Kind: "invalid_tool_call"}
	}
	return toolCallObservation{Call: call}
}

func toolResultMatches(response toolapi.Response, content string) (bool, error) {
	if len(response.Output) > 0 {
		return string(response.Output) == content, nil
	}
	persisted, err := toolResultContent(response)
	if err != nil {
		return false, err
	}
	return persisted == content, nil
}

func toolResultContent(response toolapi.Response) (string, error) {
	if len(response.Output) > 0 {
		return string(response.Output), nil
	}
	if response.Artifact != nil {
		encoded, err := json.Marshal(map[string]any{
			"truncated":   true,
			"artifact_id": response.Artifact.ID,
			"artifact":    response.Artifact,
		})
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	return `{}`, nil
}

func toolDeniedContent(call providerapi.ToolCall, err error) string {
	return toolObservationJSON("tool_denied", call, err, "Do not retry the same arguments. Interactive TUI: call fs_* / process_* / git_* with an absolute path to prompt /perm for an extra root. Relative paths resolve against the workspace root. Keep duration within the approved grant.")
}

func toolFailedContent(call providerapi.ToolCall, err error) string {
	return toolObservationJSON("tool_failed", call, err, "Read the error and tool output. Do not claim the tool succeeded. Fix the input or approach, then retry if useful.")
}

func toolCallRejectedContent(obs toolCallObservation) string {
	kind := obs.Kind
	if kind == "" {
		kind = "invalid_tool_call"
	}
	hint := "Do not retry this call as-is. Use an advertised tool name and valid JSON arguments."
	if kind == "unadvertised_tool" {
		hint = "That tool is not available in this turn. Use an advertised tool or ask the user to switch mode / grant permission."
	}
	return toolObservationJSON(kind, obs.Call, obs.err(), hint)
}

func toolObservationJSON(kind string, call providerapi.ToolCall, err error, hint string) string {
	message := kind
	if err != nil {
		message = err.Error()
	}
	payload := map[string]any{
		"error":   kind,
		"tool":    call.Name,
		"message": message,
		"hint":    hint,
	}
	encoded, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"` + kind + `","message":"tool call failed"}`
	}
	return string(encoded)
}

func isDeniedToolResult(content string) bool {
	return observationKind(content) == "tool_denied"
}

func isObservationToolResult(content string) bool {
	switch observationKind(content) {
	case "tool_denied", "tool_failed", "unadvertised_tool", "invalid_tool_call", "unknown_tool":
		return true
	default:
		return false
	}
}

func observationKind(content string) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return payload.Error
}

func hasToolCallIDs(calls []providerapi.ToolCall) bool {
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" {
			return false
		}
	}
	return len(calls) > 0
}

func addUsage(total *providerapi.Usage, next providerapi.Usage) {
	total.InputTokens += next.InputTokens
	total.OutputTokens += next.OutputTokens
	total.TotalTokens += next.TotalTokens
	total.CacheReadTokens += next.CacheReadTokens
	total.CacheWriteTokens += next.CacheWriteTokens
	if total.Cost.Currency == "" || total.Cost.Currency == next.Cost.Currency {
		if total.Cost.Currency == "" {
			total.Cost.Currency = next.Cost.Currency
		}
		total.Cost.Micros += next.Cost.Micros
	}
}

func cloneGrantMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
func budgetExhausted(usage providerapi.Usage, maxTotalTokens, maxCostMicros int64) error {
	if maxTotalTokens > 0 && int64(usage.TotalTokens) >= maxTotalTokens {
		return ErrTokenBudgetExceeded
	}
	if maxCostMicros > 0 && usage.Cost.Micros >= maxCostMicros {
		return ErrCostBudgetExceeded
	}
	return nil
}

func budgetExceeded(usage providerapi.Usage, maxTotalTokens, maxCostMicros int64) error {
	if maxTotalTokens > 0 && int64(usage.TotalTokens) > maxTotalTokens {
		return ErrTokenBudgetExceeded
	}
	if maxCostMicros > 0 && usage.Cost.Micros > maxCostMicros {
		return ErrCostBudgetExceeded
	}
	return nil
}
