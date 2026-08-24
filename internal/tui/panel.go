package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

func inkPanel(bg, border color.Color, width int) lipgloss.Style {
	s := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Background(bg)
	if width > 0 {
		s = s.Width(width)
	}
	return s
}

func sealChip(label string, fg, bg color.Color) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Padding(0, 1).
		Render(label)
}

func textureLine(width int) string {
	if width < 1 {
		return ""
	}
	var b strings.Builder
	b.Grow(width * 3)
	for i := 0; i < width; i++ {
		if i%2 == 0 {
			b.WriteRune('░')
		} else {
			b.WriteRune('▒')
		}
	}
	return b.String()
}

func textureFill(width int) string {
	if width < 1 {
		return ""
	}
	return strings.Repeat("░", width)
}

func textureFrame(inner string, width int, stamp string, stampColor color.Color) string {
	if width < 8 {
		width = 8
	}
	tex := lipgloss.NewStyle().Foreground(colorHair).Background(colorPaper)
	edge := lipgloss.NewStyle().Foreground(colorHair).Background(colorPaper)
	core := lipgloss.NewStyle().Foreground(colorHair).Background(colorInk)

	stamp = strings.ToUpper(strings.TrimSpace(stamp))
	if stamp == "" {
		stamp = "AGENT"
	}
	chip := lipgloss.NewStyle().
		Foreground(colorPaper).
		Background(stampColor).
		Render(" " + stamp + " ")

	innerW := width - 2
	body := lipgloss.NewStyle().
		Background(colorInk).
		Foreground(colorBone).
		Width(innerW).
		Render(inner)

	topFill := innerW - lipgloss.Width(chip)
	if topFill < 0 {
		topFill = 0
	}
	top := edge.Render("┌") + chip + core.Render(textureFill(topFill)) + edge.Render("┐")

	var b strings.Builder
	b.WriteString(tex.Render(textureLine(width)))
	b.WriteByte('\n')
	b.WriteString(top)
	for _, ln := range strings.Split(body, "\n") {
		b.WriteByte('\n')
		b.WriteString(edge.Render("│"))
		b.WriteString(ln)
		b.WriteString(edge.Render("│"))
	}
	b.WriteByte('\n')
	b.WriteString(edge.Render("└") + core.Render(textureFill(innerW)) + edge.Render("┘"))
	return b.String()
}
