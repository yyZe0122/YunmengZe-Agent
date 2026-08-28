// Package anthropic implements the Anthropic Messages protocol.
package anthropic

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

	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const defaultVersion = "2023-06-01"

type Config struct {
	Name             string
	BaseURL          string
	APIKeyRef        string
	Resolver         providerapi.SecretResolver
	HTTPClient       *http.Client
	MaxBodyBytes     int64
	MessagesPath     string
	ModelsPath       string
	AnthropicVersion string
	Headers          map[string]string
}

type Provider struct {
	name             string
	baseURL          *url.URL
	apiKeyRef        string
	resolver         providerapi.SecretResolver
	client           *http.Client
	maxBodyBytes     int64
	messagesPath     string
	modelsPath       string
	anthropicVersion string
	headers          http.Header
}

func New(config Config) (*Provider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "anthropic-compatible"
	}
	baseURL, err := providerhttp.ParseBaseURL("anthropic-compatible", config.BaseURL)
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
	messagesPath := strings.TrimSpace(config.MessagesPath)
	if messagesPath == "" {
		messagesPath = "/v1/messages"
	}
	modelsPath := strings.TrimSpace(config.ModelsPath)
	if modelsPath == "" {
		modelsPath = "/v1/models"
	}
	if _, err := providerhttp.Endpoint(baseURL, messagesPath); err != nil {
		return nil, err
	}
	if _, err := providerhttp.Endpoint(baseURL, modelsPath); err != nil {
		return nil, err
	}
	version := strings.TrimSpace(config.AnthropicVersion)
	if version == "" {
		version = defaultVersion
	}
	headers, err := providerhttp.Headers(config.Headers)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name: name, baseURL: baseURL, apiKeyRef: strings.TrimSpace(config.APIKeyRef), resolver: config.Resolver,
		client: client, maxBodyBytes: maxBody, messagesPath: messagesPath, modelsPath: modelsPath,
		anthropicVersion: version, headers: headers,
	}, nil
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Complete(ctx context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	body, err := p.requestBody(request)
	if err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Invalid(p.name, err)
	}
	response, err := p.do(ctx, http.MethodPost, p.messagesPath, body)
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
	var decoded messageResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("invalid JSON response"))
	}
	result := providerapi.CompletionResponse{FinishReason: decoded.StopReason, Usage: usageFromAnthropic(decoded.Usage)}
	for index, block := range decoded.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			if strings.TrimSpace(block.ID) == "" || strings.TrimSpace(block.Name) == "" || !json.Valid(block.Input) {
				return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, fmt.Errorf("invalid tool_use content block %d", index))
			}
			result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
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
		status := providerapi.HealthStatus{CheckedAt: time.Now().UTC()}
		var providerError *providerapi.ProviderError
		if errors.As(err, &providerError) {
			status.ErrorKind = providerError.Kind
		}
		return status
	}
	return providerhttp.HealthStatus(p.name, ctx, p.client, request)
}

func (p *Provider) requestBody(request providerapi.CompletionRequest) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 {
		return nil, errors.New("model and at least one message are required")
	}
	if strings.TrimSpace(request.ReasoningEffort) != "" {
		return nil, errors.New("reasoning effort is not supported by anthropic")
	}
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = modelcatalog.Lookup(request.Model).Output
		if maxTokens <= 0 {
			maxTokens = modelcatalog.DefaultMaxOutput
		}
	}
	payload := messageRequest{Model: request.Model, MaxTokens: maxTokens, Temperature: request.Temperature}
	lastUser := -1
	for i, message := range request.Messages {
		if message.Role == providerapi.RoleUser {
			lastUser = i
		}
	}
	for index, message := range request.Messages {
		switch message.Role {
		case providerapi.RoleSystem:
			if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
				return nil, fmt.Errorf("system message %d cannot contain tool calls", index)
			}
			if message.Content != "" {
				if payload.System != "" {
					payload.System += "\n\n"
				}
				payload.System += message.Content
			}
		case providerapi.RoleUser:
			if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
				return nil, fmt.Errorf("user message %d cannot contain tool calls", index)
			}
			blocks := []contentBlock{{Type: "text", Text: message.Content}}
			if index == lastUser {
				for _, img := range request.Images {
					blocks = append(blocks, contentBlock{
						Type:   "image",
						Source: &imageSource{Type: "base64", MediaType: strings.TrimSpace(img.MIME), Data: img.Base64},
					})
				}
			}
			payload.Messages = append(payload.Messages, anthropicMessage{Role: "user", Content: blocks})
		case providerapi.RoleAssistant:
			if message.ToolCallID != "" {
				return nil, fmt.Errorf("assistant message %d cannot contain a tool call ID", index)
			}
			converted := anthropicMessage{Role: "assistant"}
			if message.Content != "" {
				converted.Content = append(converted.Content, contentBlock{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid([]byte(call.Arguments)) {
					return nil, fmt.Errorf("assistant message %d has an invalid tool call", index)
				}
				converted.Content = append(converted.Content, contentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: json.RawMessage(call.Arguments)})
			}
			if len(converted.Content) == 0 {
				return nil, fmt.Errorf("assistant message %d is empty", index)
			}
			payload.Messages = append(payload.Messages, converted)
		case providerapi.RoleTool:
			if strings.TrimSpace(message.ToolCallID) == "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("tool message %d requires a tool call ID and cannot contain tool calls", index)
			}
			payload.Messages = append(payload.Messages, anthropicMessage{Role: "user", Content: []contentBlock{{
				Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Content,
			}}})
		default:
			return nil, fmt.Errorf("message %d has invalid role", index)
		}
	}
	if len(payload.Messages) == 0 {
		return nil, errors.New("at least one non-system message is required")
	}
	for index, tool := range request.Tools {
		if strings.TrimSpace(tool.Name) == "" || !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("tool %d requires a name and valid input schema", index)
		}
		payload.Tools = append(payload.Tools, anthropicTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	if request.ResponseSchema != nil {
		if strings.TrimSpace(request.ResponseSchema.Name) == "" || !json.Valid(request.ResponseSchema.Schema) {
			return nil, errors.New("response JSON schema name and valid schema are required")
		}
		payload.OutputConfig = &outputConfig{Format: outputFormat{Type: "json_schema", Schema: request.ResponseSchema.Schema}}
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
	var content *bytes.Reader
	if body == nil {
		content = bytes.NewReader(nil)
	} else {
		content = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), content)
	if err != nil {
		return nil, providerhttp.Invalid(p.name, errors.New("cannot create HTTP request"))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("anthropic-version", p.anthropicVersion)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if p.apiKeyRef != "" {
		secret, err := p.resolver.ResolveSecret(ctx, p.apiKeyRef)
		if err != nil || strings.TrimSpace(secret) == "" {
			return nil, &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorAuthentication, Err: errors.New("secret reference could not be resolved")}
		}
		request.Header.Set("x-api-key", secret)
	}
	providerhttp.ApplyHeaders(request, p.headers)
	return request, nil
}

type messageRequest struct {
	Model        string             `json:"model"`
	MaxTokens    int64              `json:"max_tokens"`
	System       string             `json:"system,omitempty"`
	Messages     []anthropicMessage `json:"messages"`
	Tools        []anthropicTool    `json:"tools,omitempty"`
	Temperature  *float64           `json:"temperature,omitempty"`
	OutputConfig *outputConfig      `json:"output_config,omitempty"`
}

type anthropicMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type outputConfig struct {
	Format outputFormat `json:"format"`
}

type outputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type messageResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func usageFromAnthropic(u anthropicUsage) providerapi.Usage {
	return providerapi.Usage{
		InputTokens:      providerapi.TokenCount(u.InputTokens),
		OutputTokens:     providerapi.TokenCount(u.OutputTokens),
		TotalTokens:      providerapi.TokenCount(u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
		CacheReadTokens:  providerapi.TokenCount(u.CacheReadInputTokens),
		CacheWriteTokens: providerapi.TokenCount(u.CacheCreationInputTokens),
	}
}
