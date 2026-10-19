package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotUsesConfigDirLocalFirst(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, Filename), "user-provider/user-model", "https://user.example")
	writeConfig(t, filepath.Join(root, LocalFilename), "deepseek/deepseek-v4-flash", "https://api.deepseek.com")
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.ProviderID != "deepseek" || resolved.ModelID != "deepseek-v4-flash" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.SelectionRef != "deepseek/deepseek-v4-flash" {
		t.Fatalf("selection = %q", resolved.SelectionRef)
	}
	if resolved.Source != filepath.Join(root, LocalFilename) {
		t.Fatalf("source = %q", resolved.Source)
	}
}

func TestSnapshotIgnoresProjectDirectory(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(projectDir, LocalFilename), "deepseek/deepseek-v4-flash", "https://api.deepseek.com")
	_, err := LoadSnapshot(configDir)
	if err == nil || !strings.Contains(err.Error(), "provider config not found") {
		t.Fatalf("expected missing config when only project has config, got %v", err)
	}
}

func TestSnapshotResolvesFileAPIKey(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "key.txt"), []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `{
  "model": "deepseek/deepseek-v4-flash",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://api.deepseek.com",
        "apiKey": "{file:key.txt}",
        "responseFormat": "json_object"
      },
      "models": {"deepseek-v4-flash": {}}
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "secret-value" || resolved.ResponseFormat != "json_object" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestSnapshotRejectsUnknownModel(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, LocalFilename), "deepseek/missing", "https://api.deepseek.com")
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected model validation error")
	}
}

func TestSnapshotAcceptsMistypedCatalogKeyWithProviderPrefix(t *testing.T) {
	root := t.TempDir()
	// Mistaken catalog key "provider/model" while selection model segment is bare.
	config := `{
  "model": "deepseek/deepseek-v4-flash",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://llm.example.com",
        "apiKey": "sk-test"
      },
      "models": {
        "deepseek/deepseek-v4-flash": { "name": "Flash" }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	// Wire id is the selection model segment (OpenCode api.id default), not the mistaken key.
	if resolved.ProviderID != "deepseek" || resolved.ModelID != "deepseek-v4-flash" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.SelectionRef != "deepseek/deepseek-v4-flash" {
		t.Fatalf("selection = %q", resolved.SelectionRef)
	}
}

func TestSnapshotNestedModelIDWirePreserved(t *testing.T) {
	root := t.TempDir()
	// OpenCode/NewAPI: model segment may contain '/' (e.g. deepseek/deepseek-v4-flash).
	config := `{
  "model": "ziy/deepseek/deepseek-v4-flash",
  "provider": {
    "ziy": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://llm.example.com/v1",
        "apiKey": "sk-test"
      },
      "models": {
        "deepseek/deepseek-v4-flash": { "name": "Flash" }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProviderID != "ziy" {
		t.Fatalf("provider = %q", resolved.ProviderID)
	}
	if resolved.SelectionRef != "ziy/deepseek/deepseek-v4-flash" {
		t.Fatalf("selection = %q", resolved.SelectionRef)
	}
	if resolved.ModelID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("wire model = %q want deepseek/deepseek-v4-flash", resolved.ModelID)
	}
	selected, refs, err := ListModelRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "ziy/deepseek/deepseek-v4-flash" {
		t.Fatalf("selected = %q", selected)
	}
	var found bool
	for _, r := range refs {
		if r.ID == "ziy/deepseek/deepseek-v4-flash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestSnapshotModelIDOverride(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "ziy/flash",
  "provider": {
    "ziy": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://llm.example.com/v1",
        "apiKey": "sk-test"
      },
      "models": {
        "flash": {
          "name": "Flash",
          "id": "deepseek/deepseek-v4-flash"
        }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SelectionRef != "ziy/flash" {
		t.Fatalf("selection = %q", resolved.SelectionRef)
	}
	if resolved.ModelID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("wire model = %q", resolved.ModelID)
	}
}

func TestSnapshotEmptyCatalogPassThrough(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "provider-a/any-model-id",
  "provider": {
    "provider-a": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://a.example.com",
        "apiKey": "sk-a"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProviderID != "provider-a" || resolved.ModelID != "any-model-id" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestSnapshotTwoProvidersSameModelID(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "provider-b/shared-model",
  "provider": {
    "provider-a": {
      "type": "openai-compatible",
      "options": { "baseURL": "https://a.example.com", "apiKey": "sk-a" },
      "models": { "shared-model": { "name": "A" } }
    },
    "provider-b": {
      "type": "openai-compatible",
      "options": { "baseURL": "https://b.example.com", "apiKey": "sk-b" },
      "models": { "shared-model": { "name": "B" } }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProviderID != "provider-b" || resolved.BaseURL != "https://b.example.com" {
		t.Fatalf("resolved = %+v", resolved)
	}
	selected, refs, err := ListModelRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "provider-b/shared-model" {
		t.Fatalf("selected = %q", selected)
	}
	var hasA, hasB bool
	for _, r := range refs {
		if r.ID == "provider-a/shared-model" {
			hasA = true
		}
		if r.ID == "provider-b/shared-model" {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestEnsureConfigDoesNotCopyFromOtherDirs(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(projectDir, LocalFilename), "deepseek/deepseek-v4-pro", "https://api.deepseek.com")
	result, err := EnsureConfig(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Path != filepath.Join(configDir, Filename) {
		t.Fatalf("ensure = %+v", result)
	}
}

func TestEnsureConfigWritesDefaultWhenEmpty(t *testing.T) {
	root := t.TempDir()
	result, err := EnsureConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Path != filepath.Join(root, Filename) {
		t.Fatalf("ensure = %+v", result)
	}
	// template uses env key; LoadSnapshot fails without env — but ListModelRefs works
	selected, models, err := ListModelRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "deepseek1/deepseek-chat" || len(models) == 0 {
		t.Fatalf("selected=%q models=%v", selected, models)
	}
	rawCfg, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawCfg), `"permission"`) || len(models) != 1 {
		t.Fatalf("default template must contain one model and no permission settings: %s", rawCfg)
	}
	agents := filepath.Join(root, AgentsFilename)
	raw, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "plan 只读分析") {
		t.Fatalf("default AGENTS.md missing rules: %s", raw)
	}
	if !strings.Contains(string(raw), "agent 直接执行工具") {
		t.Fatalf("default AGENTS.md missing direct execution semantics: %s", raw)
	}
	if strings.Contains(string(raw), "\t4.") || strings.Contains(string(raw), "\t5.") {
		t.Fatalf("default AGENTS.md has indented list items: %s", raw)
	}
	created, err := EnsureAgentsFile(root)
	if err != nil || created {
		t.Fatalf("second ensure created=%v err=%v", created, err)
	}
}

func writeConfig(t *testing.T, path, model, baseURL string) {
	t.Helper()
	providerID, _, _ := strings.Cut(model, "/")
	content := `{
  "model": %q,
  "provider": {
    %q: {
      "options": {"baseURL": %q},
      "models": {"configured-model": {}}
    }
  }
}`
	if model == "deepseek/missing" {
		content = fmt.Sprintf(content, model, providerID, baseURL)
	} else {
		_, modelID, _ := strings.Cut(model, "/")
		content = `{
  "model": %q,
  "provider": {
    %q: {
      "options": {"baseURL": %q},
      "models": {%q: {}}
    }
  }
}`
		content = fmt.Sprintf(content, model, providerID, baseURL, modelID)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProtocolAliases(t *testing.T) {
	tests := map[string]string{
		"openai-compatible": ProtocolOpenAIChat,
		"openai-compat":     ProtocolOpenAIChat,
		"ollama":            ProtocolOpenAIChat,
		"openai":            ProtocolOpenAIResponses,
		"responses":         ProtocolOpenAIResponses,
		"anthropic":         ProtocolAnthropicMessages,
		"claude":            ProtocolAnthropicMessages,
		"gemini":            ProtocolGeminiGenerate,
		"google":            ProtocolGeminiGenerate,
	}
	for alias, expected := range tests {
		t.Run(alias, func(t *testing.T) {
			resolved, err := resolveProtocol("", alias)
			if err != nil {
				t.Fatal(err)
			}
			if resolved != expected {
				t.Fatalf("resolved = %q, want %q", resolved, expected)
			}
		})
	}
}

func TestResolveProtocolRejectsConflictingFields(t *testing.T) {
	if _, err := resolveProtocol(ProtocolAnthropicMessages, "openai-compatible"); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestSnapshotResolvesHeaderPlaceholders(t *testing.T) {
	root := t.TempDir()
	t.Setenv("YMZ_TEST_HEADER", "header-secret")
	config := `{
  "model": "custom/model",
  "provider": {
    "custom": {
      "type": "anthropic",
      "options": {
        "baseURL": "https://provider.example",
        "headers": {"X-Custom-Token": "{env:YMZ_TEST_HEADER}"}
      },
      "models": {"model": {}}
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(snapshot.Main)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Protocol != ProtocolAnthropicMessages || resolved.Headers["X-Custom-Token"] != "header-secret" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestSnapshotChatCompactionAndMaxIterations(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "deepseek/deepseek-chat",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {"deepseek-chat": {}}
    }
  },
  "chat": {
    "compaction": {"enabled": false},
    "max_iterations": 32
  }
}`
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Chat.CompactionEnabled() {
		t.Fatal("expected compaction disabled")
	}
	if snapshot.Chat.MaxIterations != 32 {
		t.Fatalf("max_iterations = %d", snapshot.Chat.MaxIterations)
	}

	badIter := `{
  "model": "deepseek/deepseek-chat",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {"deepseek-chat": {}}
    }
  },
  "chat": {"max_iterations": 300}
}`
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(badIter), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected max_iterations range error")
	}
}

func TestSnapshotChatWeb(t *testing.T) {
	root := t.TempDir()
	write := func(web string) {
		t.Helper()
		body := fmt.Sprintf(`{
  "model": "deepseek/deepseek-chat",
  "provider": {"deepseek": {"type": "openai-compatible", "options": {"baseURL": "https://api.deepseek.com"}, "models": {"deepseek-chat": {}}}},
  "chat": {"web": %s}
}`, web)
		if err := os.WriteFile(filepath.Join(root, Filename), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{}`)
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Chat.WebSearchBackend() != WebSearchDDG {
		t.Fatalf("default backend = %q", snapshot.Chat.WebSearchBackend())
	}
	write(`{"search": "searxng"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected missing searxng_url")
	}
	write(`{"search": "searxng", "searxng_url": "http://127.0.0.1:8888"}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Chat.WebSearchBackend() != WebSearchSearxNG || snapshot.Chat.Web.SearxNGURL != "http://127.0.0.1:8888" {
		t.Fatalf("searxng = %+v", snapshot.Chat.Web)
	}
	t.Setenv("YMZ_TEST_TAVILY", "tvly-test")
	write(`{"search": "tavily", "tavily_key": "{env:YMZ_TEST_TAVILY}"}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Chat.Web.TavilyKey != "tvly-test" {
		t.Fatalf("tavily key = %q", snapshot.Chat.Web.TavilyKey)
	}
	write(`{"search": "bing"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected unknown backend")
	}
}

func TestSnapshotModelRolesAndValidation(t *testing.T) {
	root := t.TempDir()
	base := `{
  "model": "deepseek/deepseek-chat",
  "models": %s,
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {
        "deepseek-chat": {},
        "deepseek-flash": {}
      }
    },
    "openai": {
      "type": "openai",
      "options": {"baseURL": "https://api.openai.com"},
      "models": {"gpt-cheap": {}}
    }
  }
}`
	write := func(modelsJSON string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, Filename), []byte(fmt.Sprintf(base, modelsJSON)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`{}`)
	snapshot, err := LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Main != "deepseek/deepseek-chat" || len(snapshot.Roles) != 0 {
		t.Fatalf("main=%q roles=%v", snapshot.Main, snapshot.Roles)
	}

	write(`{"compact": "openai/gpt-cheap", "subagent": "deepseek/deepseek-flash"}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Main != "deepseek/deepseek-chat" {
		t.Fatalf("main = %q", snapshot.Main)
	}
	if snapshot.Roles[RoleCompact] != "openai/gpt-cheap" || snapshot.Roles[RoleSubagent] != "deepseek/deepseek-flash" {
		t.Fatalf("roles = %v", snapshot.Roles)
	}

	write(`{"compact": ""}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Main != "deepseek/deepseek-chat" || len(snapshot.Roles) != 0 {
		t.Fatalf("empty compact should omit role: main=%q roles=%v", snapshot.Main, snapshot.Roles)
	}

	write(`{"main": "deepseek/deepseek-flash"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected models.main rejection")
	}
	write(`{"vision": "deepseek/deepseek-chat"}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Roles[RoleVision] != "deepseek/deepseek-chat" {
		t.Fatalf("vision role = %v", snapshot.Roles)
	}
	write(`{"tts": "deepseek/deepseek-chat"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected unknown role rejection")
	}
	write(`{"web": "deepseek/deepseek-flash"}`)
	snapshot, err = LoadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Main != "deepseek/deepseek-chat" || snapshot.Roles[RoleWeb] != "deepseek/deepseek-flash" {
		t.Fatalf("web role: main=%q roles=%v", snapshot.Main, snapshot.Roles)
	}
	write(`{"compact": "missing/model"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected missing provider rejection")
	}
	write(`{"compact": "deepseek/nope"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected missing model rejection")
	}
	write(`{"compact": "not-a-ref"}`)
	if _, err := LoadSnapshot(root); err == nil {
		t.Fatal("expected bad ref format rejection")
	}
}
