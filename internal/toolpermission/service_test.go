package toolpermission

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/approval"
	"github.com/yyZe0122/yunmengze-agent/internal/kernel"
	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	storesqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

func TestClampGrantExpiry(t *testing.T) {
	issued := time.Date(2026, 9, 4, 12, 0, 1, 0, time.UTC)
	approvalExp := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	got, err := clampGrantExpiry(issued, issued.Add(24*time.Hour), approvalExp)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(approvalExp) {
		t.Fatalf("similar clamp = %s, want approval expiry %s", got, approvalExp)
	}

	got, err = clampGrantExpiry(issued, issued.Add(365*24*time.Hour), approvalExp)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(approvalExp) {
		t.Fatalf("permanent clamp = %s, want approval expiry %s", got, approvalExp)
	}

	once := issued.Add(time.Hour)
	got, err = clampGrantExpiry(issued, once, approvalExp)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(once) {
		t.Fatalf("once = %s, want %s", got, once)
	}

	_, err = clampGrantExpiry(approvalExp, approvalExp.Add(time.Hour), approvalExp)
	if !errors.Is(err, ErrInvalidDecide) {
		t.Fatalf("expired approval err = %v, want ErrInvalidDecide", err)
	}
}

func TestDecideSimilarAndPermanentWithinApprovalWindow(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	db := database.SQL()
	repo, err := kernel.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := approval.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	decideAt := t0.Add(2 * time.Second)
	approvalExp := t0.Add(24 * time.Hour)
	ws := "/tmp/ws"

	session, err := repo.CreateSession(ctx, "session-perm", t0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.CreateTaskWithSkillSnapshot(ctx, "task-perm", session.ID, "run", "run", nil, "", kernel.ExecutionModeAgent, t0)
	if err != nil {
		t.Fatal(err)
	}
	plan := approval.PlanDocument{
		PlanID: "plan-perm", TaskID: task.ID, Revision: 1,
		Objective: "session chat workspace tools",
		Budget:    approval.PlanBudget{MaxTokens: 128_000, MaxDurationMillis: 30 * 60 * 1000},
		Steps: []approval.StepScope{{
			StepID: "step-perm", Position: 0, Title: "session chat",
			Risk: policy.RiskR2, ExpectedSideEffects: []string{"execute processes under workspace roots"},
			Rollback: "none", TimeoutMillis: 60_000,
			Capabilities: []approval.CapabilityScope{
				{Capability: "process_exec", Paths: []string{ws}, MaxDurationMillis: 60_000, MaxCalls: 1, OneTime: true},
				{Capability: "process_exec", Paths: []string{ws}, MaxDurationMillis: 60_000, MaxCalls: 10_000, OneTime: false},
				{Capability: "http_get", MaxDurationMillis: 60_000, MaxCalls: 1, OneTime: true},
				{Capability: "http_get", MaxDurationMillis: 60_000, MaxCalls: 10_000, OneTime: false},
			},
		}},
	}
	doc, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := plan.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateApprovedWorkspacePlan(ctx, plan.PlanID, task.ID, task.Version, plan.Revision, hash, doc,
		[]kernel.PlanStepDraft{{ID: plan.Steps[0].StepID, Position: 0, Title: plan.Steps[0].Title, EffectLevel: string(plan.Steps[0].Risk)}},
		"test", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := approvals.RecordSystemApproval(ctx, approval.DecisionInput{
		ID: "appr-perm", Plan: plan, Scope: approval.ScopePlan,
		Decision: approval.DecisionApproved, DecidedBy: "test", Reason: "test",
		DecidedAt: t0, ExpiresAt: &approvalExp,
	}); err != nil {
		t.Fatal(err)
	}

	svc, err := New(Config{
		DB: db, Store: store, Approvals: approvals,
		Now: func() time.Time { return decideAt },
	})
	if err != nil {
		t.Fatal(err)
	}

	insert := func(id, cap, path, cmd string, args []string, host string) {
		t.Helper()
		if err := store.Insert(ctx, Request{
			ID: id, SessionID: string(session.ID), TaskID: string(task.ID), RunID: "run-1",
			PlanID: string(plan.PlanID), PlanHash: hash, StepID: string(plan.Steps[0].StepID),
			ToolCallID: id, ToolName: cap, Capability: cap,
			Path: path, Command: cmd, CommandArgs: args, NetworkDomain: host,
			State: StatePending, CreatedAt: decideAt.Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatal(err)
		}
	}

	insert("perm-once", "process_exec", ws, "go", []string{"test", "./internal/foo/"}, "")
	once, err := svc.DecideWithOptions(ctx, "perm-once", DecisionAllowOnce, "tui", DecideOptions{})
	if err != nil {
		t.Fatalf("allow_once: %v", err)
	}
	if once.State != StateAllowedOnce {
		t.Fatalf("once state = %s", once.State)
	}

	insert("perm-sim", "process_exec", ws, "go", []string{"test", "./internal/foo/"}, "")
	sim, err := svc.DecideWithOptions(ctx, "perm-sim", DecisionAllowSimilar, "tui", DecideOptions{})
	if err != nil {
		t.Fatalf("allow_similar: %v", err)
	}
	if sim.State != StateAllowedSimilar {
		t.Fatalf("similar state = %s", sim.State)
	}
	assertGrantExpiry(t, ctx, db, sim.GrantID, approvalExp)
	assertGrantCommand(t, ctx, db, sim.GrantID, "go")

	trustPath := filepath.Join(t.TempDir(), "permissions-trust.json")
	insert("perm-perm", "http_get", "", "", nil, "example.com")
	perm, err := svc.DecideWithOptions(ctx, "perm-perm", DecisionAllowPermanent, "tui", DecideOptions{
		Confirm: true, TrustPath: trustPath,
	})
	if err != nil {
		t.Fatalf("allow_permanent: %v", err)
	}
	if perm.State != StateAllowedPermanent {
		t.Fatalf("permanent state = %s", perm.State)
	}
	assertGrantExpiry(t, ctx, db, perm.GrantID, approvalExp)
}

func TestDecideRejectsExpiredApproval(t *testing.T) {
	ctx := context.Background()
	database, err := storesqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	db := database.SQL()
	repo, err := kernel.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := approval.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	approvalExp := t0.Add(24 * time.Hour)
	session, err := repo.CreateSession(ctx, "session-exp", t0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.CreateTaskWithSkillSnapshot(ctx, "task-exp", session.ID, "run", "run", nil, "", kernel.ExecutionModeAgent, t0)
	if err != nil {
		t.Fatal(err)
	}
	plan := approval.PlanDocument{
		PlanID: "plan-exp", TaskID: task.ID, Revision: 1,
		Objective: "session chat workspace tools",
		Budget:    approval.PlanBudget{MaxTokens: 128_000, MaxDurationMillis: 30 * 60 * 1000},
		Steps: []approval.StepScope{{
			StepID: "step-exp", Position: 0, Title: "session chat",
			Risk: policy.RiskR2, ExpectedSideEffects: []string{"fetch urls"},
			Rollback: "none", TimeoutMillis: 60_000,
			Capabilities: []approval.CapabilityScope{
				{Capability: "http_get", MaxDurationMillis: 60_000, MaxCalls: 1, OneTime: true},
				{Capability: "http_get", MaxDurationMillis: 60_000, MaxCalls: 10_000, OneTime: false},
			},
		}},
	}
	doc, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := plan.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateApprovedWorkspacePlan(ctx, plan.PlanID, task.ID, task.Version, plan.Revision, hash, doc,
		[]kernel.PlanStepDraft{{ID: plan.Steps[0].StepID, Position: 0, Title: plan.Steps[0].Title, EffectLevel: string(plan.Steps[0].Risk)}},
		"test", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := approvals.RecordSystemApproval(ctx, approval.DecisionInput{
		ID: "appr-exp", Plan: plan, Scope: approval.ScopePlan,
		Decision: approval.DecisionApproved, DecidedBy: "test", Reason: "test",
		DecidedAt: t0, ExpiresAt: &approvalExp,
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		DB: db, Store: store, Approvals: approvals,
		Now: func() time.Time { return approvalExp },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, Request{
		ID: "perm-late", SessionID: string(session.ID), TaskID: string(task.ID), RunID: "run-1",
		PlanID: string(plan.PlanID), PlanHash: hash, StepID: string(plan.Steps[0].StepID),
		ToolCallID: "perm-late", ToolName: "http_get", Capability: "http_get",
		NetworkDomain: "example.com", State: StatePending, CreatedAt: approvalExp.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.DecideWithOptions(ctx, "perm-late", DecisionAllowSimilar, "tui", DecideOptions{})
	if !errors.Is(err, ErrInvalidDecide) {
		t.Fatalf("expired approval err = %v, want ErrInvalidDecide", err)
	}
}

func assertGrantCommand(t *testing.T, ctx context.Context, db *sql.DB, grantID, want string) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(ctx, `SELECT command_name FROM capability_grants WHERE grant_id = ?`, grantID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("grant %s command = %q, want %q", grantID, got, want)
	}
}

func assertGrantExpiry(t *testing.T, ctx context.Context, db *sql.DB, grantID string, want time.Time) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(ctx, `SELECT expires_at FROM capability_grants WHERE grant_id = ?`, grantID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("grant %s expires_at = %s, want %s", grantID, parsed, want)
	}
}
