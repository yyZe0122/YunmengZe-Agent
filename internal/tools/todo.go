package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/internal/sessiontodo"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

// TodoBackend is the narrow surface for session todos (QE).
type TodoBackend interface {
	List(ctx context.Context, sessionID string) ([]sessiontodo.Item, error)
	Replace(ctx context.Context, sessionID string, items []sessiontodo.Item) error
}

// RegisterTodoTools registers todo_list / todo_write (R0; plan allowed).
func RegisterTodoTools(broker *Broker, backend TodoBackend) (*todoTools, error) {
	if broker == nil {
		return nil, errors.New("tool broker is required")
	}
	tt := &todoTools{backend: backend}
	for _, tool := range []Tool{
		&todoListTool{tt: tt},
		&todoWriteTool{tt: tt},
	} {
		if err := broker.Register(tool); err != nil {
			return nil, err
		}
	}
	return tt, nil
}

type todoTools struct {
	backend TodoBackend
}

func (t *todoTools) SetBackend(backend TodoBackend) {
	if t != nil {
		t.backend = backend
	}
}

func (t *todoTools) get() (TodoBackend, error) {
	if t == nil || t.backend == nil {
		return nil, fmt.Errorf("%w: todo backend unavailable", ErrToolDenied)
	}
	return t.backend, nil
}

func sessionFrom(ctx context.Context) string {
	if meta, ok := runmeta.From(ctx); ok {
		return strings.TrimSpace(meta.SessionID)
	}
	return ""
}

type todoListTool struct {
	tt *todoTools
}

func (t *todoListTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "todo_list",
		Description:          "List this session's working todos (not kernel tasks). Use to track multi-step work in the current chat.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 5_000,
		InputSchema:          json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`),
	}
}

func (t *todoListTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "todo_list"}, nil
}

func (t *todoListTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.tt.get()
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		var empty struct{}
		if err := decodeStrict(raw, &empty); err != nil {
			return nil, err
		}
	}
	sessionID := sessionFrom(ctx)
	items, err := backend.List(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return encodeTodos(items)
}

type todoWriteTool struct {
	tt *todoTools
}

type todoWriteInput struct {
	Items []todoWriteItem `json:"items"`
}

type todoWriteItem struct {
	ID       string `json:"id,omitempty"`
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`
	Position int    `json:"position,omitempty"`
}

func (t *todoWriteTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "todo_write",
		Description:          "Replace this session's working todos for multi-step work. Keep at most one in_progress. Does not expand write/exec grants. Status: pending | in_progress | completed | cancelled.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 5_000,
		InputSchema: json.RawMessage(`{
			"type":"object","additionalProperties":false,"required":["items"],
			"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["content"],
				"properties":{"id":{"type":"string"},"content":{"type":"string"},"status":{"type":"string"},"position":{"type":"integer"}}}}}
		}`),
	}
}

func (t *todoWriteTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "todo_write"}, nil
}

func (t *todoWriteTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.tt.get()
	if err != nil {
		return nil, err
	}
	var input todoWriteInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	sessionID := sessionFrom(ctx)
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	items := make([]sessiontodo.Item, 0, len(input.Items))
	for i, it := range input.Items {
		items = append(items, sessiontodo.Item{
			ID: it.ID, Content: it.Content, Status: it.Status, Position: it.Position,
		})
		if items[len(items)-1].Position == 0 {
			items[len(items)-1].Position = i
		}
	}
	if err := backend.Replace(ctx, sessionID, items); err != nil {
		return nil, err
	}
	out, err := backend.List(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return encodeTodos(out)
}

func encodeTodos(items []sessiontodo.Item) (json.RawMessage, error) {
	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		rows = append(rows, map[string]any{
			"id": it.ID, "content": it.Content, "status": it.Status, "position": it.Position,
		})
	}
	return encodeResult(map[string]any{"todos": rows, "count": len(rows)})
}
