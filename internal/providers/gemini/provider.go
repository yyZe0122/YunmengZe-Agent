// Package gemini implements the Google Gemini generateContent protocol.
package gemini

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
	Name         string
	BaseURL      string
	APIKeyRef    string
	Resolver     providerapi.SecretResolver
	HTTPClient   *http.Client
	MaxBodyBytes int64
	GeneratePath string
	ModelsPath   string
	Headers      map[string]string
}

type Provider struct {
	name         string
	baseURL      *url.URL
	apiKeyRef    string
	resolver     providerapi.SecretResolver
	client       *http.Client
	maxBodyBytes int64
	generatePath string
	modelsPath   string
	headers      http.Header
}

func New(config Config) (*Provider, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "gemini"
	}
	baseURL, err := providerhttp.ParseBaseURL("gemini", config.BaseURL)
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
	generatePath := strings.TrimSpace(config.GeneratePath)
	if generatePath == "" {
		generatePath = "/v1beta/models/{model}:generateContent"
	}
	modelsPath := strings.TrimSpace(config.ModelsPath)
	if modelsPath == "" {
		modelsPath = "/v1beta/models"
	}
	if !strings.Contains(generatePath, "{model}") {
		return nil, errors.New("gemini completionPath must contain {model}")
	}
	if _, err := providerhttp.Endpoint(baseURL, strings.ReplaceAll(generatePath, "{model}", "model")); err != nil {
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
		client: client, maxBodyBytes: maxBody, generatePath: generatePath, modelsPath: modelsPath, headers: headers,
	}, nil
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Complete(ctx context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	body, err := p.requestBody(request)
	if err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Invalid(p.name, err)
	}
	endpoint := strings.ReplaceAll(p.generatePath, "{model}", url.PathEscape(request.Model))
	response, err := p.do(ctx, http.MethodPost, endpoint, body)
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
	var decoded generateResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("invalid JSON response"))
	}
	if len(decoded.Candidates) == 0 {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("response has no candidates"))
	}
	candidate := decoded.Candidates[0]
	result := providerapi.CompletionResponse{FinishReason: candidate.FinishReason, Usage: usageFromGemini(decoded.UsageMetadata)}
	for index, part := range candidate.Content.Parts {
		result.Content += part.Text
		if part.FunctionCall != nil {
			arguments := part.FunctionCall.Args
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if strings.TrimSpace(part.FunctionCall.Name) == "" || !json.Valid(arguments) {
				return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, fmt.Errorf("invalid functionCall part %d", index))
			}
			id := strings.TrimSpace(part.FunctionCall.ID)
			if id == "" {
				id = fmt.Sprintf("gemini-call-%d", index)
			}
			result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{ID: id, Name: part.FunctionCall.Name, Arguments: string(arguments)})
		}
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, errors.New("response has no text or function calls"))
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
	if strings.TrimSpace(request.ReasoningEffort) != "" {
		return nil, errors.New("reasoning effort is not supported by gemini")
	}
	payload := generateRequest{}
	toolNamesByID := make(map[string]string)
	lastUser := -1
	for i, message := range request.Messages {
		if message.Role == providerapi.RoleUser {
			lastUser = i
		}
	}
	for index, message := range request.Messages {
		switch message.Role {
		case providerapi.RoleSystem:
			if message.ToolCallID != "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("system message %d cannot contain tool calls", index)
			}
			if message.Content != "" {
				if payload.SystemInstruction == nil {
					payload.SystemInstruction = &content{}
				}
				payload.SystemInstruction.Parts = append(payload.SystemInstruction.Parts, part{Text: message.Content})
			}
		case providerapi.RoleUser:
			if message.ToolCallID != "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("user message %d cannot contain tool calls", index)
			}
			parts := []part{{Text: message.Content}}
			if index == lastUser {
				for _, img := range request.Images {
					parts = append(parts, part{InlineData: &inlineData{MIMEType: strings.TrimSpace(img.MIME), Data: img.Base64}})
				}
			}
			payload.Contents = append(payload.Contents, content{Role: "user", Parts: parts})
		case providerapi.RoleAssistant:
			if message.ToolCallID != "" {
				return nil, fmt.Errorf("assistant message %d cannot contain a tool call ID", index)
			}
			converted := content{Role: "model"}
			if message.Content != "" {
				converted.Parts = append(converted.Parts, part{Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid([]byte(call.Arguments)) {
					return nil, fmt.Errorf("assistant message %d has an invalid tool call", index)
				}
				toolNamesByID[call.ID] = call.Name
				converted.Parts = append(converted.Parts, part{FunctionCall: &functionCall{ID: call.ID, Name: call.Name, Args: json.RawMessage(call.Arguments)}})
			}
			if len(converted.Parts) == 0 {
				return nil, fmt.Errorf("assistant message %d is empty", index)
			}
			payload.Contents = append(payload.Contents, converted)
		case providerapi.RoleTool:
			if strings.TrimSpace(message.ToolCallID) == "" || len(message.ToolCalls) > 0 {
				return nil, fmt.Errorf("tool message %d requires a tool call ID and cannot contain tool calls", index)
			}
			name := toolNamesByID[message.ToolCallID]
			if name == "" {
				return nil, fmt.Errorf("tool message %d references an unknown tool call", index)
			}
			response := json.RawMessage(nil)
			if json.Valid([]byte(message.Content)) && strings.HasPrefix(strings.TrimSpace(message.Content), "{") {
				response = json.RawMessage(message.Content)
			} else {
				encoded, _ := json.Marshal(map[string]string{"output": message.Content})
				response = encoded
			}
			payload.Contents = append(payload.Contents, content{Role: "user", Parts: []part{{FunctionResponse: &functionResponse{
				ID: message.ToolCallID, Name: name, Response: response,
			}}}})
		default:
			return nil, fmt.Errorf("message %d has invalid role", index)
		}
	}
	if len(payload.Contents) == 0 {
		return nil, errors.New("at least one non-system message is required")
	}
	if len(request.Tools) > 0 {
		declarations := make([]functionDeclaration, 0, len(request.Tools))
		for index, tool := range request.Tools {
			if strings.TrimSpace(tool.Name) == "" || !json.Valid(tool.InputSchema) {
				return nil, fmt.Errorf("tool %d requires a name and valid input schema", index)
			}
			declarations = append(declarations, functionDeclaration{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema})
		}
		payload.Tools = []geminiTool{{FunctionDeclarations: declarations}}
	}
	if request.MaxOutputTokens > 0 || request.Temperature != nil || request.ResponseSchema != nil {
		payload.GenerationConfig = &generationConfig{MaxOutputTokens: request.MaxOutputTokens, Temperature: request.Temperature}
	}
	if request.ResponseSchema != nil {
		if strings.TrimSpace(request.ResponseSchema.Name) == "" || !json.Valid(request.ResponseSchema.Schema) {
			return nil, errors.New("response JSON schema name and valid schema are required")
		}
		payload.GenerationConfig.ResponseMIMEType = "application/json"
		payload.GenerationConfig.ResponseJSONSchema = request.ResponseSchema.Schema
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
		request.Header.Set("x-goog-api-key", secret)
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

type generateRequest struct {
	SystemInstruction *content          `json:"system_instruction,omitempty"`
	Contents          []content         `json:"contents"`
	Tools             []geminiTool      `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type functionResponse struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type generationConfig struct {
	MaxOutputTokens    int64           `json:"maxOutputTokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	ResponseMIMEType   string          `json:"responseMimeType,omitempty"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
}

type generateResponse struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata geminiUsage `json:"usageMetadata"`
}

type geminiUsage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

func usageFromGemini(u geminiUsage) providerapi.Usage {
	cacheRead := u.CachedContentTokenCount
	uncached := u.PromptTokenCount
	if cacheRead > 0 && cacheRead <= u.PromptTokenCount {
		uncached = u.PromptTokenCount - cacheRead
	}
	return providerapi.Usage{
		InputTokens:     providerapi.TokenCount(uncached),
		OutputTokens:    providerapi.TokenCount(u.CandidatesTokenCount),
		TotalTokens:     providerapi.TokenCount(u.TotalTokenCount),
		CacheReadTokens: providerapi.TokenCount(cacheRead),
	}
}
