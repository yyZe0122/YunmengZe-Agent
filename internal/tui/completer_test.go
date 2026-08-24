package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func TestCompleterFiltersPrefix(t *testing.T) {
	var c completer
	c.update("/mo")
	if !c.visible || len(c.items) == 0 {
		t.Fatalf("expected items, got %#v", c.items)
	}
	if c.items[0].Name != "/model" {
		t.Fatalf("got %q", c.items[0].Name)
	}
	name := c.accept()
	if name != "/model" {
		t.Fatalf("accept = %q", name)
	}
	if c.visible {
		t.Fatal("should dismiss after accept")
	}
}

func TestCompleterHidesWithArgs(t *testing.T) {
	var c completer
	c.update("/new hello")
	if c.visible {
		t.Fatal("should hide when args present")
	}
}

func TestCompleterAllOnSlash(t *testing.T) {
	items := filterCommands("/", nil)
	if len(items) != len(slashCommands) {
		t.Fatalf("got %d want %d", len(items), len(slashCommands))
	}
	skills := []slashCommand{{Name: "/git", Desc: "skill"}}
	items = filterCommands("/", skills)
	if len(items) != len(slashCommands)+1 {
		t.Fatalf("got %d want %d", len(items), len(slashCommands)+1)
	}
}

func TestCompleterSkillPrefix(t *testing.T) {
	var c completer
	c.updateWith("/gi", nil, nil, []slashCommand{{Name: "/git", Desc: "skill · git"}})
	if !c.visible || len(c.items) == 0 || c.items[0].Name != "/git" {
		t.Fatalf("items=%#v", c.items)
	}
}

func TestCompleterMoveWraps(t *testing.T) {
	var c completer
	c.update("/")
	c.move(-1)
	if c.cursor != len(c.items)-1 {
		t.Fatalf("cursor = %d", c.cursor)
	}
	c.move(1)
	if c.cursor != 0 {
		t.Fatalf("cursor = %d", c.cursor)
	}
}

func TestInputIsCompleteCommand(t *testing.T) {
	if !inputIsCompleteCommand("/model", "/model") {
		t.Fatal("exact name")
	}
	if !inputIsCompleteCommand("/q", "/quit") {
		t.Fatal("alias should match canonical")
	}
	if inputIsCompleteCommand("/mo", "/model") {
		t.Fatal("prefix is not complete")
	}
}

func TestEnterCompletesThenExecutesModel(t *testing.T) {
	gw := &fakeGateway{
		model: gatewayclient.ModelConfig{
			Model:  "deepseek/a",
			Models: []string{"deepseek/a", "deepseek/b"},
		},
	}
	m := newModel(paths.ModeUser, gw)
	m.width, m.height = 100, 40
	m.input.SetValue("/mo")
	m.completer.update(m.input.Value())
	// Move cursor to /model among prefix matches.
	for i, item := range m.completer.items {
		if item.Name == "/model" {
			m.completer.cursor = i
			break
		}
	}
	if m.completer.selectedName() != "/model" {
		t.Fatalf("selected = %q items=%v", m.completer.selectedName(), m.completer.items)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := updated.(model)
	if mm.input.Value() != "/model" {
		t.Fatalf("after first enter input=%q want /model", mm.input.Value())
	}
	if cmd != nil {
		// First enter must not start a command (cmd may be nil).
		if msg := cmd(); msg != nil {
			if _, ok := msg.(commandDoneMsg); ok {
				t.Fatalf("first enter must not execute: %#v", msg)
			}
		}
	}

	// Re-sync completer for full command (visible with single match).
	mm.completer.update(mm.input.Value())
	updated, cmd = mm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("second enter should return execute cmd")
	}
	msg := cmd()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil || done.openList != listModels {
		t.Fatalf("done = %#v", done)
	}
	updated, _ = updated.(model).Update(done)
	mm = updated.(model)
	if mm.list != listModels {
		t.Fatalf("list = %v", mm.list)
	}
}

func TestEnterOnCompleteQuitExecutes(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.input.SetValue("/quit")
	m.completer.update(m.input.Value())
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit cmd")
	}
	msg := cmd()
	done, ok := msg.(commandDoneMsg)
	if !ok || !done.quit {
		t.Fatalf("got %#v", msg)
	}
}

func TestTabCompletesWithoutExecute(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.input.SetValue("/mo")
	m.completer.update(m.input.Value())
	for i, item := range m.completer.items {
		if item.Name == "/model" {
			m.completer.cursor = i
			break
		}
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	mm := updated.(model)
	if mm.input.Value() != "/model " {
		t.Fatalf("tab input=%q", mm.input.Value())
	}
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(commandDoneMsg); ok {
				t.Fatal("tab must not execute")
			}
		}
	}
}
