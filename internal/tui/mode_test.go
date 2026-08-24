package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func TestLandingShowsStartHints(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.layout()
	view := renderEmptySession(&m)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "ymz") || !strings.Contains(plain, "local coding agent") {
		t.Fatalf("landing:\n%s", view)
	}
	if !strings.Contains(plain, "Ctrl+P") || !strings.Contains(plain, "Ctrl+S") || !strings.Contains(plain, "Tab") {
		t.Fatalf("landing missing chips:\n%s", view)
	}
	if strings.Contains(plain, "ready") || strings.Contains(strings.ToLower(plain), "context") {
		t.Fatalf("landing leaked chrome:\n%s", view)
	}
}

func TestPillsRenderTodoProgress(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.todos = []gatewayclient.SessionTodo{
		{ID: "1", Content: "patch fs.go", Status: "in_progress"},
		{ID: "2", Content: "write tests", Status: "pending"},
		{ID: "3", Content: "done item", Status: "completed"},
	}
	got := ansi.Strip(m.renderPills(80))
	if !strings.Contains(got, "To-Do 1/3") || !strings.Contains(got, "patch fs.go") {
		t.Fatalf("pills = %q", got)
	}
	m.pillsExpanded = true
	got = ansi.Strip(m.renderPills(80))
	if !strings.Contains(got, "write tests") || !strings.Contains(got, "done item") {
		t.Fatalf("expanded pills = %q", got)
	}
}

func TestFullRefreshWithoutTodosOKKeepsPills(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.todos = []gatewayclient.SessionTodo{
		{ID: "1", Content: "patch fs.go", Status: "in_progress"},
	}
	m.pillsExpanded = true
	updated, _ := m.Update(refreshDoneMsg{kind: refreshFull})
	got := updated.(model)
	if len(got.todos) != 1 || !got.pillsExpanded {
		t.Fatalf("stale full refresh wiped pills n=%d expanded=%v", len(got.todos), got.pillsExpanded)
	}
}

func TestFullRefreshTodosOKClearsPills(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.todos = []gatewayclient.SessionTodo{
		{ID: "1", Content: "patch fs.go", Status: "in_progress"},
	}
	m.pillsExpanded = true
	updated, _ := m.Update(refreshDoneMsg{kind: refreshFull, todosOK: true})
	got := updated.(model)
	if len(got.todos) != 0 || got.pillsExpanded {
		t.Fatalf("empty todosOK should clear pills n=%d expanded=%v", len(got.todos), got.pillsExpanded)
	}
}

func TestHeaderShowsAgentVersion(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	got := ansi.Strip(m.renderHeader())
	if !strings.Contains(got, "ymz") {
		t.Fatalf("header missing ymz: %s", got)
	}
	if !strings.Contains(got, "●") && !strings.Contains(got, "○") {
		t.Fatalf("header missing sse dot: %s", got)
	}
	if strings.Contains(got, "0.0.0-dev") {
		t.Fatalf("header leaked dev version: %s", got)
	}
}

func TestTabTogglesDraftMode(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	if m.draftMode != modeAgent {
		t.Fatalf("default mode = %q", m.draftMode)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm := updated.(model)
	if mm.draftMode != modeAuto {
		t.Fatalf("after tab = %q", mm.draftMode)
	}
	box := ansi.Strip(mm.renderInputBox(80))
	if !strings.Contains(box, "AUTO") {
		t.Fatalf("input box missing AUTO:\n%s", box)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm = updated.(model)
	if mm.draftMode != modePlan {
		t.Fatalf("second tab = %q", mm.draftMode)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm = updated.(model)
	if mm.draftMode != modeAgent {
		t.Fatalf("third tab = %q", mm.draftMode)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	mm = updated.(model)
	if mm.draftMode != modePlan {
		t.Fatalf("shift-tab = %q", mm.draftMode)
	}
}

func TestPickerDoesNotClearTask(t *testing.T) {
	task := gatewayclient.Task{ID: "task-keep", Title: "keep", State: gatewayclient.TaskStateRunning}
	m := newModel(paths.ModeUser, &fakeGateway{tasks: []gatewayclient.Task{task}})
	m.width, m.height = 80, 24
	m.task = &task
	m.timeline = []timelineItem{{Kind: tlUser, Title: "objective", Body: "hello"}}
	m.layout()
	updated, _ := m.Update(commandDoneMsg{openList: listSessions, status: "session history"})
	mm := updated.(model)
	if mm.list != listSessions {
		t.Fatalf("list = %v", mm.list)
	}
	if mm.task == nil || mm.task.ID != "task-keep" {
		t.Fatalf("task cleared: %#v", mm.task)
	}
	if len(mm.timeline) == 0 {
		t.Fatal("timeline cleared")
	}
	body := ansi.Strip(renderSessionView(&mm))
	if !strings.Contains(body, "hello") {
		t.Fatalf("session view missing timeline:\n%s", body)
	}
	ov := renderPickerOverlay(&mm, 80)
	if !strings.Contains(ov, "Sessions") {
		t.Fatalf("overlay:\n%s", ov)
	}
}

func TestCtrlPgUpCyclesOlderSession(t *testing.T) {
	newer := gatewayclient.Session{ID: "sess-new", Title: "new"}
	older := gatewayclient.Session{ID: "sess-old", Title: "old"}
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newer, older}
	m.sessionID = newer.ID
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl})
	got := updated.(model)
	if got.sessionID != older.ID {
		t.Fatalf("ctrl+pgup session = %q want %q", got.sessionID, older.ID)
	}
	if cmd == nil {
		t.Fatal("ctrl+pgup should refresh")
	}
	if got.list != listNone {
		t.Fatalf("overlay still open: %v", got.list)
	}
	updated, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl})
	got = updated.(model)
	if got.sessionID != newer.ID {
		t.Fatalf("ctrl+pgdown session = %q want %q", got.sessionID, newer.ID)
	}
}

func TestCtrlPgDownFromLandingFocusesNewest(t *testing.T) {
	newest := gatewayclient.Session{ID: "sess-new"}
	oldest := gatewayclient.Session{ID: "sess-old"}
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newest, oldest}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl})
	got := updated.(model)
	if got.sessionID != newest.ID {
		t.Fatalf("landing ctrl+pgdown = %q want newest", got.sessionID)
	}
	m = newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newest, oldest}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl})
	got = updated.(model)
	if got.sessionID != oldest.ID {
		t.Fatalf("landing ctrl+pgup = %q want oldest", got.sessionID)
	}
}

func TestCtrlPgUpEmptySessions(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl})
	got := updated.(model)
	if cmd != nil {
		t.Fatal("empty list should not refresh")
	}
	if got.sessionID != "" {
		t.Fatalf("session = %q", got.sessionID)
	}
	if got.statusMsg != "no sessions" {
		t.Fatalf("status = %q", got.statusMsg)
	}
}

func TestPgUpDoesNotChangeSession(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{{ID: "sess-a"}, {ID: "sess-b"}}
	m.sessionID = "sess-a"
	m.width, m.height = 100, 40
	m.layout()
	m.viewport.SetContent(strings.Repeat("line\n", 40))
	m.viewport.GotoBottom()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	got := updated.(model)
	if got.sessionID != "sess-a" {
		t.Fatalf("plain pgup switched session to %q", got.sessionID)
	}
}

func TestPgUpWorksWhilePickerOpen(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 100, 40
	m.list = listSessions
	m.layout()
	// fill viewport
	m.viewport.SetContent(strings.Repeat("line\n", 40))
	m.viewport.GotoBottom()
	before := m.viewport.YOffset()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	mm := updated.(model)
	if mm.viewport.YOffset() >= before && before > 0 {
		t.Fatalf("expected scroll up: before=%d after=%d", before, mm.viewport.YOffset())
	}
}

func TestLayoutReservesFramePadding(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 120, 40
	m.layout()
	if m.viewport.Width() >= m.width {
		t.Fatalf("viewport width %d not inset from %d", m.viewport.Width(), m.width)
	}
	if m.viewport.Height() >= m.height {
		t.Fatalf("viewport height %d not inset from %d", m.viewport.Height(), m.height)
	}
	wantW := m.width - framePadX*2 - 1
	if m.viewport.Width() != wantW {
		t.Fatalf("empty session viewport width = %d want %d", m.viewport.Width(), wantW)
	}
	wantH := m.height - framePadY*2 - 3 - 1 - 4 - m.input.Height() - moduleGap*2
	if m.viewport.Height() != wantH {
		t.Fatalf("viewport height = %d want %d", m.viewport.Height(), wantH)
	}

	m.syncViewport(true)
	view := m.View().Content
	lines := strings.Split(view, "\n")
	if len(lines) < 3 {
		t.Fatalf("view too short:\n%s", view)
	}
	if strings.TrimSpace(ansi.Strip(lines[0])) != "" {
		t.Fatalf("expected top pad, first line = %q", lines[0])
	}
	if w := lipgloss.Width(lines[1]); w > m.width {
		t.Fatalf("header line width %d > %d: %q", w, m.width, lines[1])
	}
}

func TestLayoutNarrowWindowDoesNotOverflow(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 30, 20
	m.layout()
	if m.innerWidth() != m.width-framePadX*2 {
		t.Fatalf("innerWidth = %d want %d", m.innerWidth(), m.width-framePadX*2)
	}
	m.syncViewport(true)
	for i, line := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("line %d width %d > %d: %q", i, w, m.width, line)
		}
	}

	m.width, m.height = 18, 16
	m.layout()
	avail := m.innerWidth() - 1
	if avail < 1 {
		avail = 1
	}
	if m.viewport.Width() > avail {
		t.Fatalf("viewport width %d inflated past available %d", m.viewport.Width(), avail)
	}
	m.syncViewport(true)
	for i, line := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("narrow line %d width %d > %d: %q", i, w, m.width, line)
		}
	}
}

func TestTruncateIsSingleLine(t *testing.T) {
	got := truncate("abcdefghijklmnopqrstuvwxyz", 8)
	if strings.Contains(got, "\n") {
		t.Fatalf("truncate wrapped:\n%q", got)
	}
	if w := lipgloss.Width(got); w > 8 {
		t.Fatalf("width %d > 8: %q", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("missing ellipsis: %q", got)
	}
	cjk := truncate("云梦泽编码循环上下文", 6)
	if strings.Contains(cjk, "\n") {
		t.Fatalf("cjk wrapped:\n%q", cjk)
	}
	if w := lipgloss.Width(cjk); w > 6 {
		t.Fatalf("cjk width %d > 6: %q", w, cjk)
	}
}

func TestEmptySessionHidesContextPanel(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 120, 40
	m.layout()
	if m.showContextPanel() {
		t.Fatal("empty session should hide context panel")
	}
	m.syncViewport(true)
	plain := ansi.Strip(m.View().Content)
	if strings.Contains(plain, "context") {
		t.Fatalf("empty session leaked context:\n%s", plain)
	}

	m.sessionID = "sess-1"
	m.usageOK = true
	m.usage.TotalTokens = 12
	m.layout()
	if !m.showContextPanel() {
		t.Fatal("session with usage should show context panel")
	}
}

func TestEscEscUndo(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-1"
	m.input.SetValue("draft")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := updated.(model)
	if strings.TrimSpace(got.input.Value()) != "" {
		t.Fatalf("input = %q", got.input.Value())
	}
	_, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("second esc should fire undo")
	}
	msg := cmd()
	done, ok := msg.(commandDoneMsg)
	if !ok || done.status != "restored /tmp/ws/a.go" {
		t.Fatalf("undo cmd = %#v", msg)
	}
}

func TestEmptyEscArmsUndo(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessionID = "sess-1"
	m.input.SetValue("")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := updated.(model)
	if cmd != nil {
		t.Fatal("first empty esc should arm undo, not fire")
	}
	if got.lastEscAt.IsZero() {
		t.Fatal("first empty esc should set lastEscAt")
	}
	updated, cmd = got.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("second empty esc should fire undo")
	}
}

func TestDisplayVersionHidesDev(t *testing.T) {
	if displayVersion() != "" {
		t.Fatalf("dev version should hide, got %q", displayVersion())
	}
}

func TestNoRoundedBorderInSource(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("RoundedBorder")) {
			t.Fatalf("%s still uses RoundedBorder", name)
		}
	}
}

func TestInkPaletteHasNoTeal(t *testing.T) {
	for _, hex := range []string{
		hexNightPaper, hexNightInk, hexNightWash, hexNightHair, hexNightBone,
		hexNightFly, hexNightSeal, hexNightStamp, hexNightMix,
		hexDayPaper, hexDayInk, hexDayWash, hexDayHair, hexDayBone, hexDaySeal, hexDayStamp,
		hexPlanOchre,
	} {
		if hex == "#9EC9B8" || hex == "#2F6B62" {
			t.Fatalf("teal leftover: %s", hex)
		}
	}
}
