package providers

import (
	"context"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type requestCaptureProvider struct {
	request providerapi.CompletionRequest
}

func (p *requestCaptureProvider) Complete(_ context.Context, request providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	p.request = request
	return providerapi.CompletionResponse{}, nil
}
func (p *requestCaptureProvider) Stream(_ context.Context, request providerapi.CompletionRequest, handler providerapi.StreamHandler) error {
	p.request = request
	return providerapi.EmitResponse(providerapi.CompletionResponse{}, handler)
}
func TestModelConfiguredProviderAppliesDefaultsAndTokenCap(t *testing.T) {
	temperature := 0.25
	capture := &requestCaptureProvider{}
	provider := &modelConfiguredProvider{
		Provider: capture, maxTokens: 512, temperature: &temperature, reasoningEffort: "high",
	}
	if _, err := provider.Complete(context.Background(), providerapi.CompletionRequest{MaxOutputTokens: 4096}); err != nil {
		t.Fatal(err)
	}
	if capture.request.MaxOutputTokens != 512 {
		t.Fatalf("max output tokens = %d, want 512", capture.request.MaxOutputTokens)
	}
	if capture.request.Temperature == nil || *capture.request.Temperature != temperature {
		t.Fatalf("temperature = %v", capture.request.Temperature)
	}
	if capture.request.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q", capture.request.ReasoningEffort)
	}
}

func TestModelConfiguredProviderPreservesStricterRequestOptions(t *testing.T) {
	configuredTemperature := 0.25
	requestTemperature := 0.75
	capture := &requestCaptureProvider{}
	provider := &modelConfiguredProvider{
		Provider: capture, maxTokens: 512, temperature: &configuredTemperature, reasoningEffort: "high",
	}
	if _, err := provider.Complete(context.Background(), providerapi.CompletionRequest{
		MaxOutputTokens: 128, Temperature: &requestTemperature, ReasoningEffort: "low",
	}); err != nil {
		t.Fatal(err)
	}
	if capture.request.MaxOutputTokens != 128 || capture.request.Temperature == nil || *capture.request.Temperature != requestTemperature || capture.request.ReasoningEffort != "low" {
		t.Fatalf("request options = %+v", capture.request)
	}
}
