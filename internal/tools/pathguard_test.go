package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
)

func TestPathGuardResolvesRelativeAgainstRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := guard.Resolve("sub")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("Resolve(relative) = %q, want %q", resolved, want)
	}
}

func TestPathGuardRejectsEscape(t *testing.T) {
	root := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Resolve(filepath.Join("..", "outside")); err == nil {
		t.Fatal("Resolve(escape) succeeded; want error")
	}
}

func TestResolveForAuthAllowsAbsoluteOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Resolve(outside); err == nil {
		t.Fatal("Resolve(outside) succeeded")
	}
	got, extra, err := guard.ResolveForAuth(context.Background(), outside)
	if err != nil {
		t.Fatal(err)
	}
	if !extra {
		t.Fatal("want ExtraRoot")
	}
	want, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, extra, err := guard.ResolveForAuth(context.Background(), "../nope"); extra || err == nil {
		t.Fatal("relative escape must fail closed")
	}
}

func TestSessionExtraRootDoesNotLiftOtherSessions(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.AddSessionRoot("sess-a", outside); err != nil {
		t.Fatal(err)
	}
	ctxA := runmeta.With(context.Background(), runmeta.Context{SessionID: "sess-a"})
	if _, err := guard.ResolveContext(ctxA, outside); err != nil {
		t.Fatal(err)
	}
	ctxB := runmeta.With(context.Background(), runmeta.Context{SessionID: "sess-b"})
	if _, err := guard.ResolveContext(ctxB, outside); err == nil {
		t.Fatal("other session must not inherit extra root")
	}
	if _, extra, err := guard.ResolveForAuth(ctxB, outside); err != nil || !extra {
		t.Fatalf("other session auth extra=%v err=%v", extra, err)
	}
}

func TestOnceRootIsCallScoped(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	guard, err := NewPathGuard([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.AddOnceRoot("call-1", outside); err != nil {
		t.Fatal(err)
	}
	ctx := runmeta.With(context.Background(), runmeta.Context{CallID: "call-1"})
	if _, err := guard.ResolveContext(ctx, outside); err != nil {
		t.Fatal(err)
	}
	other := runmeta.With(context.Background(), runmeta.Context{CallID: "call-2"})
	if _, err := guard.ResolveContext(other, outside); err == nil {
		t.Fatal("other call must not inherit once root")
	}
}
