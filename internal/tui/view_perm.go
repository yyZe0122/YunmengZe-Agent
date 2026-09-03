package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

const (
	customAnswerLabel = "Type your own answer"
	dismissArmWindow  = 3 * time.Second
	cardMaxLines      = 14
)

func (m *model) cardOpen() bool {
	return m.list == listPermissions || m.list == listQuestions
}

func (m *model) dismissArmed() bool {
	return m.cardOpen() && !m.dismissUntil.IsZero() && time.Now().Before(m.dismissUntil)
}

func (m *model) armDismiss() {
	m.dismissUntil = time.Now().Add(dismissArmWindow)
}

func (m *model) clearDismiss() {
	m.dismissUntil = time.Time{}
}

func (m *model) resetQuestionDraft() {
	m.qItemIdx = 0
	m.qOptIdx = 0
	m.qDraft = map[string][]string{}
	m.qPicked = map[string][]string{}
	m.qCustomMode = false
	m.clearDismiss()
}

func (m *model) currentQuestion() (gatewayclient.UserQuestion, gatewayclient.UserQuestionItem, bool) {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.questions) {
		return gatewayclient.UserQuestion{}, gatewayclient.UserQuestionItem{}, false
	}
	q := m.questions[m.selectedIdx]
	if len(q.Questions) == 0 {
		return q, gatewayclient.UserQuestionItem{}, false
	}
	if m.qItemIdx < 0 {
		m.qItemIdx = 0
	}
	if m.qItemIdx >= len(q.Questions) {
		m.qItemIdx = len(q.Questions) - 1
	}
	return q, q.Questions[m.qItemIdx], true
}

func questionRowCount(item gatewayclient.UserQuestionItem) int {
	return len(item.Options) + 1
}

func (m *model) questionMoveItem(delta int) {
	q, _, ok := m.currentQuestion()
	if !ok {
		return
	}
	next := m.qItemIdx + delta
	if next < 0 || next >= len(q.Questions) {
		return
	}
	m.qItemIdx = next
	m.qOptIdx = 0
	m.qCustomMode = false
}

func optionSelected(picked []string, label string) bool {
	for _, v := range picked {
		if v == label {
			return true
		}
	}
	return false
}

func toggleLabel(picked []string, label string) []string {
	out := make([]string, 0, len(picked)+1)
	found := false
	for _, v := range picked {
		if v == label {
			found = true
			continue
		}
		out = append(out, v)
	}
	if !found {
		out = append(out, label)
	}
	return out
}

func renderQuestionCard(m *model, width int) string {
	q, item, ok := m.currentQuestion()
	if !ok {
		return styleDim.Render("No pending questions.")
	}
	inner := max(8, width-4)
	var b strings.Builder
	if m.dismissArmed() {
		b.WriteString(styleError.Render("press Esc again in 3s to dismiss") + "\n")
	}
	title := fmt.Sprintf("Questions (%d pending) · %d/%d", len(m.questions), m.qItemIdx+1, len(q.Questions))
	b.WriteString(styleTitle.Render(title) + "\n")
	if item.Header != "" {
		b.WriteString(styleMuted.Render(item.Header) + "\n")
	}
	b.WriteString(wrapBody(item.Question, inner) + "\n")
	if item.MultiSelect {
		b.WriteString(styleDim.Render("[multi] Space toggle · Enter next · ← → question") + "\n")
	} else {
		b.WriteString(styleDim.Render("1–9 / Enter · Type your own · ← → question") + "\n")
	}
	rows := questionRowCount(item)
	if m.qOptIdx < 0 {
		m.qOptIdx = 0
	}
	if m.qOptIdx >= rows {
		m.qOptIdx = rows - 1
	}
	picked := m.qPicked[item.ID]
	for i := 0; i < rows; i++ {
		marker := "  "
		if i == m.qOptIdx {
			marker = styleCompSel.Render("› ")
		}
		label := customAnswerLabel
		desc := ""
		if i < len(item.Options) {
			label = item.Options[i].Label
			desc = strings.TrimSpace(item.Options[i].Description)
		}
		check := "  "
		if item.MultiSelect && i < len(item.Options) && optionSelected(picked, label) {
			check = "* "
		}
		line := fmt.Sprintf("%s%d  %s%s", marker, i+1, check, label)
		if i == rows-1 && m.qCustomMode {
			line += styleDim.Render("  (type below · Enter submit)")
		}
		b.WriteString(line + "\n")
		if desc != "" {
			b.WriteString(styleDim.Render("     "+desc) + "\n")
		}
	}
	return finishCard(b.String(), width, m.dismissArmed())
}

func renderPermCard(m *model, width int) string {
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.permissions) {
		return styleDim.Render("No pending permissions.")
	}
	p := m.permissions[m.selectedIdx]
	inner := max(8, width-4)
	var b strings.Builder
	if m.dismissArmed() {
		b.WriteString(styleError.Render("press Esc again in 3s to dismiss") + "\n")
	}
	n := len(m.permissions)
	title := "Permission"
	if n > 1 {
		title = fmt.Sprintf("Permission (%d/%d)", m.selectedIdx+1, n)
	}
	b.WriteString(styleTitle.Render(title) + "  " + p.ToolName)
	if p.Risk != "" {
		b.WriteString("  " + styleDim.Render(p.Risk))
	}
	b.WriteByte('\n')
	if p.Path != "" {
		b.WriteString(wrapBody(p.Path, inner) + "\n")
	}
	if p.ExtraRoot {
		b.WriteString(styleMuted.Render("outside workspace — extra root") + "\n")
	}
	cmd := strings.TrimSpace(p.Command)
	if cmd != "" {
		line := cmd
		if len(p.CommandArgs) > 0 {
			line += " " + strings.Join(p.CommandArgs, " ")
		}
		b.WriteString(styleDim.Render("$ "+line) + "\n")
	}
	if p.NetworkDomain != "" {
		b.WriteString(styleDim.Render("host "+p.NetworkDomain) + "\n")
	}
	if p.SuggestedReason != "" {
		b.WriteString(styleMuted.Render(p.SuggestedReason) + "\n")
	}
	permanent := "3 permanent · remember this tool"
	if p.ExtraRoot {
		permanent = "3 permanent · write chat.workspace.allow"
	}
	labels := []string{
		"1 once · this call",
		"2 similar · this session",
		permanent,
		"4 deny",
	}
	if m.permCycleIdx < 0 {
		m.permCycleIdx = 0
	}
	if m.permCycleIdx > 3 {
		m.permCycleIdx = 3
	}
	for i, label := range labels {
		marker := "  "
		if i == m.permCycleIdx {
			marker = styleCompSel.Render("› ")
		}
		b.WriteString(marker + label + "\n")
	}
	b.WriteString(styleDim.Render("↑↓ · 1–4 / Enter · Esc Esc dismiss"))
	return finishCard(b.String(), width, m.dismissArmed())
}

func finishCard(body string, width int, armed bool) string {
	boxW := max(8, width-2)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	maxLines := cardMaxLines
	if maxLines < 6 {
		maxLines = 6
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncate(lines[maxLines-1], boxW-2) + "…"
	}
	st := stylePickerBox.Width(boxW)
	if armed {
		st = lipgloss.NewStyle().Foreground(colorBone).Background(colorSeal).Padding(0, 1).Width(boxW)
	}
	return st.Render(strings.Join(lines, "\n"))
}

func (m *model) questionConfirmOption(optionIdx int) tea.Cmd {
	q, item, ok := m.currentQuestion()
	if !ok {
		return nil
	}
	rows := questionRowCount(item)
	if optionIdx < 0 || optionIdx >= rows {
		return nil
	}
	if optionIdx == len(item.Options) {
		m.qCustomMode = true
		m.qOptIdx = optionIdx
		m.statusMsg = "type your answer · Enter submit"
		return nil
	}
	label := item.Options[optionIdx].Label
	if item.MultiSelect {
		m.qPicked[item.ID] = toggleLabel(m.qPicked[item.ID], label)
		return nil
	}
	return m.commitQuestionItem(q, item, []string{label})
}

func (m *model) questionConfirmHighlighted() tea.Cmd {
	_, item, ok := m.currentQuestion()
	if !ok {
		return nil
	}
	if item.MultiSelect {
		picked := append([]string(nil), m.qPicked[item.ID]...)
		if len(picked) == 0 {
			m.statusMsg = "select at least one option (Space) or Type your own"
			return nil
		}
		q, _, _ := m.currentQuestion()
		return m.commitQuestionItem(q, item, picked)
	}
	return m.questionConfirmOption(m.qOptIdx)
}

func (m *model) questionSubmitCustom(text string) tea.Cmd {
	q, item, ok := m.currentQuestion()
	if !ok {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	m.qCustomMode = false
	return m.commitQuestionItem(q, item, []string{text})
}

func (m *model) commitQuestionItem(q gatewayclient.UserQuestion, item gatewayclient.UserQuestionItem, labels []string) tea.Cmd {
	if m.qDraft == nil {
		m.qDraft = map[string][]string{}
	}
	m.qDraft[item.ID] = labels
	if m.qItemIdx+1 < len(q.Questions) {
		m.qItemIdx++
		m.qOptIdx = 0
		m.qCustomMode = false
		m.statusMsg = fmt.Sprintf("question %d/%d", m.qItemIdx+1, len(q.Questions))
		return nil
	}
	answers := map[string][]string{}
	for k, v := range m.qDraft {
		answers[k] = append([]string(nil), v...)
	}
	m.busy = true
	return m.answerQuestionCmd(q.ID, answers)
}

func (m *model) permConfirmHighlighted() tea.Cmd {
	if m.permInGrace() {
		m.statusMsg = "permission open · wait a moment (grace) · 1–4 decide"
		return nil
	}
	if m.selectedIdx < 0 || m.selectedIdx >= len(m.permissions) {
		return nil
	}
	decision := permCycleOrder[m.permCycleIdx%len(permCycleOrder)]
	m.busy = true
	return m.permDecideCmd(m.permissions[m.selectedIdx].ID, decision)
}

func (m model) dismissQuestionCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		_, err := m.gateway.DismissQuestion(ctx, id, "local-user")
		if err != nil {
			return commandDoneMsg{err: err}
		}
		sessionID := strings.TrimSpace(string(m.sessionID))
		if sessionID == "…" {
			sessionID = ""
		}
		remaining, _ := m.gateway.ListQuestions(ctx, sessionID, 20)
		msg := commandDoneMsg{status: "dismissed " + shortID(id), questions: remaining}
		if len(remaining) == 0 {
			msg.closeList = true
		} else {
			msg.openList = listQuestions
		}
		return msg
	}
}

func (m *model) dismissCurrentCard() tea.Cmd {
	m.clearDismiss()
	switch m.list {
	case listQuestions:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.questions) {
			m.closeList()
			return nil
		}
		m.busy = true
		return m.dismissQuestionCmd(m.questions[m.selectedIdx].ID)
	case listPermissions:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.permissions) {
			m.closeList()
			return nil
		}
		m.busy = true
		return m.permDecideCmd(m.permissions[m.selectedIdx].ID, "deny")
	default:
		m.closeList()
		return nil
	}
}
