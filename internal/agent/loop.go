package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/contextpack"
	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (e *Engine) Run(ctx context.Context, id, input string, options RunOptions) (string, error) {
	emit := options.Emit
	var emitMu sync.Mutex
	if emit != nil {
		options.Emit = func(event Event) { emitMu.Lock(); defer emitMu.Unlock(); emit(event) }
	}
	if strings.TrimSpace(input) == "" {
		return "", errors.New("message is required")
	}
	ctx, finish, err := e.beginRun(ctx)
	if err != nil {
		return "", err
	}
	defer finish()
	token, err := e.Store.Acquire(ctx, id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	leaseDone := make(chan struct{})
	defer func() {
		cancel()
		<-leaseDone
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = e.Store.Release(cleanup, id, token)
	}()
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.Store.Renew(ctx, id, token) != nil {
					cancel()
					return
				}
			}
		}
	}()
	s, err := e.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	b, err := e.resolve(s.Model)
	if err != nil {
		return "", err
	}
	catalog := e.Skills(s.Workspace)
	registry, err := e.registry(s, options, catalog)
	if err != nil {
		return "", err
	}
	userText := input
	input, err = e.Expand(s.Workspace, input)
	if err != nil {
		return "", err
	}
	var original *string
	if s.ParentID == "" && s.Mode == session.Agent && !strings.HasPrefix(userText, "/") {
		original = &userText
	}
	if _, err = e.Store.Append(ctx, id, providerapi.Message{Role: providerapi.RoleUser, Content: input, UserText: original}, providerapi.Usage{}); err != nil {
		return "", err
	}
	env := tools.Env{Workspace: s.Workspace, SessionID: id, ReadOnly: s.Mode == session.Plan, Child: s.ParentID != ""}
	defs := registry.Definitions(env)
	if e.Memory != nil {
		e.Memory.InvalidateSnapshot(s.ID)
	}
	prefix := e.prefix(ctx, s, catalog)
	var records []session.Record
	after := s.SummaryThrough
	last := ""
	overflowRetried := false
	for step := 0; ; step++ {
		if err = ctx.Err(); err != nil {
			return last, err
		}
		if e.Config.MaxIterations > 0 && step >= e.Config.MaxIterations {
			return last, fmt.Errorf("turn reached max_iterations=%d; send another message to continue", e.Config.MaxIterations)
		}
		for {
			select {
			case text, ok := <-options.Inbox:
				if !ok {
					options.Inbox = nil
					continue
				}
				if strings.TrimSpace(text) != "" {
					rawText := text
					text, err = e.Expand(s.Workspace, text)
					if err != nil {
						return last, err
					}
					var original *string
					if s.ParentID == "" && s.Mode == session.Agent && !strings.HasPrefix(rawText, "/") {
						original = &rawText
					}
					if _, err = e.Store.Append(ctx, id, providerapi.Message{Role: providerapi.RoleUser, Content: text, UserText: original}, providerapi.Usage{}); err != nil {
						return last, err
					}
					options.emit(Event{Type: "steer", Text: text})
				}
			default:
				goto collected
			}
		}
	collected:
		next, err := e.Store.Records(ctx, id, after)
		if err != nil {
			return last, err
		}
		records = append(records, next...)
		if len(next) > 0 {
			after = next[len(next)-1].ID
		}
		for len(records) > 0 && records[0].ID <= s.SummaryThrough {
			records = records[1:]
		}
		messages := e.messages(s, records, prefix, b.config)
		budget := contextpack.UsableWindow(b.config.ContextWindow, b.config.MaxTokens, 0) - contextpack.EstimateTools(defs)
		if budget <= 0 {
			return last, errors.New("tool definitions exceed the model context window")
		}
		if e.Config.CompactionEnabled() && contextpack.EstimateMessages(messages) > budget*3/4 {
			if changed, compactErr := e.compact(ctx, &s, records, "", options); compactErr != nil {
				options.emit(Event{Type: "info", Text: "Compaction failed: " + compactErr.Error()})
			} else if changed {
				for len(records) > 0 && records[0].ID <= s.SummaryThrough {
					records = records[1:]
				}
				messages = e.messages(s, records, prefix, b.config)
				options.emit(Event{Type: "info", Text: "Context compacted"})
			}
		}
		packed := contextpack.Pack(messages, contextpack.PackOptions{Budget: budget})
		if packed.OverBudget {
			return last, errors.New("current turn exceeds the model context window; use a model with a larger window")
		}
		response, partial, streamErr := e.stream(ctx, b, providerapi.CompletionRequest{Model: b.config.ModelID, Messages: packed.Messages, Tools: defs}, options)
		if streamErr != nil {
			if partial.Content == "" && partial.Thinking == "" && !overflowRetried && e.Config.CompactionEnabled() && providerapi.IsContextOverflow(streamErr) {
				overflowRetried = true
				if changed, ce := e.compact(ctx, &s, records, "", options); ce == nil && changed {
					continue
				}
			}
			if partial.Content != "" || partial.Thinking != "" {
				save, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				_, saveErr := e.Store.Append(save, id, providerapi.Message{Role: providerapi.RoleAssistant, Content: partial.Content, Thinking: partial.Thinking}, providerapi.Usage{})
				done()
				if saveErr != nil {
					return partial.Content, errors.Join(streamErr, saveErr)
				}
			}
			return partial.Content, streamErr
		}
		seen := map[string]bool{}
		for _, call := range response.ToolCalls {
			if seen[call.ID] {
				return last, errors.New("provider returned duplicate tool call IDs")
			}
			seen[call.ID] = true
		}
		assistant := providerapi.Message{Role: providerapi.RoleAssistant, Content: response.Content, Thinking: response.Thinking, ToolCalls: response.ToolCalls, Replay: response.Replay}
		if _, err = e.Store.Append(ctx, id, assistant, response.Usage); err != nil {
			return last, err
		}
		last = response.Content
		options.emit(Event{Type: "assistant_end"})
		if err = e.executeTools(ctx, id, registry, env, response.ToolCalls, options); err != nil {
			return last, err
		}
		if ctx.Err() != nil {
			return last, ctx.Err()
		}
		if len(response.ToolCalls) == 0 {
			if response.FinishReason == "length" || response.FinishReason == "max_tokens" || response.FinishReason == "MAX_TOKENS" {
				return last, errors.New("model reached its output limit; increase maxTokens or send a message to continue")
			}
			if options.Inbox != nil && len(options.Inbox) > 0 {
				continue
			}
			if e.Memory != nil && s.Mode == session.Agent && s.ParentID == "" {
				if ce := e.Memory.Curate(ctx, memory.CuratorConfig{Enabled: e.Config.MemoryCuratorEnabled(), MaxFacts: e.Config.MemoryCuratorMaxFacts(), TimeoutMS: e.Config.MemoryCuratorTimeoutMS(), MinTurns: e.Config.MemoryCuratorMinTurns(), MinInterval: e.Config.MemoryCuratorMinInterval(), Caller: curatorCaller{e: e, model: s.Model, options: options}}); ce != nil {
					options.emit(Event{Type: "info", Text: "Preference curation skipped: " + ce.Error()})
				}
				e.Memory.Maintain(ctx)
			}
			return last, nil
		}
	}
}

func (e *Engine) prefix(ctx context.Context, s session.Session, catalog *skillcatalog.Catalog) string {
	prefix := fmt.Sprintf("YunmengZe %s is a local coding agent. Complete the user's task using the available tools. Tool errors are observations; use them to correct your next step.\nMode: %s. Workspace: %s. Model: %s.\n", version.Version, s.Mode, s.Workspace, s.Model)
	if s.Mode == session.Plan {
		prefix += "Plan mode is read-only. Inspect and explain; do not try to bypass unavailable tools.\n"
	} else {
		prefix += "Agent tools execute directly without approval prompts. Work within the user's request.\n"
	}
	prefix += providerconfig.OverlayAgents(e.ConfigDir, s.Workspace)
	skills := catalog.Skills()
	if len(skills) > 0 {
		prefix += "\nAvailable skills (load with skill_view):\n"
		for _, sk := range skills {
			prefix += sk.ID + ": " + sk.Description + "\n"
		}
	}
	if e.Memory != nil {
		prefix += "\n" + e.Memory.FrozenSystemBlock(ctx, s.ID)
	}
	prefix += "\nUse task only when substantial independent work benefits from delegation. Set independent only for tasks with no dependencies and no overlapping files; never delegate routine small edits. The parent integrates results.\n"
	prefix += "\nUTC date: " + time.Now().UTC().Format("2006-01-02")
	return prefix
}
func (e *Engine) messages(s session.Session, records []session.Record, prefix string, model providerconfig.Resolved) []providerapi.Message {
	out := []providerapi.Message{{Role: providerapi.RoleSystem, Content: prefix}}
	native := s.SummaryReplay != nil && s.SummaryReplay.Provider == model.ProviderID+"/openai-responses" && s.SummaryReplay.Model == model.ModelID && model.Protocol == providerconfig.ProtocolOpenAIResponses
	if native {
		out = append(out, providerapi.Message{Role: providerapi.RoleAssistant, Replay: s.SummaryReplay})
	}
	if !native && s.Summary != "" {
		out = append(out, providerapi.Message{Role: providerapi.RoleSystem, Content: "Earlier conversation summary:\n" + s.Summary})
	}
	if !native && len(records) > 0 && records[0].Message.Role != providerapi.RoleUser {
		out = append(out, providerapi.Message{Role: providerapi.RoleUser, Content: "Continue the current task described in the summary, observing all user constraints."})
	}
	for _, r := range records {
		if r.Message.Role == providerapi.RoleAssistant && r.Message.Content == "" && len(r.Message.ToolCalls) == 0 && r.Message.Replay == nil {
			// Interrupted thinking remains in storage/UI but is not a valid wire message.
			continue
		}
		out = append(out, r.Message)
	}
	return out
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
