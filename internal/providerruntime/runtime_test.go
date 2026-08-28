package providerruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/gateway"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type stubMain struct {
	mu       sync.Mutex
	provider agent.StreamingProvider
	model    string
	window   int64
	sets     int
}

func (s *stubMain) SetProvider(p agent.StreamingProvider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = p
	s.sets++
	return nil
}
func (s *stubMain) SetModel(m string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = m
	return nil
}
func (s *stubMain) SetContextWindow(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = n
}

type stubChat struct {
	window int64
	maxOut int64
}

func (s *stubChat) SetContextWindow(n int64)   { s.window = n }
func (s *stubChat) SetMaxOutputTokens(n int64) { s.maxOut = n }
func (s *stubChat) SetMainModel(string)        {}

type stubSink struct {
	mu        sync.Mutex
	lastErr   string
	lastReady bool
	updates   int
}

func (s *stubSink) UpdateModelSnapshot(cfg gateway.ModelConfig, loadError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = loadError
	s.lastReady = cfg.Ready
	s.updates++
}

func writeLocalConfig(t *testing.T, dir, model, baseURL, apiKey string) {
	t.Helper()
	body := `{
  "model": "` + model + `",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "` + baseURL + `",
        "apiKey": "` + apiKey + `"
      },
      "models": {
        "deepseek-chat": { "name": "Chat", "contextWindow": 65536 },
        "deepseek-v4-flash": { "name": "Flash", "contextWindow": 128000 }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, providerconfig.LocalFilename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFromConfigDirAndReloadAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-old")
	rt, err := FromConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Provider() == nil || rt.LoadError() != "" {
		t.Fatalf("runtime = provider=%v err=%q", rt.Provider(), rt.LoadError())
	}
	main := &stubMain{}
	sink := &stubSink{}
	rt.Bind(main, nil, sink)

	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-new")
	if err := rt.ReloadFromDisk(); err != nil {
		t.Fatal(err)
	}
	main.mu.Lock()
	sets := main.sets
	main.mu.Unlock()
	if sets != 1 {
		t.Fatalf("expected SetProvider once after key change, sets=%d", sets)
	}
	// Same content again → skip
	if err := rt.ReloadFromDisk(); err != nil {
		t.Fatal(err)
	}
	main.mu.Lock()
	sets = main.sets
	main.mu.Unlock()
	if sets != 1 {
		t.Fatalf("expected skip on identical config, sets=%d", sets)
	}
}

func TestReloadFailureKeepsProvider(t *testing.T) {
	dir := t.TempDir()
	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-ok")
	rt, err := FromConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	main := &stubMain{}
	sink := &stubSink{}
	rt.Bind(main, nil, sink)
	prev := rt.Provider()

	if err := os.WriteFile(filepath.Join(dir, providerconfig.LocalFilename), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rt.ReloadFromDisk(); err == nil {
		t.Fatal("expected load error")
	}
	if rt.Provider() != prev {
		t.Fatal("provider should be retained on failure")
	}
	if rt.LoadError() == "" {
		t.Fatal("loadError should be set")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.lastReady || sink.lastErr == "" {
		t.Fatalf("sink = ready=%v err=%q", sink.lastReady, sink.lastErr)
	}
}

func TestBuildRoleEndpointsSkipsSpeech(t *testing.T) {
	dir := t.TempDir()
	body := `{
  "model": "deepseek/deepseek-chat",
  "models": {
    "subagent": "deepseek/deepseek-v4-flash",
    "speech": "deepseek/deepseek-chat",
    "vision": "deepseek/deepseek-v4-flash",
    "web": "deepseek/deepseek-v4-flash"
  },
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.example.com", "apiKey": "sk-ok"},
      "models": {
        "deepseek-chat": {"name": "Chat", "contextWindow": 65536},
        "deepseek-v4-flash": {"name": "Flash", "contextWindow": 128000}
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, providerconfig.LocalFilename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	eps, err := BuildRoleEndpoints(dir, "deepseek/deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eps[providerconfig.RoleSpeech]; ok {
		t.Fatalf("speech must not be a chat endpoint: %v", keysOf(eps))
	}
	if _, ok := eps[providerconfig.RoleVision]; !ok {
		t.Fatalf("vision missing: %v", keysOf(eps))
	}
	if _, ok := eps[providerconfig.RoleWeb]; !ok {
		t.Fatalf("web missing: %v", keysOf(eps))
	}
	if _, ok := eps[providerconfig.RoleSubagent]; !ok {
		t.Fatalf("subagent missing: %v", keysOf(eps))
	}
}

func keysOf(m map[string]agent.RoleEndpoint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSelectModelRequiresChatBound(t *testing.T) {
	dir := t.TempDir()
	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-ok")
	rt, err := FromConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No Bind → agent nil
	_, err = rt.SelectModel(context.Background(), "deepseek/deepseek-v4-flash")
	if err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("err = %v", err)
	}
}

func TestSelectModelSuppressesReload(t *testing.T) {
	dir := t.TempDir()
	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-ok")
	rt, err := FromConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	main := &stubMain{}
	rt.Bind(main, nil, &stubSink{})
	cfg, err := rt.SelectModel(context.Background(), "deepseek/deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "deepseek/deepseek-v4-flash" || !cfg.Ready {
		t.Fatalf("cfg = %+v", cfg)
	}
	main.mu.Lock()
	setsAfterSwitch := main.sets
	main.mu.Unlock()
	// Immediate reload should be suppressed
	if err := rt.ReloadFromDisk(); err != nil {
		t.Fatal(err)
	}
	main.mu.Lock()
	sets := main.sets
	main.mu.Unlock()
	if sets != setsAfterSwitch {
		t.Fatalf("reload during suppress should not re-apply, sets %d → %d", setsAfterSwitch, sets)
	}
	rt.mu.Lock()
	rt.suppressUntil = time.Now().Add(-time.Second)
	rt.mu.Unlock()
}

func TestSnapshotNotReadyWithoutAgent(t *testing.T) {
	dir := t.TempDir()
	writeLocalConfig(t, dir, "deepseek/deepseek-chat", "https://api.example.com", "sk-ok")
	rt, err := FromConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, loadErr := rt.Snapshot()
	if cfg.Ready || loadErr == "" {
		t.Fatalf("cfg=%+v loadErr=%q", cfg, loadErr)
	}
	if !strings.Contains(loadErr, "restart") {
		t.Fatalf("loadErr = %q", loadErr)
	}
}

// ensure providerapi.Provider is used in package without unused import if tests evolve
var _ providerapi.Provider
