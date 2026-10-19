// Package tools executes local tools directly. Read-only mode is enforced here,
// independently of the tool list advertised to the model.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type Env struct {
	Workspace string
	SessionID string
	ReadOnly  bool
	Child     bool
	Progress  func(string)
}

// Result carries image observations separately from bounded textual output.
type Result struct {
	UserText *string
	Text     string
	Images   []providerapi.ImagePart
	Usage    *providerapi.Usage
}
type Handler func(context.Context, Env, json.RawMessage) (any, error)
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	ReadOnly    bool
	RootOnly    bool
	Handler     Handler
}
type Registry struct {
	tools     map[string]Tool
	Timeout   time.Duration
	MaxOutput int
}

func New() *Registry {
	r := &Registry{tools: map[string]Tool{}, Timeout: 10 * time.Minute, MaxOutput: 64000}
	r.files()
	r.Register(Tool{Name: "shell", Description: "Execute a shell command in the workspace. Includes stdout, stderr and exit code.", Schema: Schema(`{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":600}}`, "command"), Handler: runShell})
	return r
}
func Schema(properties string, required ...string) json.RawMessage {
	r, _ := json.Marshal(required)
	if len(required) == 0 {
		r = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":` + properties + `,"required":` + string(r) + `,"additionalProperties":false}`)
}
func (r *Registry) Register(t Tool) {
	if t.Name == "" || t.Handler == nil || !json.Valid(t.Schema) {
		panic("invalid tool registration")
	}
	if _, ok := r.tools[t.Name]; ok {
		panic("duplicate tool: " + t.Name)
	}
	r.tools[t.Name] = t
}
func (r *Registry) Definitions(env Env) []providerapi.ToolDefinition {
	var out []providerapi.ToolDefinition
	for _, t := range r.tools {
		if env.ReadOnly && !t.ReadOnly || env.Child && t.RootOnly {
			continue
		}
		out = append(out, providerapi.ToolDefinition{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Execute(ctx context.Context, env Env, name string, args json.RawMessage) (Result, error) {
	t, ok := r.tools[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q", name)
	}
	if env.ReadOnly && !t.ReadOnly {
		return Result{}, fmt.Errorf("%s is unavailable in plan mode", name)
	}
	if env.Child && t.RootOnly {
		return Result{}, fmt.Errorf("%s is unavailable in child sessions", name)
	}
	if err := validateArguments(args, t.Schema); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	value, err := t.Handler(ctx, env, args)
	if err != nil {
		return Result{}, err
	}
	if result, ok := value.(Result); ok {
		result.Text = BoundText(result.Text, r.MaxOutput)
		return result, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return Result{}, err
	}
	if len(b) > r.MaxOutput {
		b, _ = json.Marshal(map[string]any{"truncated": true, "output": BoundText(string(b), r.MaxOutput/2-128)})
	}

	return Result{Text: string(b)}, nil
}

// Validate the top-level contract before any effect. Concrete handlers validate
// field types and semantics; this is deliberately not a JSON Schema interpreter.
func validateArguments(raw, schema json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("tool arguments must be a JSON object")
	}
	var contract struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &contract); err != nil {
		return err
	}
	for _, name := range contract.Required {
		v, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("%s is required and cannot be null", name)
		}
	}
	for name, value := range fields {
		if _, ok := contract.Properties[name]; !ok {
			return fmt.Errorf("unknown argument %q", name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s cannot be null; omit optional arguments", name)
		}
	}
	return nil
}

func BoundText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	marker := "\n... output truncated ...\n"
	n := max(0, (limit-len(marker))/2)
	head, tail := n, len(text)-n
	for head > 0 && (text[head]&0xc0) == 0x80 {
		head--
	}
	for tail < len(text) && (text[tail]&0xc0) == 0x80 {
		tail++
	}
	return text[:head] + marker + text[tail:]
}
func Decode(raw json.RawMessage, v any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
