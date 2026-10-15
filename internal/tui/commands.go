package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
	"sort"
	"strings"
)

const help = `/new 新会话 · /sessions 或 /resume 切换会话
/model [provider/model] 选择模型 · /mode plan|agent
/skills 查看技能 · /<skill-id> [参数] 加载技能
/compact [重点] 压缩上下文 · /memory 查看记忆及来源
/memory forget <id> 遗忘 · /memory edit <id> <偏好> 纠正
/refresh-memory 刷新记忆注入 · /status · /help · /quit
Tab 切换 plan/agent · Ctrl+L 模型 · Ctrl+S 会话
Enter 发送或运行中纠偏 · Shift+Enter 换行 · Esc 取消
PgUp/PgDn 滚动 · 编辑器为空时 e/E/c 折叠工具和思考
`

func (m *model) command(text string) (bool, tea.Cmd) {
	name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	args = strings.TrimSpace(args)
	switch name {
	case "q", "quit", "exit":
		if m.busy {
			m.cancel()
		}
		return true, tea.Quit
	case "new":
		if m.busy {
			m.newAfter = true
			m.cancel()
		} else {
			m.reset()
			m.refresh(true)
		}
		return true, nil
	case "help":
		m.entries = append(m.entries, entry{role: "info", text: help})
		m.refresh(true)
		return true, nil
	}
	if m.busy {
		switch name {
		case "model", "mode", "sessions", "resume", "compact", "skills", "memory", "refresh-memory", "status":
			m.status = "请先结束或取消当前运行"
			return true, nil
		}
		return false, nil
	}
	switch name {
	case "sessions", "resume":
		if args == "" {
			m.openSessions()
		} else if err := m.load(args); err != nil {
			m.status = err.Error()
		}
	case "model":
		if args == "" {
			m.openModels()
		} else if m.session.ID != "" {
			if err := m.engine.SetModel(m.ctx, m.session, args); err != nil {
				m.status = err.Error()
			} else {
				m.modelRef = args
				m.session.Model = args
			}
		} else {
			m.modelRef = args
		}
	case "mode":
		mode, err := session.ParseMode(args)
		if err != nil {
			m.status = err.Error()
			break
		}
		if m.session.ID != "" {
			if err = m.engine.Store.Configure(m.ctx, m.session.ID, mode, m.modelRef); err != nil {
				m.status = err.Error()
				break
			}
		}
		m.mode = mode
		m.session.Mode = mode
	case "skills":
		var lines []string
		workspace := m.engine.Workspace
		if m.session.Workspace != "" {
			workspace = m.session.Workspace
		}
		for _, sk := range m.engine.Skills(workspace).Skills() {
			lines = append(lines, "/"+sk.ID+" · "+sk.Description)
		}
		if len(lines) == 0 {
			lines = append(lines, "没有发现技能：添加 <config>/skills/<id>/SKILL.md 或 .yunmengze/skills/<id>/SKILL.md")
		}
		m.entries = append(m.entries, entry{role: "info", text: strings.Join(lines, "\n")})
	case "compact":
		if m.session.ID == "" {
			m.status = "没有当前会话"
			break
		}
		m.status = "正在压缩 · Esc 取消"
		m.busy = true
		m.inbox = nil
		m.events = make(chan tea.Msg, 1)
		ctx, cancel := context.WithCancel(m.ctx)
		m.cancel = cancel
		events, engine, id, root := m.events, m.engine, m.session.ID, m.ctx
		m.runs.Add(1)
		go func() {
			defer m.runs.Done()
			err := engine.Compact(ctx, id, args)
			select {
			case events <- doneMsg{err: err, status: "压缩完成"}:
			case <-root.Done():
			}
		}()
		return true, waitEvent(events, root)
	case "memory":
		if m.engine.Memory == nil {
			m.status = "记忆已禁用"
			break
		}
		action, rest, _ := strings.Cut(args, " ")
		if action == "forget" || action == "edit" {
			if m.mode == session.Plan {
				m.status = "plan 模式不能修改记忆"
				break
			}
			var err error
			if action == "forget" {
				err = m.engine.Memory.Forget(m.ctx, strings.TrimSpace(rest))
			} else {
				id, content, _ := strings.Cut(rest, " ")
				err = m.engine.Memory.Correct(m.ctx, id, content)
			}
			if err != nil {
				m.status = err.Error()
			} else {
				m.status = "记忆已更新，下次运行生效"
			}
			break
		}
		entries, err := m.engine.Memory.List(m.ctx, m.session.ID, true, 30)
		if err != nil {
			m.status = err.Error()
			break
		}
		var lines []string
		for _, e := range entries {
			line := fmt.Sprintf("%s [%s] %s", e.ID, e.Kind, e.Content)
			for _, ev := range e.Evidence {
				line += fmt.Sprintf("\n  来源消息 %d: %s", ev.MessageID, ev.Quote)
			}
			lines = append(lines, line)
		}
		m.entries = append(m.entries, entry{role: "info", text: strings.Join(lines, "\n")})
	case "refresh-memory":
		if m.engine.Memory != nil {
			m.engine.Memory.InvalidateSnapshot(m.session.ID)
			m.status = "下次运行重新加载记忆"
		}
	case "status":
		m.entries = append(m.entries, entry{role: "info", text: fmt.Sprintf("mode: %s\nmodel: %s\nsession: %s\nworkspace: %s", m.mode, m.modelRef, m.session.ID, m.session.Workspace)})
	default:
		return false, nil
	}
	m.refresh(true)
	return true, nil
}

func (m *model) commandNames() {
	m.commands = []string{"new", "sessions", "resume", "model", "mode", "skills", "compact", "memory", "refresh-memory", "status", "help", "quit"}
	if m.engine != nil {
		for name := range m.engine.Config.Commands {
			m.commands = append(m.commands, name)
		}
		workspace := m.engine.Workspace
		if m.session.Workspace != "" {
			workspace = m.session.Workspace
		}
		for _, sk := range m.engine.Skills(workspace).Skills() {
			m.commands = append(m.commands, sk.ID)
		}
	}
	sort.Strings(m.commands)
}

func (m model) suggestions() []string {
	input := m.input.Value()
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t\n") {
		return nil
	}
	prefix := strings.TrimPrefix(input, "/")
	var matches []string
	for _, name := range m.commands {
		if name == prefix {
			return nil
		}
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	return matches
}
func (m *model) openModels() {
	m.picker = "model"
	m.choice = 0
	m.choices = nil
	m.choiceIDs = nil
	for _, ref := range m.engine.Models {
		m.choices = append(m.choices, ref.ID+" "+ref.Name)
		m.choiceIDs = append(m.choiceIDs, ref.ID)
	}
}
func (m *model) openSessions() {
	items, err := m.engine.Store.List(m.ctx, m.engine.Workspace)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.picker = "session"
	m.choice = 0
	m.choices = nil
	m.choiceIDs = nil
	for _, s := range items {
		m.choices = append(m.choices, s.ID[:min(8, len(s.ID))]+" · "+s.Title)
		m.choiceIDs = append(m.choiceIDs, s.ID)
	}
}
func (m model) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.picker = ""
	case "up":
		m.choice = max(0, m.choice-1)
	case "down":
		m.choice = min(max(0, len(m.choices)-1), m.choice+1)
	case "enter":
		if len(m.choiceIDs) > 0 {
			id := m.choiceIDs[m.choice]
			if m.picker == "session" {
				if err := m.load(id); err != nil {
					m.status = err.Error()
				}
			} else {
				if m.session.ID == "" {
					m.modelRef = id
				} else if err := m.engine.SetModel(m.ctx, m.session, id); err != nil {
					m.status = err.Error()
				} else {
					m.modelRef = id
					m.session.Model = id
				}
			}
		}
		m.picker = ""
	}
	m.refresh(false)
	return m, nil
}
