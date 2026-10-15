// Package agent composes the local coding loop. CLI and TUI call Engine directly.
package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yyZe0122/yunmengze-agent/internal/memory"
	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/skillcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type Engine struct {
	snapshot   *providerconfig.Snapshot
	life       context.Context
	cancel     context.CancelFunc
	runs       sync.WaitGroup
	closed     bool
	mcpMu      sync.Mutex
	mcpClients map[string]mcpConnection
	Store      *session.Store
	Config     providerconfig.ChatConfig
	ConfigDir  string
	Workspace  string
	Main       string
	Roles      map[string]string
	Models     []providerconfig.ModelRef
	Memory     *memory.Manager
	db         *sqlite.DB
	mcp        providerconfig.MCPConfig
	mu         sync.Mutex
	providers  map[string]binding
}
type binding struct {
	provider providerapi.Provider
	config   providerconfig.Resolved
}
type Question struct {
	TaskID   string   `json:"task_id,omitempty"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}
type Event struct {
	TaskID  string             `json:"task_id,omitempty"`
	Purpose string             `json:"purpose,omitempty"`
	Type    string             `json:"type"`
	Text    string             `json:"text,omitempty"`
	Tool    string             `json:"tool,omitempty"`
	Usage   *providerapi.Usage `json:"usage,omitempty"`
}
type RunOptions struct {
	Emit  func(Event)
	Ask   func(context.Context, Question) (string, error)
	Inbox <-chan string
}

func (o RunOptions) emit(e Event) {
	if o.Emit != nil {
		o.Emit(e)
	}
}

func Open(ctx context.Context, configDir, workspace string) (*Engine, error) {
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if _, err = providerconfig.EnsureConfig(dir); err != nil {
		return nil, err
	}
	modelcatalog.Init(dir)
	snapshot, err := providerconfig.LoadSnapshot(dir)
	if err != nil {
		return nil, err
	}
	config, main := snapshot.Chat, snapshot.Main
	if main == "" {
		return nil, errors.New("configure a model in agent.json before starting")
	}
	life, cancel := context.WithCancel(ctx)
	opened := false
	defer func() {
		if !opened {
			cancel()
		}
	}()
	e := &Engine{Config: config, ConfigDir: dir, Workspace: workspace, Main: main, Roles: snapshot.Roles, Models: snapshot.Models, mcp: snapshot.MCP, providers: map[string]binding{}, snapshot: snapshot, life: life, cancel: cancel, mcpClients: map[string]mcpConnection{}}
	if _, err = e.resolve(main); err != nil {
		return nil, err
	}
	db, err := sqlite.Open(ctx, filepath.Join(dir, "core.db"))
	if err != nil {
		return nil, err
	}
	e.db = db
	e.Store = session.New(db.SQL())
	if config.MemoryEnabled() {
		store, err := memory.NewStore(db.SQL())
		if err != nil {
			db.Close()
			return nil, err
		}
		e.Memory, err = memory.New(memory.Config{Store: store, MaxInjectRunes: config.MemoryMaxInjectRunes(), DefaultTTL: config.MemoryDefaultTTL()})
		if err != nil {
			db.Close()
			return nil, err
		}
		if err = e.Memory.Initialize(ctx); err != nil {
			db.Close()
			return nil, err
		}
	}
	opened = true
	return e, nil
}
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.cancel()
	e.mu.Unlock()
	e.runs.Wait()
	for _, client := range e.mcpClients {
		client.cancel()
		_ = client.session.Close()
	}
	if e.Memory != nil {
		_ = e.Memory.Shutdown(context.Background())
	}
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}
func (e *Engine) resolve(ref string) (binding, error) {
	if ref == "" {
		ref = e.Main
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if b, ok := e.providers[ref]; ok {
		return b, nil
	}
	resolved, err := e.snapshot.Resolve(ref)
	if err != nil {
		return binding{}, err
	}
	p, err := providers.NewConfigured(*resolved)
	if err != nil {
		return binding{}, err
	}
	b := binding{provider: p, config: *resolved}
	e.providers[ref] = b
	return b, nil
}
func (e *Engine) NewSession(ctx context.Context, mode session.Mode, model string) (session.Session, error) {
	if model == "" {
		model = e.Main
	}
	if _, err := e.resolve(model); err != nil {
		return session.Session{}, err
	}
	return e.Store.Create(ctx, session.Session{Workspace: e.Workspace, Mode: mode, Model: model})
}
func (e *Engine) SetModel(ctx context.Context, s session.Session, ref string) error {
	if _, err := e.resolve(ref); err != nil {
		return err
	}
	return e.Store.Configure(ctx, s.ID, s.Mode, ref)
}
func (e *Engine) role(name, fallback string) string {
	if ref := e.Roles[name]; ref != "" {
		return ref
	}
	if fallback != "" {
		return fallback
	}
	return e.Main
}
func (e *Engine) Skills(workspace string) *skillcatalog.Catalog {
	catalog, _ := skillcatalog.Discover([]skillcatalog.Root{{Path: filepath.Join(e.ConfigDir, "skills"), Source: skillcatalog.SourceUser}, {Path: filepath.Join(workspace, ".yunmengze", "skills"), Source: skillcatalog.SourceProject}})
	return catalog
}

// Expand turns a configured slash template or explicit skill into a user message.
func (e *Engine) Expand(workspace, text string) (string, error) {
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
	catalog := e.Skills(workspace)
	if _, ok := catalog.Get(parts[0]); ok {
		b, err := catalog.Read(parts[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Use skill %s:\n%s\n\n%s", parts[0], b, args), nil
	}
	return "", fmt.Errorf("unknown command /%s", parts[0])
}

func (e *Engine) beginRun(ctx context.Context) (context.Context, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, nil, errors.New("engine is closed")
	}
	e.runs.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.life, cancel)
	return ctx, func() { stop(); cancel(); e.runs.Done() }, nil
}
