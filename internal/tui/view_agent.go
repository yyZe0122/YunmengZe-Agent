package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func renderSessionView(m *model) string {
	if m.sessionID == "" && m.task == nil && len(m.messages) == 0 {
		return renderEmptySession(m)
	}
	opts := renderOpts{Width: bubbleWidth(m.viewport.Width()), Theme: m.theme, Stream: &m.streamMD}
	return m.tlCache.render(m.timeline, m.expand, opts)
}

func renderEmptySession(m *model) string {
	w := m.viewport.Width()
	if w < 1 {
		w = m.innerWidth()
	}
	if m.width < 1 && w < 8 {
		w = 80
	}
	h := m.viewport.Height()
	if h < 1 {
		h = 20
	}

	logo := renderLogo(w)
	subtitle := styleDim.Render("local coding agent")
	chips := renderLandingChips()
	recent := renderLandingRecent(m, w)

	var b strings.Builder
	b.WriteString(logo)
	b.WriteByte('\n')
	b.WriteString(subtitle)
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(chips)
	if recent != "" {
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(recent)
	}
	body := b.String()
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body,
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(colorPaper).Foreground(colorPaper)))
}

func renderLandingChips() string {
	fg, bg := colorBone, colorWash
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sealChip("Tab mode", fg, bg),
		" ",
		sealChip("Ctrl+P", fg, bg),
		" ",
		sealChip("Ctrl+S", fg, bg),
	)
}

func renderLandingRecent(m *model, width int) string {
	type card struct {
		id, state, title string
	}
	var cards []card
	if len(m.sessions) > 0 {
		limit := min(5, len(m.sessions))
		for i := 0; i < limit; i++ {
			s := m.sessions[i]
			title := s.Title
			if title == "" {
				title = string(s.ID)
			}
			cards = append(cards, card{id: shortID(string(s.ID)), state: s.LatestState, title: title})
		}
	} else if len(m.tasks) > 0 {
		limit := min(5, len(m.tasks))
		for i := 0; i < limit; i++ {
			t := m.tasks[i]
			cards = append(cards, card{id: shortID(string(t.ID)), state: t.State, title: t.Title})
		}
	}
	if len(cards) == 0 {
		return ""
	}
	cardW := min(48, max(24, width-8))
	var b strings.Builder
	for i, c := range cards {
		if i > 0 {
			b.WriteByte('\n')
		}
		line := fmt.Sprintf("%s  %s  %s", c.id, c.state, truncate(c.title, max(8, cardW-16)))
		b.WriteString(inkPanel(colorWash, colorHair, cardW).Padding(0, 1).Render(line))
	}
	return b.String()
}
