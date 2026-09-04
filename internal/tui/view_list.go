package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

func (m *model) inListMode() bool {
	return m.list != listNone
}

func (m *model) listLen() int {
	switch m.list {
	case listModels:
		return len(m.models)
	case listJobs:
		return len(m.jobs)
	case listSessions:
		return len(m.sessions)
	case listTasks:
		return len(m.tasks)
	case listSkills:
		return len(m.skills)
	case listPermissions:
		return len(m.permissions)
	case listQuestions:
		return len(m.questions)
	default:
		return 0
	}
}

func (m *model) listTitle() string {
	switch m.list {
	case listModels:
		return "Models"
	case listJobs:
		return "Scheduled jobs"
	case listSessions:
		return "Sessions"
	case listTasks:
		return "Tasks"
	case listSkills:
		n := len(m.selectedSkillIDs)
		if n == 0 {
			return "Skills"
		}
		return fmt.Sprintf("Skills (%d selected)", n)
	case listPermissions:
		n := len(m.permissions)
		if n == 0 {
			return "Permissions"
		}
		return fmt.Sprintf("Permissions (%d pending)", n)
	case listQuestions:
		n := len(m.questions)
		if n == 0 {
			return "Questions"
		}
		return fmt.Sprintf("Questions (%d pending)", n)
	default:
		return "Picker"
	}
}

func (m *model) listHint() string {
	switch m.list {
	case listModels:
		return "↑↓ select · Enter this session · /model main global · Esc"
	case listJobs:
		return "↑↓ · Enter details · Esc · /cron <every> <obj> create"
	case listSessions:
		return "↑↓ select · Enter open · Esc · Shift+PgUp/Dn cycle"
	case listTasks:
		return "↑↓ select · Enter focus task · Esc close"
	case listSkills:
		return "↑↓ · Enter toggle · Esc done (applies to next submit)"
	case listPermissions:
		return "↑↓ · 1 once · 2 similar · 3 permanent · 4 deny · Enter · Esc Esc dismiss"
	case listQuestions:
		return "↑↓ option · ← → question · 1–9 · Space · Enter · Type your own · Esc Esc dismiss"
	default:
		return "↑↓ · Enter · Esc"
	}
}

func (m *model) listLine(i int) string {
	switch m.list {
	case listModels:
		if i < 0 || i >= len(m.models) {
			return ""
		}
		name := m.models[i]
		mark := "  "
		if name == m.effectiveModel(m.modelName) {
			mark = "* "
		}
		return mark + name
	case listJobs:
		if i < 0 || i >= len(m.jobs) {
			return ""
		}
		return formatJobLine(m.jobs[i])
	case listSessions:
		if i < 0 || i >= len(m.sessions) {
			return ""
		}
		s := m.sessions[i]
		title := s.Title
		if title == "" {
			title = string(s.ID)
		}
		mark := "  "
		if s.ID == m.sessionID {
			mark = "* "
		}
		return fmt.Sprintf("%s%s  %s  %s",
			mark,
			shortID(string(s.ID)),
			stateBadge(s.LatestState),
			styleDim.Render(truncate(title, 48)),
		)
	case listSkills:
		if i < 0 || i >= len(m.skills) {
			return ""
		}
		sk := m.skills[i]
		mark := "  "
		if skillSelected(m.selectedSkillIDs, sk.ID) {
			mark = "* "
		}
		label := sk.Name
		if label == "" {
			label = sk.ID
		}
		src := sk.Source
		if src == "" {
			src = "?"
		}
		flags := src
		if sk.Draft {
			flags += " draft"
		}
		if sk.ArchivedAt != "" {
			flags += " archived"
		}
		return fmt.Sprintf("%s%s  [%s]  %s",
			mark,
			sk.ID,
			flags,
			styleDim.Render(truncate(label, 40)),
		)
	case listPermissions:
		if i < 0 || i >= len(m.permissions) {
			return ""
		}
		p := m.permissions[i]
		path := p.Path
		if path == "" {
			path = "—"
		}
		hint := ""
		if p.SuggestedReason != "" {
			hint = " · " + p.SuggestedReason
		}
		return fmt.Sprintf("%s  %s  %s  %s%s",
			shortID(p.ID),
			p.ToolName,
			styleDim.Render(truncate(path, 36)),
			styleDim.Render(p.Risk),
			styleDim.Render(hint),
		)
	case listQuestions:
		if i < 0 || i >= len(m.questions) {
			return ""
		}
		q := m.questions[i]
		head := q.ID
		if len(q.Questions) > 0 {
			label := q.Questions[0].Question
			if q.Questions[0].Header != "" {
				label = q.Questions[0].Header + ": " + label
			}
			head = truncate(label, 56)
		}
		return fmt.Sprintf("%s  %s", shortID(q.ID), head)
	default:
		if i < 0 || i >= len(m.tasks) {
			return ""
		}
		task := m.tasks[i]
		return fmt.Sprintf("%s  %s  %s",
			shortID(string(task.ID)),
			stateBadge(task.State),
			styleDim.Render(truncate(task.Title, 48)),
		)
	}
}

func skillSelected(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}

func toggleSkillID(ids []string, id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ids
	}
	for i, s := range ids {
		if s == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return append(ids, id)
}

func formatJobLine(job schedulerapi.Job) string {
	name := job.Name
	if name == "" {
		name = "(unnamed)"
	}
	next := job.NextRunAt
	if next == "" {
		next = "—"
	}
	mode := job.ExecutionMode
	if mode == "" {
		mode = "agent"
	}
	pin := job.ModelRef
	if pin == "" {
		pin = "—"
	}
	return fmt.Sprintf("%s  %s  %s  %s  %s  next %s",
		shortID(job.ID),
		stateBadge(job.Status),
		mode,
		truncate(pin, 18),
		truncate(name, 20),
		styleDim.Render(truncate(next, 24)),
	)
}

// renderPickerOverlay draws the floating list above the input (not in viewport).
func renderPickerOverlay(m *model, width int) string {
	if m.list == listNone {
		return ""
	}
	if m.list == listPermissions {
		if m.listLen() == 0 {
			return stylePickerBox.Width(max(8, width-2)).Render(styleTitle.Render("Permissions") + "\n" + styleDim.Render("No pending permissions. (Agent mode + ungranted high-risk tools)"))
		}
		return renderPermCard(m, width)
	}
	if m.list == listQuestions {
		if m.listLen() == 0 {
			return stylePickerBox.Width(max(8, width-2)).Render(styleTitle.Render("Questions") + "\n" + styleDim.Render("No pending questions."))
		}
		return renderQuestionCard(m, width)
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(m.listTitle()) + "  ")
	b.WriteString(styleDim.Render(m.listHint()) + "\n")
	n := m.listLen()
	if n == 0 {
		empty := "No items."
		switch m.list {
		case listModels:
			empty = "No models. Use /model provider/model."
		case listJobs:
			empty = "No jobs. /cron 15m <objective> on a focused session."
		case listSessions:
			empty = "No sessions yet. Type a message to start."
		case listSkills:
			empty = "No skills found. Add <id>/SKILL.md under config or .yunmengze/skills."
		case listPermissions:
			empty = "No pending permissions. (Agent mode + ungranted high-risk tools)"
		case listQuestions:
			empty = "No pending questions."
		default:
			empty = "No tasks yet."
		}
		b.WriteString(styleDim.Render(empty))
	} else {
		maxLines := overlayMaxLines
		start := 0
		if m.selectedIdx >= maxLines {
			start = m.selectedIdx - maxLines + 1
		}
		end := start + maxLines
		if end > n {
			end = n
		}
		for i := start; i < end; i++ {
			marker := "  "
			if i == m.selectedIdx {
				marker = styleCompSel.Render("› ")
			}
			line := m.listLine(i)
			// single-line for overlay compactness
			if idx := strings.IndexByte(line, '\n'); idx >= 0 {
				line = line[:idx]
			}
			b.WriteString(marker + line)
			if i < end-1 {
				b.WriteByte('\n')
			}
		}
	}
	boxW := max(8, width-2)
	return stylePickerBox.Width(boxW).Render(b.String())
}

func (m *model) openList(kind listKind) {
	m.list = kind
	m.selectedIdx = 0
	m.helpOpen = false
	switch kind {
	case listModels:
		current := m.effectiveModel(m.modelName)
		for i, name := range m.models {
			if name == current {
				m.selectedIdx = i
				break
			}
		}
	case listPermissions:
		m.permCycleIdx = 0
		m.clearDismiss()
		// Caller sets permGraceUntil when auto-opening; manual /perm has no grace.
	case listQuestions:
		m.resetQuestionDraft()
	case listSessions:
		// Keep current task/timeline mounted; picker floats above.
	case listSkills:
		// Multi-select; selection lives in selectedSkillIDs.
	}
}

func (m *model) openPermissionsWithGrace() {
	m.openList(listPermissions)
	m.permGraceUntil = time.Now().Add(permOpenGrace)
	m.autoOpenedPermList = true
}

func (m *model) closeList() {
	m.list = listNone
	m.selectedIdx = 0
	m.permCycleIdx = 0
	m.permGraceUntil = time.Time{}
	m.resetQuestionDraft()
}

func (m *model) permInGrace() bool {
	return m.list == listPermissions && !m.permGraceUntil.IsZero() && time.Now().Before(m.permGraceUntil)
}

// permHotkeyDecision maps 1–4 / o s p d to four-tier decisions (ADR-043/046).
func permHotkeyDecision(key string) (decision string, ok bool) {
	switch key {
	case "1", "o":
		return "allow_once", true
	case "2", "s":
		return "allow_similar", true
	case "3", "p":
		return "allow_permanent", true
	case "4", "d":
		return "deny", true
	default:
		return "", false
	}
}

var permCycleOrder = []string{"allow_once", "allow_similar", "allow_permanent", "deny"}

func (m *model) listEnter() tea.Cmd {
	switch m.list {
	case listModels:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.models) {
			return nil
		}
		name := m.models[m.selectedIdx]
		m.busy = true
		return m.modelCommandCmd(name)
	case listJobs:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.jobs) {
			return nil
		}
		job := m.jobs[m.selectedIdx]
		m.statusMsg = formatJobDetail(job)
		m.errMsg = ""
		return nil
	case listSessions:
		cmd := m.focusSessionAt(m.selectedIdx)
		m.closeList()
		return cmd
	case listTasks:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.tasks) {
			return nil
		}
		t := m.tasks[m.selectedIdx]
		m.task = &t
		if t.SessionID != nil {
			m.sessionID = *t.SessionID
		}
		m.closeList()
		m.planID = ""
		m.plan = nil
		m.runs = nil
		m.messages = nil
		m.viewportContent = ""
		m.stickBottom = true
		return m.scheduleRefresh(refreshFull)
	case listSkills:
		if m.selectedIdx < 0 || m.selectedIdx >= len(m.skills) {
			return nil
		}
		sk := m.skills[m.selectedIdx]
		m.selectedSkillIDs = toggleSkillID(m.selectedSkillIDs, sk.ID)
		n := len(m.selectedSkillIDs)
		if n == 0 {
			m.statusMsg = "no skills selected for next submit"
		} else {
			m.statusMsg = fmt.Sprintf("%d skill(s) for next submit: %s", n, strings.Join(m.selectedSkillIDs, ", "))
		}
		m.errMsg = ""
		return nil
	case listPermissions:
		return m.permConfirmHighlighted()
	case listQuestions:
		return m.questionConfirmHighlighted()
	default:
		return nil
	}
}

func (m *model) focusSessionAt(i int) tea.Cmd {
	if i < 0 || i >= len(m.sessions) {
		return nil
	}
	s := m.sessions[i]
	m.sessionID = s.ID
	m.sessionModel = strings.TrimSpace(s.PreferredModel)
	applyPermissionStance(m, s.PermissionStance)
	if s.LatestTaskID != nil {
		m.task = &gatewayclient.Task{ID: *s.LatestTaskID}
	} else {
		m.task = nil
	}
	m.planID = ""
	m.plan = nil
	m.runs = nil
	m.messages = nil
	m.viewportContent = ""
	m.stickBottom = true
	m.statusMsg = fmt.Sprintf("session %s · %d/%d", shortID(string(s.ID)), i+1, len(m.sessions))
	return m.scheduleRefresh(refreshFull)
}

// cycleSession moves among m.sessions (updated_at DESC: 0 = newest).
// delta +1 = older (Shift+PgUp), -1 = newer (Shift+PgDn). Wraps.
func (m *model) cycleSession(delta int) tea.Cmd {
	if len(m.sessions) == 0 {
		m.statusMsg = "no sessions"
		return nil
	}
	if m.helpOpen {
		m.helpOpen = false
	}
	if m.completer.visible {
		m.completer.dismiss()
	}
	if m.list != listNone {
		m.closeList()
	}
	cur := -1
	if m.sessionID != "" && m.sessionID != "…" {
		for i, s := range m.sessions {
			if s.ID == m.sessionID {
				cur = i
				break
			}
		}
	}
	n := len(m.sessions)
	var next int
	if cur < 0 {
		if delta < 0 {
			next = 0
		} else {
			next = n - 1
		}
	} else {
		next = (cur + delta) % n
		if next < 0 {
			next += n
		}
	}
	m.layout()
	return m.focusSessionAt(next)
}

func formatJobDetail(job schedulerapi.Job) string {
	name := job.Name
	if name == "" {
		name = "(unnamed)"
	}
	mode := job.ExecutionMode
	if mode == "" {
		mode = "agent"
	}
	return fmt.Sprintf("job %s  %s  status=%s  mode=%s  interval=%ds  next=%s",
		shortID(job.ID), name, job.Status, mode, job.IntervalSeconds, orDash(job.NextRunAt))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
