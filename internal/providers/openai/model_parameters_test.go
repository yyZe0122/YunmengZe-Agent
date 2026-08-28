package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestRequestBodyIncludesModelParameters(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	temperature := 0.25
	request := structuredResponseRequest()
	request.MaxOutputTokens = 2048
	request.Temperature = &temperature
	request.ReasoningEffort = "high"
	body, err := provider.requestBody(request, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		MaxTokens       int64   `json:"max_tokens"`
		Temperature     float64 `json:"temperature"`
		ReasoningEffort string  `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MaxTokens != 2048 || payload.Temperature != temperature || payload.ReasoningEffort != "high" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestRequestBodyOmitsMaxTokensWhenUnset(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := structuredResponseRequest()
	request.MaxOutputTokens = 0
	body, err := provider.requestBody(request, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["max_tokens"]; ok {
		t.Fatalf("max_tokens present: %s", body)
	}
}

func TestRequestBodySerializesImages(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := providerapi.CompletionRequest{
		Model:    "gpt-4o-mini",
		Messages: []providerapi.Message{{Role: providerapi.RoleUser, Content: "what"}},
		Images:   []providerapi.ImagePart{{MIME: "image/png", Base64: "AAAA"}},
	}
	body, err := provider.requestBody(request, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "image_url") || !strings.Contains(string(body), "data:image/png;base64,AAAA") {
		t.Fatalf("body = %s", body)
	}
	plain := structuredResponseRequest()
	plainBody, err := provider.requestBody(plain, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plainBody), "image_url") {
		t.Fatalf("plain request leaked images: %s", plainBody)
	}
}
