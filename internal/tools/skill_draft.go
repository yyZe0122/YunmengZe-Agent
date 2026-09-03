package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/skillmaintain"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

// SkillDraftBackend writes drafts and records events (catalog + maintain).
type SkillDraftBackend interface {
	WriteDraft(id, body string) (skillcatalog.Skill, string, error)
	DiscardDraft(id string) (skillcatalog.Skill, error)
	RecordDraft(ctx context.Context, skillID, actor, path, hash string, discarded bool) error
}

// RegisterSkillDraftTool registers skill_draft (agent-only via chatsession grants).
func RegisterSkillDraftTool(broker *Broker, backend SkillDraftBackend) error {
	if broker == nil {
		return errors.New("tool broker is required")
	}
	return broker.Register(&skillDraftTool{backend: backend})
}

type skillDraftTool struct {
	backend SkillDraftBackend
}

func (t *skillDraftTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "skill_draft",
		Description:          "Propose or discard a draft rewrite of an existing local skill (SKILL.md.draft). Instruction text only — does not expand tool grants. User must apply the draft.",
		Risk:                 string(policy.RiskR1),
		DefaultTimeoutMillis: 8_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"required":["action","skill_id"],
			"properties":{
				"action":{"type":"string","description":"propose | discard"},
				"skill_id":{"type":"string","description":"Existing skill directory id"},
				"body":{"type":"string","description":"Full SKILL.md document including name/description frontmatter (propose)"}
			}
		}`),
	}
}

func (t *skillDraftTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	var input skillDraftInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	if strings.TrimSpace(input.SkillID) == "" {
		return Authorization{}, errors.New("skill_id is required")
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action != "propose" && action != "discard" {
		return Authorization{}, fmt.Errorf("action must be propose or discard")
	}
	if action == "propose" && strings.TrimSpace(input.Body) == "" {
		return Authorization{}, errors.New("body is required for propose")
	}
	return Authorization{Capability: "skill_draft"}, nil
}

type skillDraftInput struct {
	Action  string `json:"action"`
	SkillID string `json:"skill_id"`
	Body    string `json:"body,omitempty"`
}

func (t *skillDraftTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.backend == nil {
		return nil, fmt.Errorf("%w: skill draft backend unavailable", ErrToolDenied)
	}
	var input skillDraftInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(input.SkillID)
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "propose":
		sk, hash, err := t.backend.WriteDraft(id, input.Body)
		if err != nil {
			return nil, err
		}
		_ = t.backend.RecordDraft(ctx, sk.ID, "agent", sk.DraftPath(), hash, false)
		return encodeResult(map[string]any{
			"ok": true, "action": "propose", "skill_id": sk.ID, "content_hash": hash,
			"note": "draft written; user must apply via /skills apply " + sk.ID,
		})
	case "discard":
		sk, err := t.backend.DiscardDraft(id)
		if err != nil {
			return nil, err
		}
		_ = t.backend.RecordDraft(ctx, sk.ID, "agent", sk.DraftPath(), "", true)
		return encodeResult(map[string]any{"ok": true, "action": "discard", "skill_id": sk.ID})
	default:
		return nil, fmt.Errorf("action must be propose or discard")
	}
}

// SkillDraftAdapter binds catalog + maintain to the tool.
type SkillDraftAdapter struct {
	Catalog  *skillcatalog.Catalog
	Maintain *skillmaintain.Service
}

func (a SkillDraftAdapter) WriteDraft(id, body string) (skillcatalog.Skill, string, error) {
	if a.Catalog == nil {
		return skillcatalog.Skill{}, "", errors.New("skill catalog is unavailable")
	}
	return a.Catalog.WriteDraft(id, body)
}

func (a SkillDraftAdapter) DiscardDraft(id string) (skillcatalog.Skill, error) {
	if a.Catalog == nil {
		return skillcatalog.Skill{}, errors.New("skill catalog is unavailable")
	}
	return a.Catalog.DiscardDraft(id)
}

func (a SkillDraftAdapter) RecordDraft(ctx context.Context, skillID, actor, path, hash string, discarded bool) error {
	if a.Maintain == nil {
		return nil
	}
	return a.Maintain.RecordDraft(ctx, skillID, actor, path, hash, discarded)
}
