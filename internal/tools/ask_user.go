package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/internal/userquestion"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

type AskUserBackend interface {
	CreatePending(ctx context.Context, req userquestion.Request) (userquestion.Request, error)
	Waiter() *userquestion.Waiter
	MarkUnavailable(ctx context.Context, id, reason string) (userquestion.Request, error)
}

func RegisterAskUserTool(broker *Broker, backend AskUserBackend) (*askUserTools, error) {
	if broker == nil {
		return nil, errors.New("tool broker is required")
	}
	at := &askUserTools{backend: backend}
	if err := broker.Register(&askUserTool{at: at}); err != nil {
		return nil, err
	}
	return at, nil
}

type askUserTools struct {
	backend AskUserBackend
}

func (t *askUserTools) SetBackend(backend AskUserBackend) {
	if t != nil {
		t.backend = backend
	}
}

type askUserTool struct {
	at *askUserTools
}

func (t *askUserTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "ask_user",
		Description: "Ask the user one or more questions and wait for answers. " +
			"Use when a decision is required (approach, tradeoff, missing preference). " +
			"Each item may include options; the TUI always adds a Type your own answer choice — do not add Other/catch-all. " +
			"Set multi_select when more than one option may apply. Interactive TUI answers via a question card; CLI/cron return unavailable.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 30 * 60 * 1000,
		InputSchema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["questions"],"properties":{"questions":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"object","additionalProperties":false,"required":["id","question"],"properties":{"id":{"type":"string"},"question":{"type":"string"},"header":{"type":"string"},"multi_select":{"type":"boolean"},"options":{"type":"array","maxItems":12,"items":{"type":"object","additionalProperties":false,"required":["label"],"properties":{"label":{"type":"string"},"description":{"type":"string"}}}}}}}}}`),
	}
}

func (t *askUserTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "ask_user"}, nil
}

type askUserInput struct {
	Questions []userquestion.Item `json:"questions"`
}

func (t *askUserTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t == nil || t.at == nil || t.at.backend == nil {
		return encodeResult(map[string]any{
			"error": "unavailable", "hint": "ask_user is not configured.",
		})
	}
	var input askUserInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	meta, _ := runmeta.From(ctx)
	if !meta.Interactive {
		return encodeResult(map[string]any{
			"error":   "unavailable",
			"hint":    "No interactive user is available (CLI/cron). Continue with a reasonable default or ask in the next user turn.",
			"answers": map[string]any{},
		})
	}
	req, err := t.at.backend.CreatePending(ctx, userquestion.Request{
		SessionID:  meta.SessionID,
		TaskID:     meta.TaskID,
		RunID:      meta.RunID,
		ToolCallID: meta.CallID,
		Questions:  input.Questions,
	})
	if err != nil {
		return encodeResult(map[string]any{
			"error": "invalid_question", "message": err.Error(),
			"hint": "Fix question ids/options and retry.",
		})
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		waitCtx, cancel = context.WithTimeout(ctx, userquestion.WaitTimeout)
		defer cancel()
	}
	dec, err := t.at.backend.Waiter().Wait(waitCtx, req.ID)
	if err != nil {
		_, _ = t.at.backend.MarkUnavailable(context.WithoutCancel(ctx), req.ID, err.Error())
		return encodeResult(map[string]any{
			"error":       "unavailable",
			"question_id": req.ID,
			"message":     err.Error(),
			"hint":        "The user did not answer in time. Continue with a reasonable default.",
		})
	}
	if dec.State != userquestion.StateAnswered {
		return encodeResult(map[string]any{
			"error":       "unavailable",
			"question_id": req.ID,
			"state":       dec.State,
			"hint":        "The user did not answer. Continue with a reasonable default.",
		})
	}
	return encodeResult(map[string]any{
		"question_id": req.ID,
		"answers":     dec.Answers,
	})
}
