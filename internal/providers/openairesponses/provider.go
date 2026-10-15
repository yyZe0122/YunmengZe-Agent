// Package openairesponses implements the OpenAI Responses protocol.
package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type Config struct {
	Name          string
	BaseURL       string
	APIKeyRef     string
	Resolver      providerapi.SecretResolver
	HTTPClient    *http.Client
	MaxBodyBytes  int64
	ResponsesPath string
	Headers       map[string]string
}

type Provider struct {
	name          string
	baseURL       *url.URL
	apiKeyRef     string
	resolver      providerapi.SecretResolver
	client        *http.Client
	maxBodyBytes  int64
	responsesPath string
	headers       http.Header
}

func New(config Config) (*Provider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "openai"
	}
	baseURL, err := providerhttp.ParseBaseURL("openai-responses", config.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.APIKeyRef) != "" && config.Resolver == nil {
		return nil, errors.New("secret resolver is required when API key reference is configured")
	}
	client := config.HTTPClient
	if client == nil {
		client = providerhttp.NewClient()
	}
	maxBody := config.MaxBodyBytes
	if maxBody == 0 {
		maxBody = providerhttp.DefaultMaxResponseBytes
	}
	if maxBody < 1 {
		return nil, errors.New("maximum response body size must be positive")
	}
	responsesPath := strings.TrimSpace(config.ResponsesPath)
	if responsesPath == "" {
		responsesPath = "/v1/responses"
	}
	if _, err := providerhttp.Endpoint(baseURL, responsesPath); err != nil {
		return nil, err
	}
	headers, err := providerhttp.Headers(config.Headers)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name: name, baseURL: baseURL, apiKeyRef: strings.TrimSpace(config.APIKeyRef), resolver: config.Resolver,
		client: client, maxBodyBytes: maxBody, responsesPath: responsesPath, headers: headers,
	}, nil
}

func (p *Provider) Complete(ctx context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	body, err := p.requestBody(request)
	if err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Invalid(p.name, err)
	}
	response, err := p.do(ctx, http.MethodPost, p.responsesPath, body)
	if err != nil {
		return providerapi.CompletionResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerapi.CompletionResponse{}, providerhttp.StatusError(p.name, response)
	}
	raw, err := providerhttp.ReadLimited(response.Body, p.maxBodyBytes)
	if err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, err)
	}
	return p.decode(raw, request.Model)
}

func (p *Provider) decode(raw []byte, model string) (providerapi.CompletionResponse, error) {
	var decoded responsesResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, err)
	}
	if decoded.Status == "failed" {
		return providerapi.CompletionResponse{}, providerhttp.StreamError(p.name, decoded.Error)
	}
	result := providerapi.CompletionResponse{FinishReason: decoded.Status, Usage: usageFromResponses(decoded.Usage)}
	if decoded.IncompleteDetails.Reason == "max_output_tokens" {
		result.FinishReason = "length"
	}
	for _, rawItem := range decoded.Output {
		var item struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
			Summary []struct {
				Text string `json:"text"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return result, providerhttp.Protocol(p.name, err)
		}
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				result.Content += part.Text + part.Refusal
			}
		case "reasoning":
			for _, part := range item.Summary {
				result.Thinking += part.Text
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
				return result, providerhttp.Protocol(p.name, errors.New("invalid function_call"))
			}
			result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	data, _ := json.Marshal(decoded.Output)
	result.Replay = &providerapi.Replay{Provider: p.name + "/openai-responses", Model: model, Data: data}
	return result, nil
}

func (p *Provider) Stream(ctx context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	body, err := p.requestBody(request)
	if err != nil {
		return providerhttp.Invalid(p.name, err)
	}
	body, err = providerhttp.SetStream(body)
	if err != nil {
		return err
	}
	response, err := p.do(ctx, http.MethodPost, p.responsesPath, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerhttp.StatusError(p.name, response)
	}
	var text, thinking string
	return providerhttp.SSE(response.Body, p.maxBodyBytes, func(_ string, raw []byte) (bool, error) {
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
			Message  string          `json:"message"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return false, providerhttp.Protocol(p.name, err)
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if event.Delta != "" {
				text += event.Delta
				return false, handler(providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: event.Delta})
			}
		case "response.reasoning_summary_text.delta":
			if event.Delta != "" {
				thinking += event.Delta
				return false, handler(providerapi.StreamEvent{Type: providerapi.StreamThinking, ThinkingDelta: event.Delta})
			}
		case "response.completed", "response.incomplete":
			result, err := p.decode(event.Response, request.Model)
			if err != nil {
				return false, err
			}
			if text != "" && !strings.HasPrefix(result.Content, text) {
				return false, providerhttp.Protocol(p.name, errors.New("stream text differs from final response"))
			}
			result.Content = strings.TrimPrefix(result.Content, text)
			if strings.HasPrefix(result.Thinking, thinking) {
				result.Thinking = strings.TrimPrefix(result.Thinking, thinking)
			} else {
				result.Thinking = ""
			}
			return true, providerapi.EmitResponse(result, handler)
		case "response.failed":
			_, err := p.decode(event.Response, request.Model)
			if err == nil {
				err = providerhttp.Protocol(p.name, errors.New("response failed"))
			}
			return false, err
		case "error":
			if len(event.Error) == 0 {
				event.Error = raw
			}
			return false, providerhttp.StreamError(p.name, event.Error)
		}
		return false, nil
	})
}

func (p *Provider) requestBody(request providerapi.CompletionRequest) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 {
		return nil, errors.New("model and at least one message are required")
	}

	payload := responsesRequest{
		Model: request.Model, MaxOutputTokens: request.MaxOutputTokens, Temperature: request.Temperature, Include: []string{"reasoning.encrypted_content"},
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		payload.Reasoning = &reasoningConfig{Effort: effort}
	}
	lastUser := -1
	for i, m := range request.Messages {
		if m.Role == providerapi.RoleUser {
			lastUser = i
		}
	}
	for index, message := range request.Messages {
		switch message.Role {
		case providerapi.RoleSystem, providerapi.RoleUser:
			if message.ToolCallID != "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("message %d cannot contain tool calls", index)
			}
			images := message.Images
			if index == lastUser {
				images = append(append([]providerapi.ImagePart(nil), images...), request.Images...)
			}
			var content any = message.Content
			if len(images) > 0 {
				content = imageContent(message.Content, images)
			}
			payload.Input = append(payload.Input, inputItem{Role: string(message.Role), Content: content})
		case providerapi.RoleAssistant:
			if raw := message.ReplayFor(p.name+"/openai-responses", request.Model); raw != nil {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err != nil {
					return nil, err
				}
				for _, item := range items {
					payload.Input = append(payload.Input, item)
				}
				continue
			}
			if message.ToolCallID != "" {
				return nil, fmt.Errorf("assistant message %d cannot contain a tool call ID", index)
			}
			if message.Content != "" {
				payload.Input = append(payload.Input, inputItem{Role: "assistant", Content: message.Content})
			}
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid([]byte(call.Arguments)) {
					return nil, fmt.Errorf("assistant message %d has an invalid tool call", index)
				}
				payload.Input = append(payload.Input, inputItem{Type: "function_call", CallID: call.ID, Name: call.Name, Arguments: call.Arguments})
			}
			if message.Content == "" && len(message.ToolCalls) == 0 {
				return nil, fmt.Errorf("assistant message %d is empty", index)
			}
		case providerapi.RoleTool:
			if strings.TrimSpace(message.ToolCallID) == "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("tool message %d requires a tool call ID and cannot contain tool calls", index)
			}
			var output any = message.Content
			if len(message.Images) > 0 {
				output = imageContent(message.Content, message.Images)
			}
			payload.Input = append(payload.Input, inputItem{Type: "function_call_output", CallID: message.ToolCallID, Output: output})
		default:
			return nil, fmt.Errorf("message %d has invalid role", index)
		}
	}
	for index, tool := range request.Tools {
		if strings.TrimSpace(tool.Name) == "" || !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("tool %d requires a name and valid input schema", index)
		}
		payload.Tools = append(payload.Tools, responseTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema, Strict: false,
		})
	}
	if request.ResponseSchema != nil {
		if strings.TrimSpace(request.ResponseSchema.Name) == "" || !json.Valid(request.ResponseSchema.Schema) {
			return nil, errors.New("response JSON schema name and valid schema are required")
		}
		payload.Text = &responseText{Format: responseFormat{
			Type: "json_schema", Name: request.ResponseSchema.Name, Description: request.ResponseSchema.Description,
			Strict: request.ResponseSchema.Strict, Schema: request.ResponseSchema.Schema,
		}}
	}
	return json.Marshal(payload)
}

func (p *Provider) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	request, err := p.newRequest(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return nil, providerhttp.NetworkError(p.name, ctx, err)
	}
	return response, nil
}

func (p *Provider) newRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	if ctx == nil {
		return nil, providerhttp.Invalid(p.name, errors.New("context is required"))
	}
	requestURL, err := providerhttp.Endpoint(p.baseURL, endpoint)
	if err != nil {
		return nil, providerhttp.Invalid(p.name, err)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, providerhttp.Invalid(p.name, errors.New("cannot create HTTP request"))
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if p.apiKeyRef != "" {
		secret, err := p.resolver.ResolveSecret(ctx, p.apiKeyRef)
		if err != nil || strings.TrimSpace(secret) == "" {
			return nil, &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorAuthentication, Err: errors.New("secret reference could not be resolved")}
		}
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	providerhttp.ApplyHeaders(request, p.headers)
	return request, nil
}

type responsesRequest struct {
	Store           bool             `json:"store"`
	Include         []string         `json:"include,omitempty"`
	Model           string           `json:"model"`
	Input           []any            `json:"input"`
	Tools           []responseTool   `json:"tools,omitempty"`
	Text            *responseText    `json:"text,omitempty"`
	MaxOutputTokens int64            `json:"max_output_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	Reasoning       *reasoningConfig `json:"reasoning,omitempty"`
}

type reasoningConfig struct {
	Effort string `json:"effort"`
}

type inputItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    any    `json:"output,omitempty"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type responseText struct {
	Format responseFormat `json:"format"`
}

type responseFormat struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Schema      json.RawMessage `json:"schema"`
}

type responsesResponse struct {
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	Usage             responsesUsage    `json:"usage"`
	Error             json.RawMessage   `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func imageContent(text string, images []providerapi.ImagePart) []any {
	out := []any{map[string]string{"type": "input_text", "text": text}}
	for _, img := range images {
		out = append(out, map[string]string{"type": "input_image", "image_url": "data:" + img.MIME + ";base64," + img.Base64})
	}
	return out
}

type responsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
}

func usageFromResponses(u responsesUsage) providerapi.Usage {
	var cacheRead int64
	if u.InputTokensDetails != nil {
		cacheRead = u.InputTokensDetails.CachedTokens
	}
	uncached := u.InputTokens
	if cacheRead > 0 && cacheRead <= u.InputTokens {
		uncached = u.InputTokens - cacheRead
	}
	return providerapi.Usage{
		InputTokens:     providerapi.TokenCount(uncached),
		OutputTokens:    providerapi.TokenCount(u.OutputTokens),
		TotalTokens:     providerapi.TokenCount(u.TotalTokens),
		CacheReadTokens: providerapi.TokenCount(cacheRead),
	}
}
