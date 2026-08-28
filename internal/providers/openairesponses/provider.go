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
	"time"

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
	ModelsPath    string
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
	modelsPath    string
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
		client = &http.Client{Timeout: 60 * time.Second}
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
	modelsPath := strings.TrimSpace(config.ModelsPath)
	if modelsPath == "" {
		modelsPath = "/v1/models"
	}
	if _, err := providerhttp.Endpoint(baseURL, responsesPath); err != nil {
		return nil, err
	}
	if _, err := providerhttp.Endpoint(baseURL, modelsPath); err != nil {
		return nil, err
	}
	headers, err := providerhttp.Headers(config.Headers)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name: name, baseURL: baseURL, apiKeyRef: strings.TrimSpace(config.APIKeyRef), resolver: config.Resolver,
		client: client, maxBodyBytes: maxBody, responsesPath: responsesPath, modelsPath: modelsPath, headers: headers,
	}, nil
}

func (p *Provider) Name() string { return p.name }

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
	payload, err := providerhttp.ReadLimited(response.Body, p.maxBodyBytes)
	if err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, err)
	}
	var decoded responsesResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("invalid JSON response"))
	}
	result := providerapi.CompletionResponse{FinishReason: decoded.Status, Usage: usageFromResponses(decoded.Usage)}
	for index, item := range decoded.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" {
					result.Content += content.Text
				}
			}
		case "reasoning":
			// Best-effort: some Responses API builds emit reasoning summaries.
			for _, content := range item.Content {
				if text := strings.TrimSpace(content.Text); text != "" {
					if result.Thinking != "" {
						result.Thinking += "\n"
					}
					result.Thinking += text
				}
			}
			if summary := strings.TrimSpace(item.Summary); summary != "" {
				if result.Thinking != "" {
					result.Thinking += "\n"
				}
				result.Thinking += summary
			}
		case "function_call":
			id := strings.TrimSpace(item.CallID)
			if id == "" {
				id = strings.TrimSpace(item.ID)
			}
			if id == "" || strings.TrimSpace(item.Name) == "" || !json.Valid([]byte(item.Arguments)) {
				return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, fmt.Errorf("invalid function_call output item %d", index))
			}
			result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{ID: id, Name: item.Name, Arguments: item.Arguments})
		}
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("response has no text or tool calls"))
	}
	return result, nil
}

func (p *Provider) Stream(ctx context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	if handler == nil {
		return providerhttp.Invalid(p.name, errors.New("stream handler is required"))
	}
	response, err := p.Complete(ctx, request)
	if err != nil {
		return err
	}
	return providerapi.EmitResponse(response, handler)
}

func (p *Provider) Health(ctx context.Context) providerapi.HealthStatus {
	request, err := p.newRequest(ctx, http.MethodGet, p.modelsPath, nil)
	if err != nil {
		return healthError(err)
	}
	return providerhttp.HealthStatus(p.name, ctx, p.client, request)
}

func (p *Provider) requestBody(request providerapi.CompletionRequest) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 {
		return nil, errors.New("model and at least one message are required")
	}
	if len(request.Images) > 0 {
		return nil, errors.New("openai-responses does not support auxiliary images")
	}
	payload := responsesRequest{
		Model: request.Model, MaxOutputTokens: request.MaxOutputTokens, Temperature: request.Temperature,
	}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		payload.Reasoning = &reasoningConfig{Effort: effort}
	}
	for index, message := range request.Messages {
		switch message.Role {
		case providerapi.RoleSystem, providerapi.RoleUser:
			if message.ToolCallID != "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("message %d cannot contain tool calls", index)
			}
			payload.Input = append(payload.Input, inputItem{Role: string(message.Role), Content: message.Content})
		case providerapi.RoleAssistant:
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
			payload.Input = append(payload.Input, inputItem{Type: "function_call_output", CallID: message.ToolCallID, Output: message.Content})
		default:
			return nil, fmt.Errorf("message %d has invalid role", index)
		}
	}
	for index, tool := range request.Tools {
		if strings.TrimSpace(tool.Name) == "" || !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("tool %d requires a name and valid input schema", index)
		}
		payload.Tools = append(payload.Tools, responseTool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema, Strict: true,
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
	request.Header.Set("Accept", "application/json")
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

func healthError(err error) providerapi.HealthStatus {
	status := providerapi.HealthStatus{CheckedAt: time.Now().UTC()}
	var providerError *providerapi.ProviderError
	if errors.As(err, &providerError) {
		status.ErrorKind = providerError.Kind
	}
	return status
}

type responsesRequest struct {
	Model           string           `json:"model"`
	Input           []inputItem      `json:"input"`
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
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
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
	Status string `json:"status"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Summary   string `json:"summary,omitempty"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage responsesUsage `json:"usage"`
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
