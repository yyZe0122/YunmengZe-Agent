package providerconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesConfigDirLocalFirst(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, Filename), "user-provider/user-model", "https://user.example")
	writeConfig(t, filepath.Join(root, LocalFilename), "deepseek/deepseek-v4-flash", "https://api.deepseek.com")
	resolved, err := Load(root)
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

func TestLoadIgnoresProjectDirectory(t *testing.T) {
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
	resolved, err := Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != nil {
		t.Fatalf("expected nil when only project has config, got %+v", resolved)
	}
}

func TestLoadResolvesFileAPIKey(t *testing.T) {
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
	resolved, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "secret-value" || resolved.ResponseFormat != "json_object" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestLoadRejectsUnknownModel(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, LocalFilename), "deepseek/missing", "https://api.deepseek.com")
	if _, err := Load(root); err == nil {
		t.Fatal("expected model validation error")
	}
}

func TestLoadAcceptsMistypedCatalogKeyWithProviderPrefix(t *testing.T) {
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
	resolved, err := Load(root)
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

func TestLoadNestedModelIDWirePreserved(t *testing.T) {
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
	resolved, err := Load(root)
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

func TestLoadModelIDOverride(t *testing.T) {
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
	resolved, err := Load(root)
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

func TestLoadEmptyCatalogPassThrough(t *testing.T) {
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
	resolved, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProviderID != "provider-a" || resolved.ModelID != "any-model-id" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestLoadTwoProvidersSameModelID(t *testing.T) {
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
	resolved, err := Load(root)
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

func TestWriteSelectedModelUpdatesTopLevelOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LocalFilename)
	content := `{
  "model": "deepseek/deepseek-v4-flash",
  "provider": {
    "deepseek": {
      "options": {
        "baseURL": "https://api.deepseek.com",
        "apiKey": "{env:TEST_KEY}"
      },
      "models": {
        "deepseek-v4-flash": {},
        "deepseek-chat": {"name": "chat"}
      }
    }
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := WriteSelectedModel(root, "deepseek/deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if written != path {
		t.Fatalf("written = %q want %q", written, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model": "deepseek/deepseek-chat"`) {
		t.Fatalf("model not updated: %s", raw)
	}
	if !strings.Contains(string(raw), `{env:TEST_KEY}`) {
		t.Fatalf("apiKey placeholder lost: %s", raw)
	}
	if err := validateModelInFile(path, "deepseek", "deepseek-chat"); err != nil {
		t.Fatal(err)
	}
	if err := validateModelInFile(path, "deepseek", "missing"); err == nil {
		t.Fatal("expected missing model error")
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
	// template uses env key; Load fails without env — but ListModelRefs works
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
	if !strings.Contains(string(rawCfg), `"subagent"`) || !strings.Contains(string(rawCfg), `"compact"`) {
		t.Fatalf("default template missing models.subagent/compact: %s", rawCfg)
	}
	agents := filepath.Join(root, AgentsFilename)
	raw, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "用户没要求的不要做") {
		t.Fatalf("default AGENTS.md missing rules: %s", raw)
	}
	if !strings.Contains(string(raw), "拿不准的先问用户") {
		t.Fatalf("default AGENTS.md missing conservative cleanup: %s", raw)
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
	providerID, _, _ := cutModel(model)
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
		content = sprintf(content, model, providerID, baseURL)
	} else {
		_, modelID, _ := cutModel(model)
		content = `{
  "model": %q,
  "provider": {
    %q: {
      "options": {"baseURL": %q},
      "models": {%q: {}}
    }
  }
}`
		content = sprintf(content, model, providerID, baseURL, modelID)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cutModel(value string) (string, string, bool) {
	for i := range value {
		if value[i] == '/' {
			return value[:i], value[i+1:], true
		}
	}
	return value, "", false
}

func sprintf(format string, values ...any) string {
	return fmt.Sprintf(format, values...)
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

func TestLoadResolvesHeaderPlaceholders(t *testing.T) {
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
	resolved, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Protocol != ProtocolAnthropicMessages || resolved.Headers["X-Custom-Token"] != "header-secret" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestLoadChatDefaults(t *testing.T) {
	root := t.TempDir()
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.AllowWrite != nil || chat.Workspace != nil {
		t.Fatalf("empty dir chat = %+v", chat)
	}
	if !chat.AgentWriteCeiling() {
		t.Fatal("missing chat must allow agent write by default")
	}
	writeConfig(t, filepath.Join(root, Filename), "p/m", "https://example.com")
	chat, err = LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !chat.AgentWriteCeiling() {
		t.Fatal("missing chat block must default agent write ceiling true")
	}
	if roots := chat.EffectiveRoots("/fallback"); len(roots) != 1 || roots[0] != "/fallback" {
		t.Fatalf("EffectiveRoots = %v", roots)
	}
}

func TestLoadChatParsesAndValidates(t *testing.T) {
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
    "workspace": {"allow": ["/abs/workspace"]},
    "allow_write": true,
    "tools": {"git": true, "process": false}
  }
}`
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.AllowWrite == nil || !*chat.AllowWrite || chat.Workspace == nil || len(chat.Workspace.Allow) != 1 || chat.Workspace.Allow[0] != "/abs/workspace" {
		t.Fatalf("chat = %+v", chat)
	}
	if !chat.AgentWriteCeiling() {
		t.Fatal("allow_write true must raise ceiling")
	}
	if !chat.AgentGitEnabled() || chat.AgentProcessEnabled() {
		t.Fatalf("tools flags: git=%v process=%v", chat.AgentGitEnabled(), chat.AgentProcessEnabled())
	}
	if roots := chat.EffectiveRoots("/fallback"); len(roots) != 1 || roots[0] != "/abs/workspace" {
		t.Fatalf("EffectiveRoots = %v", roots)
	}
	if !chat.CompactionEnabled() || chat.MaxIterationsOrDefault() != 0 {
		t.Fatalf("defaults: compaction=%v max_iter=%d", chat.CompactionEnabled(), chat.MaxIterationsOrDefault())
	}

	bad := `{
  "model": "deepseek/deepseek-chat",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {"deepseek-chat": {}}
    }
  },
   "chat": {"workspace": {"allow": ["relative/path"]}}
}`
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected relative workspace.allow error")
	}
}

func TestLoadChatCompactionAndMaxIterations(t *testing.T) {
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
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.CompactionEnabled() {
		t.Fatal("expected compaction disabled")
	}
	if chat.MaxIterationsOrDefault() != 32 {
		t.Fatalf("max_iterations = %d", chat.MaxIterationsOrDefault())
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
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected max_iterations range error")
	}
}

func TestLoadChatPermissionMode(t *testing.T) {
	root := t.TempDir()
	base := `{
  "model": "deepseek/deepseek-chat",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {"deepseek-chat": {}}
    }
  },
  "chat": %s
}`
	write := func(chatJSON string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, Filename), []byte(fmt.Sprintf(base, chatJSON)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"permission": {"mode": "ask"}}`)
	if _, err := LoadChat(root); err != nil {
		t.Fatalf("legacy ask mode must still load: %v", err)
	}
	write(`{"permission": {"mode": "preauth"}}`)
	if _, err := LoadChat(root); err != nil {
		t.Fatalf("legacy preauth mode must still load: %v", err)
	}
	write(`{"permission": {"mode": "auto"}}`)
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected auto permission mode error")
	}
	write(`{"permission": {"mode": "wide-open"}}`)
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected invalid permission mode error")
	}
	write(`{"permission": {"allow": ["process"]}}`)
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !chat.AgentProcessEnabled() || chat.AgentGitEnabled() {
		t.Fatalf("allow process: process=%v git=%v", chat.AgentProcessEnabled(), chat.AgentGitEnabled())
	}
	write(`{"permission": {"allow": ["http"]}}`)
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected invalid permission.allow error")
	}
}

func TestLoadChatWeb(t *testing.T) {
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
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.WebSearchBackend() != WebSearchDDG {
		t.Fatalf("default backend = %q", chat.WebSearchBackend())
	}
	write(`{"search": "searxng"}`)
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected missing searxng_url")
	}
	write(`{"search": "searxng", "searxng_url": "http://127.0.0.1:8888"}`)
	chat, err = LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.WebSearchBackend() != WebSearchSearxNG || chat.Web.SearxNGURL != "http://127.0.0.1:8888" {
		t.Fatalf("searxng = %+v", chat.Web)
	}
	t.Setenv("YMZ_TEST_TAVILY", "tvly-test")
	write(`{"search": "tavily", "tavily_key": "{env:YMZ_TEST_TAVILY}"}`)
	chat, err = LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Web.TavilyKey != "tvly-test" {
		t.Fatalf("tavily key = %q", chat.Web.TavilyKey)
	}
	write(`{"search": "bing"}`)
	if _, err := LoadChat(root); err == nil {
		t.Fatal("expected unknown backend")
	}
}

func TestLoadModelRolesAndValidation(t *testing.T) {
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
	main, roles, err := LoadModelRoles(root)
	if err != nil {
		t.Fatal(err)
	}
	if main != "deepseek/deepseek-chat" || roles != nil {
		t.Fatalf("main=%q roles=%v", main, roles)
	}
	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}

	write(`{"compact": "openai/gpt-cheap", "subagent": "deepseek/deepseek-flash"}`)
	main, roles, err = LoadModelRoles(root)
	if err != nil {
		t.Fatal(err)
	}
	if main != "deepseek/deepseek-chat" {
		t.Fatalf("main = %q", main)
	}
	if roles[RoleCompact] != "openai/gpt-cheap" || roles[RoleSubagent] != "deepseek/deepseek-flash" {
		t.Fatalf("roles = %v", roles)
	}
	if _, err := Load(root); err != nil {
		t.Fatal(err)
	}

	write(`{"compact": ""}`)
	main, roles, err = LoadModelRoles(root)
	if err != nil {
		t.Fatal(err)
	}
	if main != "deepseek/deepseek-chat" || roles != nil {
		t.Fatalf("empty compact should omit role: main=%q roles=%v", main, roles)
	}

	write(`{"main": "deepseek/deepseek-flash"}`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected models.main rejection")
	}
	write(`{"vision": "deepseek/deepseek-chat"}`)
	main, roles, err = LoadModelRoles(root)
	if err != nil {
		t.Fatal(err)
	}
	if roles[RoleVision] != "deepseek/deepseek-chat" {
		t.Fatalf("vision role = %v", roles)
	}
	write(`{"tts": "deepseek/deepseek-chat"}`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected unknown role rejection")
	}
	write(`{"web": "deepseek/deepseek-flash"}`)
	main, roles, err = LoadModelRoles(root)
	if err != nil {
		t.Fatal(err)
	}
	if main != "deepseek/deepseek-chat" || roles[RoleWeb] != "deepseek/deepseek-flash" {
		t.Fatalf("web role: main=%q roles=%v", main, roles)
	}
	write(`{"compact": "missing/model"}`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected missing provider rejection")
	}
	write(`{"compact": "deepseek/nope"}`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected missing model rejection")
	}
	write(`{"compact": "not-a-ref"}`)
	if _, err := Load(root); err == nil {
		t.Fatal("expected bad ref format rejection")
	}
}

func TestAppendWorkspaceAllowCreatesLocal(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, Filename), "deepseek/deepseek-chat", "https://api.deepseek.com")
	path, err := AppendWorkspaceAllow(root, "/tmp/extra-root")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != LocalFilename {
		t.Fatalf("path = %s", path)
	}
	chat, err := LoadChat(root)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Workspace == nil || len(chat.Workspace.Allow) != 1 || chat.Workspace.Allow[0] != "/tmp/extra-root" {
		t.Fatalf("allow = %#v", chat.Workspace)
	}
	if _, err := AppendWorkspaceAllow(root, "/"); err == nil {
		t.Fatal("filesystem root must be rejected")
	}
}
