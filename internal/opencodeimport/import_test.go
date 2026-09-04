package opencodeimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
)

func TestConvertBasicProviderAndModel(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "model": "deepseek/deepseek-v4-flash",
  "provider": {
    "deepseek": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "apiKey": "{env:DEEPSEEK_API_KEY}",
        "baseURL": "https://api.deepseek.com/v1",
        "setCacheKey": true
      },
      "models": {
        "deepseek-v4-flash": { "name": "Flash" },
        "deepseek-v4-pro": { "name": "Pro", "id": "deepseek-reasoner" }
      }
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("model=%q", res.File.Model)
	}
	p, ok := res.File.Provider["deepseek"]
	if !ok {
		t.Fatal("missing provider deepseek")
	}
	if p.Type != "openai-compatible" {
		t.Fatalf("type=%q", p.Type)
	}
	if p.Options.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("baseURL=%q", p.Options.BaseURL)
	}
	if p.Options.APIKey != "{env:DEEPSEEK_API_KEY}" {
		t.Fatalf("apiKey=%q", p.Options.APIKey)
	}
	if p.Models["deepseek-v4-pro"].ID != "deepseek-reasoner" {
		t.Fatalf("wire id=%q", p.Models["deepseek-v4-pro"].ID)
	}
	foundDrop := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "setCacheKey") {
			foundDrop = true
			break
		}
	}
	if !foundDrop {
		t.Fatalf("expected setCacheKey drop warning, got %v", res.Warnings)
	}
}

func TestConvertDropsPluginsLSP(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "model": "a/b",
  "plugin": ["./x.ts"],
  "lsp": { "gopls": {} },
  "provider": {
    "a": {
      "options": { "baseURL": "https://example.com", "apiKey": "sk-x" },
      "models": { "b": { "name": "B" } }
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "plugin") {
		t.Fatalf("want plugin warning: %s", joined)
	}
	if !strings.Contains(joined, "lsp") {
		t.Fatalf("want lsp warning: %s", joined)
	}
}

func TestConvertMCPLocalAndRemote(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "model": "a/b",
  "provider": {
    "a": {
      "options": { "baseURL": "https://example.com", "apiKey": "sk-x" },
      "models": { "b": {} }
    }
  },
  "mcp": {
    "localfs": {
      "type": "local",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    },
    "remote": {
      "type": "remote",
      "url": "https://mcp.example.com/sse"
    },
    "simple": {
      "command": "uvx",
      "args": ["mcp-server-git"]
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.MCP == nil || len(res.File.MCP.Servers) != 3 {
		t.Fatalf("mcp servers=%v", res.File.MCP)
	}
	local := res.File.MCP.Servers["localfs"]
	if local.Command != "npx" || len(local.Args) != 3 {
		t.Fatalf("localfs=%+v", local)
	}
	simple := res.File.MCP.Servers["simple"]
	if simple.Command != "uvx" || len(simple.Args) != 1 || simple.Args[0] != "mcp-server-git" {
		t.Fatalf("simple=%+v", simple)
	}
	remote := res.File.MCP.Servers["remote"]
	if remote.URL != "https://mcp.example.com/sse" {
		t.Fatalf("remote=%+v", remote)
	}
	if remote.Type != "remote" && remote.Type != "sse" && remote.Type != "http" {
		t.Fatalf("remote type=%q", remote.Type)
	}
}

func TestConvertRoleModels(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "model": "a/b",
  "models": {
    "subagent": "a/b",
    "compact": "a/c",
    "main": "a/b",
    "title": "a/b"
  },
  "provider": {
    "a": {
      "options": { "baseURL": "https://example.com", "apiKey": "sk-x" },
      "models": { "b": {}, "c": {} }
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Models["subagent"] != "a/b" {
		t.Fatalf("subagent=%q", res.File.Models["subagent"])
	}
	if res.File.Models["compact"] != "a/c" {
		t.Fatalf("compact=%q", res.File.Models["compact"])
	}
	if _, ok := res.File.Models["main"]; ok {
		t.Fatal("models.main must be dropped")
	}
	if _, ok := res.File.Models["title"]; ok {
		t.Fatal("unknown role title must be dropped")
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "models.main") {
		t.Fatalf("want models.main warning: %s", joined)
	}
	if !strings.Contains(joined, "models.title") {
		t.Fatalf("want models.title warning: %s", joined)
	}
}

func TestConvertCompaction(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "model": "a/b",
  "provider": {
    "a": {
      "options": { "baseURL": "https://example.com", "apiKey": "sk-x" },
      "models": { "b": {} }
    }
  },
  "compaction": { "auto": false, "prune": true }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Chat == nil || res.File.Chat.Compaction == nil || res.File.Chat.Compaction.Enabled == nil {
		t.Fatal("expected chat.compaction.enabled")
	}
	if *res.File.Chat.Compaction.Enabled {
		t.Fatal("want enabled=false from auto=false")
	}
}

func TestConvertDefaultModelAndEnvKey(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "provider": {
    "omni": {
      "npm": "@ai-sdk/openai-compatible",
      "env": ["OMNI_API_KEY"],
      "options": { "baseURL": "http://localhost:1/v1" },
      "models": { "gc/grok-4.5": { "name": "grok" } }
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Model != "omni/gc/grok-4.5" {
		t.Fatalf("model=%q", res.File.Model)
	}
	if res.File.Provider["omni"].Options.APIKey != "{env:OMNI_API_KEY}" {
		t.Fatalf("apiKey=%q", res.File.Provider["omni"].Options.APIKey)
	}
}

func TestConvertJSONC(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  // comment
  "model": "a/b",
  "provider": {
    "a": {
      "options": { "baseURL": "https://example.com", "apiKey": "sk-x" },
      "models": { "b": {} }
    }
  }
}`)
	res, err := Convert(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Model != "a/b" {
		t.Fatalf("model=%q", res.File.Model)
	}
}

func TestWriteLocalRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := providerconfig.File{
		Model: "a/b",
		Provider: map[string]providerconfig.Provider{
			"a": {
				Type: "openai-compatible",
				Options: providerconfig.ProviderOptions{
					BaseURL: "https://example.com",
					APIKey:  "sk-test-literal",
				},
				Models: map[string]providerconfig.Model{
					"b": {Name: "B"},
				},
			},
		},
	}
	path, err := WriteLocal(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != providerconfig.LocalFilename {
		t.Fatalf("path=%s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded providerconfig.File
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Model != "a/b" {
		t.Fatalf("decoded model=%q", decoded.Model)
	}
	// Load path should resolve
	resolved, err := providerconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.SelectionRef != "a/b" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestConvertFileMissing(t *testing.T) {
	t.Parallel()
	_, err := ConvertFile(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error")
	}
}
