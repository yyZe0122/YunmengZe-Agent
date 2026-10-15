package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (e *Engine) registry(s session.Session, options RunOptions, catalog *skillcatalog.Catalog) (*tools.Registry, error) {
	r := tools.New()
	r.Web(e.Config)
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
	if e.Memory != nil {
		r.Register(tools.Tool{Name: "memory_search", ReadOnly: true, Description: "Search saved preferences and explicitly inspect older memory records. Older facts may be stale; do not treat them as current project state.", Schema: tools.Schema(`{"query":{"type":"string"}}`, "query"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
			var a struct {
				Query string `json:"query"`
			}
			if err := tools.Decode(raw, &a); err != nil {
				return nil, err
			}
			return e.Memory.Search(ctx, env.SessionID, a.Query, 20)
		}})
	}
	r.Register(tools.Tool{Name: "task", ReadOnly: true, Description: "Use a leaf child only for substantial separable work: general, explore (always read-only), or web. Set independent=true only when there are no dependencies and no overlapping write paths with siblings; at most two independent tasks run together. The parent integrates and verifies results. Omit task_id for a new child; supply the returned task_id to continue it. Children inherit plan restrictions.", Schema: tools.Schema(`{"prompt":{"type":"string"},"kind":{"type":"string","enum":["general","explore","web"]},"task_id":{"type":"string"},"independent":{"type":"boolean"}}`, "prompt"), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
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
			child, err = e.Store.Get(ctx, a.ID)
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
			child, err = e.Store.Create(ctx, session.Session{ParentID: s.ID, Kind: a.Kind, Workspace: s.Workspace, Mode: mode, Model: ref})
		} else {
			err = e.Store.Configure(ctx, child.ID, mode, child.Model)
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
	for name, server := range e.mcp.Servers {
		r.Register(tools.Tool{Name: "mcp_" + name, Description: "MCP server " + name + ". Omit tool to list its tools and schemas; otherwise call tool with arguments. Available only in agent mode.", Schema: tools.Schema(`{"tool":{"type":"string"},"arguments":{"type":"object"}}`), Handler: func(ctx context.Context, env tools.Env, raw json.RawMessage) (any, error) {
			var a struct {
				Tool      string          `json:"tool"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := tools.Decode(raw, &a); err != nil {
				return nil, err
			}
			client, err := e.mcpClient(ctx, env.SessionID, name, server, env.Workspace)
			if err != nil {
				return nil, err
			}

			if a.Tool == "" {
				result, err := client.ListTools(ctx)
				if err != nil {
					e.dropMCP(env.SessionID, name)
				}
				return result, err
			}
			if len(a.Arguments) == 0 {
				a.Arguments = json.RawMessage(`{}`)
			}
			result, err := client.CallTool(ctx, a.Tool, a.Arguments)
			if err != nil {
				e.dropMCP(env.SessionID, name)
			}
			return result, err
		}})
	}
	var vision tools.Vision
	if ref := e.Roles[providerconfig.RoleVision]; ref != "" {
		b, err := e.resolve(ref)
		if err != nil {
			return nil, fmt.Errorf("vision: %w", err)
		}
		if b.config.ImageCapabilityKnown && !b.config.SupportsImages {
			return nil, errors.New("configured vision model has supportsImages=false")
		}
		vision = providers.VisionBackend{Provider: b.provider, Model: b.config.ModelID}
	} else {
		b, err := e.resolve(s.Model)
		if err != nil {
			return nil, err
		}
		vision = nativeVision{supported: b.config.SupportsImages}
	}
	r.Images(vision)
	return r, nil
}

type nativeVision struct{ supported bool }

func (v nativeVision) Complete(_ context.Context, prompt string, images []providerapi.ImagePart) (tools.Result, error) {
	if !v.supported {
		return tools.Result{}, errors.New("current model image support is unknown or disabled; configure models.vision, or set supportsImages=true on a model known to accept images")
	}
	return tools.Result{Text: "Image attached for inspection. " + prompt, Images: images}, nil
}
