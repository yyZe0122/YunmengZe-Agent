package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"github.com/yyZe0122/yunmengze-agent/internal/tools"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func (e *Engine) registerVision(r *tools.Registry, s session.Session) error {
	if !e.Config.VisionEnabled() {
		return nil
	}
	var vision tools.Vision
	if ref := e.Roles[providerconfig.RoleVision]; ref != "" {
		b, err := e.resolve(ref)
		if err != nil {
			return fmt.Errorf("vision: %w", err)
		}
		if b.config.ImageCapabilityKnown && !b.config.SupportsImages {
			return errors.New("configured vision model has supportsImages=false")
		}
		vision = visionBackend{providers.VisionBackend{Provider: b.provider, Model: b.config.ModelID}}
	} else {
		b, err := e.resolve(s.Model)
		if err != nil {
			return err
		}
		vision = nativeVision{supported: b.config.SupportsImages}
	}
	r.Images(vision)
	return nil
}

type visionBackend struct{ backend providers.VisionBackend }

func (v visionBackend) Complete(ctx context.Context, prompt string, images []providerapi.ImagePart) (tools.Result, error) {
	result, err := v.backend.Complete(ctx, prompt, images)
	return tools.Result{Text: result.Text, Usage: result.Usage}, err
}

type nativeVision struct{ supported bool }

func (v nativeVision) Complete(_ context.Context, prompt string, images []providerapi.ImagePart) (tools.Result, error) {
	if !v.supported {
		return tools.Result{}, errors.New("current model image support is unknown or disabled; configure models.vision, or set supportsImages=true on a model known to accept images")
	}
	return tools.Result{Text: "Image attached for inspection. " + prompt, Images: images}, nil
}
