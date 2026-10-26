package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// Serialize built-in file mutations, including those from sibling tasks. Hashes
// still detect stale reads; this does not lock out editors or shell commands.
var fileMutation sync.Mutex

func Path(workspace, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspace, p)
	}
	return filepath.Abs(p)
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func ReadFile(path string, maxBytes int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if e == nil && int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return b, e
}
func atomicWrite(path string, b []byte) error {
	mode := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ymz-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (r *Registry) files() {
	r.Register(Tool{Name: "read", Description: "Read UTF-8 text with 1-based line offset and a SHA-256 for subsequent edits.", ReadOnly: true, Schema: Schema(`{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}}`, "path"), Handler: func(ctx context.Context, e Env, raw json.RawMessage) (any, error) {
		var a struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := Decode(raw, &a); err != nil {
			return nil, err
		}
		p, err := Path(e.Workspace, a.Path)
		if err != nil {
			return nil, err
		}
		b, err := ReadFile(p, 8<<20)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(b) {
			return nil, errors.New("file is not UTF-8 text")
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		lines := strings.Split(strings.ReplaceAll(strings.TrimPrefix(string(b), "\ufeff"), "\r\n", "\n"), "\n")
		if a.Offset <= 0 {
			a.Offset = 1
		}
		if a.Limit <= 0 || a.Limit > 2000 {
			a.Limit = 2000
		}
		start := min(a.Offset-1, len(lines))
		end := min(start+a.Limit, len(lines))
		return map[string]any{"path": p, "sha256": digest(b), "total_lines": len(lines), "offset": a.Offset, "content": strings.Join(lines[start:end], "\n")}, nil
	}})
	for _, name := range []string{"write", "edit"} {
		properties := `{"path":{"type":"string"},"expected_sha256":{"type":"string"},"content":{"type":"string"}}`
		required := []string{"path", "content"}
		description := "Write a UTF-8 file atomically. expected_sha256 detects changes since reading."
		if name == "edit" {
			properties = `{"path":{"type":"string"},"expected_sha256":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"all":{"type":"boolean"}}`
			required = []string{"path", "old_text", "new_text"}
			description = "Replace an exact text occurrence atomically. Ambiguous matches fail unless all=true."
		}
		r.Register(Tool{Name: name, Description: description, Schema: Schema(properties, required...), Handler: func(ctx context.Context, e Env, raw json.RawMessage) (any, error) {
			var a struct {
				Path     string `json:"path"`
				Expected string `json:"expected_sha256"`
				Content  string `json:"content"`
				Old      string `json:"old_text"`
				New      string `json:"new_text"`
				All      bool   `json:"all"`
			}
			if err := Decode(raw, &a); err != nil {
				return nil, err
			}
			p, err := Path(e.Workspace, a.Path)
			if err != nil {
				return nil, err
			}
			fileMutation.Lock()
			defer fileMutation.Unlock()
			b, readErr := ReadFile(p, 8<<20)
			if readErr != nil && (!errors.Is(readErr, os.ErrNotExist) || name == "edit" || a.Expected != "") {
				return nil, readErr
			}
			if a.Expected != "" && digest(b) != a.Expected {
				return nil, errors.New("file changed since read; read it again")
			}
			if name == "edit" {
				if a.Old == "" {
					return nil, errors.New("old_text cannot be empty")
				}
				if !utf8.Valid(b) {
					return nil, errors.New("file is not UTF-8 text")
				}
				original := strings.TrimPrefix(string(b), "\ufeff")
				old := strings.ReplaceAll(strings.TrimPrefix(a.Old, "\ufeff"), "\r\n", "\n")
				if old == "" {
					return nil, errors.New("old_text cannot contain only a BOM")
				}
				pattern := strings.ReplaceAll(regexp.QuoteMeta(old), "\n", "(?:\r\n|\n)")
				matcher, err := regexp.Compile(pattern)
				if err != nil {
					return nil, err
				}
				matches := matcher.FindAllStringIndex(original, -1)
				if len(matches) == 0 || len(matches) > 1 && !a.All {
					return nil, fmt.Errorf("old_text matched %d times", len(matches))
				}
				replacement := strings.ReplaceAll(a.New, "\r\n", "\n")
				crlfReplacement := strings.ReplaceAll(replacement, "\n", "\r\n")
				hasCRLF := strings.Contains(original, "\r\n")
				replacementFor := func(match string) string {
					if strings.Contains(match, "\r\n") || (!strings.Contains(match, "\n") && hasCRLF) {
						return crlfReplacement
					}
					return replacement
				}
				size := len(b)
				for _, match := range matches {
					size += len(replacementFor(original[match[0]:match[1]])) - (match[1] - match[0])
				}
				if size > 8<<20 {
					return nil, errors.New("write exceeds 8 MiB")
				}
				var edited strings.Builder
				edited.Grow(size)
				if strings.HasPrefix(string(b), "\ufeff") {
					edited.WriteString("\ufeff")
				}
				offset := 0
				for _, match := range matches {
					edited.WriteString(original[offset:match[0]])
					edited.WriteString(replacementFor(original[match[0]:match[1]]))
					offset = match[1]
				}
				edited.WriteString(original[offset:])
				a.Content = edited.String()
			}
			if len(a.Content) > 8<<20 {
				return nil, errors.New("write exceeds 8 MiB")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := atomicWrite(p, []byte(a.Content)); err != nil {
				return nil, err
			}
			return map[string]any{"path": p, "sha256": digest([]byte(a.Content)), "diff": changePreview(string(b), a.Content)}, nil
		}})
	}
	for _, name := range []string{"glob", "grep"} {
		r.Register(Tool{Name: name, ReadOnly: true, Description: map[string]string{"glob": "Find files with a glob; ** matches nested directories.", "grep": "Search UTF-8 files by Go regular expression."}[name], Schema: Schema(`{"path":{"type":"string"},"pattern":{"type":"string"},"limit":{"type":"integer","minimum":1}}`, "pattern"), Handler: func(ctx context.Context, e Env, raw json.RawMessage) (any, error) {
			var a struct {
				Path    string `json:"path"`
				Pattern string `json:"pattern"`
				Limit   int    `json:"limit"`
			}
			if err := Decode(raw, &a); err != nil {
				return nil, err
			}
			if a.Pattern == "" {
				return nil, errors.New("pattern is required")
			}
			if a.Path == "" {
				a.Path = "."
			}
			base, err := Path(e.Workspace, a.Path)
			if err != nil {
				return nil, err
			}
			if a.Limit <= 0 || a.Limit > 1000 {
				a.Limit = 100
			}
			pattern := a.Pattern
			if name == "glob" {
				pattern = regexp.QuoteMeta(filepath.ToSlash(pattern))
				pattern = strings.ReplaceAll(pattern, `\*\*/`, `(?:.*/)?`)
				pattern = strings.ReplaceAll(pattern, `\*\*`, `.*`)
				pattern = strings.ReplaceAll(pattern, `\*`, `[^/]*`)
				pattern = strings.ReplaceAll(pattern, `\?`, `[^/]`)
				pattern = "^" + pattern + "$"
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, err
			}
			var hits []string
			files := 0
			truncated := false
			err = filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					return walkErr
				}
				if len(hits) >= a.Limit || files >= 10000 {
					truncated = true
					return fs.SkipAll
				}
				if d.IsDir() {
					if p != base && (d.Name() == ".git" || d.Name() == ".cache" || d.Name() == "node_modules") {
						return fs.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				files++
				rel, _ := filepath.Rel(base, p)
				if name == "glob" {
					if re.MatchString(filepath.ToSlash(rel)) {
						hits = append(hits, p)
					}
					return nil
				}
				b, err := ReadFile(p, 1<<20)
				if err != nil {
					return nil
				}
				if !utf8.Valid(b) {
					return nil
				}
				scanner := bufio.NewScanner(strings.NewReader(string(b)))
				scanner.Buffer(make([]byte, 4096), 1<<20)
				line := 0
				for scanner.Scan() {
					line++
					if re.MatchString(scanner.Text()) {
						text := scanner.Text()
						if len(text) > 1000 {
							text = text[:1000]
						}
						hits = append(hits, fmt.Sprintf("%s:%d:%s", p, line, text))
						if len(hits) >= a.Limit {
							break
						}
					}
				}
				return scanner.Err()
			})
			return map[string]any{"matches": hits, "truncated": truncated || len(hits) >= a.Limit}, err
		}})
	}
}

func changePreview(before, after string) string {
	old := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n")
	new := strings.Split(strings.ReplaceAll(after, "\r\n", "\n"), "\n")
	start := 0
	for start < len(old) && start < len(new) && old[start] == new[start] {
		start++
	}
	if start == len(old) && start == len(new) {
		return "No content changes"
	}
	a, b := len(old), len(new)
	for a > start && b > start && old[a-1] == new[b-1] {
		a--
		b--
	}
	var out strings.Builder
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", start+1, a-start, start+1, b-start)
	for _, line := range old[start:min(a, start+40)] {
		out.WriteString("-" + line + "\n")
	}
	if a-start > 40 {
		out.WriteString("... removed lines omitted ...\n")
	}
	for _, line := range new[start:min(b, start+40)] {
		out.WriteString("+" + line + "\n")
	}
	if b-start > 40 {
		out.WriteString("... added lines omitted ...\n")
	}
	return BoundText(out.String(), 12000)
}
