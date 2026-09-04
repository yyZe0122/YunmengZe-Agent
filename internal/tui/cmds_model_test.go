package tui

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func testModelCfg() gatewayclient.ModelConfig {
	return gatewayclient.ModelConfig{
		Model:  "deepseek/a",
		Models: []string{"deepseek/a", "deepseek/b"},
		Ready:  true,
	}
}

func TestModelCommandSetsSessionNotGlobal(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	msg := m.handleLineCmd("/model deepseek/b")()
	done, ok := msg.(commandDoneMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	if done.err != nil {
		t.Fatalf("err = %v", done.err)
	}
	if done.sessionModel != "deepseek/b" {
		t.Fatalf("sessionModel = %q", done.sessionModel)
	}
	if len(gw.setModelCalls) != 0 {
		t.Fatalf("global switch = %#v", gw.setModelCalls)
	}
	if gw.sessionPreferred != "deepseek/b" {
		t.Fatalf("preferred = %q", gw.sessionPreferred)
	}
	updated, _ := m.Update(done)
	got := updated.(model)
	if got.displayModel() != "deepseek/b" {
		t.Fatalf("display = %q", got.displayModel())
	}
	if got.modelName != "deepseek/a" {
		t.Fatalf("global modelName = %q", got.modelName)
	}
}

func TestModelCommandReadyDraftDoesNotTouchGlobal(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg(), submitOK: true}
	m := newModel(paths.ModeUser, gw)
	msg := m.handleLineCmd("/model deepseek/b")()
	done := msg.(commandDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	if len(gw.setModelCalls) != 0 {
		t.Fatalf("global switch = %#v", gw.setModelCalls)
	}
	updated, _ := m.Update(done)
	got := updated.(model)
	if got.draftModel != "deepseek/b" {
		t.Fatalf("draft = %q", got.draftModel)
	}
	if got.displayModel() != "deepseek/b" {
		t.Fatalf("display = %q", got.displayModel())
	}
	submit := got.newTaskCmd("hello")()
	sdone := submit.(commandDoneMsg)
	if sdone.err != nil {
		t.Fatal(sdone.err)
	}
	if len(gw.submits) != 1 || gw.submits[0].PreferredModel != "deepseek/b" {
		t.Fatalf("submits = %#v", gw.submits)
	}
}

func TestModelMainSwitchesGlobal(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	m.sessionModel = "deepseek/b"
	msg := m.handleLineCmd("/model main deepseek/b")()
	done := msg.(commandDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	if len(gw.setModelCalls) != 1 || gw.setModelCalls[0] != "deepseek/b" {
		t.Fatalf("global switch = %#v", gw.setModelCalls)
	}
	if gw.sessionPreferred != "" {
		t.Fatalf("session prefer should be unchanged, got %q", gw.sessionPreferred)
	}
	if !strings.Contains(done.status, "global model=") {
		t.Fatalf("status = %q", done.status)
	}
}

func TestModelPreferRejected(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	msg := m.handleLineCmd("/model prefer deepseek/b")()
	done := msg.(commandDoneMsg)
	if done.err == nil || !strings.Contains(done.err.Error(), "/model main") {
		t.Fatalf("err = %v", done.err)
	}
}

func TestModelPickerOpensWithoutSwitch(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	msg := m.handleLineCmd("/model")()
	done := msg.(commandDoneMsg)
	if done.err != nil || done.openList != listModels {
		t.Fatalf("done = %#v", done)
	}
	if len(gw.setModelCalls) != 0 {
		t.Fatalf("opened picker switched global: %#v", gw.setModelCalls)
	}
}

func TestCompleterOffersModelMain(t *testing.T) {
	items := filterArgCompletions("/model", "", []string{"deepseek/a"}, nil)
	foundMain, foundModel := false, false
	for _, item := range items {
		if item.Name == "main" {
			foundMain = true
		}
		if item.Name == "deepseek/a" {
			foundModel = true
		}
	}
	if !foundMain || !foundModel {
		t.Fatalf("items = %#v", items)
	}
}

func TestStatusReadyDraftUsesDraftModelField(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg(), health: gatewayclient.Health{OK: true}}
	m := newModel(paths.ModeUser, gw)
	m.draftModel = "deepseek/b"
	msg := m.handleLineCmd("/status")()
	done := msg.(commandDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	if !strings.Contains(done.status, "draft_model=deepseek/b") {
		t.Fatalf("status = %q", done.status)
	}
	if strings.Contains(done.status, "session_model=") {
		t.Fatalf("ready draft must not be labeled session_model: %q", done.status)
	}
}

func TestStatusFocusedSessionUsesSessionModelField(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg(), health: gatewayclient.Health{OK: true}}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	m.sessionModel = "deepseek/b"
	m.draftModel = "deepseek/b"
	msg := m.handleLineCmd("/status")()
	done := msg.(commandDoneMsg)
	if done.err != nil {
		t.Fatal(done.err)
	}
	if !strings.Contains(done.status, "session_model=deepseek/b") {
		t.Fatalf("status = %q", done.status)
	}
	if strings.Contains(done.status, "draft_model=") {
		t.Fatalf("focused session must not be labeled draft_model: %q", done.status)
	}
}

func TestClearTaskKeepsStickyDraft(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	m.sessionModel = "deepseek/b"
	m.draftModel = "deepseek/b"
	updated, _ := m.Update(commandDoneMsg{clearTask: true, status: "new session"})
	got := updated.(model)
	if got.sessionID != "" {
		t.Fatalf("sessionID = %q", got.sessionID)
	}
	if got.sessionModel != "" {
		t.Fatalf("sessionModel = %q", got.sessionModel)
	}
	if got.draftModel != "deepseek/b" {
		t.Fatalf("sticky draft lost: %q", got.draftModel)
	}
	if got.displayModel() != "deepseek/b" {
		t.Fatalf("display = %q", got.displayModel())
	}
}

func TestSessionsRefreshClearsEmptyPreferredModel(t *testing.T) {
	gw := &fakeGateway{model: testModelCfg()}
	m := newModel(paths.ModeUser, gw)
	m.sessionID = "sess-1"
	m.sessionModel = "deepseek/b"
	updated, _ := m.Update(commandDoneMsg{
		sessions: []gatewayclient.Session{{ID: "sess-1"}},
	})
	got := updated.(model)
	if got.sessionModel != "" {
		t.Fatalf("empty prefer should clear sessionModel, got %q", got.sessionModel)
	}
}
