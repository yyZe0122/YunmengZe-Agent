package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/runmeta"
	coresqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

type stubSubagent struct {
	last agent.RunRequest
	err  error
	body string
}

func (s *stubSubagent) Run(_ context.Context, req agent.RunRequest) (agent.Result, error) {
	s.last = req
	if s.err != nil {
		return agent.Result{}, s.err
	}
	return agent.Result{Content: s.body, Iterations: 1}, nil
}

func TestTaskSystemPromptIncludesVersion(t *testing.T) {
	spec, ok := lookupKind(KindGeneral)
	if !ok {
		t.Fatal("missing general kind")
	}
	got := childSystemPrompt(spec.Prompt)
	want := "YunmengZe Agent " + version.Version
	if !strings.Contains(got, want) {
		t.Fatalf("prompt missing %q: %s", want, got)
	}
	if !strings.Contains(got, "sub-agent") {
		t.Fatalf("prompt missing sub-agent: %s", got)
	}
}

func TestTaskToolDefinitionAdvertisesKinds(t *testing.T) {
	tool, err := NewTaskTool(TaskToolConfig{DB: mustCoreDB(t).SQL()})
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if !strings.Contains(def.Description, "explore") || !strings.Contains(string(def.InputSchema), `"general"`) {
		t.Fatalf("definition = %+v", def)
	}
	if !strings.Contains(string(def.InputSchema), `"web"`) {
		t.Fatalf("schema must advertise web: %s", def.InputSchema)
	}
	if strings.Contains(string(def.InputSchema), `"vision"`) {
		t.Fatalf("schema must not advertise vision: %s", def.InputSchema)
	}
}

func TestTaskToolSpawnsChildRun(t *testing.T) {
	ctx := context.Background()
	database, err := coresqlite.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	db := database.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,created_at,updated_at) VALUES(?,?,?,?)", []any{"s1", "active", stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", []any{"t1", "s1", "T", "O", "running", stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,created_at,updated_at,document) VALUES(?,?,?,?,?,?,?,?)", []any{"p1", "t1", 1, "approved", "h1", stamp, stamp, `{}`}},
		{"INSERT INTO plan_steps(step_id,plan_id,position,title,state,effect_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", []any{"step1", "p1", 0, "S", "running", "R1", stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,step_id) VALUES(?,?,?,?,?,?,?)", []any{"parent-run", "t1", "p1", "running", stamp, stamp, "step1"}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	stub := &stubSubagent{body: "child-done"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub, Now: func() time.Time {
		return time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}

	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", SessionID: "s1", PlanID: "p1", PlanHash: "h1",
		StepID: "step1", Actor: "agent", TraceID: "tr",
		AllowedTools: []string{"fs_read", "task"},
		CapabilityGrantIDs: map[string][]string{
			"fs_read": {"g-read"}, "task": {"g-task"},
		},
		MaxTotalTokens: 1000, Depth: 0, CallID: "call-task-1",
	})
	args, _ := json.Marshal(map[string]any{"prompt": "summarize README"})
	out, err := tool.Execute(parentCtx, args)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["state"] != "completed" || payload["content"] != "child-done" || payload["kind"] != KindGeneral {
		t.Fatalf("payload = %#v", payload)
	}
	runID, _ := payload["run_id"].(string)
	if runID == "" || !strings.HasPrefix(runID, "c") {
		t.Fatalf("run_id = %q", runID)
	}
	if stub.last.Depth != 1 {
		t.Fatalf("child depth = %d", stub.last.Depth)
	}
	if stub.last.RunID != runID {
		t.Fatalf("runner run_id = %s want %s", stub.last.RunID, runID)
	}
	if len(stub.last.Messages) == 0 || stub.last.Messages[0].Role != providerapi.RoleSystem {
		t.Fatalf("child messages = %#v", stub.last.Messages)
	}
	if !strings.Contains(stub.last.Messages[0].Content, "YunmengZe Agent "+version.Version) {
		t.Fatalf("child system prompt = %q", stub.last.Messages[0].Content)
	}
	if stub.last.Role != "subagent" {
		t.Fatalf("child role = %q", stub.last.Role)
	}
	for _, name := range stub.last.AllowedTools {
		if name == "task" {
			t.Fatalf("child inherited task: %v", stub.last.AllowedTools)
		}
	}
	var parent string
	if err := db.QueryRow(`SELECT parent_run_id FROM runs WHERE run_id = ?`, runID).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != "parent-run" {
		t.Fatalf("parent_run_id = %q", parent)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM runs WHERE run_id = ?`, runID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "completed" {
		t.Fatalf("child run state = %q", state)
	}
}

func TestTaskToolInjectsAgentsOverlay(t *testing.T) {
	ctx := context.Background()
	database, err := coresqlite.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	db := database.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,created_at,updated_at) VALUES(?,?,?,?)", []any{"s1", "active", stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", []any{"t1", "s1", "T", "O", "running", stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,created_at,updated_at,document) VALUES(?,?,?,?,?,?,?,?)", []any{"p1", "t1", 1, "approved", "h1", stamp, stamp, `{}`}},
		{"INSERT INTO plan_steps(step_id,plan_id,position,title,state,effect_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", []any{"step1", "p1", 0, "S", "running", "R1", stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,step_id) VALUES(?,?,?,?,?,?,?)", []any{"parent-run", "t1", "p1", "running", stamp, stamp, "step1"}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, providerconfig.AgentsFilename), []byte("全局：子代理也要遵守"), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := &stubSubagent{body: "ok"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub, ConfigDir: cfg, Now: func() time.Time {
		return time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", SessionID: "s1", PlanID: "p1", PlanHash: "h1",
		StepID: "step1", Actor: "agent", TraceID: "tr",
		AllowedTools: []string{"fs_read", "task"}, Depth: 0, CallID: "call-agents",
	})
	args, _ := json.Marshal(map[string]any{"prompt": "do it"})
	if _, err := tool.Execute(parentCtx, args); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range stub.last.Messages {
		if m.Role == providerapi.RoleSystem {
			joined += m.Content + "\n"
		}
	}
	if !strings.Contains(joined, "全局：子代理也要遵守") {
		t.Fatalf("child missing AGENTS: %q", joined)
	}
}

func TestTaskToolMaxDepth(t *testing.T) {
	ctx := context.Background()
	database, err := coresqlite.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stub := &stubSubagent{body: "nope"}
	tool, err := NewTaskTool(TaskToolConfig{DB: database.SQL(), Runner: stub, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "p", TaskID: "t", PlanID: "pl", PlanHash: "h", StepID: "s",
		AllowedTools: []string{"task"}, Depth: 2, CallID: "c1",
	})
	out, err := tool.Execute(parentCtx, json.RawMessage(`{"prompt":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "max_depth_exceeded") {
		t.Fatalf("out = %s", out)
	}
	if stub.last.RunID != "" {
		t.Fatal("runner should not run at max depth")
	}
}

func TestTaskToolRequiresContext(t *testing.T) {
	database, err := coresqlite.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tool, err := NewTaskTool(TaskToolConfig{DB: database.SQL(), Runner: &stubSubagent{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"prompt":"x"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTaskToolExploreKind(t *testing.T) {
	ctx := context.Background()
	db := mustCoreDB(t).SQL()
	seedParentRun(t, db)
	stub := &stubSubagent{body: "found"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", SessionID: "s1", PlanID: "p1", PlanHash: "h1",
		StepID: "step1", AllowedTools: []string{"fs_read", "fs_grep", "fs_write", "process_exec", "http_get", "task"},
		Depth: 0, CallID: "call-explore",
	})
	out, err := tool.Execute(parentCtx, json.RawMessage(`{"prompt":"find usages","kind":"explore"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != KindExplore || payload["state"] != "completed" {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(stub.last.Messages[0].Content, "Read-only exploration") {
		t.Fatalf("explore prompt = %q", stub.last.Messages[0].Content)
	}
	for _, name := range stub.last.AllowedTools {
		switch name {
		case "fs_write", "process_exec", "http_get", "task":
			t.Fatalf("explore leaked %q: %v", name, stub.last.AllowedTools)
		}
	}
}

func TestTaskToolWebKind(t *testing.T) {
	ctx := context.Background()
	db := mustCoreDB(t).SQL()
	seedParentRun(t, db)
	stub := &stubSubagent{body: "hits"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", SessionID: "s1", PlanID: "p1", PlanHash: "h1",
		StepID: "step1", AllowedTools: []string{"fs_read", "web_search", "web_extract", "http_get", "task"},
		Depth: 0, CallID: "call-web",
	})
	out, err := tool.Execute(parentCtx, json.RawMessage(`{"prompt":"search docs","kind":"web"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != KindWeb || payload["state"] != "completed" {
		t.Fatalf("payload = %#v", payload)
	}
	if stub.last.Role != "subagent" {
		t.Fatalf("role = %q", stub.last.Role)
	}
}

func TestTaskToolUnadvertisedKindDoesNotSpawn(t *testing.T) {
	ctx := context.Background()
	db := mustCoreDB(t).SQL()
	seedParentRun(t, db)
	stub := &stubSubagent{body: "nope"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", AllowedTools: []string{"fs_read", "vision_analyze", "task"}, Depth: 0, CallID: "call-vision",
	})
	out, err := tool.Execute(parentCtx, json.RawMessage(`{"prompt":"look","kind":"vision"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), kindErrUnavailable) {
		t.Fatalf("out = %s", out)
	}
	if stub.last.RunID != "" {
		t.Fatal("runner should not run for unadvertised kind")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs WHERE parent_run_id = ?`, "parent-run").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("spawned %d child runs", n)
	}
}

func TestTaskToolForbiddenToolsDoesNotSpawn(t *testing.T) {
	ctx := context.Background()
	db := mustCoreDB(t).SQL()
	seedParentRun(t, db)
	stub := &stubSubagent{body: "nope"}
	tool, err := NewTaskTool(TaskToolConfig{DB: db, Runner: stub})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx := runmeta.With(ctx, runmeta.Context{
		RunID: "parent-run", TaskID: "t1", AllowedTools: []string{"fs_read", "process_exec", "task"}, Depth: 0, CallID: "call-forbid",
	})
	out, err := tool.Execute(parentCtx, json.RawMessage(`{"prompt":"x","kind":"explore","tools":["process_exec"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), kindErrForbidden) {
		t.Fatalf("out = %s", out)
	}
	if stub.last.RunID != "" {
		t.Fatal("runner should not run")
	}
}

func mustCoreDB(t *testing.T) *coresqlite.DB {
	t.Helper()
	database, err := coresqlite.Open(context.Background(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func seedParentRun(t *testing.T, db *sql.DB) {
	t.Helper()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,created_at,updated_at) VALUES(?,?,?,?)", []any{"s1", "active", stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", []any{"t1", "s1", "T", "O", "running", stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,created_at,updated_at,document) VALUES(?,?,?,?,?,?,?,?)", []any{"p1", "t1", 1, "approved", "h1", stamp, stamp, `{}`}},
		{"INSERT INTO plan_steps(step_id,plan_id,position,title,state,effect_level,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", []any{"step1", "p1", 0, "S", "running", "R1", stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,step_id) VALUES(?,?,?,?,?,?,?)", []any{"parent-run", "t1", "p1", "running", stamp, stamp, "step1"}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}
