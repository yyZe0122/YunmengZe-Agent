package tui

import (
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
	"github.com/yyZe0122/yunmengze-agent/internal/modelstream"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
	"github.com/yyZe0122/yunmengze-agent/pkg/eventapi"
	"github.com/yyZe0122/yunmengze-agent/pkg/schedulerapi"
)

// execMode is the Tab posture: Plan / Agent / Auto.
// Kernel execution_mode stays agent|plan; Auto is session permission_stance.
type execMode string

const (
	modeAgent execMode = "agent"
	modePlan  execMode = "plan"
	modeAuto  execMode = "auto"
)

// listKind is a floating picker overlay kind (does not replace the timeline).
type listKind int

const (
	listNone     listKind = iota
	listSessions          // true sessions (chat containers)
	listTasks             // task list (debug / focus)
	listModels
	listJobs
	listSkills      // multi-select skill picker for next submit
	listPermissions // pending tool-call permissions (ADR-043)
	listQuestions   // pending ask_user questions (ADR-052 R4)
)

const (
	runPollInterval     = 2 * time.Second
	permPollInterval    = 2 * time.Second
	animInterval        = 200 * time.Millisecond
	streamPaintInterval = 32 * time.Millisecond
	commandTimeout      = 30 * time.Second
	historyLimit        = 50
	contextBreakWidth   = 100
	contextPanelWidth   = 22
	overlayMaxLines     = 10
	helpOverlayMax      = 14
	framePadY           = 1
	framePadX           = 2
	headerHeight        = 2
	// timeline body defaults (fold large run results).
	timelineBodyMaxLines = 12
	timelineBodyMaxChars = 2400
	// thinking: live shows tail only; finished collapses to one-line summary.
	thinkingLiveMaxLines = 5
	thinkingLiveMaxChars = 800
	thinkingFoldMaxLines = 3
	thinkingFoldMaxChars = 400
	// tool results: default folded summary.
	toolResultMaxLines = 6
	toolResultMaxChars = 1200
	// permOpenGrace is how long decision hotkeys are ignored after auto-open.
	permOpenGrace = 400 * time.Millisecond
)

type model struct {
	mode    paths.Mode
	gateway Gateway

	width  int
	height int
	theme  ThemeName
	// draftMode is the Tab-selected mode for the next task submission.
	draftMode execMode

	input     textarea.Model
	viewport  viewport.Model
	completer completer

	statusMsg string
	errMsg    string
	helpOpen  bool
	sseState  string
	modelName string
	models    []string
	cwd       string
	dataDir   string
	busy      bool
	lastEscAt time.Time

	// Floating picker (sessions/models/jobs/skills). Slash completer is separate.
	list        listKind
	selectedIdx int

	tasks       []gatewayclient.Task
	sessions    []gatewayclient.Session
	jobs        []schedulerapi.Job
	skills      []gatewayclient.Skill
	commands    []gatewayclient.ChatCommand
	permissions []gatewayclient.Permission
	questions   []gatewayclient.UserQuestion
	// selectedSkillIDs is draft selection for the next task submit (kept across turns).
	selectedSkillIDs []string
	sessionID        gatewayclient.SessionID
	task             *gatewayclient.Task
	planID           gatewayclient.PlanID
	plan             *gatewayclient.Plan
	runs             []gatewayclient.Run
	messages         []gatewayclient.TranscriptMessage
	todos            []gatewayclient.SessionTodo
	pillsExpanded    bool
	// live assistant draft from model-stream (typewriter); cleared on complete or when this turn's transcript covers it.
	liveContent   string
	liveThinking  string
	liveTools     []contentBlock // streamed tool_call lines as blocks
	liveRunID     gatewayclient.RunID
	streamDirty   bool // coalesced live paint pending
	streamPaintOn bool
	streamMD      streamingMD
	timeline      []timelineItem
	tlCache       timelineRenderCache
	expand        expandState
	// journeyRows optional memory timeline rows (C4); cleared on session clear.
	journeyRows []timelineItem
	stickBottom bool

	usage         gatewayclient.TaskUsage
	usageOK       bool
	runUsage      gatewayclient.RunUsage
	runUsageOK    bool
	taskContext   gatewayclient.TaskContext
	contextOK     bool
	mcpStatus     gatewayclient.MCPStatus
	mcpOK         bool
	contextWindow int64 // model context length from ModelConfig; 0 = unknown

	pendingPermCount int
	lastPermPoll     time.Time
	// autoOpenedPermList avoids re-opening the picker every poll while pending remains.
	autoOpenedPermList bool
	autoOpenedQList    bool
	// permGraceUntil ignores decision keys briefly after auto-open (Crush-style).
	permGraceUntil time.Time
	// permCycleIdx cycles Enter: once → similar → permanent → deny.
	permCycleIdx int

	animFrame int

	history    []string
	historyIdx int // -1 = live input

	dirty           bool
	refreshing      bool
	refreshGen      uint64
	pendingRefresh  bool
	lastRunPoll     time.Time
	viewportContent string
	sseAfter        uint64
	quitting        bool
}

type tickMsg time.Time

// refreshKind selects which gateway slices to reload.
type refreshKind int

const (
	refreshFull refreshKind = iota
	refreshTask
	refreshRuns
	refreshPlan
)

type refreshDoneMsg struct {
	gen         uint64
	kind        refreshKind
	tasks       []gatewayclient.Task
	sessions    []gatewayclient.Session
	task        *gatewayclient.Task
	plan        *gatewayclient.Plan
	runs        []gatewayclient.Run
	messages    []gatewayclient.TranscriptMessage
	usage       gatewayclient.TaskUsage
	usageOK     bool
	runUsage    gatewayclient.RunUsage
	runUsageOK  bool
	taskContext gatewayclient.TaskContext
	contextOK   bool
	todos       []gatewayclient.SessionTodo
	todosOK     bool
	err         error
}

type statusDoneMsg struct {
	health   gatewayclient.Health
	model    gatewayclient.ModelConfig
	mcp      gatewayclient.MCPStatus
	mcpOK    bool
	skills   []gatewayclient.Skill
	commands []gatewayclient.ChatCommand
	err      error
}

type commandDoneMsg struct {
	status        string
	err           error
	taskID        gatewayclient.TaskID
	planID        gatewayclient.PlanID
	sessionID     gatewayclient.SessionID
	clearTask     bool
	openList      listKind
	closeList     bool
	quit          bool
	help          bool
	toggleTheme   bool
	modelName     string
	models        []string
	contextWindow int64
	jobs          []schedulerapi.Job
	sessions      []gatewayclient.Session
	skills        []gatewayclient.Skill
	skillIDs      []string // replace selectedSkillIDs when set (toggle apply)
	submitAfter   string   // after applying skillIDs, submit this objective (skill-as-slash)
	permissions   []gatewayclient.Permission
	questions     []gatewayclient.UserQuestion
	// expandMode: "", "all", "none", "last" — applied in applyCommand for /expand
	expandMode string
	// journeyRows replaces optional memory journey overlay on the timeline (C4).
	journeyRows []timelineItem
	setJourney  bool
}

// permPollDoneMsg is a background poll of pending tool permissions.
type permPollDoneMsg struct {
	permissions []gatewayclient.Permission
	err         error
	openList    bool // true when count went 0→N and list should auto-open once
}

type questionPollDoneMsg struct {
	questions []gatewayclient.UserQuestion
	err       error
	openList  bool
}

type sseEventMsg struct {
	envelope eventapi.Envelope
}

type sseStateMsg struct {
	state string
	err   error
}

type modelStreamMsg struct {
	env modelstream.Envelope
}

func newModel(mode paths.Mode, gateway Gateway) model {
	ti := textarea.New()
	ti.Placeholder = "message or /command"
	ti.CharLimit = 4000
	ti.ShowLineNumbers = false
	ti.Prompt = "› "
	ti.SetWidth(80)
	ti.SetHeight(1)
	ti.DynamicHeight = true
	ti.MinHeight = 1
	ti.MaxHeight = 6
	ti.SetVirtualCursor(false)
	km := textarea.DefaultKeyMap()
	km.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "ctrl+j"))
	ti.KeyMap = km
	ti.Focus()
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	cwd, _ := os.Getwd()
	theme := loadTheme(mode)
	applyTheme(themeByName(theme))
	m := model{
		mode: mode, gateway: gateway, theme: theme, draftMode: modeAgent,
		input: ti, viewport: vp, sseState: "connecting", dirty: true,
		stickBottom: true, historyIdx: -1, cwd: cwd,
	}
	m.applyPlaceholder()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tickCmd(), m.loadStatusCmd(), m.scheduleRefresh(refreshFull))
}

func tickCmd() tea.Cmd {
	return tea.Tick(animInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// scheduleRefresh coalesces concurrent refreshes (Crush-style in-flight gate).
func (m *model) scheduleRefresh(kind refreshKind) tea.Cmd {
	if m.refreshing {
		m.pendingRefresh = true
		m.dirty = true
		return nil
	}
	m.refreshing = true
	m.dirty = false
	m.pendingRefresh = false
	m.refreshGen++
	gen := m.refreshGen
	return m.refreshCmd(gen, kind)
}

func (m *model) pickerOpen() bool {
	return m.list != listNone || m.completer.visible
}

func (m *model) cycleDraftMode(delta int) {
	order := []execMode{modeAgent, modePlan, modeAuto}
	idx := 0
	for i, mode := range order {
		if m.draftMode == mode {
			idx = i
			break
		}
	}
	idx = (idx + delta) % len(order)
	if idx < 0 {
		idx += len(order)
	}
	m.draftMode = order[idx]
	m.applyPlaceholder()
}

func (m model) submitExecutionMode() string {
	if m.draftMode == modePlan {
		return gatewayclient.ExecutionModePlan
	}
	return gatewayclient.ExecutionModeAgent
}

func (m model) submitPermissionStance() string {
	switch m.draftMode {
	case modeAuto:
		return "auto"
	case modePlan:
		return "plan"
	default:
		return "agent"
	}
}

func applyPermissionStance(m *model, stance string) {
	if m == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(stance)) {
	case "auto":
		m.draftMode = modeAuto
	case "plan":
		m.draftMode = modePlan
	default:
		m.draftMode = modeAgent
	}
	m.applyPlaceholder()
}

func (m *model) applyPlaceholder() {
	m.applyInputStyles()
	switch m.draftMode {
	case modePlan:
		m.input.Placeholder = "plan mode · read-only analysis (no edits)"
	case modeAuto:
		m.input.Placeholder = "auto mode · this session pre-grants tests/git"
	default:
		m.input.Placeholder = "agent mode · build · /perm for tests/git"
	}
}

func (m *model) applyInputStyles() {
	s := m.input.Styles()
	core := lipgloss.NewStyle().Foreground(colorBone).Background(colorPaper)
	s.Focused.Base = core
	s.Blurred.Base = core
	s.Focused.Placeholder = styleMuted.Background(colorPaper)
	s.Blurred.Placeholder = styleMuted.Background(colorPaper)
	if strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/") {
		s.Focused.Text = styleKeyword.Background(colorPaper)
		s.Blurred.Text = styleKeyword.Background(colorPaper)
		s.Focused.Prompt = styleKeyword.Background(colorPaper)
		s.Blurred.Prompt = styleKeyword.Background(colorPaper)
	} else {
		s.Focused.Text = styleInput
		s.Blurred.Text = styleInput
		s.Focused.Prompt = styleInput
		s.Blurred.Prompt = styleInput
	}
	m.input.SetStyles(s)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if n < 1 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return ansi.TruncateWc(s, n, "…")
}
