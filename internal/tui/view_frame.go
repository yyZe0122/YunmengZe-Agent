package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/version"
)

func displayVersion() string {
	v := strings.TrimSpace(version.Version)
	if v == "" || v == "0.0.0-dev" {
		return "dev"
	}
	return v
}

func hairline(width int) string {
	if width < 1 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colorHair).Background(colorPaper).Width(width).Render(strings.Repeat("─", width))
}

func (m model) View() tea.View {
	if m.quitting {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	width := m.innerWidth()

	header := m.renderHeader()
	chat := m.renderChat()
	floats := m.renderFloats(width)
	pills := m.renderPills(width)
	editor := m.renderInputBox(width)
	status := m.renderFooter()

	var above []string
	above = append(above, header, "", chat)
	above = append(above, floats...)
	if pills != "" {
		above = append(above, "", pills)
	}
	above = append(above, "")
	aboveH := lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, above...))

	parts := append(append([]string{}, above...), editor, status)
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
	if cur := m.input.Cursor(); cur != nil {
		cur.X += framePadX
		cur.Y += framePadY + aboveH + 1
		v.Cursor = cur
	}
	return v
}

func (m model) renderHeader() string {
	width := m.innerWidth()
	if width < 1 {
		width = 1
	}
	meta := []string{styleTitle.Background(colorPaper).Render("ymz")}
	if v := displayVersion(); v != "" {
		meta = append(meta, styleMuted.Background(colorPaper).Render(v))
	}
	if title := m.sessionTitle(); title != "" {
		nameCap := min(40, max(8, width-18))
		meta = append(meta, styleDim.Background(colorPaper).Render(truncate(title, nameCap)))
	}
	meta = append(meta, sseDot(m.sseState))
	line := truncate(strings.Join(meta, "  ·  "), max(1, width))
	body := lipgloss.NewStyle().
		Foreground(colorBone).
		Background(colorPaper).
		Width(width).
		Render(line)
	return lipgloss.JoinVertical(lipgloss.Left, body, hairline(width))
}

func (m model) sessionTitle() string {
	if m.sessionID == "" {
		return ""
	}
	for _, s := range m.sessions {
		if s.ID != m.sessionID {
			continue
		}
		if t := strings.TrimSpace(s.Title); t != "" {
			return t
		}
		break
	}
	if m.task != nil {
		if t := strings.TrimSpace(m.task.Title); t != "" {
			return t
		}
	}
	return shortID(string(m.sessionID))
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
	m.applyInputStyles()
	return editorFrame(m.input.View(), width, modeLabel, modeColor, m.displayModel())
}

func (m model) displayModel() string {
	name := m.effectiveModel(m.modelName)
	if name == "" {
		return "—"
	}
	return name
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
	label := styleOK.Background(colorPaper).Render(fmt.Sprintf("to-do %d/%d", done, total))
	if current != "" && !m.pillsExpanded {
		label += "  " + styleMuted.Background(colorPaper).Render(truncate(current, max(12, width-24)))
	}
	hint := styleMuted.Background(colorPaper).Render("ctrl+t")
	line := truncate(label+"  "+hint, width)
	bar := lipgloss.NewStyle().Background(colorPaper).Width(width)
	if !m.pillsExpanded {
		return bar.Render(line)
	}
	var b strings.Builder
	b.WriteString(bar.Render(line))
	for _, item := range m.todos {
		mark := "·"
		st := styleMuted.Background(colorPaper)
		switch strings.ToLower(strings.TrimSpace(item.Status)) {
		case "completed":
			mark = "–"
			st = styleOK.Background(colorPaper)
		case "in_progress", "in-progress":
			mark = "›"
			st = styleWarn.Background(colorPaper)
		case "cancelled", "canceled":
			mark = "×"
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
	parts := []string{m.displayModel()}
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
	label := m.activityLabel()
	if active {
		if el := formatDuration(m.activityElapsed()); el != "" {
			label += " " + el
		}
	}

	rest := ""
	if !m.showContextPanel() {
		if used, maxTok, ok := m.metrics().TaskTokenUsage(); ok {
			if maxTok > 0 {
				rest = fmt.Sprintf("tok %s/%s", formatTokens(used), formatTokens(maxTok))
			} else {
				rest = fmt.Sprintf("tok %s", formatTokens(used))
			}
		}
	}
	head := strings.Join(parts, "  ·  ")
	line := head + "  ·  "
	if active {
		line += styleOK.Background(colorPaper).Render(label)
	} else if m.pendingPermCount > 0 {
		line += styleWarn.Background(colorPaper).Render(label)
	} else {
		line += label
	}
	if rest != "" {
		line += "  ·  " + rest
	}
	return lipgloss.NewStyle().
		Foreground(colorFly).
		Background(colorPaper).
		Width(width).
		Render(truncate(line, width))
}

func (m model) renderFooter() string {
	width := m.innerWidth()
	if width < 1 {
		width = 1
	}
	model := m.displayModel()
	if m.errMsg != "" {
		line := model + "  ·  " + m.errMsg
		return lipgloss.NewStyle().Foreground(colorMix).Background(colorSeal).Width(width).Render(truncate(line, width))
	}
	if m.pendingPermCount > 0 {
		rest := fmt.Sprintf("%d tool permission(s) pending · 1–4 decide · /perm", m.pendingPermCount)
		if m.statusMsg != "" {
			rest = m.statusMsg
			if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
				rest = rest[:idx] + "…"
			}
		}
		return lipgloss.NewStyle().Foreground(colorMix).Background(colorSeal).Width(width).Render(truncate(model+"  ·  "+rest, width))
	}
	if m.statusMsg != "" && m.statusMsg != "daemon ok" {
		rest := m.statusMsg
		if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
			rest = rest[:idx] + "…"
		}
		return lipgloss.NewStyle().Foreground(colorFly).Background(colorPaper).Width(width).Render(truncate(model+"  ·  "+rest, width))
	}
	return m.renderContextStrip()
}

func (m model) renderMetricsPanel(height int) string {
	var b strings.Builder
	b.WriteString(styleMetricsTitle.Background(colorPaper).Render("context") + "\n\n")

	b.WriteString(stylePanelLabel.Background(colorPaper).Render("tokens") + "\n")
	if used, maxTok, ok := m.metrics().TaskTokenUsage(); ok {
		if maxTok > 0 {
			b.WriteString(fmt.Sprintf("  %s / %s\n", formatTokens(used), formatTokens(maxTok)))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", formatTokens(used)))
		}
		if m.usage.InputTokens > 0 || m.usage.OutputTokens > 0 {
			b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf("  in %s · out %s\n",
				formatTokens(m.usage.InputTokens), formatTokens(m.usage.OutputTokens))))
		}
		if m.usage.CostMicros > 0 {
			b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf("  cost %dµ\n", m.usage.CostMicros)))
		}
		if m.runUsageOK && m.runUsage.ChildRunCount > 0 {
			b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf("  parent %s · children %s (%d)\n",
				formatTokens(m.runUsage.Self.TotalTokens),
				formatTokens(m.runUsage.Children.TotalTokens),
				m.runUsage.ChildRunCount)))
		}
	} else {
		b.WriteString(styleDim.Background(colorPaper).Render("  —") + "\n")
	}
	if p, ok := m.metrics().ContextPressure(); ok {
		b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf("  window %d%%", int(p*100+0.5))))
		if m.contextOK && m.taskContext.LastPromptTokens > 0 {
			b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf(" · prompt %s", formatTokens(m.taskContext.LastPromptTokens))))
		}
		if m.contextOK && m.taskContext.Compacted {
			b.WriteString(styleDim.Background(colorPaper).Render(" · compacted"))
		}
		b.WriteString("\n")
	}

	if summary := m.budgetSummary(); summary != "" {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorPaper).Render("budget") + "\n")
		b.WriteString("  " + summary + "\n")
	}
	if m.dataDir != "" {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorPaper).Render("data") + "\n")
		b.WriteString("  " + truncate(m.dataDir, 36) + "\n")
	}

	if rate, ok := m.metrics().CacheHitRate(); ok {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorPaper).Render("cache") + "\n")
		b.WriteString(fmt.Sprintf("  hit %.0f%%\n", rate*100))
		if m.usage.CacheReadTokens > 0 || m.usage.CacheWriteTokens > 0 {
			b.WriteString(styleDim.Background(colorPaper).Render(fmt.Sprintf("  read %s · write %s\n",
				formatTokens(m.usage.CacheReadTokens), formatTokens(m.usage.CacheWriteTokens))))
		}
	}
	if mcp := m.metrics().MCPStatus(); mcp.Enabled {
		b.WriteString("\n")
		b.WriteString(stylePanelLabel.Background(colorPaper).Render("MCP") + "\n")
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
		if m.isLanding() {
			m.viewport.SetYOffset(0)
		} else if m.stickBottom {
			m.pinViewportBottom()
		}
		return
	}
	m.viewportContent = content
	m.viewport.SetContent(content)
	if m.isLanding() {
		m.viewport.SetYOffset(0)
		return
	}
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

func (m model) renderChat() string {
	chat := m.viewport.View()
	chatW := m.viewport.Width()
	if chatW < 1 {
		chatW = m.innerWidth()
	}
	if ov := m.renderCompleterOverlay(chatW); ov != "" {
		chat = overlayBottom(chat, ov, chatW)
	}
	if !m.showContextPanel() {
		return chat
	}
	ctx := m.renderMetricsPanel(m.viewport.Height())
	return lipgloss.JoinHorizontal(lipgloss.Top,
		chat,
		stylePaper.Width(1).Render(" "),
		lipgloss.NewStyle().
			Foreground(colorFly).
			Background(colorPaper).
			Width(contextPanelWidth).
			MaxHeight(m.viewport.Height()).
			Render(ctx),
	)
}

func (m model) renderFloats(width int) []string {
	var parts []string
	if m.helpOpen {
		parts = append(parts, m.renderHelpOverlay(width))
	}
	if ov := renderPickerOverlay(&m, width); ov != "" {
		parts = append(parts, ov)
	}
	return parts
}

func (m model) renderCompleterOverlay(width int) string {
	if !m.completer.visible {
		return ""
	}
	c := m.completer.render(6)
	if c == "" {
		return ""
	}
	return stylePickerBox.Width(max(8, width)).Render(c)
}

func overlayBottom(base, overlay string, width int) string {
	if overlay == "" {
		return base
	}
	if width < 1 {
		width = 1
	}
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(overlay, "\n")
	row := lipgloss.NewStyle().Background(colorPaper).Width(width).MaxWidth(width)
	padded := make([]string, len(overLines))
	for i, line := range overLines {
		padded[i] = row.Render(line)
	}
	if len(baseLines) == 0 {
		return strings.Join(padded, "\n")
	}
	n := min(len(padded), len(baseLines))
	copy(baseLines[len(baseLines)-n:], padded[len(padded)-n:])
	return strings.Join(baseLines, "\n")
}

func (m *model) layout() {
	width := m.innerWidth()
	m.input.SetWidth(max(1, width))

	header := m.renderHeader()
	floats := m.renderFloats(width)
	pills := m.renderPills(width)
	editor := m.renderInputBox(width)
	status := m.renderFooter()

	var below []string
	below = append(below, floats...)
	if pills != "" {
		below = append(below, "", pills)
	}
	below = append(below, "", editor, status)
	belowH := lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, below...))
	chrome := lipgloss.Height(header) + 1 + belowH
	h := m.height - framePadY*2 - chrome
	if h < 5 {
		h = 5
	}
	w := width
	if m.showContextPanel() {
		w -= contextPanelWidth + 1
	}
	if w < 1 {
		w = 1
	}
	m.viewport.SetWidth(w)
	m.viewport.SetHeight(h)
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
