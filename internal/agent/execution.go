package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"io"
	"sync"
	"time"
)

func (e *Engine) stream(ctx context.Context, b binding, request providerapi.CompletionRequest, options RunOptions) (providerapi.CompletionResponse, providerapi.CompletionResponse, error) {
	for attempt := 0; ; attempt++ {
		var acc providerapi.StreamAccumulator
		emitted := false
		err := b.provider.Stream(ctx, request, func(event providerapi.StreamEvent) error {
			if err := acc.Add(event); err != nil {
				return err
			}
			emitted = true
			switch event.Type {
			case providerapi.StreamDelta:
				options.emit(Event{Type: "delta", Text: event.ContentDelta})
			case providerapi.StreamThinking:
				options.emit(Event{Type: "thinking", Text: event.ThinkingDelta})
			case providerapi.StreamComplete:
				options.emit(Event{Type: "usage", Usage: event.Usage})
			}
			return ctx.Err()
		})
		if err == nil {
			result, err := acc.Response()
			return result, acc.Partial(), err
		}
		var remote *providerapi.ProviderError
		retryable := errors.As(err, &remote) && remote.Retryable || errors.Is(err, io.ErrUnexpectedEOF)
		if emitted || attempt >= 2 || ctx.Err() != nil || !retryable {
			return providerapi.CompletionResponse{}, acc.Partial(), err
		}
		delay := time.Duration(1<<attempt) * time.Second
		if remote != nil && remote.RetryAfter > delay {
			delay = remote.RetryAfter
		}
		if delay > 30*time.Second {
			return providerapi.CompletionResponse{}, acc.Partial(), err
		}
		options.emit(Event{Type: "info", Text: "Temporary provider failure; retrying before any output was received"})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return providerapi.CompletionResponse{}, acc.Partial(), ctx.Err()
		case <-timer.C:
		}
	}
}
func independent(call providerapi.ToolCall) bool {
	if call.Name != "task" {
		return false
	}
	var args struct {
		Independent bool `json:"independent"`
	}
	return json.Unmarshal([]byte(call.Arguments), &args) == nil && args.Independent
}
func (e *Engine) executeTools(ctx context.Context, id string, registry *tools.Registry, env tools.Env, calls []providerapi.ToolCall, options RunOptions) error {
	results := make([]tools.Result, len(calls))
	run := func(index int) {
		call := calls[index]
		options.emit(Event{Type: "tool_start", Tool: call.Name, Text: call.Arguments})
		callEnv := env
		callEnv.Progress = func(text string) { options.emit(Event{Type: "tool_progress", Tool: call.Name, Text: text}) }
		result, err := registry.Execute(ctx, callEnv, call.Name, json.RawMessage(call.Arguments))
		if err != nil {
			raw, _ := json.Marshal(map[string]string{"error": err.Error()})
			result = tools.Result{Text: string(raw)}
		}
		results[index] = result
		if result.Usage != nil {
			options.emit(Event{Type: "usage", Purpose: call.Name, Usage: result.Usage})
		}
		options.emit(Event{Type: "tool_end", Tool: call.Name, Text: result.Text})
	}
	for i := 0; i < len(calls); {
		if !independent(calls[i]) {
			run(i)
			i++
			continue
		}
		end := i
		for end < len(calls) && independent(calls[end]) {
			end++
		}
		jobs := make(chan int)
		var group sync.WaitGroup
		for worker := 0; worker < min(2, end-i); worker++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for index := range jobs {
					run(index)
				}
			}()
		}
		for index := i; index < end; index++ {
			jobs <- index
		}
		close(jobs)
		group.Wait()
		i = end
	}
	// Preserve provider tool-call order even when children finish out of order.
	var saveErrors []error
	for index, call := range calls {
		result := results[index]
		save, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		_, err := e.Store.Append(save, id, providerapi.Message{Role: providerapi.RoleTool, ToolCallID: call.ID, Content: result.Text, Images: result.Images, UserText: result.UserText}, providerapi.Usage{})
		done()
		if err != nil {
			saveErrors = append(saveErrors, err)
		}
	}
	return errors.Join(saveErrors...)
}
