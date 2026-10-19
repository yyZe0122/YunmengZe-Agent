// Package providerapi defines model-provider-neutral request and response
// contracts. Provider implementations live under internal/providers.
package providerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	// UserText is the original direct input; omitted for skills, child prompts and imported history.
	UserText *string `json:"user_text,omitempty"`
	Role     Role    `json:"role"`
	Content  string  `json:"content"`
	// Thinking is optional model reasoning/scratchpad (not sent back as user-visible
	// content unless the UI chooses to show it). Provider-neutral; may be empty.
	Thinking   string      `json:"thinking,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	Images     []ImagePart `json:"images,omitempty"`
	Replay     *Replay     `json:"replay,omitempty"`
}

// Replay keeps provider-native assistant blocks intact, including opaque reasoning
// signatures. Only the originating adapter/model may replay them.
type Replay struct {
	Canonical bool            `json:"canonical,omitempty"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Data      json.RawMessage `json:"data"`
}

func (m Message) ReplayFor(provider, model string) json.RawMessage {
	if m.Replay != nil && m.Replay.Provider == provider && m.Replay.Model == model {
		return m.Replay.Data
	}
	return nil
}

type JSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Schema      json.RawMessage `json:"schema"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ImagePart struct {
	MIME   string `json:"mime"`
	Base64 string `json:"base64"`
}

type CompletionRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ResponseSchema  *JSONSchema      `json:"response_schema,omitempty"`
	MaxOutputTokens int64            `json:"max_output_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	// Images supplements the last user message for standalone image analysis.
	Images []ImagePart `json:"images,omitempty"`
}

type TokenCount int64

type Cost struct {
	Currency string `json:"currency"`
	Micros   int64  `json:"micros"`
}

type Usage struct {
	// InputTokens is non-cache / uncached input tokens when the provider
	// distinguishes cache (used with CacheReadTokens for hit rate).
	InputTokens  TokenCount `json:"input_tokens"`
	OutputTokens TokenCount `json:"output_tokens"`
	TotalTokens  TokenCount `json:"total_tokens"`
	// CacheReadTokens is input served from provider prompt cache (optional).
	CacheReadTokens TokenCount `json:"cache_read_tokens,omitempty"`
	// CacheWriteTokens is input written into provider prompt cache (optional).
	CacheWriteTokens TokenCount `json:"cache_write_tokens,omitempty"`
	// Cost is optional; most providers leave it zero (billing is vendor-side).
	Cost Cost `json:"cost"`
}

// PromptTokens is the full prompt size for context packing and pressure:
// uncached input + cache read + cache write. Prefer this over InputTokens alone
// when measuring window fill (Anthropic reports uncached-only in InputTokens).
func (u Usage) PromptTokens() TokenCount {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// CacheHitRate is cache_read / (cache_read + uncached_input).
// ok is false when there is no cache activity or the denominator is zero.
func (u Usage) CacheHitRate() (rate float64, ok bool) {
	read := int64(u.CacheReadTokens)
	uncached := int64(u.InputTokens)
	if read <= 0 && int64(u.CacheWriteTokens) <= 0 {
		return 0, false
	}
	den := read + uncached
	if den <= 0 {
		return 0, false
	}
	return float64(read) / float64(den), true
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CompletionResponse struct {
	Content      string     `json:"content"`
	Thinking     string     `json:"thinking,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage"`
	Replay       *Replay    `json:"replay,omitempty"`
}

type StreamEventType string

const (
	StreamDelta    StreamEventType = "delta"
	StreamThinking StreamEventType = "thinking_delta"
	StreamToolCall StreamEventType = "tool_call"
	StreamComplete StreamEventType = "complete"
)

type StreamEvent struct {
	Type          StreamEventType `json:"type"`
	ContentDelta  string          `json:"content_delta,omitempty"`
	ThinkingDelta string          `json:"thinking_delta,omitempty"`
	ToolCall      *ToolCall       `json:"tool_call,omitempty"`
	Usage         *Usage          `json:"usage,omitempty"`
	FinishReason  string          `json:"finish_reason,omitempty"`
	Replay        *Replay         `json:"replay,omitempty"`
}

type StreamHandler func(StreamEvent) error

type StreamProvider interface {
	Stream(context.Context, CompletionRequest, StreamHandler) error
}

var ErrInvalidStream = errors.New("invalid provider stream")

type StreamAccumulator struct {
	response  CompletionResponse
	completed bool
}

func (a *StreamAccumulator) Add(event StreamEvent) error {
	if a.completed {
		return fmt.Errorf("%w: event after completion", ErrInvalidStream)
	}
	switch event.Type {
	case StreamDelta:
		if event.ContentDelta == "" || event.ThinkingDelta != "" || event.ToolCall != nil ||
			event.Usage != nil || event.FinishReason != "" {
			return fmt.Errorf("%w: invalid content delta", ErrInvalidStream)
		}
		a.response.Content += event.ContentDelta
	case StreamThinking:
		if event.ThinkingDelta == "" || event.ContentDelta != "" || event.ToolCall != nil ||
			event.Usage != nil || event.FinishReason != "" {
			return fmt.Errorf("%w: invalid thinking delta", ErrInvalidStream)
		}
		a.response.Thinking += event.ThinkingDelta
	case StreamToolCall:
		if event.ToolCall == nil || strings.TrimSpace(event.ToolCall.ID) == "" ||
			strings.TrimSpace(event.ToolCall.Name) == "" ||
			event.ContentDelta != "" || event.ThinkingDelta != "" ||
			event.Usage != nil || event.FinishReason != "" {
			return fmt.Errorf("%w: invalid tool call", ErrInvalidStream)
		}
		a.response.ToolCalls = append(a.response.ToolCalls, *event.ToolCall)
	case StreamComplete:
		if event.ContentDelta != "" || event.ThinkingDelta != "" || event.ToolCall != nil || event.Usage == nil {
			return fmt.Errorf("%w: invalid completion", ErrInvalidStream)
		}
		a.response.Usage = *event.Usage
		a.response.FinishReason = event.FinishReason
		a.response.Replay = event.Replay
		a.completed = true
	default:
		return fmt.Errorf("%w: unknown event type %q", ErrInvalidStream, event.Type)
	}
	return nil
}

func (a *StreamAccumulator) Response() (CompletionResponse, error) {
	if !a.completed {
		return CompletionResponse{}, fmt.Errorf("%w: missing completion", ErrInvalidStream)
	}
	response := a.response
	response.ToolCalls = append([]ToolCall(nil), response.ToolCalls...)
	return response, nil
}

func CollectStream(ctx context.Context, provider StreamProvider, request CompletionRequest) (CompletionResponse, error) {
	if provider == nil {
		return CompletionResponse{}, errors.New("stream provider is required")
	}
	var accumulator StreamAccumulator
	if err := provider.Stream(ctx, request, accumulator.Add); err != nil {
		return CompletionResponse{}, err
	}
	return accumulator.Response()
}

func EmitResponse(response CompletionResponse, handler StreamHandler) error {
	if handler == nil {
		return errors.New("stream handler is required")
	}
	if response.Thinking != "" {
		if err := handler(StreamEvent{Type: StreamThinking, ThinkingDelta: response.Thinking}); err != nil {
			return err
		}
	}
	if response.Content != "" {
		if err := handler(StreamEvent{Type: StreamDelta, ContentDelta: response.Content}); err != nil {
			return err
		}
	}
	for index := range response.ToolCalls {
		toolCall := response.ToolCalls[index]
		if err := handler(StreamEvent{Type: StreamToolCall, ToolCall: &toolCall}); err != nil {
			return err
		}
	}
	usage := response.Usage
	return handler(StreamEvent{Type: StreamComplete, Usage: &usage, FinishReason: response.FinishReason, Replay: response.Replay})
}

// Partial contains only displayable output; unfinished tool calls must never run.
func (a *StreamAccumulator) Partial() CompletionResponse {
	return CompletionResponse{Content: a.response.Content, Thinking: a.response.Thinking}
}

type Provider interface {
	Complete(context.Context, CompletionRequest) (CompletionResponse, error)
	Stream(context.Context, CompletionRequest, StreamHandler) error
}

type ErrorKind string

const (
	ErrorInvalidRequest ErrorKind = "invalid_request"
	ErrorAuthentication ErrorKind = "authentication"
	ErrorRateLimited    ErrorKind = "rate_limited"
	ErrorUnavailable    ErrorKind = "unavailable"
	ErrorTimeout        ErrorKind = "timeout"
	ErrorProtocol       ErrorKind = "protocol"
)

type ProviderError struct {
	Provider   string
	Kind       ErrorKind
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider error"
	}
	message := fmt.Sprintf("provider %s: %s", e.Provider, e.Kind)
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (status %d)", e.StatusCode)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
