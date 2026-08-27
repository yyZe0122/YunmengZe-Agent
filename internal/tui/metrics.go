package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

// planBudget matches approval.PlanBudget JSON on plan documents
// (max_duration_ms — not PromptBudget's max_duration_millis).
type planBudget struct {
	MaxTokens         int64 `json:"max_tokens"`
	MaxCostMicros     int64 `json:"max_cost_micros"`
	MaxDurationMillis int64 `json:"max_duration_ms"`
}

func planBudgetOf(plan *gatewayclient.Plan) (planBudget, bool) {
	if plan == nil || len(plan.Document) == 0 {
		return planBudget{}, false
	}
	var doc struct {
		Budget planBudget `json:"budget"`
	}
	if err := json.Unmarshal(plan.Document, &doc); err != nil {
		return planBudget{}, false
	}
	b := doc.Budget
	if b.MaxTokens == 0 && b.MaxCostMicros == 0 && b.MaxDurationMillis == 0 {
		return planBudget{}, false
	}
	return b, true
}

// MCPStatus is the future MCP adapter summary for the Metrics panel.
// Enabled is false until Tool Broker exposes MCP status.
type MCPStatus struct {
	Enabled bool
	Total   int
	OK      int
	Error   int
	Detail  string
}

// SessionMetrics is the TUI chrome surface for usage / model indicators.
// Implementations may return ok=false when the underlying service is missing.
// Cache/MCP panels render only when data is present (never permanent "—").
type SessionMetrics interface {
	// TaskTokenUsage returns used tokens and optional budget max for the focused task.
	TaskTokenUsage() (used, max int64, ok bool)
	// ContextWindow is the model context length when known (from ModelConfig).
	ContextWindow() (n int, ok bool)
	// ContextPressure is last prompt fill vs usable window [0,1+] when known.
	ContextPressure() (pressure float64, ok bool)
	// CacheHitRate is provider cache hit ratio in [0,1] when known.
	CacheHitRate() (rate float64, ok bool)
	// MCPStatus summarizes MCP servers when Tool Broker exposes adapters.
	MCPStatus() MCPStatus
}

// modelMetrics is the production SessionMetrics backed by model fields.
type modelMetrics struct {
	m *model
}

func (s modelMetrics) TaskTokenUsage() (used, max int64, ok bool) {
	if s.m == nil || !s.m.usageOK {
		return 0, 0, false
	}
	used = s.m.usage.TotalTokens
	if b, okBudget := planBudgetOf(s.m.plan); okBudget && b.MaxTokens > 0 {
		max = b.MaxTokens
	}
	return used, max, true
}

func (s modelMetrics) ContextWindow() (int, bool) {
	if s.m == nil || s.m.contextWindow <= 0 {
		return 0, false
	}
	return int(s.m.contextWindow), true
}

func (s modelMetrics) ContextPressure() (float64, bool) {
	if s.m == nil || !s.m.contextOK {
		return 0, false
	}
	p := s.m.taskContext.Pressure
	if p <= 0 && s.m.taskContext.UsableTokens > 0 {
		fill := s.m.taskContext.LastPromptTokens
		if fill <= 0 {
			fill = s.m.taskContext.EstimateTokens
		}
		if fill > 0 {
			p = float64(fill) / float64(s.m.taskContext.UsableTokens)
		}
	}
	if p <= 0 && s.m.taskContext.Source == "none" {
		return 0, false
	}
	return p, true
}

func (s modelMetrics) CacheHitRate() (float64, bool) {
	if s.m == nil || !s.m.usageOK {
		return 0, false
	}
	return s.m.usage.CacheHitRate()
}

func (s modelMetrics) MCPStatus() MCPStatus {
	if s.m == nil || !s.m.mcpOK {
		return MCPStatus{Enabled: false}
	}
	st := s.m.mcpStatus
	if !st.Enabled {
		return MCPStatus{Enabled: false}
	}
	return MCPStatus{
		Enabled: true,
		Total:   st.Total,
		OK:      st.OK,
		Error:   st.Error,
		Detail:  fmt.Sprintf("%d tools", st.Tools),
	}
}

func (m *model) metrics() SessionMetrics {
	return modelMetrics{m: m}
}

type runActivity int

const (
	activityIdle runActivity = iota
	activityWaiting
	activityActive
)

func (m model) runActivity() runActivity {
	if m.task == nil {
		return activityIdle
	}
	switch m.task.State {
	case gatewayclient.TaskStatePaused:
		return activityWaiting
	case gatewayclient.TaskStateRunning:
		return activityActive
	case gatewayclient.TaskStateCompleted, gatewayclient.TaskStateFailed, gatewayclient.TaskStateCancelled:
		// fall through — check live runs
	}
	for _, run := range m.runs {
		switch run.State {
		case gatewayclient.RunStateCompleted, gatewayclient.RunStateFailed, gatewayclient.RunStateCancelled:
		default:
			return activityActive
		}
	}
	switch m.task.State {
	case gatewayclient.TaskStateCompleted, gatewayclient.TaskStateFailed, gatewayclient.TaskStateCancelled:
		return activityIdle
	default:
		return activityIdle
	}
}

func (m model) activityKind() string {
	if m.pendingPermCount > 0 {
		return "waiting permission"
	}
	switch m.runActivity() {
	case activityActive:
		if strings.TrimSpace(m.liveThinking) != "" && strings.TrimSpace(m.liveContent) == "" {
			return "thinking"
		}
		if len(m.liveTools) > 0 && strings.TrimSpace(m.liveContent) == "" {
			return "tool"
		}
		if strings.TrimSpace(m.liveContent) != "" || strings.TrimSpace(m.liveThinking) != "" {
			return "writing"
		}
		return "running"
	case activityWaiting:
		if m.task != nil && m.task.State == gatewayclient.TaskStatePaused {
			return "paused"
		}
		return "waiting"
	default:
		return "idle"
	}
}

func (m model) activityLabel() string {
	kind := m.activityKind()
	if m.runActivity() == activityActive && m.pendingPermCount == 0 {
		return runningSpinner(m.animFrame) + " " + kind
	}
	return kind
}

func (m model) activityElapsed() time.Duration {
	if m.runActivity() != activityActive {
		return 0
	}
	var earliest time.Time
	for _, run := range m.runs {
		switch run.State {
		case gatewayclient.RunStateCompleted, gatewayclient.RunStateFailed, gatewayclient.RunStateCancelled:
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, run.StartedAt)
		if err != nil {
			continue
		}
		if earliest.IsZero() || started.Before(earliest) {
			earliest = started
		}
	}
	if earliest.IsZero() && m.task != nil {
		if t, err := time.Parse(time.RFC3339Nano, m.task.UpdatedAt); err == nil {
			earliest = t
		}
	}
	if earliest.IsZero() {
		return 0
	}
	return time.Since(earliest).Round(time.Second)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%02ds", mins, secs)
	}
	hours := mins / 60
	mins = mins % 60
	return fmt.Sprintf("%dh%02dm", hours, mins)
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		if n%1000 == 0 {
			return fmt.Sprintf("%dk", n/1000)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
}
