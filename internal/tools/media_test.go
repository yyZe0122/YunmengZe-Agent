package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubVision struct {
	prompt string
	images []ImageBytes
	text   string
	err    error
}

func (s *stubVision) Complete(_ context.Context, prompt string, images []ImageBytes) (string, error) {
	s.prompt = prompt
	s.images = images
	return s.text, s.err
}

type stubSpeech struct {
	path string
	text string
	err  error
}

func (s *stubSpeech) Transcribe(_ context.Context, path, mime string) (string, error) {
	s.path = path
	_ = mime
	return s.text, s.err
}

func TestVisionAnalyzePath(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.png")
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(img, png, 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubVision{text: "a cat"}
	tool := newVisionAnalyzeTool(guard, stub)
	args, _ := json.Marshal(map[string]string{"path": img, "prompt": "what"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a cat") {
		t.Fatalf("out = %s", out)
	}
	if stub.prompt != "what" || len(stub.images) != 1 {
		t.Fatalf("stub = %+v", stub)
	}
	auth, err := tool.Authorization(context.Background(), args)
	if err != nil || auth.Path == "" || auth.NetworkDomain != "" {
		t.Fatalf("auth = %+v err=%v", auth, err)
	}
}

func TestVisionAnalyzeURLNeedsHost(t *testing.T) {
	guard, err := NewPathGuard([]string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tool := newVisionAnalyzeTool(guard, &stubVision{text: "ok"})
	args, _ := json.Marshal(map[string]string{"url": "https://example.com/a.png"})
	auth, err := tool.Authorization(context.Background(), args)
	if err != nil || auth.NetworkDomain != "example.com" {
		t.Fatalf("auth = %+v err=%v", auth, err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"http://127.0.0.1/x.png"}`))
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("ssrf err = %v", err)
	}
}

func TestAudioTranscribeTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.wav")
	if err := os.WriteFile(path, make([]byte, mediaMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubSpeech{text: "nope"}
	tool := newAudioTranscribeTool(guard, stub)
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "too_large") {
		t.Fatalf("out = %s", out)
	}
	if stub.path != "" {
		t.Fatal("must not transcribe oversized file")
	}
}

func TestVideoAnalyzeTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.mp4")
	if err := os.WriteFile(path, make([]byte, mediaMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	tool := newVideoAnalyzeTool(guard, &stubVision{text: "frames"}).(*videoAnalyzeTool)
	tool.ffmpeg = func(context.Context, string) ([]ImageBytes, error) {
		called = true
		return nil, errors.New("should not run")
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "too_large") || called {
		t.Fatalf("out = %s called=%v", out, called)
	}
}

func TestAudioTranscribe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.wav")
	if err := os.WriteFile(path, []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubSpeech{text: "hello"}
	tool := newAudioTranscribeTool(guard, stub)
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Fatalf("out = %s", out)
	}
}

func TestVideoAnalyzeMissingFFmpeg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	tool := newVideoAnalyzeTool(guard, &stubVision{text: "frames"}).(*videoAnalyzeTool)
	tool.ffmpeg = func(context.Context, string) ([]ImageBytes, error) {
		return nil, errFFmpegMissing
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ffmpeg_missing") {
		t.Fatalf("out = %s", out)
	}
}

func TestAdvertisedKindsForVision(t *testing.T) {
	got := AdvertisedKindsFor(nil)
	for _, k := range got {
		if k == KindVision || k == KindSpeech || k == KindVideo {
			t.Fatalf("unconfigured advertised %s: %v", k, got)
		}
	}
	got = AdvertisedKindsFor(map[string]struct{}{"vision": {}})
	join := strings.Join(got, ",")
	if !strings.Contains(join, KindVision) || !strings.Contains(join, KindVideo) {
		t.Fatalf("vision roles = %v", got)
	}
	if strings.Contains(join, KindSpeech) {
		t.Fatalf("speech leaked: %v", got)
	}
}

func TestResolveChildToolsVisionRequiresRole(t *testing.T) {
	parent := []string{"fs_read", "vision_analyze", "task"}
	_, err := ResolveChildTools(KindVision, parent, nil, nil)
	if err == nil {
		t.Fatal("expected unavailable")
	}
	child, err := ResolveChildTools(KindVision, parent, nil, map[string]struct{}{"vision": {}})
	if err != nil || child.Role != "vision" {
		t.Fatalf("child = %+v err=%v", child, err)
	}
}
