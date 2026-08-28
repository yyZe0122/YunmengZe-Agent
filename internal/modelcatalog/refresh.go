package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var errEmptyCatalog = errors.New("model catalog is empty")

const defaultRefreshTimeout = 5 * time.Second

// Init loads ConfigDir/cache/models.dev.json when it has at least as many
// models as the embed, else embed. Network is never used here; call Refresh
// in a background goroutine.
func Init(configDir string) {
	raw, err := os.ReadFile(cachePath(configDir))
	if err != nil {
		return
	}
	if len(raw) == 0 || int64(len(raw)) > maxCatalogBytes {
		return
	}
	c, err := parse(raw)
	if err != nil || c == nil || len(c.all) == 0 {
		slog.Warn("model catalog cache ignored", "component", "modelcatalog", "operation", "init", "result", "warning", "error", err)
		return
	}
	embedN := embedCount()
	if len(c.all) < embedN {
		slog.Warn("model catalog cache older than embed, ignored", "component", "modelcatalog", "operation", "init", "result", "warning", "cache", len(c.all), "embed", embedN)
		return
	}
	swap(c)
	slog.Info("model catalog loaded from cache", "component", "modelcatalog", "operation", "init", "result", "succeeded", "models", len(c.all))
}

func embedCount() int {
	c, err := parse(embeddedModels)
	if err != nil || c == nil {
		return 0
	}
	return len(c.all)
}

// Refresh fetches models.json and writes ConfigDir/cache when the body parses.
func Refresh(ctx context.Context, configDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := defaultRefreshTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return ctx.Err()
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "YunmengZe/0")
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are disabled")
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("models.dev status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxCatalogBytes {
		return fmt.Errorf("models.dev body exceeds %d bytes", maxCatalogBytes)
	}
	c, err := parse(raw)
	if err != nil {
		return err
	}
	if c == nil || len(c.all) == 0 {
		return errEmptyCatalog
	}
	if err := writeCache(configDir, raw); err != nil {
		slog.Warn("model catalog cache write failed", "component", "modelcatalog", "operation", "refresh", "result", "warning", "error", err)
	}
	swap(c)
	slog.Info("model catalog refreshed", "component", "modelcatalog", "operation", "refresh", "result", "succeeded", "models", len(c.all))
	return nil
}

func cachePath(configDir string) string {
	return filepath.Join(trimSpace(configDir), CacheDirName, CacheFilename)
}

func writeCache(configDir string, raw []byte) error {
	configDir = trimSpace(configDir)
	if configDir == "" {
		return errors.New("config directory is required")
	}
	dir := filepath.Join(configDir, CacheDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(dir, CacheFilename)
	tmp, err := os.CreateTemp(dir, ".models.dev-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
