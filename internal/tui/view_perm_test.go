package tui

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func TestQuestionCardShowsOptionsAndCustom(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 100, 40
	m.list = listQuestions
	m.questions = []gatewayclient.UserQuestion{{
		ID: "uq-1",
		Questions: []gatewayclient.UserQuestionItem{{
			ID: "q1", Header: "Approach", Question: "How should we ship the extra-root grant?",
			Options: []gatewayclient.UserQuestionOption{
				{Label: "Rewrite", Description: "clean break"},
				{Label: "Patch", Description: "minimal"},
			},
		}},
	}}
	m.resetQuestionDraft()
	view := renderPickerOverlay(&m, 80)
	for _, want := range []string{"How should we ship", "Rewrite", "clean break", "Patch", customAnswerLabel} {
		if !strings.Contains(view, want) {
			t.Fatalf("question card missing %q:\n%s", want, view)
		}
	}
}

func TestPermCardShowsCommandAndTiers(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 100, 40
	m.list = listPermissions
	m.permissions = []gatewayclient.Permission{{
		ID: "perm-1", ToolName: "process_shell", Path: "/tmp/ws", Risk: "R2",
		Command: "/bin/sh", CommandArgs: []string{"-c", "go test ./internal/tui/"},
	}}
	view := renderPickerOverlay(&m, 80)
	for _, want := range []string{"process_shell", "/tmp/ws", "go test", "once", "similar", "remember this tool", "deny"} {
		if !strings.Contains(view, want) {
			t.Fatalf("perm card missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "chat.workspace.allow") {
		t.Fatal("non extra-root permanent should not mention chat.workspace.allow")
	}
}

func TestPermCardExtraRootPermanentLabel(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 100, 40
	m.list = listPermissions
	m.permissions = []gatewayclient.Permission{{
		ID: "perm-2", ToolName: "fs_read", Path: "/other/dir/file.txt", ExtraRoot: true,
	}}
	view := renderPickerOverlay(&m, 80)
	if !strings.Contains(view, "chat.workspace.allow") || !strings.Contains(view, "extra root") {
		t.Fatalf("extra-root card:\n%s", view)
	}
}

func TestQuestionWizardCommitsAllItems(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.list = listQuestions
	m.questions = []gatewayclient.UserQuestion{{
		ID: "uq-2",
		Questions: []gatewayclient.UserQuestionItem{
			{ID: "a", Question: "First?", Options: []gatewayclient.UserQuestionOption{{Label: "one"}, {Label: "two"}}},
			{ID: "b", Question: "Second?", Options: []gatewayclient.UserQuestionOption{{Label: "x"}, {Label: "y"}}},
		},
	}}
	m.resetQuestionDraft()
	_ = m.questionConfirmOption(0)
	if m.qItemIdx != 1 || m.qDraft["a"][0] != "one" {
		t.Fatalf("after first: idx=%d draft=%v", m.qItemIdx, m.qDraft)
	}
	cmd := m.questionConfirmOption(1)
	if cmd == nil {
		t.Fatal("expected submit cmd on last question")
	}
}
