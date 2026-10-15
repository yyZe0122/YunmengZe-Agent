package providers

import (
	"context"
	"errors"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// VisionBackend analyzes images with an explicitly configured vision model.
type VisionBackend struct {
	Provider providerapi.Provider
	Model    string
}

func (b VisionBackend) Complete(ctx context.Context, prompt string, images []providerapi.ImagePart) (tools.Result, error) {
	if b.Provider == nil || strings.TrimSpace(b.Model) == "" {
		return tools.Result{}, errors.New("vision provider is not configured")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Describe the image."
	}
	if len(images) == 0 {
		return tools.Result{}, errors.New("no images to analyze")
	}
	resp, err := b.Provider.Complete(ctx, providerapi.CompletionRequest{
		Model: b.Model,
		Messages: []providerapi.Message{
			{Role: providerapi.RoleUser, Content: prompt},
		},
		Images: images,
	})
	if err != nil {
		return tools.Result{}, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return tools.Result{}, errors.New("vision model returned no image description")
	}
	return tools.Result{Text: strings.TrimSpace(resp.Content), Usage: &resp.Usage}, nil
}
