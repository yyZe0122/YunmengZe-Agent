package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultGlobMaxResults = 200
	maxGlobMaxResults     = 1000
	defaultGrepMaxMatches = 50
	maxGrepMaxMatches     = 200
	defaultGrepMaxFiles   = 100
	maxGrepMaxFiles       = 500
	grepMaxFileBytes      = 1 << 20 // 1 MiB per file
	grepMaxPatternLen     = 256
	grepMaxWalkFiles      = 5000
)

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Max     int    `json:"max,omitempty"`
}

func (t *fileTool) glob(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input globInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	pattern := strings.TrimSpace(input.Pattern)
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	base := strings.TrimSpace(input.Path)
	if base == "" {
		base = "."
	}
	root, err := t.guard.ResolveContext(ctx, base)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("path must be a directory for fs_glob")
	}
	limit := input.Max
	if limit <= 0 {
		limit = defaultGlobMaxResults
	}
	if limit > maxGlobMaxResults {
		limit = maxGlobMaxResults
	}
	var matches []string
	truncated := false
	if strings.Contains(pattern, "**") {
		matches, truncated, err = t.globRecursive(ctx, root, pattern, limit)
		if err != nil {
			return nil, err
		}
	} else {
		raw, globErr := filepath.Glob(filepath.Join(root, pattern))
		if globErr != nil {
			return nil, globErr
		}
		sort.Strings(raw)
		for _, match := range raw {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resolved, resErr := t.guard.ResolveContext(ctx, match)
			if resErr != nil {
				continue
			}
			if len(matches) >= limit {
				truncated = true
				break
			}
			matches = append(matches, resolved)
		}
	}
	return encodeResult(map[string]any{
		"path": root, "pattern": pattern, "matches": matches,
		"count": len(matches), "truncated": truncated,
	})
}

const (
	globMaxDepth     = 12
	globMaxWalkFiles = 5000
)

func (t *fileTool) globRecursive(ctx context.Context, root, pattern string, limit int) ([]string, bool, error) {
	matcher, err := compileGlobMatcher(pattern)
	if err != nil {
		return nil, false, err
	}
	var out []string
	truncated := false
	walked := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, "/") + 1
		}
		if d.IsDir() {
			if depth > globMaxDepth {
				return filepath.SkipDir
			}
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "bin" || name == "dist" {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		walked++
		if walked > globMaxWalkFiles {
			truncated = true
			return errGrepWalkLimit
		}
		if rel == "." || !matcher(rel) {
			return nil
		}
		resolved, resErr := t.guard.ResolveContext(ctx, path)
		if resErr != nil {
			return nil
		}
		if len(out) >= limit {
			truncated = true
			return errGrepFileLimit
		}
		out = append(out, resolved)
		return nil
	})
	if err != nil && !errors.Is(err, errGrepFileLimit) && !errors.Is(err, errGrepWalkLimit) {
		return nil, false, err
	}
	sort.Strings(out)
	return out, truncated, nil
}

func compileGlobMatcher(pattern string) (func(rel string) bool, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	// Convert ** glob to a simple regex: ** → .*  * → [^/]*  ? → [^/]
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); {
		if i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*' {
			b.WriteString(".*")
			i += 2
			if i < len(pattern) && pattern[i] == '/' {
				i++
			}
			continue
		}
		switch pattern[i] {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
		i++
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}
	return re.MatchString, nil
}

type grepInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	MaxMatches      int    `json:"max_matches,omitempty"`
	MaxFiles        int    `json:"max_files,omitempty"`
	Literal         bool   `json:"literal,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

type grepMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func (t *fileTool) grep(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input grepInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	pattern := strings.TrimSpace(input.Pattern)
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	if len(pattern) > grepMaxPatternLen {
		return nil, errors.New("pattern too long")
	}
	base := strings.TrimSpace(input.Path)
	if base == "" {
		base = "."
	}
	root, err := t.guard.ResolveContext(ctx, base)
	if err != nil {
		return nil, err
	}
	maxMatches := input.MaxMatches
	if maxMatches <= 0 {
		maxMatches = defaultGrepMaxMatches
	}
	if maxMatches > maxGrepMaxMatches {
		maxMatches = maxGrepMaxMatches
	}
	maxFiles := input.MaxFiles
	if maxFiles <= 0 {
		maxFiles = defaultGrepMaxFiles
	}
	if maxFiles > maxGrepMaxFiles {
		maxFiles = maxGrepMaxFiles
	}
	matcher, err := compileGrepMatcher(pattern, input.Literal, input.CaseInsensitive)
	if err != nil {
		return nil, err
	}
	fileGlob := strings.TrimSpace(input.Glob)
	if strings.Contains(fileGlob, "**") {
		return nil, errors.New("recursive ** in glob is not supported")
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var files []string
	if !info.IsDir() {
		files = []string{root}
	} else {
		files, err = t.collectGrepFiles(ctx, root, fileGlob, maxFiles)
		if err != nil {
			return nil, err
		}
	}

	matches := make([]grepMatch, 0, maxMatches)
	filesScanned := 0
	truncated := false
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(matches) >= maxMatches {
			truncated = true
			break
		}
		filesScanned++
		fileMatches, fileTrunc, err := grepFile(ctx, filePath, matcher, maxMatches-len(matches))
		if err != nil {
			// Skip unreadable / binary files.
			continue
		}
		matches = append(matches, fileMatches...)
		if fileTrunc {
			truncated = true
		}
	}
	if len(matches) >= maxMatches {
		truncated = true
	}
	return encodeResult(map[string]any{
		"path": root, "pattern": pattern, "matches": matches,
		"match_count": len(matches), "files_scanned": filesScanned,
		"truncated": truncated,
	})
}

type grepMatcher interface {
	MatchString(s string) bool
}

type literalMatcher struct {
	needle string
}

func (m literalMatcher) MatchString(s string) bool {
	return strings.Contains(s, m.needle)
}

func compileGrepMatcher(pattern string, literal, caseInsensitive bool) (grepMatcher, error) {
	if literal || !looksLikeRegex(pattern) {
		needle := pattern
		if caseInsensitive {
			needle = strings.ToLower(needle)
			return caseFoldLiteralMatcher{needle: needle}, nil
		}
		return literalMatcher{needle: needle}, nil
	}
	expr := pattern
	if caseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := compileBoundedRegex(expr)
	if err != nil {
		return nil, err
	}
	return re, nil
}

type caseFoldLiteralMatcher struct {
	needle string
}

func (m caseFoldLiteralMatcher) MatchString(s string) bool {
	return strings.Contains(strings.ToLower(s), m.needle)
}

func looksLikeRegex(pattern string) bool {
	return strings.ContainsAny(pattern, `.*+?[](){}^$|\`)
}

// compileBoundedRegex uses the standard library with a length guard already applied.
func compileBoundedRegex(expr string) (grepMatcher, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return regexpMatcher{re: re}, nil
}

type regexpMatcher struct {
	re *regexp.Regexp
}

func (m regexpMatcher) MatchString(s string) bool {
	return m.re.MatchString(s)
}

func (t *fileTool) collectGrepFiles(ctx context.Context, root, fileGlob string, maxFiles int) ([]string, error) {
	var files []string
	walked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "bin" || name == "dist" {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		walked++
		if walked > grepMaxWalkFiles {
			return errGrepWalkLimit
		}
		if fileGlob != "" {
			ok, matchErr := filepath.Match(fileGlob, d.Name())
			if matchErr != nil || !ok {
				return nil
			}
		}
		resolved, resErr := t.guard.ResolveContext(ctx, path)
		if resErr != nil {
			return nil
		}
		files = append(files, resolved)
		if len(files) >= maxFiles {
			return errGrepFileLimit
		}
		return nil
	})
	if err != nil && !errors.Is(err, errGrepFileLimit) && !errors.Is(err, errGrepWalkLimit) {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

var (
	errGrepFileLimit = errors.New("grep file limit")
	errGrepWalkLimit = errors.New("grep walk limit")
)

func grepFile(ctx context.Context, path string, matcher grepMatcher, remaining int) ([]grepMatch, bool, error) {
	if remaining <= 0 {
		return nil, true, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if info.Size() > grepMaxFileBytes {
		return nil, false, errors.New("file too large")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, grepMaxFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(content)) > grepMaxFileBytes {
		return nil, false, errors.New("file too large")
	}
	if err := validateTextContent(content); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	lines := strings.Split(string(content), "\n")
	out := make([]grepMatch, 0, minInt(remaining, 8))
	truncated := false
	for i, line := range lines {
		if len(out) >= remaining {
			truncated = true
			break
		}
		// Drop trailing \r from CRLF.
		line = strings.TrimSuffix(line, "\r")
		if matcher.MatchString(line) {
			out = append(out, grepMatch{Path: path, Line: i + 1, Content: truncateRunes(line, 240)})
		}
	}
	return out, truncated, nil
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
