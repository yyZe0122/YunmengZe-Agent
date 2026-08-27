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
	if !strings.Contains(plain, "local coding agent") {
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
	if !strings.Contains(got, "to-do 1/3") || !strings.Contains(got, "patch fs.go") {
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
	if !strings.Contains(got, "dev") {
		t.Fatalf("header missing version: %s", got)
	}
	if !strings.Contains(got, "●") && !strings.Contains(got, "○") {
		t.Fatalf("header missing sse dot: %s", got)
	}
	if strings.Contains(got, "0.0.0-dev") {
		t.Fatalf("header leaked dev version: %s", got)
	}
}

func TestHeaderShowsSessionTitle(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.sessionID = "sess-1"
	m.sessions = []gatewayclient.Session{{ID: "sess-1", Title: "fix layout chrome"}}
	got := ansi.Strip(m.renderHeader())
	if !strings.Contains(got, "fix layout chrome") {
		t.Fatalf("header missing session title: %s", got)
	}
}

func TestTabTogglesDraftMode(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	if m.draftMode != modeAgent {
		t.Fatalf("default mode = %q", m.draftMode)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm := updated.(model)
	if mm.draftMode != modePlan {
		t.Fatalf("after tab = %q", mm.draftMode)
	}
	mm.modelName = "local/qwen"
	box := ansi.Strip(mm.renderInputBox(80))
	if !strings.Contains(box, "PLAN") {
		t.Fatalf("input box missing PLAN:\n%s", box)
	}
	if !strings.Contains(box, "local/qwen") {
		t.Fatalf("input box missing model:\n%s", box)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm = updated.(model)
	if mm.draftMode != modeAuto {
		t.Fatalf("second tab = %q", mm.draftMode)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm = updated.(model)
	if mm.draftMode != modeAgent {
		t.Fatalf("third tab = %q", mm.draftMode)
	}
	updated, _ = mm.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	mm = updated.(model)
	if mm.draftMode != modeAuto {
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

func TestShiftPgUpCyclesOlderSession(t *testing.T) {
	newer := gatewayclient.Session{ID: "sess-new", Title: "new"}
	older := gatewayclient.Session{ID: "sess-old", Title: "old"}
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newer, older}
	m.sessionID = newer.ID
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	got := updated.(model)
	if got.sessionID != older.ID {
		t.Fatalf("shift+pgup session = %q want %q", got.sessionID, older.ID)
	}
	if cmd == nil {
		t.Fatal("shift+pgup should refresh")
	}
	if got.list != listNone {
		t.Fatalf("overlay still open: %v", got.list)
	}
	updated, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModShift})
	got = updated.(model)
	if got.sessionID != newer.ID {
		t.Fatalf("shift+pgdown session = %q want %q", got.sessionID, newer.ID)
	}
}

func TestShiftPgDownFromLandingFocusesNewest(t *testing.T) {
	newest := gatewayclient.Session{ID: "sess-new"}
	oldest := gatewayclient.Session{ID: "sess-old"}
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newest, oldest}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModShift})
	got := updated.(model)
	if got.sessionID != newest.ID {
		t.Fatalf("landing shift+pgdown = %q want newest", got.sessionID)
	}
	m = newModel(paths.ModeUser, &fakeGateway{})
	m.sessions = []gatewayclient.Session{newest, oldest}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	got = updated.(model)
	if got.sessionID != oldest.ID {
		t.Fatalf("landing shift+pgup = %q want oldest", got.sessionID)
	}
}

func TestShiftPgUpEmptySessions(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
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
	wantW := m.innerWidth()
	if m.viewport.Width() != wantW {
		t.Fatalf("empty session viewport width = %d want %d", m.viewport.Width(), wantW)
	}
	chrome := layoutChromeHeight(&m)
	wantH := m.height - framePadY*2 - chrome
	if wantH < 5 {
		wantH = 5
	}
	if m.viewport.Height() != wantH {
		t.Fatalf("viewport height = %d want %d (chrome=%d)", m.viewport.Height(), wantH, chrome)
	}

	m.syncViewport(true)
	view := m.View()
	lines := strings.Split(view.Content, "\n")
	if len(lines) < 3 {
		t.Fatalf("view too short:\n%s", view.Content)
	}
	if strings.TrimSpace(ansi.Strip(lines[0])) != "" {
		t.Fatalf("expected top pad, first line = %q", lines[0])
	}
	if w := lipgloss.Width(lines[1]); w > m.width {
		t.Fatalf("header line width %d > %d: %q", w, m.width, lines[1])
	}
	if view.Cursor == nil {
		t.Fatal("expected hardware cursor for IME")
	}
	if view.Cursor.Y < framePadY+headerHeight {
		t.Fatalf("cursor Y %d is not below header", view.Cursor.Y)
	}
}

func layoutChromeHeight(m *model) int {
	width := m.innerWidth()
	header := m.renderHeader()
	floats := m.renderFloats(width)
	pills := m.renderPills(width)
	editor := m.renderInputBox(width)
	status := m.renderFooter()
	var below []string
	below = append(below, floats...)
	if pills != "" {
		below = append(below, "", pills)
	}
	below = append(below, "", editor, status)
	return lipgloss.Height(header) + 1 + lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, below...))
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
	avail := m.innerWidth()
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

func TestDisplayVersionShowsDev(t *testing.T) {
	if displayVersion() != "dev" {
		t.Fatalf("dev version = %q want dev", displayVersion())
	}
}

func TestCompleterDoesNotLiftEditor(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.layout()
	m.syncViewport(true)
	beforeH := m.viewport.Height()
	before := m.View()
	if before.Cursor == nil {
		t.Fatal("expected cursor")
	}
	y := before.Cursor.Y

	m.input.SetValue("/")
	m.refreshCompleter()
	if !m.completer.visible || len(m.completer.items) == 0 {
		t.Fatalf("completer not visible items=%d", len(m.completer.items))
	}
	m.layout()
	m.syncViewport(true)
	after := m.View()
	if m.viewport.Height() != beforeH {
		t.Fatalf("completer shrank viewport %d → %d", beforeH, m.viewport.Height())
	}
	if after.Cursor == nil {
		t.Fatal("expected cursor after completer")
	}
	if after.Cursor.Y != y {
		t.Fatalf("cursor Y %d → %d", y, after.Cursor.Y)
	}
	ov := ansi.Strip(m.renderCompleterOverlay(m.innerWidth()))
	if !strings.Contains(ov, "/new") && !strings.Contains(ov, "/sessions") {
		t.Fatalf("completer overlay:\n%s", ov)
	}
}

func TestStatusRendersBelowEditor(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.modelName = "local/qwen"
	m.layout()
	m.syncViewport(true)
	plain := ansi.Strip(m.View().Content)
	agent := strings.Index(plain, "AGENT")
	model := strings.Index(plain, "local/qwen")
	if agent < 0 || model < 0 {
		t.Fatalf("missing editor/status:\n%s", plain)
	}
}

func TestFooterKeepsModelWithStatus(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.modelName = "local/qwen"
	m.statusMsg = "Build · session session-f405… · task task-00e0bab…"
	got := ansi.Strip(m.renderFooter())
	if !strings.Contains(got, "local/qwen") {
		t.Fatalf("footer dropped model:\n%s", got)
	}
	if !strings.Contains(got, "Build") {
		t.Fatalf("footer dropped status:\n%s", got)
	}
	box := ansi.Strip(m.renderInputBox(80))
	if !strings.Contains(box, "AGENT") || !strings.Contains(box, "local/qwen") {
		t.Fatalf("editor stamp missing model:\n%s", box)
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
		if name != "raster.go" && (bytes.Contains(raw, []byte("░")) || bytes.Contains(raw, []byte("▒"))) {
			t.Fatalf("%s still uses noise texture", name)
		}
	}
}

func TestInkPaletteHasNoTeal(t *testing.T) {
	for _, hex := range []string{
		hexNightPaper, hexNightInk, hexNightWash, hexNightHair, hexNightBone,
		hexNightFly, hexNightSeal, hexNightMix,
		hexNightPine, hexNightWater, hexNightGold,
		hexDayPaper, hexDayInk, hexDayWash, hexDayHair, hexDayBone, hexDaySeal,
		hexDayPine, hexDayWater, hexDayGold,
	} {
		if hex == "#9EC9B8" || hex == "#2F6B62" {
			t.Fatalf("teal leftover: %s", hex)
		}
	}
}

func TestNightModesAreDistinct(t *testing.T) {
	applyTheme(nightTheme)
	if colorModeAgent == colorModePlan || colorModeAgent == colorModeAuto || colorModePlan == colorModeAuto {
		t.Fatal("Tab mode colors must contrast")
	}
	if colorKeyword == colorModeAuto || colorKeyword == colorErr {
		t.Fatal("slash keyword must not share AUTO/error red")
	}
	if colorKeyword != colorOK {
		t.Fatal("slash keyword should use pine")
	}
}
