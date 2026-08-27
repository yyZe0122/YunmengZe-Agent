package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

func TestMascotBraille(t *testing.T) {
	got := renderMascot(moodIdle, 0, mascotMedium, colorPaper)
	plain := ansi.Strip(got)
	lines := strings.Split(plain, "\n")
	w, h := mascotDims(mascotMedium)
	if len(lines) != h {
		t.Fatalf("height = %d want %d", len(lines), h)
	}
	if !hasBraille(plain) {
		t.Fatalf("missing braille:\n%s", plain)
	}
	if strings.Contains(plain, "▀") {
		t.Fatalf("half-block leaked:\n%s", plain)
	}
	for i, line := range lines {
		if n := lipgloss.Width(line); n != w {
			t.Fatalf("line %d width = %d want %d: %q", i, n, w, line)
		}
	}
}

func TestMascotIdleDiffersWrite(t *testing.T) {
	idle := ansi.Strip(renderMascot(moodIdle, 0, mascotMedium, colorPaper))
	write := ansi.Strip(renderMascot(moodWrite, 0, mascotMedium, colorPaper))
	if idle == write {
		t.Fatalf("idle and write frames match")
	}
}

func TestBrushColorByMood(t *testing.T) {
	if brushColor(moodIdle) != colorBrush {
		t.Fatalf("idle color")
	}
	if brushColor(moodWrite) != colorSeal {
		t.Fatalf("write should be seal")
	}
	if brushColor(moodThink) != colorModePlan {
		t.Fatalf("think should be gold")
	}
}

func TestMascotWriteCycles(t *testing.T) {
	a := ansi.Strip(renderMascot(moodWrite, 0, mascotMedium, colorPaper))
	b := ansi.Strip(renderMascot(moodWrite, 1, mascotMedium, colorPaper))
	c := ansi.Strip(renderMascot(moodWrite, 4, mascotMedium, colorPaper))
	if a == b {
		t.Fatalf("write frames 0 and 1 should differ")
	}
	if a != c {
		t.Fatalf("write frame 4 should wrap toward frame 0")
	}
}

func TestMascotStateFromActivity(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	if mascotState(&m) != moodLift {
		t.Fatalf("connecting = %q", mascotState(&m))
	}
	m.sseState = "ok"
	if mascotState(&m) != moodIdle {
		t.Fatalf("idle = %q", mascotState(&m))
	}
	m.errMsg = "boom"
	if mascotState(&m) != moodStuck {
		t.Fatalf("err = %q", mascotState(&m))
	}
	m.errMsg = ""
	m.pendingPermCount = 2
	if mascotState(&m) != moodWait {
		t.Fatalf("perm = %q", mascotState(&m))
	}
	m.pendingPermCount = 0
	m.task = &gatewayclient.Task{State: gatewayclient.TaskStatePaused}
	if mascotState(&m) != moodPause {
		t.Fatalf("paused = %q", mascotState(&m))
	}
	m.task = &gatewayclient.Task{State: gatewayclient.TaskStateFailed}
	if mascotState(&m) != moodStuck {
		t.Fatalf("failed = %q", mascotState(&m))
	}
	m.task = &gatewayclient.Task{State: gatewayclient.TaskStateRunning}
	m.liveThinking = "hmm"
	if mascotState(&m) != moodThink {
		t.Fatalf("thinking = %q", mascotState(&m))
	}
	m.liveThinking = ""
	m.liveContent = "hello"
	if mascotState(&m) != moodWrite {
		t.Fatalf("writing = %q", mascotState(&m))
	}
	m.liveContent = ""
	m.liveTools = []contentBlock{{Kind: blockToolCall, Text: "fs_read"}}
	if mascotState(&m) != moodTool {
		t.Fatalf("tool = %q", mascotState(&m))
	}
}

func TestHeaderCompactTwoRows(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.sseState = "ok"
	got := ansi.Strip(m.renderHeader())
	lines := strings.Split(got, "\n")
	if len(lines) != headerHeight {
		t.Fatalf("header lines = %d want %d:\n%s", len(lines), headerHeight, got)
	}
	if !strings.Contains(got, "ymz") {
		t.Fatalf("header missing ymz:\n%s", got)
	}
	if !strings.Contains(got, "dev") {
		t.Fatalf("header missing version:\n%s", got)
	}
	if hasBraille(got) {
		t.Fatalf("header should not draw mascot:\n%s", got)
	}
	if strings.Contains(got, "0.0.0-dev") {
		t.Fatalf("header leaked dev version: %s", got)
	}
}

func TestHeaderNarrowNoOverflow(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 30, 20
	m.sseState = "ok"
	got := m.renderHeader()
	plain := ansi.Strip(got)
	for i, line := range strings.Split(plain, "\n") {
		if w := lipgloss.Width(line); w > m.innerWidth() {
			t.Fatalf("header line %d width %d > %d: %q", i, w, m.innerWidth(), line)
		}
	}
}

func TestLandingShowsMascot(t *testing.T) {
	m := newModel(paths.ModeUser, &fakeGateway{})
	m.width, m.height = 80, 24
	m.sseState = "ok"
	m.layout()
	plain := ansi.Strip(renderEmptySession(&m))
	if !strings.Contains(plain, "local coding agent") {
		t.Fatalf("landing:\n%s", plain)
	}
	if !hasBraille(plain) {
		t.Fatalf("landing missing braille:\n%s", plain)
	}
}

func TestNoNoiseTextureInSource(t *testing.T) {
	// chrome helpers must not reintroduce ░▒ noise
	if strings.Contains(hairline(8), "░") || strings.Contains(hairline(8), "▒") {
		t.Fatal("hairline used noise")
	}
}
