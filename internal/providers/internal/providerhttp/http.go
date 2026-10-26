// Package providerhttp contains shared HTTP mechanics for JSON model providers.
package providerhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const DefaultMaxResponseBytes int64 = 4 << 20

func ParseBaseURL(label, value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%s base URL must be an absolute HTTP(S) URL", label)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s base URL must not contain userinfo, query, or fragment", label)
	}
	return parsed, nil
}

func Endpoint(base *url.URL, endpoint string) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || reference.IsAbs() || reference.Host != "" || reference.Fragment != "" || !strings.HasPrefix(reference.Path, "/") {
		return nil, errors.New("provider endpoint must be an absolute path with an optional query")
	}
	result := *base
	basePath := strings.TrimRight(base.Path, "/")
	path := reference.Path
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	result.Path = basePath + path
	result.RawPath = ""
	result.RawQuery = reference.RawQuery
	return &result, nil
}

func Headers(values map[string]string) (http.Header, error) {
	headers := make(http.Header, len(values))
	for name, value := range values {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "\r\n:") {
			return nil, errors.New("provider header name is invalid")
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("provider header %q contains a newline", name)
		}
		headers.Set(name, value)
	}
	return headers, nil
}

func ApplyHeaders(request *http.Request, headers http.Header) {
	for name, values := range headers {
		request.Header.Del(name)
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
}

func ReadLimited(reader io.Reader, maximum int64) ([]byte, error) {
	limited := io.LimitReader(reader, maximum+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximum {
		return nil, errors.New("provider response body exceeds configured limit")
	}
	return payload, nil
}

func NetworkError(provider string, ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &providerapi.ProviderError{Provider: provider, Kind: providerapi.ErrorUnavailable, Err: context.Canceled}
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		return &providerapi.ProviderError{
			Provider: provider, Kind: providerapi.ErrorTimeout, Retryable: true, Err: context.DeadlineExceeded,
		}
	}
	return &providerapi.ProviderError{
		Provider: provider, Kind: providerapi.ErrorUnavailable, Retryable: true, Err: errors.New("HTTP request failed"),
	}
}

func StatusError(provider string, response *http.Response) error {
	kind := providerapi.ErrorProtocol
	retryable := false
	switch response.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		kind = providerapi.ErrorInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = providerapi.ErrorAuthentication
	case http.StatusTooManyRequests:
		kind, retryable = providerapi.ErrorRateLimited, true
	default:
		if response.StatusCode >= 500 {
			kind, retryable = providerapi.ErrorUnavailable, true
		}
	}
	message := "provider rejected the request"
	raw, _ := ReadLimited(response.Body, 16<<10)
	var details struct {
		Error struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &details) == nil && details.Error.Message != "" {
		message = strings.Join(strings.Fields(details.Error.Message), " ")
		if len(message) > 1500 {
			message = message[:1500]
		}
		if details.Error.Type != "" {
			message = details.Error.Type + ": " + message
		}
		if len(details.Error.Code) > 0 && len(details.Error.Code) < 160 {
			message += " (" + string(details.Error.Code) + ")"
		}
	}
	return &providerapi.ProviderError{
		Provider: provider, Kind: kind, StatusCode: response.StatusCode, Retryable: retryable,
		RetryAfter: ParseRetryAfter(response.Header.Get("Retry-After")), Err: errors.New(message),
	}
}

func Invalid(provider string, err error) error {
	return &providerapi.ProviderError{Provider: provider, Kind: providerapi.ErrorInvalidRequest, Err: err}
}

func Protocol(provider string, err error) error {
	return &providerapi.ProviderError{Provider: provider, Kind: providerapi.ErrorProtocol, Retryable: true, Err: err}
}

// ParseRetryAfter accepts a delay in seconds or an HTTP date.
func ParseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 0
}

// NewClient permits long streaming generations while bounding connection setup
// and the entire request. Callers can impose shorter operation deadlines.
func NewClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: transport, Timeout: 10 * time.Minute}
}
