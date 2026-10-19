package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	storesqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

func TestStoreListSearchLegacyRecords(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := NewStore(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	// Existing installations can contain session/global facts; production no longer creates them.
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memory_entries(entry_id,session_id,content,kind,created_at) VALUES
		('e1','s1','prefer Go 1.26','session',?),
		('e2','','global note','curated',?)`, now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memory_entries_fts(entry_id,content) SELECT entry_id,content FROM memory_entries`); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListRecent(ctx, "s1", true, true, 10, "")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	found, err := store.Search(ctx, "s1", "Go 1.26", true, 10, "")
	if err != nil || len(found) != 1 || !strings.Contains(found[0].Content, "Go") {
		t.Fatalf("search = %v err=%v", found, err)
	}
}

func TestStoreFreeze(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := NewStore(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := New(Config{Store: store, MaxInjectRunes: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.savePreference(ctx, preferenceProposal{Key: "a", Category: "workflow", Content: "session fact A"}, mgr.now().Format(time.RFC3339Nano), 0); err != nil {
		t.Fatal(err)
	}
	block1 := mgr.FrozenSystemBlock(ctx, "s1")
	if !strings.Contains(block1, "session fact A") {
		t.Fatalf("frozen = %q", block1)
	}
	// Mid-session write must not change frozen block until invalidate.
	if err := store.savePreference(ctx, preferenceProposal{Key: "b", Category: "workflow", Content: "session fact B after freeze"}, mgr.now().Format(time.RFC3339Nano), 0); err != nil {
		t.Fatal(err)
	}
	block2 := mgr.FrozenSystemBlock(ctx, "s1")
	if block2 != block1 {
		t.Fatalf("frozen changed without invalidate: %q vs %q", block1, block2)
	}
	mgr.InvalidateSnapshot("s1")
	block3 := mgr.FrozenSystemBlock(ctx, "s1")
	if !strings.Contains(block3, "session fact B") {
		t.Fatalf("after refresh frozen = %q", block3)
	}
	list, err := mgr.List(ctx, "s1", true, 10)
	if err != nil || len(list) < 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}

}

func TestManagerSystemPrompt(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := NewStore(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := New(Config{Store: store, MaxInjectRunes: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.savePreference(ctx, preferenceProposal{Key: "terse", Category: "response_style", Content: "User prefers terse replies"}, mgr.now().Format(time.RFC3339Nano), 0); err != nil {
		t.Fatal(err)
	}
	block := mgr.buildInjectBlock(ctx, "sess-a")
	if !strings.Contains(block, "terse") {
		t.Fatalf("block = %q", block)
	}
	if err := mgr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMemoryExpiryAndInjection(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := NewStore(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	clock := now
	mgr, err := New(Config{
		Store: store,
		Now:   func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memory_entries(entry_id,session_id,content,kind,created_at,expires_at) VALUES
		('legacy-session','s1','session fact with ttl','session',?,?),
		('legacy-global','','global curated no ttl','curated',?,'')`, now.Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mgr.buildInjectBlock(ctx, "s1"), "session fact with ttl") {
		t.Fatal("legacy session facts must not be injected")
	}

	clock = now.Add(2 * time.Hour)
	if err := mgr.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := scanEntry(database.SQL().QueryRowContext(ctx,
		`SELECT `+entrySelectCols+` FROM memory_entries WHERE entry_id = ?`, "legacy-session"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt == "" {
		t.Fatalf("expected soft archive, got %+v", got)
	}
	block := mgr.buildInjectBlock(ctx, "s1")
	if strings.Contains(block, "session fact with ttl") {
		t.Fatalf("archived fact still injected: %q", block)
	}
	if strings.Contains(block, "global curated no ttl") {
		t.Fatalf("legacy curated facts must not be injected: %q", block)
	}
	active, err := mgr.List(ctx, "s1", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range active {
		if e.ID == "legacy-session" {
			t.Fatalf("archived entry still in default list: %+v", e)
		}
	}
}
