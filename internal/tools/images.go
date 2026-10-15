package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type Vision interface {
	Complete(context.Context, string, []providerapi.ImagePart) (Result, error)
}

func (r *Registry) Images(vision Vision) {
	r.Register(Tool{Name: "vision_analyze", ReadOnly: true, Description: "Inspect an image file or URL using the current model, or the configured vision model.", Schema: Schema(`{"path":{"type":"string"},"url":{"type":"string"},"prompt":{"type":"string"}}`), Handler: func(ctx context.Context, e Env, raw json.RawMessage) (any, error) {
		var a struct {
			Path   string `json:"path"`
			URL    string `json:"url"`
			Prompt string `json:"prompt"`
		}
		if err := Decode(raw, &a); err != nil {
			return nil, err
		}
		var b []byte
		var err error
		if (a.Path == "") == (a.URL == "") {
			return nil, errors.New("supply exactly one of path or url")
		}
		if a.URL != "" {
			req, e := httpRequest(ctx, a.URL)
			if e != nil {
				return nil, e
			}
			b, _, err = doHTTP(newWebClient(), req, 16<<20)
		} else {
			p, e := Path(e.Workspace, a.Path)
			if e != nil {
				return nil, e
			}
			b, err = ReadFile(p, 16<<20)
		}
		if err != nil {
			return nil, err
		}
		part, err := imagePart(b)
		if err != nil {
			return nil, err
		}
		return vision.Complete(ctx, a.Prompt, []providerapi.ImagePart{part})
	}})
}

func imagePart(b []byte) (providerapi.ImagePart, error) {
	mime := http.DetectContentType(b)
	if !strings.HasPrefix(mime, "image/") {
		return providerapi.ImagePart{}, errors.New("unsupported image type")
	}
	return providerapi.ImagePart{MIME: mime, Base64: base64.StdEncoding.EncodeToString(b)}, nil
}
