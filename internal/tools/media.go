package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

const (
	mediaMaxBytes       = 16 * 1024 * 1024
	mediaVisionMaxSide  = 768
	mediaVideoMaxFrames = 8
	mediaFFmpegTimeout  = 45 * time.Second
)

var imageMIME = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp",
}

var audioMIME = map[string]string{
	".wav": "audio/wav", ".mp3": "audio/mpeg", ".m4a": "audio/mp4",
	".ogg": "audio/ogg", ".webm": "audio/webm",
}

var videoExt = map[string]struct{}{
	".mp4": {}, ".webm": {}, ".mkv": {}, ".mov": {},
}

// ImageBytes is one auxiliary image for VisionCompleter.
type ImageBytes struct {
	MIME string
	Data []byte
}

// VisionCompleter runs one auxiliary vision Complete (ADR-055).
type VisionCompleter interface {
	Complete(ctx context.Context, prompt string, images []ImageBytes) (string, error)
}

// AudioTranscriber runs Whisper-compatible transcription (not chat Complete).
type AudioTranscriber interface {
	Transcribe(ctx context.Context, path, mime string) (string, error)
}

// RegisterMediaTools registers vision_analyze / audio_transcribe / video_analyze when backends are set.
func RegisterMediaTools(broker *Broker, guard *PathGuard, vision VisionCompleter, speech AudioTranscriber) error {
	if broker == nil {
		return errors.New("tool broker is required")
	}
	if guard == nil {
		return errors.New("path guard is required")
	}
	if vision != nil {
		if err := broker.Register(newVisionAnalyzeTool(guard, vision)); err != nil {
			return err
		}
		if err := broker.Register(newVideoAnalyzeTool(guard, vision)); err != nil {
			return err
		}
	}
	if speech != nil {
		if err := broker.Register(newAudioTranscribeTool(guard, speech)); err != nil {
			return err
		}
	}
	return nil
}

type visionAnalyzeTool struct {
	guard  *PathGuard
	vision VisionCompleter
	client *http.Client
}

func newVisionAnalyzeTool(guard *PathGuard, vision VisionCompleter) Tool {
	return &visionAnalyzeTool{guard: guard, vision: vision, client: noRedirectClient()}
}

func (t *visionAnalyzeTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "vision_analyze", Description: "Analyze an image with the configured vision model. Prefer workspace path. url waits /perm (similar = host) and uses the SSRF baseline. Plan/cron cannot fetch urls. Returns text for the main model.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 60000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"url":{"type":"string"},"prompt":{"type":"string"}},"anyOf":[{"required":["path"]},{"required":["url"]}]}`),
	}
}

type visionInput struct {
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

func (t *visionAnalyzeTool) Authorization(raw json.RawMessage) (Authorization, error) {
	input, err := parseVisionInput(raw)
	if err != nil {
		return Authorization{}, err
	}
	if input.Path != "" {
		resolved, err := t.guard.Resolve(input.Path)
		if err != nil {
			return Authorization{}, err
		}
		return Authorization{Capability: "vision_analyze", Path: resolved}, nil
	}
	parsed, err := parsePublicHTTPURL(input.URL)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: "vision_analyze", NetworkDomain: strings.ToLower(parsed.Hostname())}, nil
}

func (t *visionAnalyzeTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	input, err := parseVisionInput(raw)
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		prompt = "Describe the image."
	}
	var data []byte
	var mime string
	source := "path"
	if input.Path != "" {
		resolved, err := t.guard.Resolve(input.Path)
		if err != nil {
			return nil, err
		}
		data, mime, err = readMediaFile(resolved, imageMIME)
		if err != nil {
			return encodeResult(map[string]any{"error": "read_failed", "message": err.Error()})
		}
	} else {
		source = "url"
		parsed, err := parsePublicHTTPURL(input.URL)
		if err != nil {
			return nil, err
		}
		if err := validateHTTPGetURLHost(ctx, parsed.Hostname()); err != nil {
			return nil, err
		}
		data, mime, err = fetchImageURL(ctx, t.client, input.URL)
		if err != nil {
			return encodeResult(map[string]any{"error": "fetch_failed", "message": err.Error()})
		}
	}
	if t.vision == nil {
		return encodeResult(map[string]any{"error": "unavailable", "message": "models.vision is not configured"})
	}
	text, err := t.vision.Complete(ctx, prompt, []ImageBytes{{MIME: mime, Data: data}})
	if err != nil {
		return encodeResult(map[string]any{"error": "vision_failed", "message": err.Error()})
	}
	return encodeResult(map[string]any{"text": strings.TrimSpace(text), "source": source, "mime": mime})
}

func parseVisionInput(raw json.RawMessage) (visionInput, error) {
	var input visionInput
	if err := decodeStrict(raw, &input); err != nil {
		return input, err
	}
	input.Path = strings.TrimSpace(input.Path)
	input.URL = strings.TrimSpace(input.URL)
	if input.Path != "" && input.URL != "" {
		return input, errors.New("path and url are mutually exclusive")
	}
	if input.Path == "" && input.URL == "" {
		return input, errors.New("path or url is required")
	}
	return input, nil
}

type audioTranscribeTool struct {
	guard  *PathGuard
	speech AudioTranscriber
}

func newAudioTranscribeTool(guard *PathGuard, speech AudioTranscriber) Tool {
	return &audioTranscribeTool{guard: guard, speech: speech}
}

func (t *audioTranscribeTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "audio_transcribe", Description: "Transcribe a workspace audio or video file with the configured speech (Whisper) model. Returns text.",
		Risk: string(policy.RiskR1), DefaultTimeoutMillis: 120000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string"}}}`),
	}
}

func (t *audioTranscribeTool) Authorization(raw json.RawMessage) (Authorization, error) {
	path, err := parsePathOnly(raw)
	if err != nil {
		return Authorization{}, err
	}
	resolved, err := t.guard.Resolve(path)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: "audio_transcribe", Path: resolved}, nil
}

func (t *audioTranscribeTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	path, err := parsePathOnly(raw)
	if err != nil {
		return nil, err
	}
	resolved, err := t.guard.Resolve(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	mime := audioMIME[ext]
	if mime == "" {
		if _, ok := videoExt[ext]; ok {
			mime = "video/mp4"
		}
	}
	if mime == "" {
		return encodeResult(map[string]any{"error": "unsupported_type", "message": "audio_transcribe accepts wav/mp3/m4a/ogg/webm or mp4/webm/mkv"})
	}
	if err := rejectOversizeMedia(resolved); err != nil {
		return encodeResult(map[string]any{"error": "too_large", "message": err.Error()})
	}
	if t.speech == nil {
		return encodeResult(map[string]any{"error": "unavailable", "message": "models.speech is not configured"})
	}
	text, err := t.speech.Transcribe(ctx, resolved, mime)
	if err != nil {
		return encodeResult(map[string]any{"error": "transcribe_failed", "message": err.Error()})
	}
	return encodeResult(map[string]any{"text": strings.TrimSpace(text), "path": resolved})
}

type videoAnalyzeTool struct {
	guard  *PathGuard
	vision VisionCompleter
	ffmpeg func(ctx context.Context, path string) ([]ImageBytes, error)
}

func newVideoAnalyzeTool(guard *PathGuard, vision VisionCompleter) Tool {
	t := &videoAnalyzeTool{guard: guard, vision: vision}
	t.ffmpeg = t.extractFrames
	return t
}

func (t *videoAnalyzeTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "video_analyze", Description: "Analyze a workspace video by extracting up to 8 frames with ffmpeg and sending them to the vision model. Missing ffmpeg returns an observation error.",
		Risk: string(policy.RiskR1), DefaultTimeoutMillis: 120000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string"},"prompt":{"type":"string"}}}`),
	}
}

type videoInput struct {
	Path   string `json:"path"`
	Prompt string `json:"prompt,omitempty"`
}

func (t *videoAnalyzeTool) Authorization(raw json.RawMessage) (Authorization, error) {
	var input videoInput
	if err := decodeStrict(raw, &input); err != nil {
		return Authorization{}, err
	}
	resolved, err := t.guard.Resolve(strings.TrimSpace(input.Path))
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: "video_analyze", Path: resolved}, nil
}

func (t *videoAnalyzeTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input videoInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	resolved, err := t.guard.Resolve(strings.TrimSpace(input.Path))
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if _, ok := videoExt[ext]; !ok {
		return encodeResult(map[string]any{"error": "unsupported_type", "message": "video_analyze accepts mp4/webm/mkv/mov"})
	}
	if err := rejectOversizeMedia(resolved); err != nil {
		return encodeResult(map[string]any{"error": "too_large", "message": err.Error()})
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		prompt = "Describe what happens in these video frames."
	}
	if t.ffmpeg == nil {
		t.ffmpeg = t.extractFrames
	}
	frames, err := t.ffmpeg(ctx, resolved)
	if err != nil {
		code := "ffmpeg_failed"
		if errors.Is(err, errFFmpegMissing) {
			code = "ffmpeg_missing"
		}
		return encodeResult(map[string]any{"error": code, "message": err.Error()})
	}
	if t.vision == nil {
		return encodeResult(map[string]any{"error": "unavailable", "message": "models.vision is not configured"})
	}
	text, err := t.vision.Complete(ctx, prompt, frames)
	if err != nil {
		return encodeResult(map[string]any{"error": "vision_failed", "message": err.Error()})
	}
	return encodeResult(map[string]any{"text": strings.TrimSpace(text), "frames": len(frames), "path": resolved})
}

var errFFmpegMissing = errors.New("ffmpeg is not installed")

func (t *videoAnalyzeTool) extractFrames(ctx context.Context, path string) ([]ImageBytes, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errFFmpegMissing
	}
	dir, err := os.MkdirTemp("", "ymz-video-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	outPattern := filepath.Join(dir, "frame-%02d.jpg")
	fctx, cancel := context.WithTimeout(ctx, mediaFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(fctx, bin, "-hide_banner", "-loglevel", "error", "-i", path,
		"-vf", "fps=1,scale='min("+fmt.Sprintf("%d", mediaVisionMaxSide)+",iw)':-1",
		"-frames:v", fmt.Sprintf("%d", mediaVideoMaxFrames), "-q:v", "5", outPattern)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	frames := make([]ImageBytes, 0, mediaVideoMaxFrames)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".jpg") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || len(data) > mediaMaxBytes {
			continue
		}
		frames = append(frames, ImageBytes{MIME: "image/jpeg", Data: data})
		if len(frames) >= mediaVideoMaxFrames {
			break
		}
	}
	if len(frames) == 0 {
		return nil, errors.New("ffmpeg produced no frames")
	}
	return frames, nil
}

func rejectOversizeMedia(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("path is a directory")
	}
	if info.Size() > mediaMaxBytes {
		return fmt.Errorf("file exceeds %d bytes", mediaMaxBytes)
	}
	return nil
}

func parsePathOnly(raw json.RawMessage) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return "", err
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return "", errors.New("path is required")
	}
	return path, nil
}

func parsePublicHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("URL host is required and userinfo is forbidden")
	}
	return parsed, nil
}

func readMediaFile(path string, allowed map[string]string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := allowed[ext]
	if !ok {
		return nil, "", fmt.Errorf("unsupported file type %s", ext)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		return nil, "", errors.New("path is a directory")
	}
	if info.Size() > mediaMaxBytes {
		return nil, "", fmt.Errorf("file exceeds %d bytes", mediaMaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

func fetchImageURL(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", webExtractUA)
	body, contentType, err := doHTTP(client, req, mediaMaxBytes)
	if err != nil {
		return nil, "", err
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		if look := sniffImageMIME(body); look != "" {
			mime = look
		} else {
			return nil, "", fmt.Errorf("url is not an image (%s)", contentType)
		}
	}
	return body, mime, nil
}

func sniffImageMIME(body []byte) string {
	if len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff {
		return "image/jpeg"
	}
	if len(body) >= 8 && bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return "image/png"
	}
	if len(body) >= 6 && (bytes.Equal(body[:6], []byte("GIF87a")) || bytes.Equal(body[:6], []byte("GIF89a"))) {
		return "image/gif"
	}
	if len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	return ""
}
