package editrev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/artifacts"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	coresqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

func TestSnapshotAndRewind(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx = runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	afterSum := sha256.Sum256([]byte("new\n"))
	afterHex := hex.EncodeToString(afterSum[:])
	if err := store.SnapshotBeforeWrite(ctx, path, []byte("old\n"), afterHex, KindModify); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := store.Rewind(ctx, "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != path {
		t.Fatalf("path=%s", got.Path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old\n" {
		t.Fatalf("rewound=%q", body)
	}
}

func TestListByRunIDsNewestFirst(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("a0"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b0"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctxA := runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	if err := store.SnapshotBeforeWrite(ctxA, a, []byte("a0"), "aa", KindModify); err != nil {
		t.Fatal(err)
	}
	ctxB := runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	if err := store.SnapshotBeforeWrite(ctxB, b, []byte("b0"), "bb", KindModify); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListByRunIDs(ctx, "s1", []string{"r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("n=%d", len(got))
	}
	if got[0].CreatedAt < got[1].CreatedAt {
		t.Fatalf("not newest-first: %#v", got)
	}
}

func TestRewindCreateRemovesFile(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "new.txt")
	ctx = runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	after := sha256.Sum256([]byte("hello"))
	if err := store.SnapshotBeforeWrite(ctx, path, nil, hex.EncodeToString(after[:]), KindCreate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "s1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("create rewind should remove file: %v", err)
	}
}

func TestRewindDeleteRestoresFile(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gone.txt")
	if err := os.WriteFile(path, []byte("keep\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx = runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	if err := store.SnapshotBeforeWrite(ctx, path, []byte("keep\n"), "", KindDelete); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "s1", ""); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "keep\n" {
		t.Fatalf("restored=%q", body)
	}
}

func TestRewindMkdirRemovesEmptyDir(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "d")
	ctx = runmeta.With(ctx, runmeta.Context{SessionID: "s1", RunID: "r1"})
	if err := store.SnapshotBeforeWrite(ctx, path, nil, "", KindMkdir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "s1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mkdir rewind should remove dir: %v", err)
	}
}

func TestInferKindRewindsLegacyCreateFingerprint(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	after := sha256.Sum256([]byte("hello"))
	stamp := "2026-08-27T12:00:00Z"
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO edit_revisions (revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"erev-legacy-create", "s1", path, "", hex.EncodeToString(after[:]), "", "r1", KindModify, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "s1", "erev-legacy-create"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy create should delete file: %v", err)
	}
}

func TestInferKindRewindsLegacyMkdirFingerprint(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	arts, err := artifacts.NewStore(db.SQL(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db.SQL(), arts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO edit_revisions (revision_id, session_id, path, sha_before, sha_after, artifact_id, run_id, kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"erev-legacy-mkdir", "s1", path, "", "", "", "r1", KindModify, "2026-08-27T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "s1", "erev-legacy-mkdir"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy mkdir should remove dir: %v", err)
	}
}
