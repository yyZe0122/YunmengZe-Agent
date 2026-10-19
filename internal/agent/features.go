package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

// Optional capabilities are assembled explicitly; the file/shell registry remains the core.
func (e *Engine) registry(s session.Session, options RunOptions, catalog *skillcatalog.Catalog) (*tools.Registry, error) {
	r := tools.New()
	e.registerWeb(r)
	e.registerSkills(r, catalog)
	if options.Ask != nil {
		r.Register(tools.Tool{Name: "ask_user", ReadOnly: true, Description: "Ask the user a focused question, optionally with choices or multiple selections.", Schema: tools.Schema(`{"question":{"type":"string"},"options":{"type":"array","items":{"type":"string"}},"multiple":{"type":"boolean"}}`, "question"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
			var q Question
			if err := tools.Decode(raw, &q); err != nil {
				return nil, err
			}
			if strings.TrimSpace(q.Question) == "" || len(q.Options) > 12 {
				return nil, errors.New("question is required; at most 12 options")
			}
			answer, err := options.Ask(ctx, q)
			var original *string
			if !env.Child && !env.ReadOnly {
				original = &answer
			}
			return tools.Result{Text: answer, UserText: original}, err
		}})
	}
	e.registerMemory(r)
	e.registerTask(r, s, options)
	e.registerMCP(r)
	if err := e.registerVision(r, s); err != nil {
		return nil, err
	}
	return r, nil
}
