package providerconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesSelectedModelOptions(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "deepseek/deepseek-v4-flash",
  "provider": {
    "deepseek": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://api.deepseek.com"
      },
      "models": {
        "deepseek-v4-flash": {
          "name": "DeepSeek V4 Flash",
          "temperature": 0.25,
          "maxTokens": 2048,
          "contextWindow": 65536,
          "reasoningEffort": "high"
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
	if resolved.Temperature == nil || *resolved.Temperature != 0.25 {
		t.Fatalf("temperature = %v", resolved.Temperature)
	}
	if resolved.MaxTokens != 2048 || resolved.ContextWindow != 65536 || resolved.ReasoningEffort != "high" {
		t.Fatalf("resolved model options = %+v", resolved)
	}
	if resolved.ResponseFormat != "" {
		t.Fatalf("response format = %q, want automatic", resolved.ResponseFormat)
	}
}

func TestLoadFillsContextWindowFromCatalog(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "deepseek1/deepseek-v4-flash",
  "provider": {
    "deepseek1": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://api.deepseek.com"},
      "models": {"deepseek-v4-flash": {"name": "Flash"}}
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
	if resolved.MaxTokens != 0 {
		t.Fatalf("maxTokens should stay unset for omit-on-wire, got %d", resolved.MaxTokens)
	}
	if resolved.ContextWindow < 100_000 {
		t.Fatalf("contextWindow = %d, want catalog fill", resolved.ContextWindow)
	}
}

func TestLoadAcceptsLimitAlias(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "test/custom",
  "provider": {
    "test": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://provider.example"},
      "models": {"custom": {"name": "Custom", "limit": {"context": 200000, "output": 32000}}}
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
	if resolved.ContextWindow != 200000 || resolved.MaxTokens != 32000 {
		t.Fatalf("limit alias = %+v", resolved)
	}
}

func TestLoadExplicitFieldsBeatLimit(t *testing.T) {
	root := t.TempDir()
	config := `{
  "model": "test/custom",
  "provider": {
    "test": {
      "type": "openai-compatible",
      "options": {"baseURL": "https://provider.example"},
      "models": {
        "custom": {
          "maxTokens": 1111,
          "contextWindow": 2222,
          "limit": {"context": 999999, "output": 888888}
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
	if resolved.ContextWindow != 2222 || resolved.MaxTokens != 1111 {
		t.Fatalf("explicit should win: %+v", resolved)
	}
}

func TestLoadRejectsInvalidSelectedModelOptions(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		modelBlock string
		want       string
	}{
		{name: "temperature", provider: "openai-compatible", modelBlock: `"temperature": 2.1`, want: "temperature"},
		{name: "max tokens", provider: "openai-compatible", modelBlock: `"maxTokens": -1`, want: "maxTokens"},
		{name: "context window", provider: "openai-compatible", modelBlock: `"contextWindow": -1`, want: "contextWindow"},
		{name: "limit context", provider: "openai-compatible", modelBlock: `"limit": {"context": -1, "output": 1}`, want: "limit.context"},
		{name: "unsupported reasoning", provider: "anthropic", modelBlock: `"reasoningEffort": "high"`, want: "reasoningEffort"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			config := `{
  "model": "test/model",
  "provider": {
    "test": {
      "type": "` + test.provider + `",
      "options": {"baseURL": "https://provider.example"},
      "models": {"model": {` + test.modelBlock + `}}
    }
  }
}`
			if err := os.WriteFile(filepath.Join(root, LocalFilename), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
