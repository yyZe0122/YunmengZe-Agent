package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

type readInput struct {
	Path     string `json:"path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

func (t *fileTool) read(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input readInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.ResolveContext(ctx, input.Path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	byteCap := input.MaxBytes
	if byteCap <= 0 {
		byteCap = defaultFileReadBytes
	}
	readLimit := byteCap
	if byteCap <= (1<<63-1)-int64(utf8.UTFMax) {
		readLimit += int64(utf8.UTFMax)
	}
	content, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	byteTrunc := int64(len(content)) > byteCap
	content, err = textPrefix(content, byteCap)
	if err != nil {
		return nil, err
	}
	hash := sha256Hex(content)
	lines := strings.Split(normalizeNewlines(string(content)), "\n")
	offset := input.Offset
	if offset < 1 {
		offset = 1
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLineLimit
	}
	if limit > maxReadLineLimit {
		limit = maxReadLineLimit
	}
	lineTrunc := offset-1+limit < len(lines)
	if offset > len(lines)+1 {
		offset = len(lines) + 1
		lineTrunc = false
	}
	body := numberedSlice(lines, offset, limit)
	return encodeResult(map[string]any{
		"path": path, "content": body, "sha256": hash,
		"offset": offset, "limit": limit, "line_count": len(lines),
		"size_bytes": len(content), "truncated": byteTrunc || lineTrunc,
	})
}

func textPrefix(content []byte, limit int64) ([]byte, error) {
	end := len(content)
	if limit < int64(end) {
		end = int(limit)
	}
	position := 0
	for position < end {
		if content[position] == 0 {
			return nil, errors.New("file contains binary data")
		}
		r, size := utf8.DecodeRune(content[position:])
		if r == utf8.RuneError && size == 1 {
			return nil, errors.New("file content is not valid UTF-8 text")
		}
		if position+size > end {
			break
		}
		position += size
	}
	return content[:position], nil
}

func validateTextContent(content []byte) error {
	_, err := textPrefix(content, int64(len(content)))
	return err
}
func (t *fileTool) list(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.ResolveContext(ctx, input.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	result := make([]entry, 0, len(entries))
	for _, item := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kind := "file"
		if item.IsDir() {
			kind = "directory"
		} else if item.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		result = append(result, entry{Name: item.Name(), Type: kind})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return encodeResult(map[string]any{"path": path, "entries": result})
}

func (t *fileTool) stat(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	path, err := t.guard.ResolveContext(ctx, input.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encodeResult(map[string]any{
		"path": path, "name": info.Name(), "size_bytes": info.Size(), "mode": info.Mode().String(),
		"is_directory": info.IsDir(), "modified_at": info.ModTime().UTC(),
	})
}
