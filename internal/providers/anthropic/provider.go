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

	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const defaultVersion = "2023-06-01"

type Config struct {
	Name             string
	BaseURL          string
	APIKey           string
	HTTPClient       *http.Client
	MaxBodyBytes     int64
	MessagesPath     string
	AnthropicVersion string
	Headers          map[string]string
}

type Provider struct {
	name             string
	baseURL          *url.URL
	apiKey           string
	client           *http.Client
	maxBodyBytes     int64
	messagesPath     string
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
	messagesPath := strings.TrimSpace(config.MessagesPath)
	if messagesPath == "" {
		messagesPath = "/v1/messages"
	}
	if _, err := providerhttp.Endpoint(baseURL, messagesPath); err != nil {
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
		name: name, baseURL: baseURL, apiKey: config.APIKey,
		client: client, maxBodyBytes: maxBody, messagesPath: messagesPath,
		anthropicVersion: version, headers: headers,
	}, nil
}

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
	return p.decode(payload, request.Model)
}

func (p *Provider) decode(raw []byte, model string) (providerapi.CompletionResponse, error) {
	var decoded messageResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return providerapi.CompletionResponse{}, providerhttp.Protocol(p.name, err)
	}
	result := providerapi.CompletionResponse{FinishReason: decoded.StopReason, Usage: usageFromAnthropic(decoded.Usage)}
	for _, rawBlock := range decoded.Content {
		var block struct {
			Type, Text, Thinking, ID, Name string
			Input                          json.RawMessage
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return result, providerhttp.Protocol(p.name, err)
		}
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "thinking":
			result.Thinking += block.Thinking
		case "tool_use":
			if block.ID == "" || block.Name == "" || !json.Valid(block.Input) {
				return result, providerhttp.Protocol(p.name, errors.New("invalid tool_use block"))
			}
			result.ToolCalls = append(result.ToolCalls, providerapi.ToolCall{ID: block.ID, Name: block.Name, Arguments: string(block.Input)})
		}
	}
	replay, _ := json.Marshal(decoded.Content)
	result.Replay = &providerapi.Replay{Provider: p.name + "/anthropic", Model: model, Data: replay}
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
	response, err := p.do(ctx, http.MethodPost, p.messagesPath, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerhttp.StatusError(p.name, response)
	}
	var result messageResponse
	var blocks []map[string]json.RawMessage
	var inputs []string
	started := false
	return providerhttp.SSE(response.Body, p.maxBodyBytes, func(_ string, raw []byte) (bool, error) {
		var event struct {
			Type         string
			Index        int
			Message      messageResponse
			ContentBlock map[string]json.RawMessage `json:"content_block"`
			Delta        struct {
				Type, Text, Thinking, Signature string
				PartialJSON                     string `json:"partial_json"`
				StopReason                      string `json:"stop_reason"`
			}
			Usage anthropicUsage
			Error json.RawMessage
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return false, providerhttp.Protocol(p.name, err)
		}
		switch event.Type {
		case "message_start":
			result = event.Message
			started = true
		case "content_block_start":
			if event.ContentBlock == nil || event.Index != len(blocks) || len(blocks) >= 1024 {
				return false, providerhttp.Protocol(p.name, errors.New("invalid content block index"))
			}
			blocks = append(blocks, event.ContentBlock)
			inputs = append(inputs, "")
		case "content_block_delta":
			if event.Index < 0 || event.Index >= len(blocks) {
				return false, providerhttp.Protocol(p.name, errors.New("unknown content block index"))
			}
			block := blocks[event.Index]
			field, delta, kind := "", "", providerapi.StreamEventType("")
			switch event.Delta.Type {
			case "text_delta":
				field, delta, kind = "text", event.Delta.Text, providerapi.StreamDelta
			case "thinking_delta":
				field, delta, kind = "thinking", event.Delta.Thinking, providerapi.StreamThinking
			case "signature_delta":
				field, delta = "signature", event.Delta.Signature
			case "input_json_delta":
				inputs[event.Index] += event.Delta.PartialJSON
			}
			if field != "" {
				var old string
				_ = json.Unmarshal(block[field], &old)
				block[field], _ = json.Marshal(old + delta)
			}
			if kind != "" {
				if delta != "" {
					event := providerapi.StreamEvent{Type: kind}
					if kind == providerapi.StreamDelta {
						event.ContentDelta = delta
					} else {
						event.ThinkingDelta = delta
					}
					return false, handler(event)
				}
			}
		case "content_block_stop":
			if event.Index < 0 || event.Index >= len(blocks) {
				return false, providerhttp.Protocol(p.name, errors.New("unknown stopped content block"))
			}
			if inputs[event.Index] != "" {
				if !json.Valid([]byte(inputs[event.Index])) {
					return false, providerhttp.Protocol(p.name, errors.New("invalid tool input JSON"))
				}
				blocks[event.Index]["input"] = json.RawMessage(inputs[event.Index])
			}
		case "message_delta":
			result.StopReason = event.Delta.StopReason
			result.Usage.OutputTokens = event.Usage.OutputTokens
		case "message_stop":
			if !started || result.StopReason == "" {
				return false, providerhttp.Protocol(p.name, errors.New("incomplete message stream"))
			}
			result.Content = nil
			for _, block := range blocks {
				encoded, _ := json.Marshal(block)
				result.Content = append(result.Content, encoded)
			}
			encoded, _ := json.Marshal(result)
			final, err := p.decode(encoded, request.Model)
			if err != nil {
				return false, err
			}
			final.Content = ""
			final.Thinking = ""
			return true, providerapi.EmitResponse(final, handler)
		case "error":
			return false, providerhttp.StreamError(p.name, event.Error)
		}
		return false, nil
	})
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
	for i, m := range request.Messages {
		if m.Role == providerapi.RoleUser {
			lastUser = i
		}
	}
	add := func(role string, blocks []json.RawMessage) {
		n := len(payload.Messages)
		if n > 0 && payload.Messages[n-1].Role == role {
			payload.Messages[n-1].Content = append(payload.Messages[n-1].Content, blocks...)
		} else {
			payload.Messages = append(payload.Messages, anthropicMessage{Role: role, Content: blocks})
		}
	}
	for index, message := range request.Messages {
		switch message.Role {
		case providerapi.RoleSystem:
			if payload.System != "" {
				payload.System += "\n\n"
			}
			payload.System += message.Content
		case providerapi.RoleUser:
			images := append([]providerapi.ImagePart(nil), message.Images...)
			if index == lastUser {
				images = append(images, request.Images...)
			}
			add("user", imageBlocks(message.Content, images))
		case providerapi.RoleAssistant:
			var blocks []json.RawMessage
			if replay := message.ReplayFor(p.name+"/anthropic", request.Model); len(replay) > 0 {
				if err := json.Unmarshal(replay, &blocks); err != nil {
					return nil, err
				}
			} else {
				blocks = imageBlocks(message.Content, nil)
				for _, call := range message.ToolCalls {
					if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
						return nil, errors.New("invalid assistant tool call")
					}
					raw, _ := json.Marshal(contentBlock{Type: "tool_use", ID: call.ID, Name: call.Name, Input: json.RawMessage(call.Arguments)})
					blocks = append(blocks, raw)
				}
			}
			if len(blocks) == 0 {
				return nil, fmt.Errorf("assistant message %d is empty", index)
			}
			add("assistant", blocks)
		case providerapi.RoleTool:
			if message.ToolCallID == "" {
				return nil, errors.New("tool result requires call ID")
			}
			var output any = message.Content
			if len(message.Images) > 0 {
				output = imageBlocks(message.Content, message.Images)
			}
			raw, _ := json.Marshal(contentBlock{Type: "tool_result", ToolUseID: message.ToolCallID, Content: output})
			add("user", []json.RawMessage{raw})
		default:
			return nil, fmt.Errorf("invalid role %q", message.Role)
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
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("anthropic-version", p.anthropicVersion)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if p.apiKey != "" {
		request.Header.Set("x-api-key", p.apiKey)
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
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   any             `json:"content,omitempty"`
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
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      anthropicUsage    `json:"usage"`
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

func imageBlocks(text string, images []providerapi.ImagePart) []json.RawMessage {
	var blocks []json.RawMessage
	if text != "" {
		raw, _ := json.Marshal(contentBlock{Type: "text", Text: text})
		blocks = append(blocks, raw)
	}
	for _, img := range images {
		raw, _ := json.Marshal(contentBlock{Type: "image", Source: &imageSource{Type: "base64", MediaType: img.MIME, Data: img.Base64}})
		blocks = append(blocks, raw)
	}
	return blocks
}
