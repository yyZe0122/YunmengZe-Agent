package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

func (e *Engine) registerTask(r *tools.Registry, s session.Session, options RunOptions) {
	if !e.Config.TaskEnabled() {
		return
	}
	schema := `{"prompt":{"type":"string"},"kind":{"type":"string","enum":["general","explore","web"]},"task_id":{"type":"string"},"independent":{"type":"boolean"}}`
	if !e.Config.WebEnabled() {
		schema = strings.Replace(schema, `,"web"`, "", 1)
	}
	kinds := "general or explore (always read-only)"
	if e.Config.WebEnabled() {
		kinds += ", or web"
	}
	r.Register(tools.Tool{Name: "task", ReadOnly: true, RootOnly: true, Description: "Use a leaf child only for substantial separable work: " + kinds + ". Set independent=true only when there are no dependencies and no overlapping write paths with siblings; at most two independent tasks run together. The parent integrates and verifies results. Omit task_id for a new child; supply the returned task_id to continue it. Children inherit plan restrictions.", Schema: tools.Schema(schema, "prompt"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
		var a struct {
			Prompt      string `json:"prompt"`
			Kind        string `json:"kind"`
			ID          string `json:"task_id"`
			Independent bool   `json:"independent"`
		}
		if err := tools.Decode(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Prompt) == "" {
			return nil, errors.New("prompt is required")
		}
		var child session.Session
		var err error
		if a.ID != "" {
			child, err = e.store.Get(ctx, a.ID)
			if err != nil {
				return nil, err
			}
			if child.ParentID != s.ID {
				return nil, errors.New("task_id does not belong to this parent session")
			}
			if a.Kind != "" && a.Kind != child.Kind {
				return nil, errors.New("cannot change a child kind")
			}
			a.Kind = child.Kind
		}
		if a.Kind == "" {
			a.Kind = "general"
		}
		if a.Kind == "web" && !e.Config.WebEnabled() {
			return nil, errors.New("web tasks are disabled")
		}
		if a.Kind != "general" && a.Kind != "explore" && a.Kind != "web" {
			return nil, errors.New("kind must be general, explore or web")
		}
		mode := s.Mode
		if env.ReadOnly || a.Kind == "explore" {
			mode = session.Plan
		}
		ref := e.role(providerconfig.RoleSubagent, s.Model)
		if a.Kind == "web" {
			ref = e.role(providerconfig.RoleWeb, ref)
		}
		if a.ID == "" {
			child, err = e.store.Create(ctx, session.Session{ParentID: s.ID, Kind: a.Kind, Workspace: s.Workspace, Mode: mode, Model: ref})
		} else {
			err = e.store.Configure(ctx, child.ID, mode, child.Model)
		}
		if err != nil {
			return nil, err
		}
		options.emit(Event{Type: "task_start", TaskID: child.ID, Text: a.Kind})
		childOptions := RunOptions{Emit: func(event Event) { event.TaskID = child.ID; options.emit(event) }}
		if options.Ask != nil {
			childOptions.Ask = func(ctx context.Context, q Question) (string, error) { q.TaskID = child.ID; return options.Ask(ctx, q) }
		}
		text, runErr := e.Run(ctx, child.ID, a.Prompt, childOptions)
		status := "completed"
		if runErr != nil {
			status = runErr.Error()
		}
		options.emit(Event{Type: "task_end", TaskID: child.ID, Text: status})
		result := map[string]string{"task_id": child.ID, "result": tools.BoundText(text, 10000)}
		if runErr != nil {
			result["error"] = tools.BoundText(runErr.Error(), 1000)
		}
		encoded, _ := json.Marshal(result)
		return tools.Result{Text: string(encoded)}, nil
	}})
}
