package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (r *Runner) packForProvider(
	messages []providerapi.Message,
	definitions []providerapi.ToolDefinition,
	request RunRequest,
	model string,
	maxOutputTokens int64,
) (packed []providerapi.Message, rawEstimate, estimate, usable int64) {
	window := request.ContextWindow
	if window <= 0 {
		r.mu.RLock()
		window = r.contextWindow
		r.mu.RUnlock()
	}
	if window <= 0 {
		window = contextpack.DefaultContextWindow
	}
	toolEst := contextpack.EstimateTools(definitions)
	usable = contextpack.UsableWindow(window, contextpack.ClampMaxOutput(maxOutputTokens), 0)
	budget := int64(0)
	if usable > 0 {
		budget = usable - toolEst
		if budget < 1024 {
			budget = 1024
		}
		if r.calibrator != nil {
			ratio := r.calibrator.Ratio(model)
			if ratio > 0 {
				budget = int64(float64(budget)/ratio + 0.5)
				if budget < 1024 {
					budget = 1024
				}
			}
		}
	}
	_ = budget
	// L1 only: never L2/L3 the assembled ContextView on the hot path (ADR-051).
	maxRunes := r.maxToolResultRunes
	if maxRunes <= 0 {
		maxRunes = DefaultMaxToolResultRunes
	}
	out := cloneProviderMessages(messages)
	_ = contextpack.TrimToolBodies(out, 0, maxRunes)
	rawEstimate = contextpack.EstimateMessages(out) + toolEst
	estimate = rawEstimate
	if r.calibrator != nil {
		estimate = r.calibrator.Apply(model, rawEstimate)
	}
	return out, rawEstimate, estimate, usable
}

func cloneProviderMessages(in []providerapi.Message) []providerapi.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]providerapi.Message, len(in))
	for i, msg := range in {
		out[i] = msg
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = append([]providerapi.ToolCall(nil), msg.ToolCalls...)
		}
	}
	return out
}

func (r *Runner) observeUsage(
	ctx context.Context,
	request RunRequest,
	model string,
	usage providerapi.Usage,
	rawEstimate, estimate, usable int64,
	historyMsgs int,
	compacted bool,
) {
	prompt := int64(usage.PromptTokens())
	if r.calibrator != nil && rawEstimate > 0 && prompt > 0 {
		r.calibrator.Observe(model, rawEstimate, prompt)
	}
	if r.contextStore == nil || strings.TrimSpace(request.TaskID) == "" {
		return
	}
	window := request.ContextWindow
	if window <= 0 {
		r.mu.RLock()
		window = r.contextWindow
		r.mu.RUnlock()
	}
	ratio := 1.0
	calibrated := false
	if r.calibrator != nil {
		ratio = r.calibrator.Ratio(model)
		calibrated = ratio != 1
	}
	snap := contextpack.Snapshot{
		TaskID:           request.TaskID,
		SessionID:        request.SessionID,
		Model:            model,
		ContextWindow:    window,
		MaxOutputTokens:  request.MaxOutputTokens,
		UsableTokens:     usable,
		LastPromptTokens: prompt,
		LastOutputTokens: int64(usage.OutputTokens),
		EstimateTokens:   estimate,
		CacheReadTokens:  int64(usage.CacheReadTokens),
		CacheWriteTokens: int64(usage.CacheWriteTokens),
		Source:           contextpack.SourceProviderUsage,
		EstimateSource:   contextpack.SourceLocalEstimate,
		Ratio:            ratio,
		Calibrated:       calibrated,
		Compacted:        compacted,
		HistoryMessages:  historyMsgs,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := r.contextStore.Upsert(ctx, snap); err != nil {
		slog.Warn("context snapshot upsert failed", "component", "agent", "operation", "context_snapshot",
			"result", "warning", "task_id", request.TaskID, "error", err)
	}
}

const (
	maxProviderAttempts  = 3
	maxProviderRetryWait = 5 * time.Second
)

func (r *Runner) collectProviderResponse(ctx context.Context, provider StreamingProvider, req providerapi.CompletionRequest, runReq RunRequest) (providerapi.CompletionResponse, int, error) {
	for attempt := 1; attempt <= maxProviderAttempts; attempt++ {
		response, err := r.collectOnce(ctx, provider, req, runReq)
		if err == nil {
			return response, attempt, nil
		}
		delay, retry := providerRetryDelay(err, attempt)
		if !retry {
			return response, attempt, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return providerapi.CompletionResponse{}, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return providerapi.CompletionResponse{}, maxProviderAttempts, errors.New("provider retry loop exhausted")
}

func (r *Runner) collectOnce(ctx context.Context, provider StreamingProvider, req providerapi.CompletionRequest, runReq RunRequest) (providerapi.CompletionResponse, error) {
	if r.stream == nil {
		return providerapi.CollectStream(ctx, provider, req)
	}
	var accumulator providerapi.StreamAccumulator
	side := func(event providerapi.StreamEvent) error {
		r.stream.Publish(runReq.SessionID, runReq.TaskID, runReq.RunID, runReq.ParentRunID, event)
		return nil
	}
	handler := providerapi.TeeStreamHandler(accumulator.Add, side)
	if err := provider.Stream(ctx, req, handler); err != nil {
		return providerapi.CompletionResponse{}, err
	}
	return accumulator.Response()
}

func providerRetryDelay(err error, attempt int) (time.Duration, bool) {
	var providerErr *providerapi.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Retryable || attempt >= maxProviderAttempts {
		return 0, false
	}
	delay := providerErr.RetryAfter
	if delay <= 0 {
		delay = 100 * time.Millisecond * time.Duration(1<<(attempt-1))
	}
	if delay > maxProviderRetryWait {
		delay = maxProviderRetryWait
	}
	return delay, true
}
