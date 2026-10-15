package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/yyZe0122/yunmengze-agent/internal/version"
	"sort"
	"strings"
)

var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

func (m model) overlay() string {
	var out []string
	if m.question != nil {
		q := m.question.question
		if q.TaskID != "" {
			out = append(out, muted.Render("task "+q.TaskID[:min(8, len(q.TaskID))]))
		}
		out = append(out, accent.Render(cleanText(q.Question)))
		start := max(0, m.choice-3)
		end := min(len(q.Options), start+7)
		for i := start; i < end; i++ {
			mark := "○"
			if q.Multiple {
				mark = "☐"
				if m.selected[i] {
					mark = "☑"
				}
			}
			cursor := " "
			if i == m.choice {
				cursor = "›"
			}
			out = append(out, fmt.Sprintf("%s %s %s", cursor, mark, cleanText(q.Options[i])))
		}
	}
	if m.picker != "" {
		out = append(out, accent.Render(m.picker+" · ↑↓ / Enter / Esc"))
		if len(m.choices) == 0 {
			out = append(out, "（空）")
		}
		start := max(0, m.choice-3)
		for i := start; i < min(len(m.choices), start+7); i++ {
			prefix := "  "
			if i == m.choice {
				prefix = "› "
			}
			out = append(out, prefix+cleanText(m.choices[i]))
		}
	}
	if m.question == nil && m.picker == "" {
		suggestions := m.suggestions()
		start := max(0, m.suggestion-3)
		for i := start; i < min(len(suggestions), start+7); i++ {
			prefix := "  "
			if i == m.suggestion {
				prefix = "› "
			}
			out = append(out, prefix+"/"+suggestions[i])
		}
	}
	return ansi.Wrap(strings.Join(out, "\n"), max(10, m.width-2), "")
}
func (m *model) refresh(bottom bool) {
	width := max(10, m.width-2)
	m.viewport.SetWidth(m.width)
	var lines []string
	var renderer *glamour.TermRenderer
	for i := range m.entries {
		e := &m.entries[i]
		label := e.role
		if e.name != "" {
			label += " · " + e.name
		}
		text := ""
		if e.folded {
			label += " · e 展开"
		} else {
			if e.width != width {
				e.rendered = ansi.Wrap(cleanText(e.text), width, "")
				if e.role == "assistant" {
					if renderer == nil {
						renderer, _ = glamour.NewTermRenderer(glamour.WithStandardStyle("notty"), glamour.WithWordWrap(width))
					}
					if renderer != nil {
						if rendered, err := renderer.Render(cleanText(e.text)); err == nil {
							e.rendered = strings.TrimSpace(rendered)
						}
					}
				}
				e.width = width
			}
			text = e.rendered
		}
		lines = append(lines, accent.Render(cleanText(label))+"\n"+text)
	}
	if m.thinking != "" {
		lines = append(lines, muted.Render("思考中…"))
	}
	if m.stream != "" {
		lines = append(lines, accent.Render("assistant")+"\n"+ansi.Wrap(cleanText(m.stream), width, ""))
	}
	var taskIDs []string
	for id := range m.tasks {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)
	for _, id := range taskIDs {
		task := m.tasks[id]
		if task.status == "completed" {
			continue
		}
		lines = append(lines, muted.Render(fmt.Sprintf("task %s · %s · tokens %d", id[:min(8, len(id))], cleanText(task.status), task.tokens)))
	}
	overlayHeight := 0
	if overlay := m.overlay(); overlay != "" {
		overlayHeight = lipgloss.Height(overlay)
	}
	m.viewport.SetHeight(max(1, m.height-3-lipgloss.Height(m.input.View())-overlayHeight))
	m.viewport.SetContent(strings.Join(lines, "\n\n"))
	if bottom {
		m.viewport.GotoBottom()
	}
}
func (m model) View() tea.View {
	head := accent.Render("ymz "+version.Version) + " · " + string(m.mode) + " · " + cleanText(m.modelRef)
	head = ansi.Truncate(head, m.width, "…") + "\n" + muted.Render(strings.Repeat("─", max(1, m.width)))
	before := head + "\n" + m.viewport.View() + "\n"
	if overlay := m.overlay(); overlay != "" {
		before += overlay + "\n"
	}
	body := before + m.input.View() + "\n" + muted.Render(ansi.Truncate(cleanText(m.status), m.width, "…"))
	view := tea.NewView(body)
	view.AltScreen = true
	if cursor := m.input.Cursor(); cursor != nil && m.picker == "" {
		cursor.Y += lipgloss.Height(before) - 1
		view.Cursor = cursor
	}
	return view
}

func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' || r == 127 {
			return -1
		}
		return r
	}, ansi.Strip(text))
}
