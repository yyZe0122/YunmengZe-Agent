package openairesponses

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"net/http"
	"net/url"
	"strings"
)

// Compact returns the entire canonical window, never just its encrypted item.
func (p *Provider) Compact(ctx context.Context, request providerapi.CompletionRequest, focus string) (*providerapi.Replay, providerapi.Usage, error) {
	raw, err := p.requestBody(request)
	if err != nil {
		return nil, providerapi.Usage{}, err
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, providerapi.Usage{}, err
	}
	payload := map[string]any{"model": request.Model, "input": body["input"]}
	if strings.TrimSpace(focus) != "" {
		payload["instructions"] = "Preserve the user's task and constraints. Compaction focus: " + focus
	}
	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, providerapi.Usage{}, err
	}
	endpoint, err := url.Parse(p.responsesPath)
	if err != nil {
		return nil, providerapi.Usage{}, err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/compact"
	response, err := p.do(ctx, http.MethodPost, endpoint.String(), raw)
	if err != nil {
		return nil, providerapi.Usage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerapi.Usage{}, providerhttp.StatusError(p.name, response)
	}
	raw, err = providerhttp.ReadLimited(response.Body, p.maxBodyBytes)
	if err != nil {
		return nil, providerapi.Usage{}, err
	}
	var compacted struct {
		Output json.RawMessage `json:"output"`
		Usage  responsesUsage  `json:"usage"`
	}
	if err = json.Unmarshal(raw, &compacted); err != nil {
		return nil, providerapi.Usage{}, providerhttp.Protocol(p.name, err)
	}
	var items []json.RawMessage
	if err = json.Unmarshal(compacted.Output, &items); err != nil || len(items) == 0 {
		return nil, providerapi.Usage{}, providerhttp.Protocol(p.name, errors.New("empty compacted window"))
	}
	return &providerapi.Replay{Provider: p.name + "/openai-responses", Model: request.Model, Data: compacted.Output, Canonical: true}, usageFromResponses(compacted.Usage), nil
}
