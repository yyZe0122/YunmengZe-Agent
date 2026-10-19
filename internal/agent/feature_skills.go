package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

func (e *Engine) registerSkills(r *tools.Registry, catalog *skillcatalog.Catalog) {
	if !e.Config.SkillsEnabled() {
		return
	}
	r.Register(tools.Tool{Name: "skills_list", ReadOnly: true, Description: "List file skills and descriptions. Load a selected body with skill_view.", Schema: tools.Schema(`{}`), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
		return catalog.Skills(), nil
	}})
	r.Register(tools.Tool{Name: "skill_view", ReadOnly: true, Description: "Load a SKILL.md body, or a linked file within that skill directory.", Schema: tools.Schema(`{"id":{"type":"string"},"file_path":{"type":"string"}}`, "id"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
		var a struct {
			ID   string `json:"id"`
			File string `json:"file_path"`
		}
		if err := tools.Decode(raw, &a); err != nil {
			return nil, err
		}
		var b []byte
		var err error
		if a.File != "" {
			b, err = catalog.ReadLinked(a.ID, a.File)
		} else {
			b, err = catalog.Read(a.ID)
		}
		return string(b), err
	}})
}

func (e *Engine) Skills(workspace string) (*skillcatalog.Catalog, []skillcatalog.Diagnostic) {
	if !e.Config.SkillsEnabled() {
		return &skillcatalog.Catalog{}, nil
	}
	return skillcatalog.Discover([]skillcatalog.Root{{Path: filepath.Join(e.ConfigDir, "skills"), Source: skillcatalog.SourceUser}, {Path: filepath.Join(workspace, ".yunmengze", "skills"), Source: skillcatalog.SourceProject}})
}

// expand uses the same discovery result as this run's tools and prompt.
func (e *Engine) expand(text string, catalog *skillcatalog.Catalog) (string, error) {
	if !strings.HasPrefix(text, "/") {
		return text, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "/"), " ", 2)
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}
	if cmd, ok := e.Config.LookupCommand(parts[0]); ok {
		return providerconfig.ExpandChatCommandTemplate(cmd.Template, args), nil
	}
	if _, ok := catalog.Get(parts[0]); ok {
		b, err := catalog.Read(parts[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Use skill %s:\n%s\n\n%s", parts[0], b, args), nil
	}
	return "", fmt.Errorf("unknown command /%s", parts[0])
}
