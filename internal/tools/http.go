package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

type httpGetTool struct {
	client       *http.Client
	maximumBytes int64
}

func newHTTPGetTool(maximumBytes int64) Tool {
	if maximumBytes <= 0 {
		maximumBytes = 16 * 1024 * 1024
	}
	return &httpGetTool{
		maximumBytes: maximumBytes,
		client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are disabled; approve the target URL explicitly")
		}},
	}
}

func (t *httpGetTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "http_get", Description: "HTTP/HTTPS GET without redirects. Interactive agent waits /perm (similar = that host). SSRF baseline blocks localhost/private/link-local. Plan and cron never receive this grant. Do not use process_shell to fetch URLs.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 30000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["url"],"properties":{"url":{"type":"string"},"max_bytes":{"type":"integer","minimum":1}}}`),
	}
}

type httpGetInput struct {
	URL      string `json:"url"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

func (t *httpGetTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	input, parsed, err := t.parse(raw)
	if err != nil {
		return Authorization{}, err
	}
	_ = input
	return Authorization{Capability: "http_get", NetworkDomain: strings.ToLower(parsed.Hostname())}, nil
}

func (t *httpGetTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	input, parsed, err := t.parse(raw)
	if err != nil {
		return nil, err
	}
	if err := validateHTTPGetURLHost(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	limit := input.MaxBytes
	if limit < 0 {
		return nil, errors.New("max_bytes cannot be negative")
	}
	if limit == 0 || limit > t.maximumBytes {
		limit = t.maximumBytes
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "YunmengZe/0")
	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}
	return encodeResult(map[string]any{
		"url": input.URL, "status_code": response.StatusCode, "content_type": response.Header.Get("Content-Type"),
		"body": string(body), "size_bytes": len(body), "truncated": truncated,
	})
}

func (t *httpGetTool) parse(raw json.RawMessage) (httpGetInput, *url.URL, error) {
	var input httpGetInput
	if err := decodeStrict(raw, &input); err != nil {
		return input, nil, err
	}
	if input.MaxBytes < 0 {
		return input, nil, errors.New("max_bytes cannot be negative")
	}
	parsed, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil {
		return input, nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return input, nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return input, nil, errors.New("URL host is required and userinfo is forbidden")
	}
	// Cheap host-literal checks before Execute (Authorization / early fail).
	// Defer DNS-heavy failures to Execute with request context.
	host := strings.ToLower(parsed.Hostname())
	if _, err2 := netip.ParseAddr(host); err2 == nil {
		if err := validateHTTPGetURLHost(context.Background(), host); err != nil {
			return input, nil, err
		}
	} else if _, blocked := blockedHostExact[host]; blocked ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasPrefix(host, "metadata.") {
		if err := validateHTTPGetURLHost(context.Background(), host); err != nil {
			return input, nil, err
		}
	}
	return input, parsed, nil
}
