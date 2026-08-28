package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// CompactSummaryPrompt is the structured head-summary system prompt (OpenCode/Hermes-style).
const CompactSummaryPrompt = `Output exactly the Markdown structure shown inside <template> and keep the section order unchanged. Do not include the <template> tags in your response.
<template>
## Objective
- [one or two brief sentences describing what the user is trying to accomplish]

## Important Details
- [constraints/preferences, decisions and why, important facts/assumptions, exact context needed to continue, or "(none)"]

## Work State
### Completed
- [finished work, verified facts, or changes made; otherwise "(none)"]

### Active
- [current work, partial changes, or investigation state; otherwise "(none)"]

### Blocked
- [blockers, failing commands, or unknowns; otherwise "(none)"]

## Next Move
1. [immediate concrete action, or "(none)"]
2. [next action if known, or "(none)"]

## Relevant Files
- [file or directory path: why it matters, or "(none)"]
</template>

Rules:
- Keep every section, even when empty.
- Use terse bullets, not prose paragraphs.
- Preserve exact file paths, symbols, commands, error strings, URLs, and identifiers when known.
- Do not mention the summary process or that context was compacted.`

const maxStepsPrompt = `CRITICAL - MAXIMUM STEPS REACHED
The maximum number of tool-loop steps allowed for this task has been reached. Tools are disabled until next user input. Respond with text only.
Any attempt to use tools is a critical violation. Respond with text ONLY.`

const loopDetectedPrompt = `CRITICAL - TOOL LOOP DETECTED
The same tool calls (name + arguments) repeated too many times in a short window. Tools are disabled for this turn. Summarize what you tried, what failed, and ask the user how to proceed. Respond with text only.`

// ProposeMemoryFacts runs a short no-tool aux call for H1-lite memory curator.
// Uses the compact model role when configured (ADR-045); otherwise main.
func (r *Runner) ProposeMemoryFacts(ctx context.Context, userText, assistantText string, maxFacts int) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if r == nil {
		return "", fmt.Errorf("%w: runner is required", ErrInvalidRequest)
	}
	if maxFacts <= 0 {
		maxFacts = 3
	}
	provider, model, _ := r.snapshotForRole("compact")
	if provider == nil || strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("%w: provider/model unavailable for curator", ErrInvalidRequest)
	}
	// Import cycle avoidance: prompt text is built by caller (memory package) via free functions
	// is not available here — inline a minimal user body.
	user := fmt.Sprintf("Max facts: %d\n\nUser:\n%s\n\nAssistant:\n%s\n",
		maxFacts, truncateForCurator(userText, 1_200), truncateForCurator(assistantText, 1_200))
	req := providerapi.CompletionRequest{
		Model: model,
		Messages: []providerapi.Message{
			{Role: providerapi.RoleSystem, Content: memoryCuratorSystemPrompt},
			{Role: providerapi.RoleUser, Content: user},
		},
		MaxOutputTokens: 512,
	}
	var content strings.Builder
	err := provider.Stream(ctx, req, func(ev providerapi.StreamEvent) error {
		if ev.Type == providerapi.StreamDelta {
			content.WriteString(ev.ContentDelta)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content.String()), nil
}

const memoryCuratorSystemPrompt = `You extract durable user preferences and stable facts for a local coding agent memory.
Return ONLY a JSON array of strings (0 to N items). No markdown fences, no commentary.
Each string must be one short fact in the user's language (max ~120 chars).
Include only preferences, constraints, environment facts, or decisions that should survive later turns.
Omit task-specific progress, tool logs, code dumps, secrets, and one-off questions.
If nothing durable, return [].`

func truncateForCurator(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// CompactSummary asks the model for a structured head summary (no tools). Used by chat compaction.
// Uses the compact model role when configured (ADR-045); otherwise main.
func (r *Runner) CompactSummary(ctx context.Context, head []providerapi.Message) (string, error) {
	return r.CompactSummaryWithPrevious(ctx, head, "")
}

// CompactSummaryWithPrevious merges previousSummary when non-empty (anchor update).
func (r *Runner) CompactSummaryWithPrevious(ctx context.Context, head []providerapi.Message, previousSummary string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if len(head) == 0 && strings.TrimSpace(previousSummary) == "" {
		return "", nil
	}
	provider, model, _ := r.snapshotForRole("compact")
	body := contextpack.ExtractiveSummary(head, 12_000)
	user := body
	if prev := strings.TrimSpace(previousSummary); prev != "" {
		user = "Update the anchored summary below using the conversation history above.\n" +
			"Preserve still-true details, remove stale details, and merge in the new facts.\n" +
			"<previous-summary>\n" + prev + "\n</previous-summary>\n\n" + body
	} else {
		user = "Create a new anchored summary from the conversation history.\n\n" + body
	}
	req := providerapi.CompletionRequest{
		Model: model,
		Messages: []providerapi.Message{
			{Role: providerapi.RoleSystem, Content: CompactSummaryPrompt},
			{Role: providerapi.RoleUser, Content: user},
		},
		MaxOutputTokens: 2048,
	}
	var content strings.Builder
	err := provider.Stream(ctx, req, func(ev providerapi.StreamEvent) error {
		if ev.Type == providerapi.StreamDelta {
			content.WriteString(ev.ContentDelta)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(content.String())
	if out == "" {
		return contextpack.ExtractiveSummary(head, 4_000), nil
	}
	return out, nil
}

// maybeCompactMidTurn trims new tool bodies (L1/L2) then rebuilds via Build if over budget.
func (r *Runner) maybeCompactMidTurn(ctx context.Context, messages []providerapi.Message, request RunRequest, model string, from int) ([]providerapi.Message, bool) {
	if len(messages) < 4 {
		return messages, false
	}
	maxRunes := r.maxToolResultRunes
	if maxRunes <= 0 {
		maxRunes = DefaultMaxToolResultRunes
	}
	_ = contextpack.TrimToolBodies(messages, from, maxRunes)
	window := request.ContextWindow
	if window <= 0 {
		r.mu.RLock()
		window = r.contextWindow
		r.mu.RUnlock()
	}
	if window <= 0 {
		window = contextpack.DefaultContextWindow
	}
	raw := contextpack.EstimateMessages(messages)
	maxOut := contextpack.ClampMaxOutput(request.MaxOutputTokens)
	usable := contextpack.UsableWindow(window, maxOut, 0)
	est := raw
	if r.calibrator != nil && strings.TrimSpace(model) != "" {
		est = r.calibrator.Apply(model, raw)
	}
	over := usable > 0 && est > usable
	if !contextpack.ShouldCompact(est, usable, over) {
		_ = contextpack.ClearOldToolResults(messages, 0, 0)
		return messages, false
	}
	slog.Info("agent mid-turn compact",
		"component", "agent", "operation", "mid_turn_compact", "result", "succeeded",
		"run_id", request.RunID, "task_id", request.TaskID,
		"estimate_tokens", est, "usable_tokens", usable)
	return r.rebuildProviderView(ctx, messages, request, model), true
}

// rebuildProviderView reassembles Prefix+Summary+Tail+Ephemeral with the same Build used at turn start.
func (r *Runner) rebuildProviderView(ctx context.Context, messages []providerapi.Message, request RunRequest, model string) []providerapi.Message {
	if len(messages) == 0 {
		return messages
	}
	prefix, rest := contextpack.SplitLeadingSystem(messages)
	var existingSummary string
	if n := len(prefix); n > 0 && strings.HasPrefix(prefix[n-1].Content, "[Prior") {
		existingSummary = prefix[n-1].Content
		prefix = prefix[:n-1]
	}
	history, ephemeral := contextpack.SplitCodingEphemeral(rest)
	window := request.ContextWindow
	if window <= 0 {
		r.mu.RLock()
		window = r.contextWindow
		r.mu.RUnlock()
	}
	if window <= 0 {
		window = contextpack.DefaultContextWindow
	}
	maxOut := contextpack.ClampMaxOutput(request.MaxOutputTokens)
	usable := contextpack.UsableWindow(window, maxOut, 0)
	budget := contextpack.HistoryBudget(usable)
	if budget <= 0 {
		budget = contextpack.MinHistoryBudget
	}
	summary := existingSummary
	if len(history) > 4 {
		head, tail := contextpack.SplitHeadTail(history, 2)
		if len(head) > 0 {
			allowLLM := true
			if r.contextStore != nil && strings.TrimSpace(request.SessionID) != "" {
				allowLLM = r.contextStore.AllowLLMCompact(ctx, request.SessionID, time.Now().UTC(),
					contextpack.DefaultAntiThrashWindow, contextpack.DefaultAntiThrashMax)
			}
			var sum string
			if allowLLM {
				var err error
				sum, err = r.CompactSummary(ctx, head)
				if err != nil || strings.TrimSpace(sum) == "" {
					sum = contextpack.ExtractiveSummary(head, 4_000)
				}
			} else {
				sum = contextpack.ExtractiveSummary(head, 4_000)
			}
			summary = sum
			history = tail
			_, history = contextpack.SplitLeadingSystem(history)
		}
	}
	view := contextpack.Build(contextpack.BuildInput{
		Prefix:    prefix,
		History:   history,
		Ephemeral: ephemeral,
		Summary:   summary,
	}, contextpack.BuildOptions{
		Budget:             budget,
		Model:              model,
		MaxToolResultRunes: r.maxToolResultRunes,
	})
	return view.Messages()
}
