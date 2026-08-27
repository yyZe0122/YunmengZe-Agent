package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func TestPlanBudgetOf(t *testing.T) {
	t.Parallel()

	if _, ok := planBudgetOf(nil); ok {
		t.Fatal("nil plan")
	}
	if _, ok := planBudgetOf(&gatewayclient.Plan{}); ok {
		t.Fatal("empty document")
	}
	if _, ok := planBudgetOf(&gatewayclient.Plan{Document: json.RawMessage(`{"budget":{}}`)}); ok {
		t.Fatal("zero budget")
	}
	if _, ok := planBudgetOf(&gatewayclient.Plan{Document: json.RawMessage(`not-json`)}); ok {
		t.Fatal("invalid json")
	}

	doc := json.RawMessage(`{"budget":{"max_tokens":128000,"max_cost_micros":0,"max_duration_ms":1800000}}`)
	b, ok := planBudgetOf(&gatewayclient.Plan{Document: doc})
	if !ok {
		t.Fatal("expected budget")
	}
	if b.MaxTokens != 128000 || b.MaxDurationMillis != 1800000 || b.MaxCostMicros != 0 {
		t.Fatalf("budget = %#v", b)
	}
}

func TestBudgetSummaryFromPlanDocument(t *testing.T) {
	t.Parallel()

	m := model{}
	if m.budgetSummary() != "" {
		t.Fatalf("empty plan: %q", m.budgetSummary())
	}
	m.plan = &gatewayclient.Plan{
		Document: json.RawMessage(`{"budget":{"max_tokens":128000,"max_cost_micros":50,"max_duration_ms":60000}}`),
	}
	got := m.budgetSummary()
	want := "tok=128000 cost_µ=50 dur_ms=60000"
	if got != want {
		t.Fatalf("budgetSummary = %q, want %q", got, want)
	}
}

func TestTaskTokenUsageMaxFromPlanBudget(t *testing.T) {
	t.Parallel()

	m := model{
		usageOK: true,
		usage:   gatewayclient.TaskUsage{TotalTokens: 100, InputTokens: 40, OutputTokens: 60},
		plan: &gatewayclient.Plan{
			Document: json.RawMessage(`{"budget":{"max_tokens":128000,"max_duration_ms":1}}`),
		},
	}
	used, max, ok := m.metrics().TaskTokenUsage()
	if !ok || used != 100 || max != 128000 {
		t.Fatalf("TaskTokenUsage = %d/%d ok=%v", used, max, ok)
	}
}

func TestContextWindowFromModelConfig(t *testing.T) {
	t.Parallel()
	applyTheme(themeByName(ThemeNight))

	m := model{width: 80, contextWindow: 65536, modelName: "p/m", cwd: "/tmp"}
	n, ok := m.metrics().ContextWindow()
	if !ok || n != 65536 {
		t.Fatalf("ContextWindow = %d ok=%v", n, ok)
	}
	strip := m.renderContextStrip()
	if !strings.Contains(strip, "ctx") || strings.Contains(strip, "ctx —") {
		t.Fatalf("strip should show context window: %q", strip)
	}

	m.contextWindow = 0
	if _, ok := m.metrics().ContextWindow(); ok {
		t.Fatal("expected unavailable")
	}
	strip = m.renderContextStrip()
	if !strings.Contains(strip, "ctx —") {
		t.Fatalf("strip should show unknown ctx: %q", strip)
	}
}

func TestMetricsPanelShowsCostWhenPresent(t *testing.T) {
	t.Parallel()
	applyTheme(themeByName(ThemeNight))

	m := model{
		usageOK: true,
		usage: gatewayclient.TaskUsage{
			TotalTokens: 10, InputTokens: 4, OutputTokens: 6, CostMicros: 1234,
		},
		plan: &gatewayclient.Plan{
			Document: json.RawMessage(`{"budget":{"max_tokens":1000,"max_duration_ms":1}}`),
		},
		dataDir: "/data/ymz",
	}
	panel := m.renderMetricsPanel(30)
	for _, want := range []string{"tokens", "cost 1234µ", "tok=1000", "budget"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel missing %q:\n%s", want, panel)
		}
	}
	if !strings.Contains(panel, "10 / 1k") {
		t.Fatalf("expected used/max tokens in panel:\n%s", panel)
	}
}

func TestMetricsPanelShowsCacheHitRate(t *testing.T) {
	t.Parallel()
	applyTheme(themeByName(ThemeNight))
	m := model{
		usageOK: true,
		usage: gatewayclient.TaskUsage{
			TotalTokens: 100, InputTokens: 50, OutputTokens: 10,
			CacheReadTokens: 50, CacheWriteTokens: 5,
		},
	}
	panel := m.renderMetricsPanel(40)
	if !strings.Contains(panel, "cache") || !strings.Contains(panel, "hit 50%") {
		t.Fatalf("panel missing cache hit:\n%s", panel)
	}
	if !strings.Contains(panel, "read") {
		t.Fatalf("panel missing cache read:\n%s", panel)
	}
}

func TestActivityLabelSpinnerWhenRunning(t *testing.T) {
	sid := gatewayclient.SessionID("s1")
	m := model{
		animFrame: 3,
		task:      &gatewayclient.Task{ID: "t1", State: gatewayclient.TaskStateRunning, SessionID: &sid},
	}
	if kind := m.activityKind(); kind != "running" {
		t.Fatalf("kind = %q", kind)
	}
	got := m.activityLabel()
	want := runningSpinner(3) + " running"
	if got != want {
		t.Fatalf("label = %q want %q", got, want)
	}
	m.liveThinking = "hmm"
	got = m.activityLabel()
	if got != runningSpinner(3)+" thinking" {
		t.Fatalf("thinking = %q", got)
	}
}

func TestMetricsPanelShowsMCPWhenEnabled(t *testing.T) {
	t.Parallel()
	applyTheme(themeByName(ThemeNight))
	m := model{
		mcpOK: true,
		mcpStatus: gatewayclient.MCPStatus{
			Enabled: true, Total: 2, OK: 1, Error: 1, Tools: 3,
		},
	}
	panel := m.renderMetricsPanel(40)
	if !strings.Contains(panel, "MCP") || !strings.Contains(panel, "1 ok") {
		t.Fatalf("panel missing MCP:\n%s", panel)
	}
}
