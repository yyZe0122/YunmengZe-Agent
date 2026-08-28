package openairesponses

import (
	"encoding/json"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestRequestBodyIncludesModelParameters(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	temperature := 0.25
	body, err := provider.requestBody(providerapi.CompletionRequest{
		Model: "test-model", Messages: []providerapi.Message{{Role: providerapi.RoleUser, Content: "hello"}},
		MaxOutputTokens: 2048, Temperature: &temperature, ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		MaxOutputTokens int64   `json:"max_output_tokens"`
		Temperature     float64 `json:"temperature"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MaxOutputTokens != 2048 || payload.Temperature != temperature || payload.Reasoning.Effort != "high" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRequestBodyRejectsImages(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.requestBody(providerapi.CompletionRequest{
		Model: "m", Messages: []providerapi.Message{{Role: providerapi.RoleUser, Content: "x"}},
		Images: []providerapi.ImagePart{{MIME: "image/png", Base64: "AA"}},
	})
	if err == nil {
		t.Fatal("expected images rejection")
	}
}
