package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestWhisperBackendTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("ct = %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Fatalf("auth = %s", r.Header.Get("Authorization"))
		}
		if _, err := r.MultipartReader(); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello world"}`)
	}))
	t.Cleanup(srv.Close)
	backend, err := NewWhisperBackend(providerconfig.Resolved{
		BaseURL: srv.URL, APIKey: "k", ModelID: "whisper-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(path, []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := backend.Transcribe(context.Background(), path, "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
}

type captureProvider struct {
	last providerapi.CompletionRequest
}

func (c *captureProvider) Name() string { return "cap" }
func (c *captureProvider) Complete(_ context.Context, req providerapi.CompletionRequest) (providerapi.CompletionResponse, error) {
	c.last = req
	return providerapi.CompletionResponse{Content: "seen"}, nil
}
func (c *captureProvider) Stream(context.Context, providerapi.CompletionRequest, providerapi.StreamHandler) error {
	return nil
}
func (c *captureProvider) Health(context.Context) providerapi.HealthStatus {
	return providerapi.HealthStatus{}
}

func TestVisionBackendComplete(t *testing.T) {
	cap := &captureProvider{}
	text, err := (VisionBackend{Provider: cap, Model: "gpt-4o-mini"}).Complete(context.Background(), "look", []providerapi.ImagePart{{MIME: "image/png", Base64: "AA"}})
	if err != nil || text != "seen" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if len(cap.last.Images) != 1 || cap.last.Model != "gpt-4o-mini" {
		t.Fatalf("req = %+v", cap.last)
	}
}
