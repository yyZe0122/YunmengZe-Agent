package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type writeInput struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

func (t *fileTool) write(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input writeInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.Resolve(input.Path)
	if err != nil {
		return nil, err
	}
	created := false
	before, beforeHash, err := readExistingText(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if errors.Is(err, os.ErrNotExist) {
		created = true
	}
	if want := strings.TrimSpace(input.ExpectedSHA256); want != "" && want != beforeHash {
		return encodeResult(map[string]any{
			"error": "expected_sha256 mismatch", "path": path, "sha256": beforeHash,
		})
	}
	kind := "modify"
	if created {
		kind = "create"
	}
	if err := t.checkpointWrite(ctx, path, []byte(before), sha256Hex([]byte(input.Content)), kind); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := atomicWrite(ctx, path, []byte(input.Content)); err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{
		"path": path, "size_bytes": len(input.Content),
		"sha256": sha256Hex([]byte(input.Content)),
		"diff":   unifiedDiff(filepath.Base(path), before, input.Content),
	})
}

type patchInput struct {
	Path           string `json:"path"`
	Old            string `json:"old"`
	New            string `json:"new"`
	ReplaceAll     bool   `json:"replace_all,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
}

func (t *fileTool) patch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input patchInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Old == "" {
		return nil, errors.New("patch old text is required")
	}
	path, err := t.guard.Resolve(input.Path)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateTextContent(content); err != nil {
		return nil, err
	}
	currentHash := sha256Hex(content)
	if want := strings.TrimSpace(input.ExpectedSHA256); want != "" && want != currentHash {
		return encodeResult(map[string]any{
			"error": "expected_sha256 mismatch", "path": path, "sha256": currentHash,
		})
	}
	updated, n, hint, matchErr := matchPatchOld(string(content), input.Old, input.New, input.ReplaceAll)
	if matchErr != nil {
		return encodeResult(map[string]any{
			"error": matchErr.Error(), "path": path, "sha256": currentHash,
			"context": hint,
		})
	}
	if err := t.checkpointWrite(ctx, path, content, sha256Hex([]byte(updated)), "modify"); err != nil {
		return nil, err
	}
	if err := atomicWrite(ctx, path, []byte(updated)); err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{
		"path": path, "replacements": n, "size_bytes": len(updated),
		"sha256": sha256Hex([]byte(updated)),
		"diff":   unifiedDiff(filepath.Base(path), string(content), updated),
	})
}

func readExistingText(path string) (string, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	if err := validateTextContent(content); err != nil {
		return "", sha256Hex(content), err
	}
	return string(content), sha256Hex(content), nil
}

func (t *fileTool) mkdir(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.Resolve(input.Path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		return encodeResult(map[string]any{"path": path, "existed": true})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := t.checkpointWrite(ctx, path, nil, "", "mkdir"); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{"path": path})
}

func (t *fileTool) remove(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.Resolve(input.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("fs_remove deletes one regular file; directories are not removed")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to remove symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fs_remove only deletes regular files")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := t.checkpointWrite(ctx, path, before, "", "delete"); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{"path": path, "removed": true})
}

func (t *fileTool) checkpointWrite(ctx context.Context, path string, before []byte, shaAfter, kind string) error {
	if t == nil || t.checkpoints == nil {
		return nil
	}
	return t.checkpoints.SnapshotBeforeWrite(ctx, path, before, shaAfter, kind)
}

func atomicWrite(ctx context.Context, path string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".yunmengze-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}
