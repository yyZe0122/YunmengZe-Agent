package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m model) View() tea.View {
	if m.quitting {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	width := m.innerWidth()

	header := m.renderHeader()
	chat := m.viewport.View()
	if m.showContextPanel() {
		ctx := m.renderMetricsPanel(m.viewport.Height())
		chat = lipgloss.JoinHorizontal(lipgloss.Top,
			chat,
			stylePaper.Width(1).Render(" "),
			inkPanel(colorWash, colorHair, contextPanelWidth).
				MaxHeight(m.viewport.Height()).
				Render(ctx),
		)
	}

	var floatParts []string
	if m.helpOpen {
		floatParts = append(floatParts, m.renderHelpOverlay(width))
	}
	if ov := renderPickerOverlay(&m, width); ov != "" {
		floatParts = append(floatParts, ov)
	}
	if m.completer.visible {
		if c := m.completer.render(6); c != "" {
			floatParts = append(floatParts, stylePickerBox.Width(max(8, width-2)).Render(c))
		}
	}

	pills := m.renderPills(width)
	footer := m.renderFooter()
	inputBox := m.renderInputBox(width)

	parts := []string{header, "", chat}
	parts = append(parts, floatParts...)
	if pills != "" {
		parts = append(parts, "", pills)
	}
	parts = append(parts, "", footer, inputBox)
	frame := lipgloss.JoinVertical(lipgloss.Left, parts...)
	box := lipgloss.NewStyle().
		Foreground(colorBone).
		Background(colorPaper).
		Padding(framePadY, framePadX)
	if m.width > 0 {
		box = box.MaxWidth(m.width)
	}
	v := tea.NewView(box.Render(frame))
	v.AltScreen = true
	return v
}

func (m model) renderHeader() string {
	width := m.innerWidth()
	if width < 1 {
		width = 1
	}
	parts := []string{styleTitle.Background(colorWash).Render("ymz")}
	if m.modelName != "" {
		parts = append(parts, sealChip(truncate(m.modelName, 28), colorBone, colorInk))
	}
	parts = append(parts, sseDot(m.sseState))
	if m.task != nil {
		parts = append(parts, styleDim.Background(colorWash).Render(shortID(string(m.task.ID))), stateBadge(m.task.State))
	}
	line := truncate(strings.Join(parts, "  ·  "), max(1, width-2))
	return lipgloss.NewStyle().
		Foreground(colorBone).
		Background(colorWash).
		Width(width).
		Padding(1, 1).
		Render(line)
}

func (m model) renderInputBox(width int) string {
	modeColor := colorModeAgent
	modeLabel := "AGENT"
	switch m.draftMode {
	case modePlan:
		modeColor = colorModePlan
		modeLabel = "PLAN"
	case modeAuto:
		modeColor = colorModeAuto
		modeLabel = "AUTO"
	}
	busy := ""
	if m.busy || m.runActivity() == activityActive {
		busy = " " + styleWarn.Render(m.spinner.View())
	}
	innerW := max(1, width-2)
	m.applyInputStyles()
	if busy != "" {
		m.input.Placeholder = strings.TrimSpace(m.input.Placeholder)
	}
	body := m.input.View()
	hint := ""
	if m.sessionID == "" && m.task == nil && len(m.messages) == 0 {
		hint = styleMuted.Background(colorInk).Render("Tab plan/agent/auto · Ctrl+PgUp/Dn sessions")
	} else {
		hint = styleMuted.Background(colorInk).Render("Enter send · Esc Esc undo · Ctrl+PgUp/Dn sessions")
	}
	if busy != "" {
		hint += busy
	}
	inner := body
	if hint != "" {
		inner += "\n" + truncate(hint, max(1, innerW-2))
	}
	return textureFrame(inner, width, modeLabel, modeColor)
}

func (m model) renderPills(width int) string {
	if len(m.todos) == 0 {
		return ""
	}
	done, total := 0, 0
	current := ""
	for _, item := range m.todos {
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "completed", "cancelled", "canceled":
			done++
			total++
		case "in_progress", "in-progress":
			total++
			if current == "" {
				current = item.Content
			}
		default:
			total++
		}
	}
	if total == 0 {
		return ""
	}
	label := styleOK.Background(colorWash).Render(fmt.Sprintf("To-Do %d/%d", done, total))
	if current != "" && !m.pillsExpanded {
		label += "  " + styleMuted.Background(colorWash).Render(truncate(current, max(12, width-24)))
	}
	hint := styleMuted.Background(colorWash).Render("ctrl+t")
	line := truncate(label+"  "+hint, width)
	bar := lipgloss.NewStyle().Background(colorWash).Width(width)
	if !m.pillsExpanded {
		return bar.Render(line)
	}
	var b strings.Builder
	b.WriteString(bar.Render(line))
	for _, item := range m.todos {
		mark := "○"
		st := styleMuted.Background(colorWash)
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "completed":
			mark = "●"
			st = styleOK.Background(colorWash)
		case "in_progress", "in-progress":
			mark = "◌"
			st = styleWarn.Background(colorWash)
		case "cancelled", "canceled":
			mark = "–"
		}
		b.WriteByte('\n')
		b.WriteString(bar.Render(truncate("  "+st.Render(mark)+" "+item.Content, width)))
	}
	return b.String()
}

func (m model) renderContextStrip() string {
	width := m.innerWidth()
	if width < 1 {
		width = 1
	}
	parts := []string{}
	modelName := m.modelName
	if modelName == "" {
		modelName = "—"
	}
	parts = append(parts, modelName)
	cwd := m.cwd
	if cwd == "" {
		cwd = "—"
	}
	parts = append(parts, truncate(cwd, 40))

	ctxPart := "ctx —"
	if n, ok := m.metrics().ContextWindow(); ok {
		ctxPart = fmt.Sprintf("ctx %s", formatTokens(int64(n)))
		if p, okP := m.metrics().ContextPressure(); okP {
			ctxPart = fmt.Sprintf("ctx %s %d%%", formatTokens(int64(n)), int(p*100+0.5))
		}
	}
	parts = append(parts, ctxPart)

	active := m.runActivity() == activityActive
	pulse := heartbeatWave(active, m.animFrame)
	if active {
		pulse = styleHeart.Render(pulse)
	} else {
		pulse = styleMuted.Render(pulse)
	}
	label := m.activityLabel()
	if active {
		if el := formatDuration(m.activityElapsed()); el != "" {
			label += " " + el
		}
	}
	parts = append(parts, pulse+" "+label)

	if !m.showContextPanel() {
		if used, maxTok, ok := m.metrics().TaskTokenUsage(); ok {
			if maxTok > 0 {
				parts = append(parts, fmt.Sprintf("tok %s/%s", formatTokens(used), formatTokens(maxTok)))
			} else {
				parts = append(parts, fmt.Sprintf("tok %s", formatTokens(used)))
			}
		}
	}
	return lipgloss.NewStyle().
		Foreground(colorBone).
		Background(colorHair).
		Width(width).
		Render(truncate(strings.Join(parts, "  ·  "), width))
}

func (m model) renderFooter() string {
	width := m.innerWidth()
	if width < 1 {
		width = 1
	}
	bar := lipgloss.NewStyle().Foreground(colorBone).Background(colorHair).Width(width)
	if m.errMsg != "" {
		return lipgloss.NewStyle().Foreground(colorMix).Background(colorSeal).Width(width).Render(truncate(m.errMsg, width))
	}
	if m.pendingPermCount > 0 {
		line := fmt.Sprintf("%d tool permission(s) pending · 1–4 decide · /perm", m.pendingPermCount)
		if m.statusMsg != "" {
			line = m.statusMsg
			if idx := strings.IndexByte(line, '\n'); idx >= 0 {
				line = line[:idx] + "…"
			}
		}
		return lipgloss.NewStyle().Foreground(colorMix).Background(colorSeal).Width(width).Render(truncate(line, width))
	}
	if m.statusMsg != "" && m.statusMsg != "daemon ok" {
		line := m.statusMsg
		if idx := strings.IndexByte(line, '\n'); idx >= 0 {
			line = line[:idx] + "…"
		}
		return bar.Render(truncate(line, width))
	}
	return m.renderContextStrip()
}

func (m model) renderMetricsPanel(height int) string {
	var b strings.Builder
	b.WriteString(styleMetricsTitle.Background(colorWash).Render("context") + "\n\n")

	b.WriteString(stylePanelLabel.Background(colorWash).Render("tokens") + "\n")
	if used, maxTok, ok := m.metrics().TaskTokenUsage(); ok {
		if maxTok > 0 {
			b.WriteString(fmt.Sprintf("  %s / %s\n", formatTokens(used), formatTokens(maxTok)))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", formatTokens(used)))
		}
		if m.usage.InputTokens > 0 || m.usage.OutputTokens > 0 {
			b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf("  in %s · out %s\n",
				formatTokens(m.usage.InputTokens), formatTokens(m.usage.OutputTokens))))
		}
		if m.usage.CostMicros > 0 {
			b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf("  cost %dµ\n", m.usage.CostMicros)))
		}
		if m.runUsageOK && m.runUsage.ChildRunCount > 0 {
			b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf("  parent %s · children %s (%d)\n",
				formatTokens(m.runUsage.Self.TotalTokens),
				formatTokens(m.runUsage.Children.TotalTokens),
				m.runUsage.ChildRunCount)))
		}
	} else {
		b.WriteString(styleDim.Background(colorWash).Render("  —") + "\n")
	}
	if p, ok := m.metrics().ContextPressure(); ok {
		b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf("  window %d%%", int(p*100+0.5))))
		if m.contextOK && m.taskContext.LastPromptTokens > 0 {
			b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf(" · prompt %s", formatTokens(m.taskContext.LastPromptTokens))))
		}
		if m.contextOK && m.taskContext.Compacted {
			b.WriteString(styleDim.Background(colorWash).Render(" · compacted"))
		}
		b.WriteString("\n")
	}

	if summary := m.budgetSummary(); summary != "" {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorWash).Render("budget") + "\n")
		b.WriteString("  " + summary + "\n")
	}
	if m.dataDir != "" {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorWash).Render("data") + "\n")
		b.WriteString("  " + truncate(m.dataDir, 36) + "\n")
	}

	if rate, ok := m.metrics().CacheHitRate(); ok {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorWash).Render("cache") + "\n")
		b.WriteString(fmt.Sprintf("  hit %.0f%%\n", rate*100))
		if m.usage.CacheReadTokens > 0 || m.usage.CacheWriteTokens > 0 {
			b.WriteString(styleDim.Background(colorWash).Render(fmt.Sprintf("  read %s · write %s\n",
				formatTokens(m.usage.CacheReadTokens), formatTokens(m.usage.CacheWriteTokens))))
		}
	}
	if mcp := m.metrics().MCPStatus(); mcp.Enabled {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorWash).Render("MCP") + "\n")
		line := fmt.Sprintf("  %d ok · %d err · %d total", mcp.OK, mcp.Error, mcp.Total)
		if mcp.Detail != "" {
			line += " · " + mcp.Detail
		}
		b.WriteString(line + "\n")
	}

	content := strings.TrimRight(b.String(), "\n")
	lines := strings.Split(content, "\n")
	if height > 0 && len(lines) > height {
		content = strings.Join(lines[:height], "\n")
	}
	return content
}

func (m model) budgetSummary() string {
	b, ok := planBudgetOf(m.plan)
	if !ok {
		return ""
	}
	return fmt.Sprintf("tok=%d cost_µ=%d dur_ms=%d", b.MaxTokens, b.MaxCostMicros, b.MaxDurationMillis)
}

func (m model) renderHelpOverlay(width int) string {
	text := strings.TrimSpace(helpText())
	lines := strings.Split(text, "\n")
	if len(lines) > helpOverlayMax {
		lines = append(lines[:helpOverlayMax], styleMuted.Render("… Esc to close"))
		text = strings.Join(lines, "\n")
	}
	boxW := max(8, width-2)
	return styleHelpBox.Width(boxW).Render(text)
}

func (m *model) syncViewport(force bool) {
	content := renderSessionView(m)
	if !force && content == m.viewportContent {
		if m.stickBottom {
			m.pinViewportBottom()
		}
		return
	}
	m.viewportContent = content
	m.viewport.SetContent(content)
	if m.stickBottom {
		m.pinViewportBottom()
	}
}

func (m *model) pinViewportBottom() {
	total := m.viewport.TotalLineCount()
	h := m.viewport.Height()
	if h < 1 {
		h = 1
	}
	y := total - h
	if y < 0 {
		y = 0
	}
	if m.viewport.YOffset() == y {
		return
	}
	m.viewport.SetYOffset(y)
}

func (m *model) layout() {
	header := 3
	footer := 1
	if m.completer.visible {
		footer += min(6, len(m.completer.items)) + 2
	}
	if m.list != listNone {
		n := min(overlayMaxLines, max(1, m.listLen()))
		footer += n + 3
	}
	if m.helpOpen {
		footer += helpOverlayMax + 2
	}
	footer += 4 + m.input.Height()
	if len(m.todos) > 0 {
		footer += 1
		if m.pillsExpanded {
			footer += len(m.todos)
		}
	}
	h := m.height - framePadY*2 - header - footer - moduleGap*2
	if h < 5 {
		h = 5
	}
	w := m.width - framePadX*2 - 1
	if m.showContextPanel() {
		w -= contextPanelWidth + 1
	}
	if w < 1 {
		w = 1
	}
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
	m.input.SetWidth(max(1, m.innerWidth()-4))
}

func (m model) innerWidth() int {
	w := m.width - framePadX*2
	if w < 1 {
		return 1
	}
	return w
}

func (m *model) showContextPanel() bool {
	if m.width < contextBreakWidth {
		return false
	}
	if m.sessionID == "" && m.task == nil {
		return false
	}
	_, _, ok := m.metrics().TaskTokenUsage()
	return ok
}
