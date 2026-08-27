package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

func sealChip(label string, fg color.Color) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Underline(true).
		Render(label)
}

func stampInk(stamp string) color.Color {
	if stamp == "PLAN" {
		return colorStampInk
	}
	return colorBone
}

func editorFrame(inner string, width int, stamp string, stampColor color.Color, modelName string) string {
	if width < 8 {
		width = 8
	}
	stamp = strings.ToUpper(strings.TrimSpace(stamp))
	if stamp == "" {
		stamp = "AGENT"
	}
	chip := lipgloss.NewStyle().
		Foreground(stampInk(stamp)).
		Background(stampColor).
		Render(" " + stamp + " ")
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = "—"
	}
	model := lipgloss.NewStyle().
		Foreground(colorBone).
		Background(colorPaper).
		Bold(true).
		Render(modelName)
	gap := lipgloss.NewStyle().Background(colorPaper).Render(" ")
	used := lipgloss.Width(chip) + 1 + lipgloss.Width(model) + 1
	rule := hairline(max(1, width-used))
	top := lipgloss.JoinHorizontal(lipgloss.Center, chip, gap, model, gap, rule)
	body := lipgloss.NewStyle().
		Background(colorPaper).
		Foreground(colorBone).
		Width(width).
		Render(inner)
	return top + "\n" + body
}
