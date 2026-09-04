package corequery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/coreidentity"
	coresqlite "github.com/yyZe0122/yunmengze-agent/internal/store/sqlite"
)

func TestSessionTranscriptIncludesUserAndAssistant(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)

	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at) VALUES(?,?,?,?,?)",
			[]any{"session-1", "active", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-1", "session-1", "Hello", "Hello world", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-1", "task-1", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-1", "task-1", "plan-1", "completed", stamp, stamp, 1}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-1", 0, "assistant_message",
				`{"role":"assistant","content":"Hi there"}`, `{}`, "stop", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-1", 1, "assistant_message",
				`{"role":"assistant","content":"","tool_calls":[{"id":"c1","name":"read","arguments":"{\"path\":\"x\"}"}]}`,
				`{}`, "tool_calls", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-1", 2, "tool_result",
				`{"role":"tool","content":"file contents","tool_call_id":"c1"}`, `{}`, "", "c1", stamp}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListSessions(ctx, SessionListOptions{Page: Page{Limit: 10}, Sort: SortDescending})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "session-1" || sessions[0].Title != "Hello" {
		t.Fatalf("sessions = %#v", sessions)
	}

	msgs, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-1"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	var sawTool, sawAssistant bool
	for _, m := range msgs {
		roles = append(roles, m.Role)
		if m.Role == "assistant" && m.Content == "Hi there" {
			sawAssistant = true
		}
		if m.Role == "tool" && m.Content == "file contents" {
			sawTool = true
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "read" {
			// ok
		}
	}
	if !sawAssistant {
		t.Fatalf("missing assistant content: %#v", msgs)
	}
	if !sawTool {
		t.Fatalf("missing tool result: %#v", msgs)
	}
	// user objective synthetic + assistant + tool_call assistant + tool
	if len(msgs) < 3 {
		t.Fatalf("roles=%v msgs=%#v", roles, msgs)
	}
}

func TestSessionTranscriptFiltersInternalStepPrompt(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)

	stepPrompt := "Task objective: 你好\nApproved plan objective: greet user\nCurrent step: list workspace\nApproved capabilities: []"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at) VALUES(?,?,?,?,?)",
			[]any{"session-ghost", "active", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-ghost", "session-ghost", "你好", "你好", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-ghost", "task-ghost", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-ghost", "task-ghost", "plan-ghost", "completed", stamp, stamp, 1}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-ghost", 0, "input_message",
				fmt.Sprintf(`{"role":"user","content":%q}`, stepPrompt), `{}`, "", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-ghost", 1, "assistant_message",
				`{"role":"assistant","content":"你好！有什么可以帮忙的？"}`, `{}`, "stop", "", stamp}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}

	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-ghost"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	var users, assistants int
	for _, m := range msgs {
		if m.Role == "user" {
			users++
			if strings.Contains(m.Content, "Current step:") || strings.Contains(m.Content, "Approved capabilities:") {
				t.Fatalf("internal step prompt leaked into transcript: %#v", m)
			}
			if m.Content != "你好" {
				t.Fatalf("user bubble = %q want 你好", m.Content)
			}
			if !strings.HasPrefix(m.ID, "task-user:") {
				t.Fatalf("expected task-user anchor, got %q", m.ID)
			}
		}
		if m.Role == "assistant" && m.Content == "你好！有什么可以帮忙的？" {
			assistants++
		}
	}
	if users != 1 {
		t.Fatalf("user bubbles = %d want 1; msgs=%#v", users, msgs)
	}
	if assistants != 1 {
		t.Fatalf("assistant bubbles = %d want 1; msgs=%#v", assistants, msgs)
	}

	taskMsgs, err := store.TaskTranscript(ctx, coreidentity.TaskID("task-ghost"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range taskMsgs {
		if m.Role == "user" && (strings.Contains(m.Content, "Current step:") || strings.Contains(m.Content, "Task objective:")) {
			t.Fatalf("TaskTranscript leaked step prompt: %#v", m)
		}
	}
}

func TestSessionTranscriptTailKeepsNewest(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	sessionStamp := base.Format(time.RFC3339Nano)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO sessions(session_id,state,version,created_at,updated_at) VALUES(?,?,?,?,?)`,
		"session-tail", "active", 1, sessionStamp, sessionStamp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		stamp := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		taskID := fmt.Sprintf("task-%d", i)
		runID := fmt.Sprintf("run-%d", i)
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			taskID, "session-tail", fmt.Sprintf("T%d", i), fmt.Sprintf("user-%d", i), "completed", "agent", 1, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("plan-%d", i), taskID, 1, "approved", "h", "{}", 1, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)`,
			runID, taskID, fmt.Sprintf("plan-%d", i), "completed", stamp, stamp, 1); err != nil {
			t.Fatal(err)
		}
		asstStamp := base.Add(time.Duration(i)*time.Minute + 30*time.Second).Format(time.RFC3339Nano)
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			runID, 0, "assistant_message",
			fmt.Sprintf(`{"role":"assistant","content":"asst-%d"}`, i), `{}`, "stop", "", asstStamp); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	asc, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-tail"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if len(asc) < 4 || asc[0].Content != "user-0" {
		t.Fatalf("ASC still oldest-first: %#v", asc)
	}
	tail, err := store.SessionTranscriptTail(ctx, coreidentity.SessionID("session-tail"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 3 {
		t.Fatalf("tail len=%d want 3: %#v", len(tail), tail)
	}
	if tail[0].CreatedAt > tail[1].CreatedAt {
		t.Fatalf("tail must be chronological: %#v", tail)
	}
	for _, m := range tail {
		if m.Content == "user-0" || m.Content == "asst-0" {
			t.Fatalf("oldest turn leaked into tail: %#v", tail)
		}
	}
}

func TestAlignTranscriptTailCutKeepsToolPair(t *testing.T) {
	msgs := []TranscriptMessage{
		{ID: "old-user", Role: "user", Content: "old"},
		{ID: "asst", Role: "assistant", Content: "call", ToolCalls: []TranscriptToolCall{{ID: "c1", Name: "fs_read"}}},
		{ID: "tool", Role: "tool", Content: "body", ToolCallID: "c1"},
		{ID: "new-user", Role: "user", Content: "new"},
	}
	if got := alignTranscriptTailCut(msgs, 2); got != 1 {
		t.Fatalf("cut=%d want 1 (include assistant)", got)
	}
}

func TestIsInternalStepPrompt(t *testing.T) {
	if !isInternalStepPrompt("Task objective: x\nCurrent step: y") {
		t.Fatal("expected step prompt")
	}
	if isInternalStepPrompt("你好") {
		t.Fatal("plain user text must not filter")
	}
	if isInternalStepPrompt("Task objective alone without markers") {
		t.Fatal("objective marker alone is not enough")
	}
}

func TestListMemoryExcludesArchivedByDefault(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	sqlDB := db.SQL()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO memory_entries(entry_id,session_id,content,source,tags_json,created_at,kind,priority,expires_at,updated_at,archived_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{"mem-live", "s1", "live fact", "user", "[]", stamp, "session", 0, "", stamp, ""}},
		{`INSERT INTO memory_entries(entry_id,session_id,content,source,tags_json,created_at,kind,priority,expires_at,updated_at,archived_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			[]any{"mem-arch", "s1", "old fact", "user", "[]", stamp, "session", 0, past, stamp, past}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.ListMemory(ctx, MemoryListOptions{Page: Page{Limit: 10}, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ID != "mem-live" {
		t.Fatalf("live = %+v", live)
	}
	arch, err := store.ListMemory(ctx, MemoryListOptions{Page: Page{Limit: 10}, SessionID: "s1", IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(arch) != 1 || arch[0].ID != "mem-arch" || arch[0].ArchivedAt == "" {
		t.Fatalf("archived = %+v", arch)
	}
}

func TestListSkillEvents(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	sqlDB := db.SQL()
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO skill_events(event_id, skill_id, action, actor, path, content_hash, created_at)
		VALUES(?,?,?,?,?,?,?)`,
		"ev-1", "demo", "used", "user", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSkillEvents(ctx, SkillEventListOptions{Page: Page{Limit: 10}, SkillID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "ev-1" || items[0].Action != "used" {
		t.Fatalf("events = %+v", items)
	}
}

func TestListSessionTodos(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO sessions(session_id,state,version,created_at,updated_at) VALUES(?,?,?,?,?)`,
		"session-1", "active", 1, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO session_todos(session_id,item_id,content,status,position,updated_at) VALUES(?,?,?,?,?,?)`,
		"session-1", "t1", "patch fs.go", "in_progress", 0, stamp); err != nil {
		t.Fatal(err)
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSessionTodos(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "t1" || items[0].Status != "in_progress" {
		t.Fatalf("todos = %+v", items)
	}
	if _, err := store.ListSessionTodos(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session err = %v", err)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := New(db.SQL())
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListSessions(ctx, SessionListOptions{Page: Page{Limit: 10}, Sort: SortDescending})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("got %#v", items)
	}
}

func TestSessionTranscriptSkipsHiddenTasks(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	meta := `{"hidden_task_ids":["task-hidden"]}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at,metadata) VALUES(?,?,?,?,?,?)",
			[]any{"session-hide", "active", 1, stamp, stamp, meta}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-keep", "session-hide", "Keep", "keep me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-hidden", "session-hide", "Hide", "hide me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-keep", "task-keep", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-hidden", "task-hidden", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-keep", "task-keep", "plan-keep", "completed", stamp, stamp, 1}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-hidden", "task-hidden", "plan-hidden", "completed", stamp, stamp, 1}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-keep", 0, "assistant_message", `{"role":"assistant","content":"kept"}`, `{}`, "stop", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-hidden", 0, "assistant_message", `{"role":"assistant","content":"gone"}`, `{}`, "stop", "", stamp}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-hide"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.TaskID == "task-hidden" || strings.Contains(m.Content, "hide me") || strings.Contains(m.Content, "gone") {
			t.Fatalf("hidden turn leaked: %#v", m)
		}
	}
	tail, err := store.SessionTranscriptTail(ctx, coreidentity.SessionID("session-hide"), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tail {
		if m.TaskID == "task-hidden" {
			t.Fatalf("hidden turn in tail: %#v", m)
		}
	}
}

func TestSessionTranscriptLimitIgnoresHiddenRows(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	meta := `{"hidden_task_ids":["task-hidden"]}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at,metadata) VALUES(?,?,?,?,?,?)",
			[]any{"session-limit", "active", 1, stamp, stamp, meta}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-keep", "session-limit", "Keep", "keep me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-hidden", "session-limit", "Hide", "hide me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-keep", "task-keep", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-hidden", "task-hidden", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-keep", "task-keep", "plan-keep", "completed", stamp, stamp, 1}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-hidden", "task-hidden", "plan-hidden", "completed", stamp, stamp, 1}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			"run-hidden", i, "assistant_message", fmt.Sprintf(`{"role":"assistant","content":"gone-%d"}`, i), `{}`, "stop", "", stamp); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			"run-keep", i, "assistant_message", fmt.Sprintf(`{"role":"assistant","content":"keep-%d"}`, i), `{}`, "stop", "", stamp); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-limit"), TranscriptOptions{Page: Page{Limit: 3}})
	if err != nil {
		t.Fatal(err)
	}
	var assistant int
	for _, m := range msgs {
		if m.TaskID == "task-hidden" {
			t.Fatalf("hidden leaked under limit: %#v", m)
		}
		if m.Role == "assistant" {
			assistant++
		}
	}
	if assistant != 3 {
		t.Fatalf("assistant n=%d want 3 (got %#v)", assistant, msgs)
	}
}

func TestHiddenTaskIsNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	meta := `{"hidden_task_ids":["task-hidden"]}`
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at,metadata) VALUES(?,?,?,?,?,?)",
			[]any{"session-hide", "active", 1, stamp, stamp, meta}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-keep", "session-hide", "Keep", "keep me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-hidden", "session-hide", "Hide", "hide me", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-hidden", "task-hidden", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-hidden", "task-hidden", "plan-hidden", "completed", stamp, stamp, 1}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, TaskListOptions{Page: Page{Limit: 20}, Sort: SortDescending})
	if err != nil {
		t.Fatal(err)
	}
	for _, tsk := range tasks {
		if tsk.ID == "task-hidden" {
			t.Fatalf("ListTasks leaked hidden: %#v", tsk)
		}
	}
	if _, err := store.GetTask(ctx, coreidentity.TaskID("task-hidden")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask err=%v", err)
	}
	if _, err := store.TaskTranscript(ctx, coreidentity.TaskID("task-hidden"), TranscriptOptions{Page: Page{Limit: 10}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TaskTranscript err=%v", err)
	}
	if _, err := store.GetRun(ctx, coreidentity.RunID("run-hidden")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRun err=%v", err)
	}
	sess, err := store.GetSession(ctx, coreidentity.SessionID("session-hide"))
	if err != nil {
		t.Fatal(err)
	}
	if sess.TaskCount != 1 {
		t.Fatalf("task_count=%d", sess.TaskCount)
	}
	if sess.LatestTaskID == nil || *sess.LatestTaskID != "task-keep" {
		t.Fatalf("latest=%v", sess.LatestTaskID)
	}
}

func TestSessionTranscriptExcludesChildRuns(t *testing.T) {
	ctx := context.Background()
	db, err := coresqlite.Open(ctx, t.TempDir()+"/core.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlDB := db.SQL()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO sessions(session_id,state,version,created_at,updated_at) VALUES(?,?,?,?,?)",
			[]any{"session-child", "active", 1, stamp, stamp}},
		{"INSERT INTO tasks(task_id,session_id,title,objective,state,execution_mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"task-child", "session-child", "Hello", "user turn", "completed", "agent", 1, stamp, stamp}},
		{"INSERT INTO plans(plan_id,task_id,revision,state,scope_hash,document,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)",
			[]any{"plan-child", "task-child", 1, "approved", "h", "{}", 1, stamp, stamp}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version) VALUES(?,?,?,?,?,?,?)",
			[]any{"run-parent", "task-child", "plan-child", "completed", stamp, stamp, 1}},
		{"INSERT INTO runs(run_id,task_id,plan_id,state,started_at,updated_at,version,parent_run_id) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-child", "task-child", "plan-child", "completed", stamp, stamp, 1, "run-parent"}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-parent", 0, "assistant_message", `{"role":"assistant","content":"parent reply"}`, `{}`, "stop", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-child", 0, "input_message", `{"role":"user","content":"child prompt leak"}`, `{}`, "", "", stamp}},
		{"INSERT INTO agent_run_records(run_id,position,record_type,message,usage,finish_reason,tool_call_id,created_at) VALUES(?,?,?,?,?,?,?,?)",
			[]any{"run-child", 1, "assistant_message", `{"role":"assistant","content":"child internals"}`, `{}`, "stop", "", stamp}},
	} {
		if _, err := sqlDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	store, err := New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := store.SessionTranscript(ctx, coreidentity.SessionID("session-child"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "child prompt leak") || strings.Contains(m.Content, "child internals") {
			t.Fatalf("child record leaked into session transcript: %#v", m)
		}
	}
	tail, err := store.SessionTranscriptTail(ctx, coreidentity.SessionID("session-child"), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tail {
		if strings.Contains(m.Content, "child prompt leak") || strings.Contains(m.Content, "child internals") {
			t.Fatalf("child record leaked into session tail: %#v", m)
		}
	}
	taskMsgs, err := store.TaskTranscript(ctx, coreidentity.TaskID("task-child"), TranscriptOptions{Page: Page{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	var sawChild bool
	for _, m := range taskMsgs {
		if strings.Contains(m.Content, "child internals") {
			sawChild = true
		}
	}
	if !sawChild {
		t.Fatalf("TaskTranscript should still include child records: %#v", taskMsgs)
	}
}
