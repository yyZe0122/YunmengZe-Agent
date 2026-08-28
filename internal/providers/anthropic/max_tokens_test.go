package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestRequestBodyUsesCatalogOrDefaultMaxTokens(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := provider.requestBody(providerapi.CompletionRequest{
		Model:    "claude-sonnet-5",
		Messages: []providerapi.Message{{Role: providerapi.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MaxTokens != 128000 && payload.MaxTokens != modelcatalog.Lookup("claude-sonnet-5").Output {
		t.Fatalf("max_tokens = %d", payload.MaxTokens)
	}
}
