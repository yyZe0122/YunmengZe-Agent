package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (e *Engine) initMemory(ctx context.Context) error {
	if !e.Config.MemoryEnabled() {
		return nil
	}
	store, err := memory.NewStore(e.db.SQL())
	if err != nil {
		return err
	}
	e.memory, err = memory.New(memory.Config{Store: store, MaxInjectRunes: e.Config.MemoryMaxInjectRunes(), DefaultTTL: e.Config.MemoryDefaultTTL()})
	if err != nil {
		return err
	}
	return e.memory.Initialize(ctx)
}

func (e *Engine) registerMemory(r *tools.Registry) {
	if e.memory != nil {
		r.Register(tools.Tool{Name: "memory_search", ReadOnly: true, Description: "Search saved preferences and explicitly inspect older memory records. Older facts may be stale; do not treat them as current project state.", Schema: tools.Schema(`{"query":{"type":"string"}}`, "query"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
			var a struct {
				Query string `json:"query"`
			}
			if err := tools.Decode(raw, &a); err != nil {
				return nil, err
			}
			return e.memory.Search(ctx, env.SessionID, a.Query, 20)
		}})
	}
}

func (e *Engine) curateMemory(ctx context.Context, s session.Session, options RunOptions) {
	if e.memory != nil && s.Mode == session.Agent && s.ParentID == "" {
		if ce := e.memory.Curate(ctx, memory.CuratorConfig{Evidence: memoryEvidenceReader{store: e.store}, Enabled: e.Config.MemoryCuratorEnabled(), MaxFacts: e.Config.MemoryCuratorMaxFacts(), TimeoutMS: e.Config.MemoryCuratorTimeoutMS(), MinTurns: e.Config.MemoryCuratorMinTurns(), MinInterval: e.Config.MemoryCuratorMinInterval(), Caller: curatorCaller{e: e, model: s.Model, options: options}}); ce != nil {
			options.emit(Event{Type: "info", Text: "Preference curation skipped: " + ce.Error()})
		}
		e.memory.Maintain(ctx)
	}
}

func (e *Engine) memoryBlock(ctx context.Context, sessionID string) string {
	if e.memory == nil {
		return ""
	}
	return "\n" + e.memory.FrozenSystemBlock(ctx, sessionID)
}

type MemoryEvidence struct {
	MessageID int64
	Quote     string
}

type MemoryEntry struct {
	ID, Kind, Content string
	Evidence          []MemoryEvidence
}

func (e *Engine) MemoryEntries(ctx context.Context, sessionID string) ([]MemoryEntry, error) {
	if e.memory == nil {
		return nil, errors.New("memory is disabled")
	}
	entries, err := e.memory.List(ctx, sessionID, true, 30)
	if err != nil {
		return nil, err
	}
	result := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		item := MemoryEntry{ID: entry.ID, Kind: entry.Kind, Content: entry.Content}
		for _, evidence := range entry.Evidence {
			item.Evidence = append(item.Evidence, MemoryEvidence{MessageID: evidence.MessageID, Quote: evidence.Quote})
		}
		result = append(result, item)
	}
	return result, nil
}

func (e *Engine) ForgetMemory(ctx context.Context, mode session.Mode, id string) error {
	if e.memory == nil {
		return errors.New("memory is disabled")
	}
	if mode == session.Plan {
		return errors.New("plan mode cannot modify memory")
	}
	return e.memory.Forget(ctx, id)
}

func (e *Engine) CorrectMemory(ctx context.Context, mode session.Mode, id, content string) error {
	if e.memory == nil {
		return errors.New("memory is disabled")
	}
	if mode == session.Plan {
		return errors.New("plan mode cannot modify memory")
	}
	return e.memory.Correct(ctx, id, content)
}

func (e *Engine) RefreshMemory(sessionID string) {
	if e.memory != nil {
		e.memory.InvalidateSnapshot(sessionID)
	}
}

// Session owns transcript SQL; memory consumes only the evidence it needs.
type memoryEvidenceReader struct{ store *session.Store }

func (r memoryEvidenceReader) ReadUserEvidence(ctx context.Context, through int64) ([]memory.UserSample, []memory.UserSample, error) {
	previous, current, err := r.store.UserEvidence(ctx, through)
	if err != nil {
		return nil, nil, err
	}
	convert := func(source []session.UserEvidence) []memory.UserSample {
		result := make([]memory.UserSample, 0, len(source))
		for _, sample := range source {
			result = append(result, memory.UserSample{ID: sample.ID, Session: sample.Session, Text: sample.Text, CreatedAt: sample.CreatedAt})
		}
		return result
	}
	return convert(previous), convert(current), nil
}

type curatorCaller struct {
	e       *Engine
	model   string
	options RunOptions
}

func (c curatorCaller) ProposeMemoryFacts(ctx context.Context, user, assistant string, maxFacts int) (string, error) {
	b, err := c.e.resolve(c.e.role(providerconfig.RoleMemory, c.model))
	if err != nil {
		return "", err
	}
	prompt := memory.BuildCuratorUserPrompt(user, assistant, maxFacts)
	if contextpack.EstimateText(prompt)+contextpack.EstimateText(memory.CuratorSystemPrompt) > contextpack.UsableWindow(b.config.ContextWindow, 1024, 0) {
		return "", errors.New("preference batch exceeds memory model context; configure a larger-context memory model")
	}
	r, err := b.provider.Complete(ctx, providerapi.CompletionRequest{Model: b.config.ModelID, Messages: []providerapi.Message{{Role: providerapi.RoleSystem, Content: memory.CuratorSystemPrompt}, {Role: providerapi.RoleUser, Content: prompt}}, MaxOutputTokens: 1024})
	if err == nil {
		c.options.emit(Event{Type: "usage", Purpose: "memory", Usage: &r.Usage})
	}
	return r.Content, err
}
