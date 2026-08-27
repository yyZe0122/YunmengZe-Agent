package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileToolByName(t *testing.T, root, name string) Tool {
	t.Helper()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range newFileTools(guard) {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("missing %s", name)
	return nil
}

func TestFSReadOffsetLimitSHA256(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 1; i <= 10; i++ {
		body.WriteString("line-")
		body.WriteByte(byte('0' + i%10))
		body.WriteByte('\n')
	}
	path := filepath.Join(root, "n.txt")
	if err := os.WriteFile(path, []byte(body.String()), 0o640); err != nil {
		t.Fatal(err)
	}
	read := fileToolByName(t, root, "fs_read")
	raw, err := read.Execute(context.Background(), json.RawMessage(`{"path":"n.txt","offset":3,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Content   string `json:"content"`
		SHA256    string `json:"sha256"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
		LineCount int    `json:"line_count"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Offset != 3 || out.Limit != 2 || !out.Truncated {
		t.Fatalf("meta=%+v", out)
	}
	if !strings.Contains(out.Content, "     3|") || !strings.Contains(out.Content, "     4|") {
		t.Fatalf("content=%q", out.Content)
	}
	if len(out.SHA256) != 64 {
		t.Fatalf("sha=%q", out.SHA256)
	}
}

func TestFSPatchCRLFIndentHashAndDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	original := "package main\r\nfunc Hello() {\r\n\treturn\r\n}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	patch := fileToolByName(t, root, "fs_patch")
	raw, err := patch.Execute(context.Background(), json.RawMessage(`{"path":"a.go","old":"func Hello() {\nreturn\n}","new":"func Hello() {\n\treturn 1\n}"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Error        string `json:"error"`
		Replacements int    `json:"replacements"`
		Diff         string `json:"diff"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Error != "" || out.Replacements != 1 {
		t.Fatalf("patch=%+v", out)
	}
	if !strings.Contains(out.Diff, "+") || !strings.Contains(out.Diff, "--- a/a.go") {
		t.Fatalf("diff=%q", out.Diff)
	}
	raw, err = patch.Execute(context.Background(), json.RawMessage(`{"path":"a.go","old":"nope","new":"x","expected_sha256":"deadbeef"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Error, "expected_sha256") || out.SHA256 == "" {
		t.Fatalf("hash reject=%+v", out)
	}
	raw, err = patch.Execute(context.Background(), json.RawMessage(`{"path":"a.go","old":"missing-token-xyz","new":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var miss struct {
		Error   string `json:"error"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(raw, &miss); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(miss.Error, "not found") || !strings.Contains(miss.Context, "|") {
		t.Fatalf("context=%+v", miss)
	}
}

func TestFSWriteExpectedHashAndDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "w.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	write := fileToolByName(t, root, "fs_write")
	raw, err := write.Execute(context.Background(), json.RawMessage(`{"path":"w.txt","content":"new\n","expected_sha256":"00"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Error  string `json:"error"`
		SHA256 string `json:"sha256"`
		Diff   string `json:"diff"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Error, "expected_sha256") {
		t.Fatalf("want hash reject: %+v", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old\n" {
		t.Fatalf("file mutated on hash reject: %q", got)
	}
	raw, err = write.Execute(context.Background(), json.RawMessage(`{"path":"w.txt","content":"new\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Diff, "-old") || !strings.Contains(out.Diff, "+new") {
		t.Fatalf("diff=%q", out.Diff)
	}
}

func TestFSGlobDoubleStar(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "c.go"), []byte("package c\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	glob := fileToolByName(t, root, "fs_glob")
	raw, err := glob.Execute(context.Background(), json.RawMessage(`{"pattern":"**/*.go","path":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Matches []string `json:"matches"`
		Count   int      `json:"count"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 2 {
		t.Fatalf("**/*.go count=%d matches=%v", out.Count, out.Matches)
	}
}

func TestFSRemoveRegularFileOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.txt")
	if err := os.WriteFile(path, []byte("bye\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	rm := fileToolByName(t, root, "fs_remove")
	if _, err := rm.Execute(context.Background(), json.RawMessage(`{"path":"x.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	dir := filepath.Join(root, "d")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := rm.Execute(context.Background(), json.RawMessage(`{"path":"d"}`)); err == nil {
		t.Fatal("expected directory remove to fail")
	}
}

func TestTruncateToolOutputIncludesArtifactID(t *testing.T) {
	body := truncateToolOutput([]byte(strings.Repeat("x", 5000)), 64, "art-1")
	var out struct {
		Truncated  bool   `json:"truncated"`
		Preview    string `json:"preview"`
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || out.ArtifactID != "art-1" || out.Preview == "" {
		t.Fatalf("%+v", out)
	}
}
