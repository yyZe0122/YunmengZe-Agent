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
	opts := renderOpts{Width: bubbleWidth(m.viewport.Width()), Theme: m.theme, Stream: &m.streamMD, Anim: m.animFrame}
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

	size := landingMascotSize(w)
	_, mh := mascotDims(size)
	if h < mh+10 {
		size = mascotMedium
		_, mh = mascotDims(size)
	}
	if h < mh+8 {
		size = mascotCompact
	}
	brush := renderMascot(mascotState(m), m.animFrame, size, colorPaper)
	chips := renderLandingChips()
	recent := renderLandingRecent(m, w)

	var b strings.Builder
	b.WriteString(brush)
	b.WriteByte('\n')
	if h >= 16 {
		b.WriteByte('\n')
	}
	b.WriteString(styleDim.Render("local coding agent"))
	b.WriteByte('\n')
	if h >= 14 {
		b.WriteByte('\n')
	}
	b.WriteString(chips)
	if recent != "" && h >= 18 {
		b.WriteByte('\n')
		b.WriteByte('\n')
		b.WriteString(recent)
	}
	body := b.String()
	if strings.Count(body, "\n")+1 >= h {
		return body
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body,
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(colorPaper).Foreground(colorPaper)))
}

func renderLandingChips() string {
	fg := colorFly
	return lipgloss.JoinHorizontal(lipgloss.Top,
		sealChip("Tab mode", fg),
		styleMuted.Render("   "),
		sealChip("Ctrl+P", fg),
		styleMuted.Render("   "),
		sealChip("Ctrl+S", fg),
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
		b.WriteString(styleMuted.Background(colorPaper).Render(line))
	}
	return b.String()
}
