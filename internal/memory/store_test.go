package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	storesqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestStoreInsertListSearch(t *testing.T) {
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
	if err := store.Insert(ctx, Entry{
		ID: "e1", SessionID: "s1", Content: "prefer Go 1.26", Source: SourceUser,
		CreatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, Entry{
		ID: "e2", SessionID: "", Content: "global note", Source: SourceBuiltin,
		CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano),
	}); err != nil {
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

func TestStoreForgetPromoteAndFreeze(t *testing.T) {
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
	if err := mgr.Remember(ctx, "s1", "session fact A", SourceUser, nil); err != nil {
		t.Fatal(err)
	}
	block1 := mgr.FrozenSystemBlock(ctx, "s1")
	if !strings.Contains(block1, "session fact A") {
		t.Fatalf("frozen = %q", block1)
	}
	// Mid-session write must not change frozen block until invalidate.
	if err := mgr.Remember(ctx, "s1", "session fact B after freeze", SourceUser, nil); err != nil {
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
	list, err := mgr.List(ctx, "s1", false, 10)
	if err != nil || len(list) < 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	promoted, err := mgr.Promote(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.SessionID != "" || promoted.Kind != KindCurated {
		t.Fatalf("promoted = %+v", promoted)
	}
	if err := mgr.Forget(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSystemPromptAndPreCompress(t *testing.T) {
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
	if err := mgr.Remember(ctx, "sess-a", "User prefers terse replies", SourceUser, nil); err != nil {
		t.Fatal(err)
	}
	block := mgr.SystemPromptBlock(ctx, "sess-a")
	if !strings.Contains(block, "terse") {
		t.Fatalf("block = %q", block)
	}
	head := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: "Please use absolute paths under /tmp/ws always"},
		{Role: providerapi.RoleAssistant, Content: "ok"},
	}
	mgr.OnPreCompress(ctx, "sess-a", head)
	list, err := store.ListRecent(ctx, "sess-a", false, true, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.Source == SourcePreCompress && e.Kind == KindDetail {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pre_compress detail entry: %+v", list)
	}
	if err := mgr.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultTTLAndSoftArchive(t *testing.T) {
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
		Store: store, DefaultTTL: time.Hour,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Remember(ctx, "s1", "session fact with ttl", SourceUser, nil); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RememberKind(ctx, "", "global curated no ttl", SourceUser, nil, KindCurated, 10, ""); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListRecent(ctx, "s1", true, true, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var session, curated Entry
	for _, e := range list {
		switch e.Content {
		case "session fact with ttl":
			session = e
		case "global curated no ttl":
			curated = e
		}
	}
	if session.ID == "" || session.ExpiresAt != now.Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("session ttl = %+v", session)
	}
	if curated.ID == "" || curated.ExpiresAt != "" {
		t.Fatalf("curated should have no ttl: %+v", curated)
	}
	if !strings.Contains(mgr.SystemPromptBlock(ctx, "s1"), "session fact with ttl") {
		t.Fatal("expected session fact in inject")
	}

	clock = now.Add(2 * time.Hour)
	if err := mgr.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt == "" {
		t.Fatalf("expected soft archive, got %+v", got)
	}
	block := mgr.SystemPromptBlock(ctx, "s1")
	if strings.Contains(block, "session fact with ttl") {
		t.Fatalf("archived fact still injected: %q", block)
	}
	if !strings.Contains(block, "global curated no ttl") {
		t.Fatalf("curated missing from inject: %q", block)
	}
	active, err := mgr.List(ctx, "s1", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range active {
		if e.ID == session.ID {
			t.Fatalf("archived entry still in default list: %+v", e)
		}
	}
}

func TestDeleteTranscriptByRunIDs(t *testing.T) {
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
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.IndexTranscript(ctx, "s1", "run-keep", 0, "assistant_message", "keep me", stamp); err != nil {
		t.Fatal(err)
	}
	if err := store.IndexTranscript(ctx, "s1", "run-hide", 0, "assistant_message", "hide me", stamp); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTranscriptByRunIDs(ctx, []string{"run-hide"}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchTranscript(ctx, "s1", "hide", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.RunID == "run-hide" || strings.Contains(h.Content, "hide me") {
			t.Fatalf("hidden run leaked: %+v", h)
		}
	}
	kept, err := store.SearchTranscript(ctx, "s1", "keep", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) == 0 {
		t.Fatal("kept run missing")
	}
}
