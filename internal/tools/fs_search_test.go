package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSGlobAndGrep(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc Hello() {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("hello world\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.go"), []byte("package sub\n// marker-xyz\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	tools := newFileTools(guard)
	var globTool, grepTool Tool
	for _, tool := range tools {
		switch tool.Definition().Name {
		case "fs_glob":
			globTool = tool
		case "fs_grep":
			grepTool = tool
		}
	}
	if globTool == nil || grepTool == nil {
		t.Fatal("missing fs_glob or fs_grep")
	}

	ctx := context.Background()
	raw, err := globTool.Execute(ctx, json.RawMessage(`{"pattern":"*.go","path":"."}`))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var globOut struct {
		Matches []string `json:"matches"`
		Count   int      `json:"count"`
	}
	if err := json.Unmarshal(raw, &globOut); err != nil {
		t.Fatal(err)
	}
	if globOut.Count != 1 {
		t.Fatalf("glob top-level *.go count=%d matches=%v", globOut.Count, globOut.Matches)
	}

	raw, err = grepTool.Execute(ctx, json.RawMessage(`{"pattern":"marker-xyz","path":"."}`))
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	var grepOut struct {
		Matches []struct {
			Path    string `json:"path"`
			Line    int    `json:"line"`
			Content string `json:"content"`
		} `json:"matches"`
		MatchCount int `json:"match_count"`
	}
	if err := json.Unmarshal(raw, &grepOut); err != nil {
		t.Fatal(err)
	}
	if grepOut.MatchCount != 1 {
		t.Fatalf("grep matches=%d %+v", grepOut.MatchCount, grepOut.Matches)
	}
	if !strings.Contains(grepOut.Matches[0].Path, "c.go") {
		t.Fatalf("path = %q", grepOut.Matches[0].Path)
	}

	// Escape outside root must fail authorization path resolve.
	if _, err := globTool.Authorization(context.Background(), json.RawMessage(`{"pattern":"*","path":".."}`)); err == nil {
		t.Fatal("expected escape to fail authorization")
	}
}
