package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderOpts controls bubble width and markdown theme for one paint.
type renderOpts struct {
	Width  int
	Theme  ThemeName
	Stream *streamingMD
	Anim   int
}

func defaultRenderOpts() renderOpts {
	return renderOpts{Width: 72, Theme: ThemeNight}
}

var runningFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}

func runningSpinner(frame int) string {
	if frame < 0 {
		frame = -frame
	}
	return runningFrames[frame%len(runningFrames)]
}

func bubbleWidth(termW int) int {
	if termW <= 0 {
		termW = 80
	}
	w := termW - 2
	if w < 8 {
		w = 8
	}
	return w
}

// wrapBody wraps plain text to width (rune-aware, soft break on spaces).
func wrapBody(s string, width int) string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) []string {
	runes := []rune(line)
	if len(runes) <= width {
		return []string{line}
	}
	var lines []string
	for len(runes) > width {
		cut := width
		for i := width; i > width/2; i-- {
			if runes[i-1] == ' ' {
				cut = i
				break
			}
		}
		lines = append(lines, string(runes[:cut]))
		runes = runes[cut:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return lines
}

func renderUserBlock(body string, width int) string {
	innerW := width - 2
	if innerW < 8 {
		innerW = 8
	}
	var content string
	if strings.Contains(body, "\x1b[") {
		content = body
	} else {
		content = wrapBody(body, innerW)
	}
	bar := lipgloss.NewStyle().Background(colorSeal).Foreground(colorSeal).Width(1)
	text := lipgloss.NewStyle().Background(colorPaper).Foreground(colorBone).Width(max(1, width-1)).Padding(0, 1).Render(content)
	return lipgloss.JoinHorizontal(lipgloss.Top, bar.Render(" "), text)
}

func renderAssistantBlock(body string, width int) string {
	innerW := width
	if innerW < 8 {
		innerW = 8
	}
	var content string
	if strings.Contains(body, "\x1b[") {
		content = body
	} else {
		var b strings.Builder
		for i, ln := range strings.Split(wrapBody(body, innerW), "\n") {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(styleTLReply.Background(colorInk).Render(ln))
		}
		content = b.String()
	}
	return lipgloss.NewStyle().Background(colorInk).Foreground(colorBone).Width(width).Render(content)
}

func renderThinkingLine(title string, width int) string {
	return lipgloss.NewStyle().
		Background(colorPaper).
		Foreground(colorFly).
		Width(max(1, width)).
		Render(truncate(title, width))
}

func renderToolCard(body string, width int) string {
	return lipgloss.NewStyle().
		Background(colorPaper).
		Foreground(colorBone).
		Width(width).
		Render(body)
}

func renderDoneBanner(title string, state string, width int) string {
	if title == "" {
		title = "done"
	}
	label := title
	if state != "" && state != title {
		label += " · " + state
	}
	return lipgloss.NewStyle().
		Foreground(colorSeal).
		Background(colorPaper).
		Width(max(1, width)).
		Render(truncate("— "+label, width))
}

func renderSystemLine(prefix, title, state string, titleStyle lipgloss.Style, anim int) string {
	if strings.HasPrefix(title, "waiting permission") {
		return styleWarn.Render("● " + title)
	}
	if state == "running" || strings.HasPrefix(title, "running") {
		return styleOK.Render(runningSpinner(anim) + " " + title)
	}
	line := titleStyle.Render(prefix + " " + title)
	if state != "" {
		line += "  " + stateBadge(state)
	}
	return line
}

func blockTitleThinking(lines int) string {
	n := lines
	if n < 1 {
		n = 1
	}
	return fmt.Sprintf("think %d", n)
}

func blockTitleTool(name, preview string) string {
	if name == "" {
		name = "tool"
	}
	if preview != "" {
		return "· " + name + " · " + preview
	}
	return "· " + name
}
