package providers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/anthropic"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/gemini"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/openai"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/openairesponses"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// NewConfigured builds the protocol adapter selected by the resolved JSON configuration.
func NewConfigured(config providerconfig.Resolved) (providerapi.Provider, error) {
	var provider providerapi.Provider
	var err error
	switch config.Protocol {
	case providerconfig.ProtocolOpenAIChat:
		provider, err = openai.New(openai.Config{
			Name: config.ProviderID, BaseURL: config.BaseURL, APIKey: config.APIKey,
			CompletionPath: config.CompletionPath, ResponseFormat: config.ResponseFormat, Headers: config.Headers,
		})
	case providerconfig.ProtocolAnthropicMessages:
		provider, err = anthropic.New(anthropic.Config{
			Name: config.ProviderID, BaseURL: config.BaseURL, APIKey: config.APIKey,
			MessagesPath: config.CompletionPath, AnthropicVersion: config.AnthropicVersion, Headers: config.Headers,
		})
	case providerconfig.ProtocolOpenAIResponses:
		provider, err = openairesponses.New(openairesponses.Config{
			Name: config.ProviderID, BaseURL: config.BaseURL, APIKey: config.APIKey,
			ResponsesPath: config.CompletionPath, Headers: config.Headers,
		})
	case providerconfig.ProtocolGeminiGenerate:
		provider, err = gemini.New(gemini.Config{
			Name: config.ProviderID, BaseURL: config.BaseURL, APIKey: config.APIKey,
			GeneratePath: config.CompletionPath, Headers: config.Headers,
		})
	default:
		return nil, fmt.Errorf("unsupported provider protocol %q", config.Protocol)
	}
	if err != nil {
		return nil, err
	}
	if config.MaxTokens == 0 && config.Temperature == nil && strings.TrimSpace(config.ReasoningEffort) == "" {
		return provider, nil
	}
	return &modelConfiguredProvider{
		Provider: provider, maxTokens: config.MaxTokens,
		temperature: config.Temperature, reasoningEffort: strings.TrimSpace(config.ReasoningEffort),
	}, nil
}

type modelConfiguredProvider struct {
	providerapi.Provider
	maxTokens       int64
	temperature     *float64
	reasoningEffort string
}

func (p *modelConfiguredProvider) Complete(ctx context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	return p.Provider.Complete(ctx, p.apply(request))
}

func (p *modelConfiguredProvider) Stream(ctx context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	return p.Provider.Stream(ctx, p.apply(request), handler)
}

func (p *modelConfiguredProvider) apply(request providerapi.CompletionRequest) providerapi.CompletionRequest {
	if p.maxTokens > 0 && (request.MaxOutputTokens <= 0 || request.MaxOutputTokens > p.maxTokens) {
		request.MaxOutputTokens = p.maxTokens
	}
	if request.Temperature == nil && p.temperature != nil {
		value := *p.temperature
		request.Temperature = &value
	}
	if strings.TrimSpace(request.ReasoningEffort) == "" {
		request.ReasoningEffort = p.reasoningEffort
	}
	return request
}

func (p *modelConfiguredProvider) Compact(ctx context.Context, request providerapi.CompletionRequest, focus string) (*providerapi.Replay, providerapi.Usage, error) {
	compactor, ok := p.Provider.(interface {
		Compact(context.Context, providerapi.CompletionRequest, string) (*providerapi.Replay, providerapi.Usage, error)
	})
	if !ok {
		return nil, providerapi.Usage{}, errors.New("native compaction is unavailable")
	}
	return compactor.Compact(ctx, request, focus)
}
