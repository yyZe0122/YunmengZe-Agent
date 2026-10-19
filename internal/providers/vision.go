package providers

import (
	"context"
	"errors"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// VisionBackend analyzes images with an explicitly configured vision model.
type VisionBackend struct {
	Provider providerapi.Provider
	Model    string
}

type VisionResult struct {
	Text  string
	Usage *providerapi.Usage
}

func (b VisionBackend) Complete(ctx context.Context, prompt string, images []providerapi.ImagePart) (VisionResult, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = "Describe the image."
	}
	if len(images) == 0 {
		return VisionResult{}, errors.New("no images to analyze")
	}
	resp, err := b.Provider.Complete(ctx, providerapi.CompletionRequest{
		Model: b.Model,
		Messages: []providerapi.Message{
			{Role: providerapi.RoleUser, Content: prompt},
		},
		Images: images,
	})
	if err != nil {
		return VisionResult{}, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return VisionResult{}, errors.New("vision model returned no image description")
	}
	return VisionResult{Text: strings.TrimSpace(resp.Content), Usage: &resp.Usage}, nil
}
