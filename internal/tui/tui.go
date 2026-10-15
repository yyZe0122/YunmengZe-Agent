// Package tui is the interactive frontend of the in-process agent.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-isatty"
	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/session"
)

type entry struct {
	role, text, name string
	folded           bool
	rendered         string
	width            int
}
type doneMsg struct {
	err    error
	status string
}
type questionMsg struct {
	question agent.Question
	reply    chan string
}
type renderMsg struct{}
type questionClosedMsg struct{ reply chan string }
type taskView struct {
	status string
	tokens providerapi.TokenCount
}
type model struct {
	questions                []questionMsg
	renderPending            bool
	tasks                    map[string]taskView
	totalTokens              providerapi.TokenCount
	engine                   *agent.Engine
	ctx                      context.Context
	runs                     *sync.WaitGroup
	session                  session.Session
	mode                     session.Mode
	modelRef                 string
	input                    textarea.Model
	viewport                 viewport.Model
	width, height            int
	entries                  []entry
	stream, thinking, status string
	busy                     bool
	newAfter                 bool
	cancel                   context.CancelFunc
	inbox                    chan string
	events                   chan tea.Msg
	question                 *questionMsg
	selected                 map[int]bool
	choice                   int
	draft                    string
	picker                   string
	choices                  []string
	choiceIDs                []string
	commands                 []string
	suggestion               int
}

func Run(ctx context.Context, e *agent.Engine, initial session.Session, mode session.Mode, ref string) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return errors.New("TUI requires an interactive terminal; use ymz run for scripts")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var runs sync.WaitGroup
	input := textarea.New()
	input.Placeholder = "输入消息 · /help · Tab 切换 plan / agent"
	input.ShowLineNumbers = false
	input.SetWidth(78)
	input.SetHeight(3)
	input.CharLimit = 0
	input.SetVirtualCursor(false)
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	input.Focus()
	m := model{engine: e, ctx: ctx, runs: &runs, session: initial, mode: mode, modelRef: ref, input: input, viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(18)), width: 80, height: 26, status: "就绪"}
	if initial.ID != "" {
		m.mode = initial.Mode
		m.modelRef = initial.Model
		if err := m.load(initial.ID); err != nil {
			return err
		}
	}
	m.commandNames()
	m.refresh(true)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	cancel()
	runs.Wait()
	return err
}
func (m model) Init() tea.Cmd { return textarea.Blink }
func (m *model) load(id string) error {
	s, err := m.engine.Store.Get(m.ctx, id)
	if err != nil {
		return err
	}
	if s.ParentID != "" {
		return errors.New("child sessions are resumed through the task tool")
	}
	records, err := m.engine.Store.Records(m.ctx, id, 0)
	if err != nil {
		return err
	}
	m.session = s
	m.mode = s.Mode
	m.modelRef = s.Model
	m.commandNames()
	m.entries = nil
	m.tasks = nil
	m.totalTokens = 0
	m.stream, m.thinking = "", ""
	names := map[string]string{}
	for _, r := range records {
		for _, c := range r.Message.ToolCalls {
			names[c.ID] = c.Name
		}
		if r.Message.Thinking != "" {
			m.entries = append(m.entries, entry{role: "thinking", text: r.Message.Thinking, folded: true})
		}
		if r.Message.Content != "" {
			m.entries = append(m.entries, entry{role: string(r.Message.Role), text: r.Message.Content, name: names[r.Message.ToolCallID], folded: r.Message.Role == "tool"})
		}
	}
	m.refresh(true)
	return nil
}
func waitEvent(events <-chan tea.Msg, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-events:
			return event
		case <-ctx.Done():
			return doneMsg{err: ctx.Err()}
		}
	}
}
func (m *model) start(text string) tea.Cmd {
	if m.session.ID == "" {
		s, err := m.engine.NewSession(m.ctx, m.mode, m.modelRef)
		if err != nil {
			m.status = err.Error()
			return nil
		}
		m.session = s
		m.modelRef = s.Model
	}
	m.entries = append(m.entries, entry{role: "user", text: text})
	m.busy = true
	m.tasks = map[string]taskView{}
	m.totalTokens = 0
	m.status = "运行中 · Enter 纠偏 · Esc 取消"
	m.stream = ""
	m.thinking = ""
	m.inbox = make(chan string, 32)
	m.events = make(chan tea.Msg, 256)
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	events, inbox, root, engine, id := m.events, m.inbox, m.ctx, m.engine, m.session.ID
	send := func(msg tea.Msg) {
		select {
		case events <- msg:
		case <-root.Done():
		}
	}
	m.runs.Add(1)
	go func() {
		defer m.runs.Done()
		_, err := engine.Run(ctx, id, text, agent.RunOptions{Inbox: inbox, Emit: func(event agent.Event) { send(event) }, Ask: func(ctx context.Context, q agent.Question) (string, error) {
			reply := make(chan string, 1)
			send(questionMsg{question: q, reply: reply})
			select {
			case answer := <-reply:
				return answer, nil
			case <-ctx.Done():
				send(questionClosedMsg{reply: reply})
				return "", ctx.Err()
			}
		}})
		send(doneMsg{err: err})
	}()
	m.refresh(true)
	return waitEvent(events, m.ctx)
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(20, msg.Width)
		m.height = max(10, msg.Height)
		m.input.SetWidth(max(1, m.width-2))
		m.refresh(false)
		return m, nil
	case renderMsg:
		m.renderPending = false
		m.refresh(m.viewport.AtBottom())
		return m, nil
	case agent.Event:
		if msg.Type == "usage" && msg.Usage != nil {
			m.totalTokens += msg.Usage.TotalTokens
		}
		if msg.TaskID != "" {
			state := m.tasks[msg.TaskID]
			switch msg.Type {
			case "task_start":
				state.status = "运行中 · " + msg.Text
			case "tool_start":
				state.status = "执行 " + msg.Tool
			case "tool_progress":
				state.status = "执行 " + msg.Tool
			case "usage":
				if msg.Usage != nil {
					state.tokens += msg.Usage.TotalTokens
				}
			case "task_end":
				state.status = msg.Text
				m.entries = append(m.entries, entry{role: "task", name: msg.TaskID[:min(8, len(msg.TaskID))], text: fmt.Sprintf("%s · tokens %d", msg.Text, state.tokens)})
			}
			m.tasks[msg.TaskID] = state
			return m, tea.Batch(waitEvent(m.events, m.ctx), m.queueRender())
		}
		switch msg.Type {
		case "delta":
			m.stream += msg.Text
		case "thinking":
			m.thinking += msg.Text
		case "assistant_end":
			if m.thinking != "" {
				m.entries = append(m.entries, entry{role: "thinking", text: m.thinking, folded: true})
				m.thinking = ""
			}
			if m.stream != "" {
				m.entries = append(m.entries, entry{role: "assistant", text: m.stream})
				m.stream = ""
			}
		case "tool_start":
			m.status = "执行 " + msg.Tool
		case "tool_progress":
			lines := strings.Split(strings.TrimSpace(msg.Text), "\n")
			m.status = msg.Tool + " · " + lines[len(lines)-1]
		case "tool_end":
			text := msg.Text
			folded := true
			if msg.Tool == "edit" || msg.Tool == "write" {
				var output struct {
					Diff string `json:"diff"`
					Path string `json:"path"`
				}
				if json.Unmarshal([]byte(text), &output) == nil && output.Diff != "" {
					text = output.Path + "\n" + output.Diff
					folded = false
				}
			}
			m.entries = append(m.entries, entry{role: "tool", text: text, name: msg.Tool, folded: folded})
		case "steer":
			m.entries = append(m.entries, entry{role: "user", text: msg.Text})
		case "usage":
			if msg.Usage != nil {
				m.status = fmt.Sprintf("运行中 · tokens %d · Esc 取消", m.totalTokens)
			}
		case "info":
			m.entries = append(m.entries, entry{role: "info", text: msg.Text})
			m.status = msg.Text
		}
		return m, tea.Batch(waitEvent(m.events, m.ctx), m.queueRender())
	case questionMsg:
		m.questions = append(m.questions, msg)
		m.nextQuestion()
		m.refresh(false)
		return m, waitEvent(m.events, m.ctx)
	case questionClosedMsg:
		if m.question != nil && m.question.reply == msg.reply {
			m.question = nil
			m.input.SetValue(m.draft)
		}
		pending := m.questions[:0]
		for _, q := range m.questions {
			if q.reply != msg.reply {
				pending = append(pending, q)
			}
		}
		m.questions = pending
		m.nextQuestion()
		m.refresh(false)
		return m, waitEvent(m.events, m.ctx)
	case doneMsg:
		m.busy = false
		if m.question != nil {
			m.input.SetValue(m.draft)
		}
		m.question = nil
		m.questions = nil
		if m.cancel != nil {
			m.cancel()
		}
		if m.thinking != "" {
			m.entries = append(m.entries, entry{role: "thinking", text: m.thinking, folded: true})
			m.thinking = ""
		}
		if m.stream != "" {
			m.entries = append(m.entries, entry{role: "assistant", text: m.stream})
			m.stream = ""
		}
		m.status = fmt.Sprintf("就绪 · tokens %d", m.totalTokens)
		if msg.status != "" {
			m.status = msg.status
		}
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		if m.newAfter {
			m.reset()
			m.newAfter = false
		}
		m.refresh(true)
		if m.inbox != nil {
			var pending []string
			for len(m.inbox) > 0 {
				pending = append(pending, <-m.inbox)
			}
			if len(pending) > 0 {
				m.input.SetValue(strings.Join(pending, "\n"))
				m.status += " · 未发送的纠偏已回填"
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			if m.busy {
				m.cancel()
				m.status = "正在取消"
				return m, nil
			}
			return m, tea.Quit
		}
		if m.question != nil {
			return m.questionKey(msg)
		}
		if m.picker != "" {
			return m.pickerKey(msg)
		}
		if suggestions := m.suggestions(); len(suggestions) > 0 {
			switch key {
			case "up", "down":
				if key == "up" {
					m.suggestion = max(0, m.suggestion-1)
				} else {
					m.suggestion = min(len(suggestions)-1, m.suggestion+1)
				}
				m.refresh(false)
				return m, nil
			case "enter":
				m.input.SetValue("/" + suggestions[min(m.suggestion, len(suggestions)-1)] + " ")
				m.input.CursorEnd()
				m.refresh(false)
				return m, nil
			}
		}
		switch key {
		case "esc":
			if m.busy {
				m.cancel()
				m.status = "正在取消"
			} else {
				m.input.Reset()
			}
			return m, nil
		case "tab", "shift+tab":
			if m.busy {
				m.status = "本轮结束或取消后可切换模式"
				return m, nil
			}
			mode := session.Plan
			if m.mode == session.Plan {
				mode = session.Agent
			}
			if m.session.ID != "" {
				if err := m.engine.Store.Configure(m.ctx, m.session.ID, mode, m.modelRef); err != nil {
					m.status = err.Error()
					return m, nil
				}
			}
			m.mode = mode
			m.session.Mode = mode
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.Reset()
			if strings.HasPrefix(text, "/") {
				handled, cmd := m.command(text)
				if handled {
					return m, cmd
				}
			}
			if m.busy {
				select {
				case m.inbox <- text:
					m.status = "纠偏已排队"
				default:
					m.status = "纠偏队列已满"
					m.input.SetValue(text)
				}
				return m, nil
			}
			cmd := m.start(text)
			return m, cmd
		case "ctrl+l":
			if !m.busy {
				m.openModels()
				m.refresh(false)
			}
			return m, nil
		case "ctrl+s":
			if !m.busy {
				m.openSessions()
				m.refresh(false)
			}
			return m, nil
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "e", "E", "c":
			if m.input.Value() == "" {
				for i := len(m.entries) - 1; i >= 0; i-- {
					if m.entries[i].role == "tool" || m.entries[i].role == "thinking" {
						if key == "e" {
							m.entries[i].folded = !m.entries[i].folded
							break
						}
						m.entries[i].folded = key == "c"
					}
				}
				m.refresh(false)
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	previous := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if previous != m.input.Value() {
		m.suggestion = 0
		m.refresh(false)
	}
	return m, cmd
}
func (m *model) reset() {
	m.session = session.Session{}
	m.entries = nil
	m.tasks = nil
	m.totalTokens = 0
	m.stream = ""
	m.thinking = ""
	m.input.Reset()
	m.picker = ""
	m.status = "新会话"
	m.commandNames()
}
func (m model) questionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	q := m.question.question
	switch msg.String() {
	case "esc":
		m.cancel()
		m.question = nil
		m.input.SetValue(m.draft)
		m.status = "正在取消"
		return m, nil
	case "up":
		m.choice = max(0, m.choice-1)
		m.refresh(false)
		return m, nil
	case "down":
		m.choice = min(max(0, len(q.Options)-1), m.choice+1)
		m.refresh(false)
		return m, nil
	case "space":
		if m.input.Value() == "" && q.Multiple && len(q.Options) > 0 {
			m.selected[m.choice] = !m.selected[m.choice]
			m.refresh(false)
			return m, nil
		}
	case "enter":
		answer := strings.TrimSpace(m.input.Value())
		if answer == "" {
			var chosen []string
			for i, option := range q.Options {
				if q.Multiple && m.selected[i] || !q.Multiple && i == m.choice {
					chosen = append(chosen, option)
				}
			}
			answer = strings.Join(chosen, "; ")
		}
		if answer == "" {
			m.status = "请选择或输入答案"
			return m, nil
		}
		m.question.reply <- answer
		m.question = nil
		m.input.SetValue(m.draft)
		m.status = "继续运行"
		m.nextQuestion()
		m.refresh(false)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) queueRender() tea.Cmd {
	if m.renderPending {
		return nil
	}
	m.renderPending = true
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return renderMsg{} })
}
func (m *model) nextQuestion() {
	if m.question != nil || len(m.questions) == 0 {
		return
	}
	q := m.questions[0]
	m.questions = m.questions[1:]
	m.question = &q
	m.choice = 0
	m.selected = map[int]bool{}
	m.draft = m.input.Value()
	m.input.Reset()
	m.status = "↑↓ 选择 · Space 多选 · Enter 回答 · 可直接输入答案"
}
