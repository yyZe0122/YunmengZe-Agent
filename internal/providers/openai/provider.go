// Package openai implements an OpenAI-compatible Chat Completions provider.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const (
	defaultMaxResponseBytes int64 = 4 << 20
	maxProviderErrorBytes   int64 = 32 << 10
)

const (
	ResponseFormatAuto       = "auto"
	ResponseFormatJSONSchema = "json_schema"
	ResponseFormatJSONObject = "json_object"
)

type Config struct {
	Name           string
	BaseURL        string
	APIKeyRef      string
	Resolver       providerapi.SecretResolver
	HTTPClient     *http.Client
	MaxBodyBytes   int64
	CompletionPath string
	ModelsPath     string
	ResponseFormat string
	Headers        map[string]string
}

type Provider struct {
	name            string
	baseURL         *url.URL
	apiKeyRef       string
	resolver        providerapi.SecretResolver
	client          *http.Client
	maxBodyBytes    int64
	completionPath  string
	modelsPath      string
	responseFormat  string
	responseFormats sync.Map
	headers         http.Header
}

func New(config Config) (*Provider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "openai-compatible"
	}
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("openai-compatible base URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("openai-compatible base URL must not contain userinfo, query, or fragment")
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
		maxBody = defaultMaxResponseBytes
	}
	if maxBody < 1 {
		return nil, errors.New("maximum response body size must be positive")
	}
	completionPath := strings.TrimSpace(config.CompletionPath)
	if completionPath == "" {
		completionPath = "/v1/chat/completions"
	}
	modelsPath := strings.TrimSpace(config.ModelsPath)
	if modelsPath == "" {
		modelsPath = "/v1/models"
	}
	if _, err := providerhttp.Endpoint(parsed, completionPath); err != nil {
		return nil, err
	}
	if _, err := providerhttp.Endpoint(parsed, modelsPath); err != nil {
		return nil, err
	}
	responseFormat := strings.TrimSpace(config.ResponseFormat)
	if responseFormat == "" {
		responseFormat = ResponseFormatAuto
	}
	if responseFormat != ResponseFormatAuto && responseFormat != ResponseFormatJSONSchema && responseFormat != ResponseFormatJSONObject {
		return nil, fmt.Errorf("unsupported response format %q", responseFormat)
	}
	headers, err := providerhttp.Headers(config.Headers)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name: name, baseURL: parsed, apiKeyRef: strings.TrimSpace(config.APIKeyRef),
		resolver: config.Resolver, client: client, maxBodyBytes: maxBody,
		completionPath: completionPath, modelsPath: modelsPath, responseFormat: responseFormat, headers: headers,
	}, nil
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Complete(ctx context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	response, err := p.doCompletion(ctx, request, false)
	if err != nil {
		return providerapi.CompletionResponse{}, err
	}
	defer response.Body.Close()
	payload, err := readLimited(response.Body, p.maxBodyBytes)
	if err != nil {
		return providerapi.CompletionResponse{}, p.responseError(ctx, err)
	}
	var decoded chatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return providerapi.CompletionResponse{}, p.protocol(errors.New("invalid JSON response"))
	}
	if len(decoded.Choices) == 0 {
		return providerapi.CompletionResponse{}, p.protocol(errors.New("response has no choices"))
	}
	choice := decoded.Choices[0]
	result := providerapi.CompletionResponse{
		Content: choice.Message.Content, FinishReason: choice.FinishReason,
		Usage: usageFrom(decoded.Usage),
	}
	for _, call := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{
			ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments,
		})
	}
	return result, nil
}

func (p *Provider) Stream(ctx context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	if handler == nil {
		return p.invalid(errors.New("stream handler is required"))
	}
	response, err := p.doCompletion(ctx, request, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	reader := bufio.NewScanner(io.LimitReader(response.Body, p.maxBodyBytes+1))
	bufferSize := int(min(p.maxBodyBytes, int64(64<<10)))
	reader.Buffer(make([]byte, bufferSize), int(p.maxBodyBytes))
	var toolCalls []providerapi.ToolCall
	var finishReason string
	var usage providerapi.Usage
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if finishReason == "" {
				return p.protocol(errors.New("stream completed without finish reason"))
			}
			for index := range toolCalls {
				toolCall := toolCalls[index]
				if strings.TrimSpace(toolCall.ID) == "" || strings.TrimSpace(toolCall.Name) == "" || !json.Valid([]byte(toolCall.Arguments)) {
					return p.protocol(errors.New("invalid completed streaming tool call"))
				}
				if err := handler(providerapi.StreamEvent{Type: providerapi.StreamToolCall, ToolCall: &toolCall}); err != nil {
					return err
				}
			}
			return handler(providerapi.StreamEvent{
				Type: providerapi.StreamComplete, FinishReason: finishReason, Usage: &usage,
			})
		}
		var chunk streamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return p.protocol(errors.New("invalid streaming JSON response"))
		}
		if next := usageFrom(chunk.Usage); next != (providerapi.Usage{}) {
			usage = next
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if finishReason != "" {
			return p.protocol(errors.New("stream event after finish reason"))
		}
		choice := chunk.Choices[0]
		if choice.Delta.ReasoningContent != "" {
			if err := handler(providerapi.StreamEvent{
				Type: providerapi.StreamThinking, ThinkingDelta: choice.Delta.ReasoningContent,
			}); err != nil {
				return err
			}
		}
		if choice.Delta.Content != "" {
			if err := handler(providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: choice.Delta.Content}); err != nil {
				return err
			}
		}
		for _, call := range choice.Delta.ToolCalls {
			if call.Index < 0 || call.Index > 1024 {
				return p.protocol(errors.New("invalid streaming tool call index"))
			}
			for len(toolCalls) <= call.Index {
				toolCalls = append(toolCalls, providerapi.ToolCall{})
			}
			current := &toolCalls[call.Index]
			if call.ID != "" {
				if current.ID != "" && current.ID != call.ID {
					return p.protocol(errors.New("conflicting streaming tool call ID"))
				}
				current.ID = call.ID
			}
			if call.Function.Name != "" {
				if current.Name != "" && current.Name != call.Function.Name {
					return p.protocol(errors.New("conflicting streaming tool call name"))
				}
				current.Name = call.Function.Name
			}
			current.Arguments += call.Function.Arguments
		}
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
	}
	if err := reader.Err(); err != nil {
		return p.responseError(ctx, err)
	}
	return p.protocol(errors.New("stream ended before [DONE]"))
}
func (p *Provider) Health(ctx context.Context) providerapi.HealthStatus {
	started := time.Now()
	status := providerapi.HealthStatus{CheckedAt: started.UTC()}
	response, err := p.do(ctx, http.MethodGet, p.modelsPath, nil)
	status.Latency = time.Since(started)
	if err != nil {
		var providerErr *providerapi.ProviderError
		if errors.As(err, &providerErr) {
			status.ErrorKind = providerErr.Kind
		}
		return status
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := p.statusError(response)
		var providerErr *providerapi.ProviderError
		if errors.As(err, &providerErr) {
			status.ErrorKind = providerErr.Kind
		}
		return status
	}
	status.Healthy = true
	return status
}

func (p *Provider) doCompletion(ctx context.Context, request providerapi.CompletionRequest, stream bool) (*http.Response, error) {
	format := p.responseFormatFor(request.Model)
	fellBack := false
	for {
		body, err := p.requestBodyWithFormat(request, stream, format)
		if err != nil {
			return nil, p.invalid(err)
		}
		response, err := p.do(ctx, http.MethodPost, p.completionPath, body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if fellBack {
				p.responseFormats.Store(strings.TrimSpace(request.Model), format)
			}
			return response, nil
		}
		statusErr := p.statusError(response)
		_ = response.Body.Close()
		if request.ResponseSchema == nil || format != ResponseFormatJSONSchema || !p.shouldFallbackToJSONObject(response.StatusCode, statusErr) {
			return nil, statusErr
		}
		format = ResponseFormatJSONObject
		fellBack = true
	}
}

func (p *Provider) shouldFallbackToJSONObject(statusCode int, err error) bool {
	if p.responseFormat != ResponseFormatAuto || (statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound && statusCode != http.StatusUnprocessableEntity) {
		return false
	}
	message := strings.ToLower(err.Error())
	mentionsFormat := strings.Contains(message, "response_format") || strings.Contains(message, "json_schema")
	unsupported := strings.Contains(message, "unavailable") || strings.Contains(message, "unsupported") ||
		strings.Contains(message, "not support") || strings.Contains(message, "unknown") || strings.Contains(message, "invalid")
	return mentionsFormat && unsupported
}

func (p *Provider) responseFormatFor(model string) string {
	if p.responseFormat != ResponseFormatAuto {
		return p.responseFormat
	}
	if value, ok := p.responseFormats.Load(strings.TrimSpace(model)); ok {
		return value.(string)
	}
	return ResponseFormatJSONSchema
}

func (p *Provider) requestBody(request providerapi.CompletionRequest, stream bool) ([]byte, error) {
	return p.requestBodyWithFormat(request, stream, p.responseFormatFor(request.Model))
}

func (p *Provider) requestBodyWithFormat(request providerapi.CompletionRequest, stream bool, format string) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 {
		return nil, errors.New("model and at least one message are required")
	}
	lastUser := -1
	for i, message := range request.Messages {
		if message.Role == providerapi.RoleUser {
			lastUser = i
		}
	}
	messages := make([]chatMessage, len(request.Messages))
	for i, message := range request.Messages {
		if message.Role != providerapi.RoleSystem && message.Role != providerapi.RoleUser &&
			message.Role != providerapi.RoleAssistant && message.Role != providerapi.RoleTool {
			return nil, fmt.Errorf("message %d has invalid role", i)
		}
		if message.Role == providerapi.RoleTool {
			if strings.TrimSpace(message.ToolCallID) == "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("tool message %d requires a tool call ID and cannot contain tool calls", i)
			}
		} else if strings.TrimSpace(message.ToolCallID) != "" {
			return nil, fmt.Errorf("non-tool message %d cannot contain a tool call ID", i)
		}
		if len(message.ToolCalls) > 0 && message.Role != providerapi.RoleAssistant {
			return nil, fmt.Errorf("non-assistant message %d cannot contain tool calls", i)
		}
		converted := chatMessage{Role: string(message.Role), Content: message.Content, ToolCallID: message.ToolCallID}
		if i == lastUser && len(request.Images) > 0 && message.Role == providerapi.RoleUser {
			parts := []chatContentPart{{Type: "text", Text: message.Content}}
			for _, img := range request.Images {
				parts = append(parts, chatContentPart{
					Type:     "image_url",
					ImageURL: &chatImageURL{URL: "data:" + strings.TrimSpace(img.MIME) + ";base64," + img.Base64},
				})
			}
			converted.Content = parts
		}
		for _, call := range message.ToolCalls {
			if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid([]byte(call.Arguments)) {
				return nil, fmt.Errorf("assistant message %d has an invalid tool call", i)
			}
			converted.ToolCalls = append(converted.ToolCalls, chatToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: chatFunction{Name: call.Name, Arguments: call.Arguments},
			})
		}
		messages[i] = converted
	}
	payload := chatRequest{
		Model: request.Model, Messages: messages, Stream: stream,
		MaxTokens: request.MaxOutputTokens, Temperature: request.Temperature,
		ReasoningEffort: strings.TrimSpace(request.ReasoningEffort),
	}
	toolNames := make(map[string]struct{}, len(request.Tools))
	for i, definition := range request.Tools {
		name := strings.TrimSpace(definition.Name)
		if name == "" || !json.Valid(definition.InputSchema) {
			return nil, fmt.Errorf("tool %d requires a name and valid input schema", i)
		}
		if _, exists := toolNames[name]; exists {
			return nil, fmt.Errorf("duplicate tool name %q", name)
		}
		toolNames[name] = struct{}{}
		payload.Tools = append(payload.Tools, chatTool{
			Type:     "function",
			Function: chatToolDefinition{Name: name, Description: definition.Description, Parameters: definition.InputSchema},
		})
	}
	if request.ResponseSchema != nil {
		if strings.TrimSpace(request.ResponseSchema.Name) == "" || !json.Valid(request.ResponseSchema.Schema) {
			return nil, errors.New("response JSON schema name and valid schema are required")
		}
		payload.ResponseFormat = &responseFormat{Type: format}
		if format == ResponseFormatJSONSchema {
			payload.ResponseFormat.JSONSchema = &responseJSONSchema{
				Name: request.ResponseSchema.Name, Description: request.ResponseSchema.Description,
				Strict: request.ResponseSchema.Strict, Schema: request.ResponseSchema.Schema,
			}
		}
	}
	return json.Marshal(payload)
}

func (p *Provider) do(ctx context.Context, method, endpoint string, body []byte) (*http.Response, error) {
	if ctx == nil {
		return nil, p.invalid(errors.New("context is required"))
	}
	requestURL, err := providerhttp.Endpoint(p.baseURL, endpoint)
	if err != nil {
		return nil, p.invalid(err)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return nil, p.invalid(errors.New("cannot create HTTP request"))
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if p.apiKeyRef != "" {
		secret, err := p.resolver.ResolveSecret(ctx, p.apiKeyRef)
		if err != nil {
			return nil, &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorAuthentication, Err: errors.New("secret reference could not be resolved")}
		}
		if strings.TrimSpace(secret) == "" {
			return nil, &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorAuthentication, Err: errors.New("resolved secret is empty")}
		}
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	providerhttp.ApplyHeaders(request, p.headers)
	response, err := p.client.Do(request)
	if err != nil {
		return nil, p.networkError(ctx, err)
	}
	return response, nil
}

func (p *Provider) networkError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &providerapi.ProviderError{
			Provider: p.name, Kind: providerapi.ErrorUnavailable, Err: context.Canceled,
		}
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		return &providerapi.ProviderError{
			Provider: p.name, Kind: providerapi.ErrorTimeout, Retryable: true, Err: context.DeadlineExceeded,
		}
	}
	return &providerapi.ProviderError{
		Provider: p.name, Kind: providerapi.ErrorUnavailable, Retryable: true, Err: errors.New("HTTP request failed"),
	}
}

func (p *Provider) responseError(ctx context.Context, err error) error {
	classified := p.networkError(ctx, err)
	var providerError *providerapi.ProviderError
	if errors.As(classified, &providerError) &&
		(providerError.Kind == providerapi.ErrorTimeout || errors.Is(classified, context.Canceled)) {
		return classified
	}
	return p.protocol(err)
}

func (p *Provider) invalid(err error) error {
	return &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorInvalidRequest, Err: err}
}

func (p *Provider) protocol(err error) error {
	return &providerapi.ProviderError{Provider: p.name, Kind: providerapi.ErrorProtocol, Retryable: true, Err: err}
}

func (p *Provider) statusError(response *http.Response) error {
	kind := providerapi.ErrorProtocol
	retryable := false
	switch response.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		kind = providerapi.ErrorInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = providerapi.ErrorAuthentication
	case http.StatusTooManyRequests:
		kind, retryable = providerapi.ErrorRateLimited, true
	default:
		if response.StatusCode >= 500 {
			kind, retryable = providerapi.ErrorUnavailable, true
		}
	}
	return &providerapi.ProviderError{
		Provider: p.name, Kind: kind, StatusCode: response.StatusCode,
		Retryable: retryable, RetryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
		Err: providerStatusMessage(response.Body),
	}
}

func providerStatusMessage(body io.Reader) error {
	fallback := errors.New("provider rejected the request")
	if body == nil {
		return fallback
	}
	payload, err := readLimited(body, maxProviderErrorBytes)
	if err != nil {
		return fallback
	}
	var envelope struct {
		Error struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    json.RawMessage `json:"code"`
			Param   json.RawMessage `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return fallback
	}
	message := boundedStatusText(envelope.Error.Message, 512)
	if message == "" {
		return fallback
	}
	details := make([]string, 0, 3)
	if value := boundedStatusText(envelope.Error.Type, 96); value != "" {
		details = append(details, "type="+value)
	}
	if value := statusScalar(envelope.Error.Code); value != "" {
		details = append(details, "code="+value)
	}
	if value := statusScalar(envelope.Error.Param); value != "" {
		details = append(details, "param="+value)
	}
	if len(details) != 0 {
		message += " (" + strings.Join(details, ", ") + ")"
	}
	return errors.New(message)
}

func statusScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return boundedStatusText(typed, 96)
	case float64, bool:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func boundedStatusText(value string, maximum int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum]) + "..."
	}
	return value
}

func readLimited(reader io.Reader, maximum int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximum {
		return nil, errors.New("provider response exceeds configured size limit")
	}
	return payload, nil
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}
	return 0
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Tools           []chatTool      `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	MaxTokens       int64           `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatTool struct {
	Type     string             `json:"type"`
	Function chatToolDefinition `json:"function"`
}

type chatToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responseFormat struct {
	Type       string              `json:"type"`
	JSONSchema *responseJSONSchema `json:"json_schema,omitempty"`
}

type responseJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Schema      json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage tokenUsage `json:"usage"`
}

type streamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage tokenUsage `json:"usage"`
}

type tokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	// PromptTokensDetails carries optional cache metrics (OpenAI-compatible).
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

func usageFrom(value tokenUsage) providerapi.Usage {
	var cacheRead int64
	if value.PromptTokensDetails != nil {
		cacheRead = value.PromptTokensDetails.CachedTokens
	}
	// InputTokens = uncached prompt tokens when cache is reported as part of prompt_tokens.
	uncached := value.PromptTokens
	if cacheRead > 0 && cacheRead <= value.PromptTokens {
		uncached = value.PromptTokens - cacheRead
	}
	return providerapi.Usage{
		InputTokens:     providerapi.TokenCount(uncached),
		OutputTokens:    providerapi.TokenCount(value.CompletionTokens),
		TotalTokens:     providerapi.TokenCount(value.TotalTokens),
		CacheReadTokens: providerapi.TokenCount(cacheRead),
	}
}
