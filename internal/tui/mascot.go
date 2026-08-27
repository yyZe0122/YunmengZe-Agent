package tui

import (
	"image/color"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

type mascotMood string

const (
	moodIdle  mascotMood = "idle"
	moodThink mascotMood = "think"
	moodWrite mascotMood = "write"
	moodTool  mascotMood = "tool"
	moodWait  mascotMood = "wait"
	moodPause mascotMood = "pause"
	moodStuck mascotMood = "stuck"
	moodLift  mascotMood = "lift"
)

type mascotSize int

const (
	mascotMedium mascotSize = iota
	mascotLanding
	mascotCompact
)

const (
	mascotMediumW   = 16
	mascotMediumH   = 4
	mascotLandingW  = 28
	mascotLandingH  = 16
	mascotCompactW  = 8
	mascotCompactH  = 4
	mascotCompactAt = 40
	landingLargeAt  = 60
)

func mascotState(m *model) mascotMood {
	if m == nil {
		return moodIdle
	}
	if strings.TrimSpace(m.errMsg) != "" {
		return moodStuck
	}
	if m.task != nil && m.task.State == gatewayclient.TaskStateFailed {
		return moodStuck
	}
	if m.pendingPermCount > 0 {
		return moodWait
	}
	if m.task != nil && m.task.State == gatewayclient.TaskStatePaused {
		return moodPause
	}
	switch m.activityKind() {
	case "thinking":
		return moodThink
	case "writing":
		return moodWrite
	case "tool":
		return moodTool
	case "waiting permission":
		return moodWait
	case "paused", "waiting":
		return moodPause
	case "running":
		return moodWrite
	}
	if m.sseState == "connecting" || m.sseState == "reconnecting" {
		return moodLift
	}
	return moodIdle
}

func landingMascotSize(width int) mascotSize {
	if width >= landingLargeAt {
		return mascotLanding
	}
	if width >= mascotCompactAt {
		return mascotMedium
	}
	return mascotCompact
}

func mascotDims(size mascotSize) (int, int) {
	switch size {
	case mascotLanding:
		return mascotLandingW, mascotLandingH
	case mascotCompact:
		return mascotCompactW, mascotCompactH
	default:
		return mascotMediumW, mascotMediumH
	}
}

func brushColor(mood mascotMood) color.Color {
	switch mood {
	case moodWrite, moodTool, moodStuck:
		return colorSeal
	case moodThink, moodWait:
		return colorModePlan
	case moodPause:
		return colorFly
	default:
		return colorBrush
	}
}

func renderMascot(mood mascotMood, frame int, size mascotSize, bg color.Color) string {
	w, h := mascotDims(size)
	c := newCanvas(w, h, brushViewW, brushViewH)
	drawBrush(c, mood, frame)
	return c.b.encodeBraille(bg, brushColor(mood), colorSeal)
}

func (m *model) isLanding() bool {
	return m.sessionID == "" && m.task == nil && len(m.messages) == 0
}
