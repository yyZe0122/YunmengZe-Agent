package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestParseSlash(t *testing.T) {
	name, arg := parseSlash("/new hello world")
	if name != "/new" || arg != "hello world" {
		t.Fatalf("got %q %q", name, arg)
	}
	name, arg = parseSlash("/new")
	if name != "/new" || arg != "" {
		t.Fatalf("bare /new = %q %q", name, arg)
	}
	name, arg = parseSlash("plain text")
	if name != "" || arg != "plain text" {
		t.Fatalf("got %q %q", name, arg)
	}
}

func TestCanonicalAliases(t *testing.T) {
	if canonicalSlash("/q") != "/quit" {
		t.Fatal(canonicalSlash("/q"))
	}
	if canonicalSlash("/exit") != "/quit" {
		t.Fatal(canonicalSlash("/exit"))
	}
	if canonicalSlash("/clear") != "/back" {
		t.Fatal(canonicalSlash("/clear"))
	}
	if canonicalSlash("/sessions") != "/sessions" {
		t.Fatal(canonicalSlash("/sessions"))
	}
	name, _ := parseSlash("/q")
	if name != "/quit" {
		t.Fatalf("parse alias = %q", name)
	}
}

func TestHelpTextListsCommands(t *testing.T) {
	text := ansi.Strip(helpText())
	for _, want := range []string{"/new", "/tasks", "/cron", "/compact", "/perm", "/expand", "/journey", "/memory", "/refresh-memory", "/model", "/skills", "/theme", "Tab", "every", "skill-id", "e / E / c", "Ctrl+PgUp"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q", want)
		}
	}
	if strings.Contains(text, "/approve") {
		t.Fatal("help should not list removed /approve")
	}
	if strings.Contains(text, "a / r") || strings.Contains(text, "approve → run") {
		t.Fatal("help should not advertise removed approval keys/workflow")
	}
	if strings.Contains(text, "always opens a fresh session") || strings.Contains(text, "[message") || strings.Contains(text, "with text") {
		t.Fatal("help should not advertise /new with a message")
	}
	if !strings.Contains(text, "leave session") || !strings.Contains(text, "cancels a running turn") {
		t.Fatal("help should describe /new as leave + cancel")
	}
}

func TestPaintKeywordsHighlightsSlash(t *testing.T) {
	out := paintKeywords("try /new then /skills apply")
	want := "try " + styleKeyword.Render("/new") + " then " + styleKeyword.Render("/skills") + " apply"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestIsBuiltinSlash(t *testing.T) {
	if !isBuiltinSlash("/model") || !isBuiltinSlash("/MODEL") {
		t.Fatal("expected /model builtin")
	}
	if isBuiltinSlash("/git") {
		t.Fatal("/git should not be builtin")
	}
}

func TestBareNewClearsToReady(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-old"
	m.task = &gatewayclient.Task{ID: "task-old", Title: "old"}
	m.messages = []gatewayclient.TranscriptMessage{{Role: "user", Content: "hi"}}
	m.timeline = []timelineItem{{Kind: tlUser, Title: "you", Body: "hi"}}

	msg := m.handleLineCmd("/new")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil || !done.clearTask || done.status != "new session" {
		t.Fatalf("bare /new = %#v", done)
	}

	updated, _ := m.Update(done)
	got := updated.(model)
	if got.sessionID != "" || got.task != nil || len(got.messages) != 0 || len(got.timeline) != 0 {
		t.Fatalf("focus leftover session=%q task=%v msgs=%d tl=%d",
			got.sessionID, got.task, len(got.messages), len(got.timeline))
	}
	view := renderSessionView(&got)
	if !strings.Contains(ansi.Strip(view), "local coding agent") {
		t.Fatalf("expected ready page:\n%s", view)
	}
}

func TestBareNewDropsOldStream(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-old"
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	updated, _ = got.Update(modelStreamMsg{env: modelstream.Envelope{
		SessionID: "sess-old",
		Event:     providerapi.StreamEvent{Type: providerapi.StreamDelta, ContentDelta: "stale"},
	}})
	got = updated.(model)
	if got.liveContent != "" {
		t.Fatalf("unfocused TUI accepted old stream: %q", got.liveContent)
	}
	if view := renderSessionView(&got); !strings.Contains(ansi.Strip(view), "local coding agent") {
		t.Fatalf("expected ready page:\n%s", view)
	}
}

func TestBareNewDropsStaleRefresh(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-old"
	m.refreshGen = 3
	m.refreshing = true
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	if got.refreshGen <= 3 || got.refreshing {
		t.Fatalf("clearTask should bump gen and drop in-flight: gen=%d refreshing=%v", got.refreshGen, got.refreshing)
	}
	sid := gatewayclient.SessionID("sess-old")
	task := &gatewayclient.Task{ID: "task-old", Title: "old", SessionID: &sid}
	updated, _ = got.Update(refreshDoneMsg{
		gen:      3,
		kind:     refreshFull,
		task:     task,
		messages: []gatewayclient.TranscriptMessage{{Role: "user", Content: "old turn"}},
	})
	got = updated.(model)
	if got.sessionID != "" || got.task != nil || len(got.messages) != 0 {
		t.Fatalf("stale refresh restored focus session=%q task=%v msgs=%d",
			got.sessionID, got.task, len(got.messages))
	}
}

func TestBareNewClearsPermissionUI(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-old"
	m.permissions = []gatewayclient.Permission{{ID: "perm-1"}}
	m.pendingPermCount = 1
	m.autoOpenedPermList = true
	m.list = listPermissions
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	if got.pendingPermCount != 0 || len(got.permissions) != 0 || got.autoOpenedPermList || got.list != listNone {
		t.Fatalf("perm leftover count=%d perms=%d auto=%v list=%v",
			got.pendingPermCount, len(got.permissions), got.autoOpenedPermList, got.list)
	}
	footer := got.renderFooter()
	if strings.Contains(footer, "permission") {
		t.Fatalf("footer still shows pending perms:\n%s", footer)
	}
}

func TestBareNewClearsTodos(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-old"
	m.todos = []gatewayclient.SessionTodo{
		{ID: "1", Content: "patch fs.go", Status: "in_progress"},
		{ID: "2", Content: "write tests", Status: "pending"},
	}
	m.pillsExpanded = true
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	if len(got.todos) != 0 || got.pillsExpanded {
		t.Fatalf("todo leftover n=%d expanded=%v", len(got.todos), got.pillsExpanded)
	}
	if pills := got.renderPills(80); pills != "" {
		t.Fatalf("pills still rendered:\n%s", pills)
	}
}

func TestBareNewDropsPermissionSSE(t *testing.T) {
	gw := &fakeGateway{permissions: []gatewayclient.Permission{{ID: "perm-1"}}}
	m := newModel(paths.ModeUser, gw)
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	updated, cmd := got.Update(sseEventMsg{envelope: eventapi.Envelope{Type: "permission.pending"}})
	got = updated.(model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(permPollDoneMsg); ok {
				t.Fatal("unfocused TUI polled permissions after SSE")
			}
		}
	}
	updated, _ = got.Update(permPollDoneMsg{permissions: gw.permissions, openList: true})
	got = updated.(model)
	if got.pendingPermCount != 0 || len(got.permissions) != 0 || got.list != listNone {
		t.Fatalf("unfocused TUI restored perms count=%d list=%v", got.pendingPermCount, got.list)
	}
}

func TestNewWithMessageIsUsageError(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	msg := m.handleLineCmd("/new hello")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.clearTask || done.err == nil || done.err.Error() != "usage: /new" {
		t.Fatalf("/new hello = %#v", done)
	}
}

func TestBareNewCancelsRunningTurn(t *testing.T) {
	task := gatewayclient.Task{ID: "task-run", Title: "old", State: gatewayclient.TaskStateRunning}
	gw := &fakeGateway{tasks: []gatewayclient.Task{task}}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-old"
	m.task = &task

	msg := m.handleLineCmd("/new")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil || !done.clearTask || done.taskID != "" {
		t.Fatalf("leave = %#v", done)
	}
	if len(gw.controlCalls) != 1 || gw.controlCalls[0] != gatewayclient.TaskActionCancel {
		t.Fatalf("control calls = %#v", gw.controlCalls)
	}

	updated, _ := m.Update(done)
	got := updated.(model)
	if got.sessionID != "" || got.task != nil {
		t.Fatalf("focus leftover session=%q task=%v", got.sessionID, got.task)
	}
}

func TestBareNewIdleSkipsCancel(t *testing.T) {
	task := gatewayclient.Task{ID: "task-done", Title: "old", State: gatewayclient.TaskStateCompleted}
	gw := &fakeGateway{tasks: []gatewayclient.Task{task}}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-old"
	m.task = &task

	msg := m.handleLineCmd("/new")()
	done := msg.(commandDoneMsg)
	if done.err != nil || !done.clearTask {
		t.Fatalf("idle /new = %#v", done)
	}
	if len(gw.controlCalls) != 0 {
		t.Fatalf("idle /new cancelled: %#v", gw.controlCalls)
	}
}

func TestBareNewCancelFailureStillLeaves(t *testing.T) {
	task := gatewayclient.Task{ID: "task-run", Title: "old", State: gatewayclient.TaskStateRunning}
	gw := &fakeGateway{tasks: []gatewayclient.Task{task}, controlErr: errors.New("cancel denied")}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-old"
	m.task = &task

	msg := m.handleLineCmd("/new")()
	done := msg.(commandDoneMsg)
	if !done.clearTask || done.err == nil || !strings.Contains(done.err.Error(), "cancel denied") {
		t.Fatalf("failed cancel = %#v", done)
	}

	updated, _ := m.Update(done)
	got := updated.(model)
	if got.sessionID != "" || got.task != nil {
		t.Fatalf("should still leave session=%q task=%v", got.sessionID, got.task)
	}
	if !strings.Contains(got.errMsg, "cancel denied") {
		t.Fatalf("footer err = %q", got.errMsg)
	}
	if view := renderSessionView(&got); !strings.Contains(ansi.Strip(view), "local coding agent") {
		t.Fatalf("expected ready page:\n%s", view)
	}
}

func TestPlainTextOnReadyStillSubmits(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	msg := m.handleLineCmd("hello")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.clearTask {
		t.Fatal("plain text on ready should submit, not leave")
	}
	if done.err == nil || done.err.Error() == "usage: /new" || strings.Contains(done.err.Error(), "empty message") {
		t.Fatalf("expected submit path, got %#v", done)
	}
}

func TestPlainTextOnRunningTurnSteers(t *testing.T) {
	task := gatewayclient.Task{ID: "task-run", Title: "old", State: gatewayclient.TaskStateRunning}
	gw := &fakeGateway{tasks: []gatewayclient.Task{task}}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-run"
	m.task = &task
	msg := m.handleLineCmd("do this instead")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil {
		t.Fatalf("steer err = %v", done.err)
	}
	if !strings.Contains(done.status, "steering") {
		t.Fatalf("status = %q", done.status)
	}
	if len(gw.steers) != 1 || gw.steers[0] != "do this instead" {
		t.Fatalf("steers = %#v", gw.steers)
	}
}
