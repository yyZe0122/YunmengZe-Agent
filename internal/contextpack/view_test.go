package contextpack

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestBuildOrderAndPrefixStable(t *testing.T) {
	prefix := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: "sys"},
		{Role: providerapi.RoleSystem, Content: "AGENTS.md"},
		{Role: providerapi.RoleSystem, Content: "skills"},
		{Role: providerapi.RoleSystem, Content: "frozen memory"},
	}
	history := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: "old path=/tmp/a.go error=boom"},
		{Role: providerapi.RoleAssistant, Content: "old reply"},
		{Role: providerapi.RoleUser, Content: "mid"},
		{Role: providerapi.RoleAssistant, Content: "mid reply"},
		{Role: providerapi.RoleUser, Content: "recent path=/tmp/b.go"},
		{Role: providerapi.RoleAssistant, Content: "recent"},
	}
	ephemeral := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: "current"},
	}
	view := Build(BuildInput{
		Prefix:    prefix,
		History:   history,
		Ephemeral: ephemeral,
		Summary:   "prior work on /tmp/a.go",
	}, BuildOptions{Budget: 50_000})
	msgs := view.Messages()
	if len(msgs) < 6 {
		t.Fatalf("messages=%d", len(msgs))
	}
	for i, want := range []string{"sys", "AGENTS.md", "skills", "frozen memory"} {
		if msgs[i].Role != providerapi.RoleSystem || msgs[i].Content != want {
			t.Fatalf("prefix[%d]=%+v", i, msgs[i])
		}
	}
	if msgs[4].Role != providerapi.RoleSystem || !strings.Contains(msgs[4].Content, "prior work") {
		t.Fatalf("summary=%+v", msgs[4])
	}
	if msgs[len(msgs)-1].Content != "current" {
		t.Fatalf("ephemeral last=%+v", msgs[len(msgs)-1])
	}
	if !view.Compacted {
		t.Fatal("summary should mark Compacted")
	}
}

func TestBuildL3DoesNotDropPrefix(t *testing.T) {
	prefix := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: "sys-keep"},
		{Role: providerapi.RoleSystem, Content: "AGENTS-keep"},
	}
	big := strings.Repeat("T", 8_000)
	var history []providerapi.Message
	for i := 0; i < 8; i++ {
		history = append(history,
			providerapi.Message{Role: providerapi.RoleUser, Content: "u"},
			providerapi.Message{Role: providerapi.RoleAssistant, Content: big},
		)
	}
	view := Build(BuildInput{
		Prefix:    prefix,
		History:   history,
		Ephemeral: []providerapi.Message{{Role: providerapi.RoleUser, Content: "now"}},
	}, BuildOptions{Budget: 800})
	msgs := view.Messages()
	if len(msgs) < 3 {
		t.Fatalf("too few messages: %+v", msgs)
	}
	if msgs[0].Content != "sys-keep" || msgs[1].Content != "AGENTS-keep" {
		t.Fatalf("prefix dropped: %+v", msgs[:2])
	}
	if msgs[len(msgs)-1].Content != "now" {
		t.Fatal("current user dropped")
	}
}

func TestExtractiveSummaryNewestFirstKeepsRecentPaths(t *testing.T) {
	head := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: strings.Repeat("old ", 80) + "path=/old/stale.go"},
		{Role: providerapi.RoleAssistant, Content: strings.Repeat("x", 80)},
		{Role: providerapi.RoleUser, Content: "recent path=/src/main.go error=compile failed"},
		{Role: providerapi.RoleAssistant, Content: "fixed compile"},
	}
	sum := ExtractiveSummary(head, 280)
	if !strings.Contains(sum, "/src/main.go") {
		t.Fatalf("missing recent path: %q", sum)
	}
	if !strings.Contains(sum, "compile") {
		t.Fatalf("missing error: %q", sum)
	}
}

func TestHistoryBudgetAndClamp(t *testing.T) {
	if HistoryBudget(0) != 0 {
		t.Fatal("zero usable")
	}
	if got := HistoryBudget(10_000); got != 6_000 {
		t.Fatalf("budget=%d", got)
	}
	if got := HistoryBudget(1_000); got != 1_000 {
		t.Fatalf("small window must not exceed usable: %d", got)
	}
	if got := HistoryBudget(3_000); got != MinHistoryBudget {
		t.Fatalf("floor=%d", got)
	}
	if ClampMaxOutput(0) != DefaultMaxOutputTokens {
		t.Fatal("unset → 128k")
	}
	if ClampMaxOutput(128_000) != 128_000 || ClampMaxOutput(384_000) != 384_000 {
		t.Fatal("keep configured maxTokens")
	}
	if ClampMaxOutput(2048) != 2048 {
		t.Fatal("keep small configured maxTokens")
	}
}

func TestSplitCodingEphemeralKeepsTodos(t *testing.T) {
	rest := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: "old"},
		{Role: providerapi.RoleAssistant, Content: "ok"},
		{Role: providerapi.RoleSystem, Content: TodoSystemPrefix + "\n- [in_progress] patch"},
		{Role: providerapi.RoleUser, Content: "now"},
		{Role: providerapi.RoleAssistant, Content: "working"},
	}
	history, ephemeral := SplitCodingEphemeral(rest)
	if len(history) != 2 || history[0].Content != "old" {
		t.Fatalf("history=%+v", history)
	}
	if len(ephemeral) != 3 {
		t.Fatalf("ephemeral=%+v", ephemeral)
	}
	if !IsTodoSystem(ephemeral[0]) {
		t.Fatalf("todo not first ephemeral: %+v", ephemeral[0])
	}
	if ephemeral[1].Content != "now" {
		t.Fatalf("current user=%+v", ephemeral[1])
	}
}

func TestExtractiveSummaryRuneBudgetCJK(t *testing.T) {
	head := []providerapi.Message{
		{Role: providerapi.RoleUser, Content: strings.Repeat("旧文件路径 ", 40) + "path=/old/stale.go"},
		{Role: providerapi.RoleAssistant, Content: strings.Repeat("长", 80)},
		{Role: providerapi.RoleUser, Content: "最近 path=/src/主.go error=编译失败"},
	}
	sum := ExtractiveSummary(head, 220)
	if !strings.Contains(sum, "/src/主.go") {
		t.Fatalf("missing recent CJK path: %q", sum)
	}
}
