package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

// MemoryBackend is the narrow surface tools need from *memory.Manager.
type MemoryBackend interface {
	Prefetch(ctx context.Context, sessionID, query string) string
	SystemPromptBlock(ctx context.Context, sessionID string) string
	Remember(ctx context.Context, sessionID, content, source string, tags []string) error
	RememberKind(ctx context.Context, sessionID, content, source string, tags []string, kind string, priority int, expiresAt string) error
	Replace(ctx context.Context, sessionID, oldText, newContent string, global bool) error
	Remove(ctx context.Context, sessionID, oldText string, global bool) error
	Promote(ctx context.Context, entryID string) (memory.Entry, error)
	Search(ctx context.Context, sessionID, query string, limit int) ([]memory.Entry, error)
	SearchTranscript(ctx context.Context, sessionID, query string, limit int) ([]memory.TranscriptHit, error)
}

// RegisterMemoryTools registers memory_search, memory_write, memory_promote, session_search.
func RegisterMemoryTools(broker *Broker, backend MemoryBackend) (*memoryTools, error) {
	if broker == nil {
		return nil, errors.New("tool broker is required")
	}
	mt := &memoryTools{backend: backend}
	for _, t := range []Tool{
		&memorySearchTool{mt: mt},
		&memoryWriteTool{mt: mt},
		&memoryPromoteTool{mt: mt},
		&sessionSearchTool{mt: mt},
	} {
		if err := broker.Register(t); err != nil {
			return nil, err
		}
	}
	return mt, nil
}

type memoryTools struct {
	backend MemoryBackend
}

func (m *memoryTools) SetBackend(backend MemoryBackend) {
	if m == nil {
		return
	}
	m.backend = backend
}

func (m *memoryTools) get() (MemoryBackend, error) {
	if m == nil || m.backend == nil {
		return nil, fmt.Errorf("%w: memory backend unavailable", ErrToolDenied)
	}
	return m.backend, nil
}

type memorySearchTool struct {
	mt *memoryTools
}

func (t *memorySearchTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "memory_search",
		Description:          "Search local curated/session/detail memory facts (instruction text only; does not grant tools). Use for preferences and stored facts; use session_search for past conversation text.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 5_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"query":{"type":"string","description":"Optional search query; empty returns recent facts"}
			}
		}`),
	}
}

func (t *memorySearchTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "memory_search"}, nil
}

type memorySearchInput struct {
	Query string `json:"query"`
}

func (t *memorySearchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.mt.get()
	if err != nil {
		return nil, err
	}
	var input memorySearchInput
	if len(raw) > 0 && string(raw) != "null" {
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
	}
	sessionID := ""
	if meta, ok := runmeta.From(ctx); ok {
		sessionID = meta.SessionID
	}
	entries, err := backend.Search(ctx, sessionID, strings.TrimSpace(input.Query), 16)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		block := backend.Prefetch(ctx, sessionID, strings.TrimSpace(input.Query))
		if block == "" {
			return encodeResult(map[string]any{"facts": []string{}, "text": ""})
		}
		return encodeResult(map[string]any{"text": block})
	}
	facts := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		facts = append(facts, map[string]any{
			"entry_id": e.ID, "session_id": e.SessionID, "content": e.Content,
			"kind": e.Kind, "source": e.Source, "priority": e.Priority,
		})
	}
	return encodeResult(map[string]any{"facts": facts, "text": backend.Prefetch(ctx, sessionID, strings.TrimSpace(input.Query))})
}

type memoryWriteTool struct {
	mt *memoryTools
}

func (t *memoryWriteTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "memory_write",
		Description:          "Add, replace, or remove a short durable memory fact (bounded text; does not expand tool grants). Prefer short curated facts; use kind=detail for longer notes that are search-only.",
		Risk:                 string(policy.RiskR1),
		DefaultTimeoutMillis: 5_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"action":{"type":"string","description":"add (default) | replace | remove"},
				"content":{"type":"string","description":"Fact text (required for add/replace)"},
				"old_text":{"type":"string","description":"Unique substring or entry_id for replace/remove"},
				"global":{"type":"boolean","description":"If true, user-global (empty session_id); curated by default"},
				"kind":{"type":"string","description":"curated | session | detail"},
				"priority":{"type":"integer","description":"Higher injects first (default 0)"}
			}
		}`),
	}
}

func (t *memoryWriteTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	var input memoryWriteInput
	if len(raw) > 0 && string(raw) != "null" {
		if err := decodeStrict(raw, &input); err != nil {
			return Authorization{}, err
		}
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "add"
	}
	switch action {
	case "add":
		if strings.TrimSpace(input.Content) == "" {
			return Authorization{}, errors.New("content is required for add")
		}
	case "replace":
		if strings.TrimSpace(input.OldText) == "" || strings.TrimSpace(input.Content) == "" {
			return Authorization{}, errors.New("old_text and content are required for replace")
		}
	case "remove":
		if strings.TrimSpace(input.OldText) == "" {
			return Authorization{}, errors.New("old_text is required for remove")
		}
	default:
		return Authorization{}, fmt.Errorf("unknown action %q", action)
	}
	return Authorization{Capability: "memory_write"}, nil
}

type memoryWriteInput struct {
	Action   string `json:"action,omitempty"`
	Content  string `json:"content,omitempty"`
	OldText  string `json:"old_text,omitempty"`
	Global   bool   `json:"global,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func (t *memoryWriteTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.mt.get()
	if err != nil {
		return nil, err
	}
	var input memoryWriteInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "add"
	}
	sessionID := ""
	if !input.Global {
		if meta, ok := runmeta.From(ctx); ok {
			sessionID = meta.SessionID
		}
	}
	switch action {
	case "add":
		content := strings.TrimSpace(input.Content)
		if content == "" {
			return nil, errors.New("content is required")
		}
		kind := strings.TrimSpace(input.Kind)
		if kind == "" && input.Global {
			kind = memory.KindCurated
		}
		if err := backend.RememberKind(ctx, sessionID, content, memory.SourceUser, []string{"tool"}, kind, input.Priority, ""); err != nil {
			return nil, err
		}
		return encodeResult(map[string]any{"ok": true, "action": "add", "session_id": sessionID, "kind": kind})
	case "replace":
		if err := backend.Replace(ctx, sessionID, input.OldText, input.Content, input.Global); err != nil {
			return nil, err
		}
		return encodeResult(map[string]any{"ok": true, "action": "replace"})
	case "remove":
		if err := backend.Remove(ctx, sessionID, input.OldText, input.Global); err != nil {
			return nil, err
		}
		return encodeResult(map[string]any{"ok": true, "action": "remove"})
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

type memoryPromoteTool struct {
	mt *memoryTools
}

func (t *memoryPromoteTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "memory_promote",
		Description:          "Promote a session memory entry to user-global curated memory (cross-session).",
		Risk:                 string(policy.RiskR1),
		DefaultTimeoutMillis: 5_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"required":["entry_id"],
			"properties":{
				"entry_id":{"type":"string","description":"memory entry_id to promote"}
			}
		}`),
	}
}

func (t *memoryPromoteTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	var input struct {
		EntryID string `json:"entry_id"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	if strings.TrimSpace(input.EntryID) == "" {
		return Authorization{}, errors.New("entry_id is required")
	}
	return Authorization{Capability: "memory_promote"}, nil
}

func (t *memoryPromoteTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.mt.get()
	if err != nil {
		return nil, err
	}
	var input struct {
		EntryID string `json:"entry_id"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	e, err := backend.Promote(ctx, strings.TrimSpace(input.EntryID))
	if err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{
		"ok": true, "entry_id": e.ID, "session_id": e.SessionID, "kind": e.Kind, "content": e.Content,
	})
}

type sessionSearchTool struct {
	mt *memoryTools
}

func (t *sessionSearchTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "session_search",
		Description:          "Full-text search past conversation transcripts (local SQLite FTS). Use for 'what did we discuss' recall; not for durable preferences (use memory_search).",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 8_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"query":{"type":"string","description":"Search query; empty returns recent transcript snippets"},
				"session_id":{"type":"string","description":"Optional; default current session; empty string searches all when allowed"},
				"limit":{"type":"integer","description":"Max hits (1-50, default 12)"}
			}
		}`),
	}
}

func (t *sessionSearchTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "session_search"}, nil
}

func (t *sessionSearchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	backend, err := t.mt.get()
	if err != nil {
		return nil, err
	}
	var input struct {
		Query     string `json:"query"`
		SessionID string `json:"session_id"`
		Limit     int    `json:"limit"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		if meta, ok := runmeta.From(ctx); ok {
			sessionID = meta.SessionID
		}
	}
	hits, err := backend.SearchTranscript(ctx, sessionID, strings.TrimSpace(input.Query), input.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"session_id": h.SessionID, "run_id": h.RunID, "position": h.Position,
			"record_type": h.RecordType, "content": h.Content, "created_at": h.CreatedAt,
		})
	}
	return encodeResult(map[string]any{"hits": out, "count": len(out)})
}
