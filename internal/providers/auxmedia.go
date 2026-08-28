package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers/internal/providerhttp"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// VisionBackend runs one Complete with Images (ADR-055).
type VisionBackend struct {
	Provider providerapi.Provider
	Model    string
}

func (b VisionBackend) Complete(ctx context.Context, prompt string, images []providerapi.ImagePart) (string, error) {
	if b.Provider == nil || strings.TrimSpace(b.Model) == "" {
		return "", errors.New("vision provider is not configured")
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Describe the image."
	}
	if len(images) == 0 {
		return "", errors.New("no images to analyze")
	}
	resp, err := b.Provider.Complete(ctx, providerapi.CompletionRequest{
		Model: b.Model,
		Messages: []providerapi.Message{
			{Role: providerapi.RoleUser, Content: prompt},
		},
		Images: images,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// WhisperBackend posts multipart /v1/audio/transcriptions (not chat Complete).
type WhisperBackend struct {
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
	Client  *http.Client
}

func NewWhisperBackend(resolved providerconfig.Resolved) (*WhisperBackend, error) {
	base, err := providerhttp.ParseBaseURL("speech", resolved.BaseURL)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(resolved.ModelID)
	if model == "" {
		return nil, errors.New("speech model id is required")
	}
	return &WhisperBackend{
		BaseURL: strings.TrimRight(base.String(), "/"),
		APIKey:  resolved.APIKey,
		Model:   model,
		Headers: resolved.Headers,
		Client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (b *WhisperBackend) Transcribe(ctx context.Context, path, mime string) (string, error) {
	if b == nil {
		return "", errors.New("speech backend is not configured")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	_ = writer.WriteField("model", b.Model)
	if err := writer.Close(); err != nil {
		return "", err
	}
	endpoint, err := transcriptionURL(b.BaseURL)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if strings.TrimSpace(b.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}
	for k, v := range b.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whisper http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("whisper json: %w", err)
	}
	return strings.TrimSpace(parsed.Text), nil
}

func transcriptionURL(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" {
		return "", errors.New("speech base URL is invalid")
	}
	ep, err := providerhttp.Endpoint(parsed, "/v1/audio/transcriptions")
	if err != nil {
		return "", err
	}
	return ep.String(), nil
}

func fileName(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return path
	}
	return path[i+1:]
}
