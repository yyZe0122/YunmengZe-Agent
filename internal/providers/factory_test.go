package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestNewConfiguredUsesSelectedProtocol(t *testing.T) {
	tests := []struct {
		name           string
		protocol       string
		expectedPath   string
		expectedHeader string
		response       string
		assertBody     func(*testing.T, map[string]any)
	}{
		{
			name: "openai chat", protocol: providerconfig.ProtocolOpenAIChat,
			expectedPath: "/v1/chat/completions", expectedHeader: "Authorization",
			response: `{"choices":[{"message":{"content":"chat"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				requireKey(t, body, "messages")
				requireKey(t, body, "tools")
				requireKey(t, body, "response_format")
			},
		},
		{
			name: "openai responses", protocol: providerconfig.ProtocolOpenAIResponses,
			expectedPath: "/v1/responses", expectedHeader: "Authorization",
			response: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"responses"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				requireKey(t, body, "input")
				requireKey(t, body, "tools")
				requireKey(t, body, "text")
			},
		},
		{
			name: "anthropic", protocol: providerconfig.ProtocolAnthropicMessages,
			expectedPath: "/v1/messages", expectedHeader: "x-api-key",
			response: `{"content":[{"type":"text","text":"anthropic"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				requireKey(t, body, "messages")
				requireKey(t, body, "max_tokens")
				requireKey(t, body, "tools")
				requireKey(t, body, "output_config")
			},
		},
		{
			name: "gemini", protocol: providerconfig.ProtocolGeminiGenerate,
			expectedPath: "/v1beta/models/test-model:generateContent", expectedHeader: "x-goog-api-key",
			response: `{"candidates":[{"content":{"parts":[{"text":"gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
			assertBody: func(t *testing.T, body map[string]any) {
				requireKey(t, body, "contents")
				requireKey(t, body, "tools")
				requireKey(t, body, "generationConfig")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.RequestURI() != test.expectedPath {
					t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
				}
				if got := request.Header.Get(test.expectedHeader); got == "" {
					t.Errorf("missing %s", test.expectedHeader)
				}
				if got := request.Header.Get("X-YunmengZe-Test"); got != "configured" {
					t.Errorf("custom header = %q", got)
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				} else {
					test.assertBody(t, body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))
			defer server.Close()

			provider, err := NewConfigured(providerconfig.Resolved{
				ProviderID: "test", Protocol: test.protocol, ModelID: "test-model", BaseURL: server.URL,
				APIKey: "secret", Headers: map[string]string{"X-YunmengZe-Test": "configured"},
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := provider.Complete(context.Background(), providerapi.CompletionRequest{
				Model: "test-model", Messages: []providerapi.Message{{Role: providerapi.RoleUser, Content: "hello"}},
				MaxOutputTokens: 64,
				Tools:           []providerapi.ToolDefinition{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
				ResponseSchema:  &providerapi.JSONSchema{Name: "answer", Strict: true, Schema: json.RawMessage(`{"type":"object"}`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if response.Content == "" || response.Usage.TotalTokens != 3 {
				t.Fatalf("response = %+v", response)
			}
		})
	}
}

func requireKey(t *testing.T, object map[string]any, key string) {
	t.Helper()
	if _, ok := object[key]; !ok {
		t.Errorf("request body missing %q: %#v", key, object)
	}
}
