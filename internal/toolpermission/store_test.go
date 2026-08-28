package toolpermission

import (
	"context"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	storesqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

func TestStoreInsertListMark(t *testing.T) {
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
	if err := store.Insert(ctx, Request{
		ID: "perm-1", SessionID: "s1", TaskID: "t1", RunID: "r1",
		PlanID: "p1", PlanHash: "h", StepID: "st1",
		ToolCallID: "c1", ToolName: "process_exec", Capability: "process_exec",
		Path: "/tmp/ws", State: StatePending,
		CreatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListPending(ctx, "s1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if err := store.MarkDecided(ctx, "perm-1", DecisionDeny, StateDenied, "", "user", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	list, err = store.ListPending(ctx, "s1", 10)
	if err != nil || len(list) != 0 {
		t.Fatalf("after decide list = %v", list)
	}
	got, err := store.Get(ctx, "perm-1")
	if err != nil || got.State != StateDenied {
		t.Fatalf("got = %+v err=%v", got, err)
	}
}

func TestPathRelated(t *testing.T) {
	if pathRelated("/tmp/foo", "/tmp/foobar") {
		t.Fatal("prefix without separator must not match")
	}
	if !pathRelated("/tmp/ws", "/tmp/ws/a.go") {
		t.Fatal("directory prefix should match")
	}
	if !pathRelated("/tmp/ws/a.go", "/tmp/ws") {
		t.Fatal("reverse directory prefix should match")
	}
	if !pathRelated("/tmp/ws", "/tmp/ws") {
		t.Fatal("equal paths should match")
	}
	if !pathRelated("", "") {
		t.Fatal("both empty should match")
	}
	if pathRelated("/tmp/ws", "") || pathRelated("", "/tmp/ws") {
		t.Fatal("one empty must not match")
	}
}

func TestSuggestHabit(t *testing.T) {
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
	stamp := now.Format(time.RFC3339Nano)
	insert := func(id, session, path, decision, state string) {
		t.Helper()
		if err := store.Insert(ctx, Request{
			ID: id, SessionID: session, TaskID: "t1", RunID: "r1",
			PlanID: "p1", PlanHash: "h", StepID: "st1",
			ToolCallID: id, ToolName: "process_exec", Capability: "process_exec",
			Path: path, State: StatePending, CreatedAt: stamp,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDecided(ctx, id, decision, state, "", "user", stamp); err != nil {
			t.Fatal(err)
		}
	}
	insert("perm-sim", "s1", "/tmp/ws", DecisionAllowSimilar, StateAllowedSimilar)
	insert("perm-deny", "s1", "/tmp/other", DecisionDeny, StateDenied)
	insert("perm-other-sess", "s2", "/tmp/ws", DecisionAllowOnce, StateAllowedOnce)

	svc := &Service{store: store}
	hint := svc.SuggestHabit(ctx, Request{
		SessionID: "s1", ToolName: "process_exec", Capability: "process_exec", Path: "/tmp/ws/a.go",
	})
	if hint.Decision != DecisionAllowSimilar || hint.Reason == "" {
		t.Fatalf("similar hint = %+v", hint)
	}
	hint = svc.SuggestHabit(ctx, Request{
		SessionID: "s1", ToolName: "process_exec", Capability: "process_exec", Path: "/tmp/other/x",
	})
	if hint.Decision != "" || hint.Reason == "" {
		t.Fatalf("deny hint should have reason only: %+v", hint)
	}
	hint = svc.SuggestHabit(ctx, Request{
		SessionID: "s1", ToolName: "process_exec", Capability: "process_exec", Path: "",
	})
	if hint.Decision != "" || hint.Reason != "" {
		t.Fatalf("empty path must not match prior with path: %+v", hint)
	}
	hint = svc.SuggestHabit(ctx, Request{
		SessionID: "s1", ToolName: "process_exec", Capability: "process_exec", Path: "/tmp/wsother",
	})
	if hint.Decision != "" {
		t.Fatalf("foobar-style prefix must not match: %+v", hint)
	}
	hint = svc.SuggestHabit(ctx, Request{
		SessionID: "s3", ToolName: "process_exec", Capability: "process_exec", Path: "/tmp/ws/a.go",
	})
	if hint.Decision != "" {
		t.Fatalf("other session must not leak: %+v", hint)
	}
}

func TestWaiterNotify(t *testing.T) {
	w := NewWaiter()
	w.Register("p1")
	done := make(chan Decision, 1)
	go func() {
		d, err := w.Wait(context.Background(), "p1")
		if err != nil {
			t.Errorf("wait: %v", err)
			return
		}
		done <- d
	}()
	time.Sleep(20 * time.Millisecond)
	w.Notify(Decision{PermissionID: "p1", Decision: DecisionAllowOnce, GrantID: "g1", State: StateAllowedOnce})
	select {
	case d := <-done:
		if d.GrantID != "g1" {
			t.Fatalf("%+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestFindPlanScopeVisionURLSkipsPathGrant(t *testing.T) {
	plan := approval.PlanDocument{Steps: []approval.StepScope{{
		StepID: "st1",
		Capabilities: []approval.CapabilityScope{
			{Capability: "vision_analyze", Paths: []string{"/tmp/ws"}, MaxDurationMillis: 30000, MaxCalls: 8},
			{Capability: "vision_analyze", MaxDurationMillis: 30000, MaxCalls: 1, OneTime: true},
			{Capability: "vision_analyze", MaxDurationMillis: 30000, MaxCalls: 8},
		},
	}}}
	got, err := findPlanScope(plan, "st1", "vision_analyze", "vision_analyze", DecisionAllowSimilar, "", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) != 0 || got.OneTime || got.MaxCalls != 8 {
		t.Fatalf("url similar must pick host session scope: %+v", got)
	}
	got, err = findPlanScope(plan, "st1", "vision_analyze", "vision_analyze", DecisionAllowOnce, "", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !got.OneTime || got.MaxCalls != 1 || len(got.Paths) != 0 {
		t.Fatalf("url once must pick host once scope: %+v", got)
	}
	got, err = findPlanScope(plan, "st1", "vision_analyze", "vision_analyze", DecisionAllowSimilar, "/tmp/ws/a.png", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) == 0 {
		t.Fatalf("path similar must pick path scope: %+v", got)
	}
}
