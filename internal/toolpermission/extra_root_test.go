package toolpermission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
)

func TestExtraRootFromPathRejectsVolumeRoot(t *testing.T) {
	if _, err := extraRootFromPath("/"); err == nil {
		t.Fatal("filesystem root must be rejected")
	}
}

func TestExtraRootFromPathUsesParentForFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := extraRootFromPath(file)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		want = filepath.Clean(dir)
	}
	if got != want && got != filepath.Clean(dir) {
		t.Fatalf("got %q want parent %q", got, dir)
	}
}

func TestExtraRootDecisionSkipsInWorkspace(t *testing.T) {
	scope := approval.CapabilityScope{Capability: "fs_read", Paths: []string{"/tmp/ws"}}
	req := Request{Capability: "fs_read", Path: "/tmp/ws/file.txt"}
	extra, _, err := extraRootDecision(scope, req)
	if err != nil {
		t.Fatal(err)
	}
	if extra {
		t.Fatal("in-workspace path is not extra-root")
	}
}
