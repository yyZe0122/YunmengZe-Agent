// Package providerruntime owns main provider load, hot-reload, and /model switch
// for the daemon (ADR-048). Gateway does not call providers.
package providerruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/applicationerror"
	"github.com/yyZe0122/yunmengze-agent/internal/gateway"
	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// ErrChatNotBound is returned when config is OK but agent/chat was never wired
// (daemon started without a successful provider load). Caller must restart.
var ErrChatNotBound = errors.New("chat is unavailable until config validates and the daemon is restarted (ymz config validate && ymz restart)")

// MainEndpoint is the main agent stack (SetProvider/SetModel/context window).
type MainEndpoint interface {
	SetProvider(provider agent.StreamingProvider) error
	SetModel(model string) error
	SetContextWindow(n int64)
}

// ContextWindow applies packing window / output-cap updates (chat service).
type ContextWindow interface {
	SetContextWindow(n int64)
	SetMaxOutputTokens(n int64)
	SetMainModel(model string)
}

// SnapshotSink updates GET /v1/config/model after reload or switch.
type SnapshotSink interface {
	UpdateModelSnapshot(config gateway.ModelConfig, loadError string)
}

// Runtime holds the main provider client and hot-reload state.
type Runtime struct {
	mu sync.Mutex

	configDir   string
	provider    providerapi.Provider
	model       string
	selectedRef string
	loadError   string
	fingerprint string

	agent   MainEndpoint
	chat    ContextWindow
	gateway SnapshotSink

	// suppressUntil ignores filesystem reloads after SelectModel writes config.
	suppressUntil time.Time
}

// FromConfigDir loads provider config. Incomplete config returns a Runtime with
// LoadError set (gateway still starts). Hard provider construction errors return err.
func FromConfigDir(configDir string) (*Runtime, error) {
	configDir = strings.TrimSpace(configDir)
	modelcatalog.Init(configDir)
	rt := &Runtime{configDir: configDir}
	configured, err := providerconfig.Load(configDir)
	if err != nil {
		slog.Warn("provider config not loaded", "component", "daemon", "operation", "load_config", "result", "warning",
			"config_dir", configDir, "error", err)
		rt.loadError = err.Error()
		return rt, nil
	}
	if configured == nil {
		rt.loadError = "no agent.json or agent.local.json in config dir"
		return rt, nil
	}
	provider, err := providers.NewConfigured(*configured)
	if err != nil {
		return nil, fmt.Errorf("configure provider: %w", err)
	}
	rt.provider = provider
	rt.model = configured.ModelID
	rt.selectedRef = configured.SelectionRef
	if rt.selectedRef == "" {
		rt.selectedRef = configured.ProviderID + "/" + configured.ModelID
	}
	rt.fingerprint = Fingerprint(configured)
	return rt, nil
}

// Bind attaches agent/chat/gateway after composition (may be nil when load failed).
func (r *Runtime) Bind(agent MainEndpoint, chat ContextWindow, snap SnapshotSink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agent = agent
	r.chat = chat
	r.gateway = snap
}

// StartCatalogRefresh refreshes models.dev in the background and reapplies packing window/cap.
func (r *Runtime) StartCatalogRefresh(ctx context.Context) {
	if r == nil {
		return
	}
	configDir := r.configDir
	go func() {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := modelcatalog.Refresh(ctx, configDir); err != nil {
			slog.Warn("model catalog refresh failed", "component", "modelcatalog", "operation", "refresh", "result", "warning", "error", err)
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.loadError != "" || r.selectedRef == "" {
			return
		}
		resolved, err := providerconfig.ResolveModel(r.configDir, r.selectedRef)
		if err != nil || resolved == nil {
			return
		}
		_ = r.applyMainLocked(r.provider, resolved.ModelID, resolved.ContextWindow, resolved.MaxTokens)
		r.pushSnapshotLocked("")
	}()
}

// Provider returns the current main provider (may be nil).
func (r *Runtime) Provider() providerapi.Provider {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.provider
}

// Model returns the wire/API model id sent to the provider.
func (r *Runtime) Model() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model
}

// SelectedRef returns the full selection ref (providerID/modelID…; modelID may contain '/').
func (r *Runtime) SelectedRef() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectedRef
}

// LoadError is non-empty when the last successful config apply is missing.
func (r *Runtime) LoadError() string {
	if r == nil {
		return "provider runtime is not configured"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadError
}

// ChatBound is true when agent was wired at daemon start (not late-bound).
func (r *Runtime) ChatBound() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agent != nil
}

// NoteFingerprint refreshes fingerprint from disk for the current selection.
func (r *Runtime) NoteFingerprint() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadError != "" || r.selectedRef == "" {
		return
	}
	if resolved, err := providerconfig.ResolveModel(r.configDir, r.selectedRef); err == nil && resolved != nil {
		r.fingerprint = Fingerprint(resolved)
	}
}

// ReloadFromDisk reloads provider config after agent.json / env change.
// On failure keeps the previous successful provider client (in-flight runs keep it)
// but sets loadError so SelectModel is unavailable until a successful reload.
func (r *Runtime) ReloadFromDisk() error {
	if r == nil {
		return errors.New("provider runtime is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.suppressUntil.IsZero() && time.Now().Before(r.suppressUntil) {
		return nil
	}

	configured, err := providerconfig.Load(r.configDir)
	if err != nil {
		r.loadError = err.Error()
		r.pushSnapshotLocked(r.loadError)
		return err
	}
	if configured == nil {
		r.loadError = "no agent.json or agent.local.json in config dir"
		r.pushSnapshotLocked(r.loadError)
		return errors.New(r.loadError)
	}
	fp := Fingerprint(configured)
	if fp != "" && fp == r.fingerprint && r.provider != nil && r.loadError == "" {
		return nil
	}
	provider, err := providers.NewConfigured(*configured)
	if err != nil {
		r.loadError = err.Error()
		r.pushSnapshotLocked(r.loadError)
		return err
	}
	selected := configured.SelectionRef
	if selected == "" {
		selected = configured.ProviderID + "/" + configured.ModelID
	}
	if err := r.applyMainLocked(provider, configured.ModelID, configured.ContextWindow, configured.MaxTokens); err != nil {
		return err
	}
	r.provider = provider
	r.model = configured.ModelID
	r.selectedRef = selected
	r.loadError = ""
	r.fingerprint = fp
	slog.Info("provider config reloaded", "component", "configreload", "operation", "reload", "result", "succeeded",
		"model", selected, "wire_model", configured.ModelID, "base_url", configured.BaseURL, "context_window", configured.ContextWindow)
	r.pushSnapshotLocked("")
	return nil
}

// SelectModel implements gateway.ModelSwitcher.
func (r *Runtime) SelectModel(_ context.Context, ref string) (gateway.ModelConfig, error) {
	if r == nil {
		return gateway.ModelConfig{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, errors.New("provider runtime is not configured"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if errMsg := strings.TrimSpace(r.loadError); errMsg != "" {
		return gateway.ModelConfig{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, errors.New(errMsg))
	}
	if r.agent == nil {
		return gateway.ModelConfig{}, applicationerror.Wrap(applicationerror.CodeUnavailable, false, ErrChatNotBound)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return gateway.ModelConfig{}, errors.New("model is required")
	}
	if ref == r.selectedRef {
		cfg, err := r.modelConfigLocked()
		if err != nil {
			return gateway.ModelConfig{}, err
		}
		return r.decorateReady(cfg), nil
	}
	resolved, err := providerconfig.ResolveModel(r.configDir, ref)
	if err != nil {
		return gateway.ModelConfig{}, err
	}
	provider, err := providers.NewConfigured(*resolved)
	if err != nil {
		return gateway.ModelConfig{}, err
	}
	// Suppress watcher reload from our own WriteSelectedModel.
	r.suppressUntil = time.Now().Add(1500 * time.Millisecond)
	writtenPath, err := providerconfig.WriteSelectedModel(r.configDir, ref)
	if err != nil {
		r.suppressUntil = time.Time{}
		return gateway.ModelConfig{}, err
	}
	if err := r.applyMainLocked(provider, resolved.ModelID, resolved.ContextWindow, resolved.MaxTokens); err != nil {
		return gateway.ModelConfig{}, err
	}
	r.provider = provider
	r.model = resolved.ModelID
	r.selectedRef = resolved.SelectionRef
	if r.selectedRef == "" {
		r.selectedRef = resolved.ProviderID + "/" + resolved.ModelID
	}
	r.loadError = ""
	r.fingerprint = Fingerprint(resolved)
	slog.Info("model switched", "component", "daemon", "operation", "select_model", "result", "succeeded",
		"model", r.selectedRef, "wire_model", resolved.ModelID, "context_window", resolved.ContextWindow, "config_path", writtenPath)
	cfg, err := r.modelConfigLocked()
	if err != nil {
		return gateway.ModelConfig{}, err
	}
	return r.decorateReady(cfg), nil
}

// Snapshot returns the secret-free model list for gateway bootstrap.
func (r *Runtime) Snapshot() (gateway.ModelConfig, string) {
	if r == nil {
		return gateway.ModelConfig{Models: []string{}, Ready: false, Error: "provider runtime is not configured"}, "provider runtime is not configured"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, err := r.modelConfigLocked()
	if err != nil {
		msg := err.Error()
		return gateway.ModelConfig{Models: []string{}, Ready: false, Error: msg}, msg
	}
	loadErr := r.effectiveLoadErrorLocked()
	cfg = r.decorateWithError(cfg, loadErr)
	return cfg, loadErr
}

func (r *Runtime) applyMainLocked(provider providerapi.Provider, model string, contextWindow, maxTokens int64) error {
	if r.agent != nil {
		if err := r.agent.SetProvider(provider); err != nil {
			return err
		}
		if err := r.agent.SetModel(model); err != nil {
			return err
		}
		r.agent.SetContextWindow(contextWindow)
	}
	if r.chat != nil {
		r.chat.SetContextWindow(contextWindow)
		r.chat.SetMaxOutputTokens(maxTokens)
		r.chat.SetMainModel(model)
	}
	return nil
}

func (r *Runtime) pushSnapshotLocked(loadError string) {
	if r.gateway == nil {
		return
	}
	cfg, err := r.modelConfigLocked()
	if err != nil {
		cfg = gateway.ModelConfig{
			Model:  r.selectedRef,
			Models: []string{},
		}
		if r.selectedRef != "" {
			cfg.Models = []string{r.selectedRef}
		}
	}
	if loadError == "" {
		loadError = r.effectiveLoadErrorLocked()
	}
	cfg = r.decorateWithError(cfg, loadError)
	r.gateway.UpdateModelSnapshot(cfg, loadError)
}

func (r *Runtime) modelConfigLocked() (gateway.ModelConfig, error) {
	selected, refs, err := providerconfig.ListModelRefs(r.configDir)
	if err != nil {
		return gateway.ModelConfig{}, err
	}
	if strings.TrimSpace(r.selectedRef) != "" {
		selected = r.selectedRef
	} else if configured, loadErr := providerconfig.Load(r.configDir); loadErr == nil && configured != nil {
		selected = configured.SelectionRef
		if selected == "" {
			selected = configured.ProviderID + "/" + configured.ModelID
		}
	} else if strings.TrimSpace(r.model) != "" {
		selected = r.model
	}
	models := make([]string, 0, len(refs)+1)
	seen := map[string]struct{}{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	add(selected)
	for _, ref := range refs {
		add(ref.ID)
	}
	cfg := gateway.ModelConfig{Model: selected, Models: models}
	if selected != "" {
		if resolved, resolveErr := providerconfig.ResolveModel(r.configDir, selected); resolveErr == nil && resolved != nil {
			cfg.ContextWindow = resolved.ContextWindow
		}
	}
	return cfg, nil
}

func (r *Runtime) effectiveLoadErrorLocked() string {
	if msg := strings.TrimSpace(r.loadError); msg != "" {
		return msg
	}
	if r.agent == nil {
		return ErrChatNotBound.Error()
	}
	return ""
}

func (r *Runtime) decorateReady(cfg gateway.ModelConfig) gateway.ModelConfig {
	return r.decorateWithError(cfg, r.effectiveLoadErrorLocked())
}

func (r *Runtime) decorateWithError(cfg gateway.ModelConfig, loadError string) gateway.ModelConfig {
	if cfg.Models == nil {
		cfg.Models = []string{}
	}
	loadError = strings.TrimSpace(loadError)
	cfg.Error = loadError
	cfg.Ready = loadError == "" && r.agent != nil && r.provider != nil
	return cfg
}
