package providers

import (
	"context"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type captureProvider struct {
	last providerapi.CompletionRequest
}

func (c *captureProvider) Complete(_ context.Context, req providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	c.last = req
	return providerapi.CompletionResponse{Content: "seen"}, nil
}
func (c *captureProvider) Stream(context.Context, providerapi.CompletionRequest, providerapi.StreamHandler) error {
	return nil
}
func TestVisionBackendComplete(t *testing.T) {
	cap := &captureProvider{}
	text, err := (VisionBackend{Provider: cap, Model: "gpt-4o-mini"}).Complete(context.Background(), "look", []providerapi.ImagePart{{MIME: "image/png", Base64: "AA"}})
	if err != nil || text.Text != "seen" {
		t.Fatalf("text=%q err=%v", text.Text, err)
	}
	if len(cap.last.Images) != 1 || cap.last.Model != "gpt-4o-mini" {
		t.Fatalf("req = %+v", cap.last)
	}
}
