package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

const (
	skillsListHint = "Use skill_view(name) to load SKILL.md; then skill_view(name, file_path) for linked files."
	skillViewHint  = "To view linked files, call skill_view(name, file_path) where file_path is e.g. references/api.md."
)

// SkillCatalog is the narrow catalog surface for list/view tools.
type SkillCatalog interface {
	Skills() []skillcatalog.Skill
	Get(id string) (skillcatalog.Skill, bool)
	Read(id string) ([]byte, error)
	ListLinked(id string) ([]string, error)
	ReadLinked(id, rel string) ([]byte, error)
	Match(query string, limit int) []skillcatalog.Skill
}

// SkillUsageFilter hides archived skills and records skill_view usage.
type SkillUsageFilter interface {
	ArchivedSkillIDs(ctx context.Context) map[string]struct{}
	RecordUsed(ctx context.Context, skillIDs []string, actor string) error
}

// RegisterSkillTools registers Hermes-style skills_list and skill_view (ADR-036).
func RegisterSkillTools(broker *Broker, catalog SkillCatalog, usage SkillUsageFilter) error {
	if broker == nil {
		return errors.New("tool broker is required")
	}
	shared := &skillTools{catalog: catalog, usage: usage}
	if err := broker.Register(&skillsListTool{st: shared}); err != nil {
		return err
	}
	return broker.Register(&skillViewTool{st: shared})
}

type skillTools struct {
	catalog SkillCatalog
	usage   SkillUsageFilter

	mu   sync.Mutex
	seen map[string]map[string]struct{}
}

func (s *skillTools) archived(ctx context.Context) map[string]struct{} {
	if s == nil || s.usage == nil {
		return nil
	}
	return s.usage.ArchivedSkillIDs(ctx)
}

func (s *skillTools) recordUsed(ctx context.Context, id string) {
	if s == nil || s.usage == nil {
		return
	}
	actor := "agent"
	if rc, ok := runmeta.From(ctx); ok && strings.TrimSpace(rc.Actor) != "" {
		actor = rc.Actor
	}
	_ = s.usage.RecordUsed(ctx, []string{id}, actor)
}

func (s *skillTools) dedupKey(ctx context.Context, name, filePath string) (string, string) {
	runID := ""
	if rc, ok := runmeta.From(ctx); ok {
		runID = strings.TrimSpace(rc.RunID)
	}
	if runID == "" {
		return "", ""
	}
	return runID, name + "\x00" + filePath
}

func (s *skillTools) alreadyViewed(ctx context.Context, name, filePath string) bool {
	runID, key := s.dedupKey(ctx, name, filePath)
	if runID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[runID][key]
	return ok
}

func (s *skillTools) markViewed(ctx context.Context, name, filePath string) {
	runID, key := s.dedupKey(ctx, name, filePath)
	if runID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[string]map[string]struct{})
	}
	set := s.seen[runID]
	if set == nil {
		set = make(map[string]struct{})
		s.seen[runID] = set
	}
	set[key] = struct{}{}
}

type skillsListTool struct {
	st *skillTools
}

func (t *skillsListTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "skills_list",
		Description:          "List available local skills (id + description only). Call skill_view(name) to load SKILL.md. Optional query ranks matches as suggested. Instruction text only — does not expand tool grants.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 5_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"properties":{
				"query":{"type":"string","description":"Optional keyword query to rank suggested skills"}
			}
		}`),
	}
}

func (t *skillsListTool) Authorization(context.Context, json.RawMessage) (Authorization, error) {
	return Authorization{Capability: "skills_list"}, nil
}

type skillsListInput struct {
	Query string `json:"query,omitempty"`
}

func (t *skillsListTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.st == nil || t.st.catalog == nil {
		return nil, fmt.Errorf("%w: skill catalog is unavailable", ErrToolDenied)
	}
	var input skillsListInput
	if len(raw) > 0 && string(raw) != "null" {
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
	}
	archived := t.st.archived(ctx)
	all := t.st.catalog.Skills()
	suggested := map[string]struct{}{}
	query := strings.TrimSpace(input.Query)
	if query != "" {
		for _, sk := range t.st.catalog.Match(query, skillcatalog.MaxAutoSkills) {
			if _, skip := archived[sk.ID]; skip {
				continue
			}
			suggested[sk.ID] = struct{}{}
		}
	}
	type item struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Suggested   bool   `json:"suggested,omitempty"`
	}
	out := make([]item, 0, len(all))
	for _, sk := range all {
		if _, skip := archived[sk.ID]; skip {
			continue
		}
		_, hit := suggested[sk.ID]
		out = append(out, item{
			ID: sk.ID, Name: sk.Name, Description: sk.Description,
			Source: string(sk.Source), Suggested: hit,
		})
	}
	hint := skillsListHint
	if len(out) == 0 {
		hint = "no skills discovered"
	}
	return encodeResult(map[string]any{"skills": out, "hint": hint})
}

type skillViewTool struct {
	st *skillTools
}

func (t *skillViewTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name:                 "skill_view",
		Description:          "Load a skill's SKILL.md, or a linked file under that skill directory. First call without file_path returns the body plus linked_files. Then skill_view(name, file_path) for references/templates/scripts. Use skills_list to see ids. Instruction text only — does not expand tool grants.",
		Risk:                 string(policy.RiskR0),
		DefaultTimeoutMillis: 8_000,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"additionalProperties":false,
			"required":["name"],
			"properties":{
				"name":{"type":"string","description":"Skill directory id from skills_list"},
				"file_path":{"type":"string","description":"Optional relative path inside the skill directory"}
			}
		}`),
	}
}

func (t *skillViewTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	var input skillViewInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	if strings.TrimSpace(input.Name) == "" {
		return Authorization{}, errors.New("name is required")
	}
	return Authorization{Capability: "skill_view"}, nil
}

type skillViewInput struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path,omitempty"`
}

func (t *skillViewTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.st == nil || t.st.catalog == nil {
		return nil, fmt.Errorf("%w: skill catalog is unavailable", ErrToolDenied)
	}
	var input skillViewInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(input.Name)
	if id == "" {
		return nil, errors.New("name is required")
	}
	if _, skip := t.st.archived(ctx)[id]; skip {
		return nil, fmt.Errorf("%w: skill %q is archived; use skills_list", ErrToolDenied, id)
	}
	sk, ok := t.st.catalog.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: skill %q not found; use skills_list", ErrToolDenied, id)
	}
	rel := strings.TrimSpace(input.FilePath)
	if t.st.alreadyViewed(ctx, id, rel) {
		return encodeResult(map[string]any{
			"ok": true, "deduped": true, "id": sk.ID,
			"hint": "already loaded in this run; refer to the earlier skill_view result",
		})
	}
	if rel != "" {
		body, err := t.st.catalog.ReadLinked(id, rel)
		if err != nil {
			return nil, err
		}
		t.st.markViewed(ctx, id, rel)
		return encodeResult(map[string]any{
			"ok": true, "id": sk.ID, "name": sk.Name, "source": string(sk.Source),
			"file_path": rel, "content": string(body),
		})
	}
	body, err := t.st.catalog.Read(id)
	if err != nil {
		return nil, err
	}
	linked, err := t.st.catalog.ListLinked(id)
	if err != nil {
		return nil, err
	}
	t.st.recordUsed(ctx, id)
	t.st.markViewed(ctx, id, rel)
	return encodeResult(map[string]any{
		"ok": true, "id": sk.ID, "name": sk.Name, "source": string(sk.Source),
		"content": string(body), "linked_files": linked, "hint": skillViewHint,
	})
}
