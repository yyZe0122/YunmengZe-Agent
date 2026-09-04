package tasksubmission

import (
	"context"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	storesqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

type stubChat struct{}

func (stubChat) StartChat(_ context.Context, req ChatStartRequest) (ChatStartResult, error) {
	return ChatStartResult{Task: req.Task}, nil
}

func TestEnsureSessionOmitsStanceLeavesAuto(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo, err := kernel.NewRepository(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	session, err := repo.CreateSessionWithWorkspace(ctx, "session-auto", t.TempDir(), kernel.PermissionStanceAuto, now)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		Repository: repo, Chat: stubChat{}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, Request{
		SessionID: session.ID, Title: "run", Objective: "run",
		ExecutionMode: kernel.ExecutionModeAgent, EnsureSession: true,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, err := repo.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PermissionStance != kernel.PermissionStanceAuto {
		t.Fatalf("stance = %q, want auto (omit must not overwrite)", got.PermissionStance)
	}
}

func TestEnsureSessionExplicitStanceUpdates(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo, err := kernel.NewRepository(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	session, err := repo.CreateSessionWithWorkspace(ctx, "session-auto", t.TempDir(), kernel.PermissionStanceAuto, now)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		Repository: repo, Chat: stubChat{}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, Request{
		SessionID: session.ID, Title: "run", Objective: "run",
		ExecutionMode: kernel.ExecutionModeAgent, PermissionStance: kernel.PermissionStanceAgent,
		EnsureSession: true,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, err := repo.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PermissionStance != kernel.PermissionStanceAgent {
		t.Fatalf("stance = %q, want agent", got.PermissionStance)
	}
}

func TestEnsureSessionPreferredModelWrittenBeforeChat(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo, err := kernel.NewRepository(database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	chat := &captureChat{}
	svc, err := New(Config{
		Repository: repo, Chat: chat, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(ctx, Request{
		Title: "run", Objective: "run", ExecutionMode: kernel.ExecutionModeAgent,
		EnsureSession: true, PreferredModel: "deepseek/chat",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if chat.task.SessionID == "" {
		t.Fatal("expected session")
	}
	got, err := repo.GetSession(ctx, chat.task.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PreferredModel != "deepseek/chat" {
		t.Fatalf("preferred = %q", got.PreferredModel)
	}
}

type captureChat struct {
	task kernel.Task
}

func (c *captureChat) StartChat(_ context.Context, req ChatStartRequest) (ChatStartResult, error) {
	c.task = req.Task
	return ChatStartResult{Task: req.Task}, nil
}
