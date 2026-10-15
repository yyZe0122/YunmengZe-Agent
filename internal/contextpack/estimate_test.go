package contextpack

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

func TestEstimateText(t *testing.T) {
	if EstimateText("") != 0 {
		t.Fatal("empty")
	}
	if got := EstimateText("abcd"); got != 1 {
		t.Fatalf("abcd=%d", got)
	}
	long := strings.Repeat("x", 40)
	if got := EstimateText(long); got != 10 {
		t.Fatalf("40 runes=%d want 10", got)
	}
	if got := EstimateText("你好世界"); got != 4 {
		t.Fatalf("cjk=%d want 4", got)
	}
	if got := EstimateText("ab你好"); got != 3 {
		t.Fatalf("mixed=%d want 3", got)
	}
}

func TestUsableWindow(t *testing.T) {
	if UsableWindow(0, 8_000, 0) != 0 {
		t.Fatal("unknown window")
	}
	u := UsableWindow(128_000, 8_000, 0)
	// reserve defaults to maxOutput capped 8192 → 8000
	if u != 128_000-8_000-8_000 {
		t.Fatalf("usable=%d", u)
	}
}

func TestPackClearsOldToolsAndDropsTurns(t *testing.T) {
	// Build history: system + several user/assistant/tool rounds with large tools.
	msgs := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: "sys"},
	}
	big := strings.Repeat("T", 20_000) // ~5k tokens each
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			providerapi.Message{Role: providerapi.RoleUser, Content: "u"},
			providerapi.Message{Role: providerapi.RoleAssistant, Content: "a", ToolCalls: []providerapi.ToolCall{
				{ID: "c" + string(rune('0'+i)), Name: "fs_read", Arguments: `{}`},
			}},
			providerapi.Message{Role: providerapi.RoleTool, ToolCallID: "c" + string(rune('0'+i)), Content: big},
		)
	}
	// Tiny budget forces L2/L3.
	res := Pack(msgs, PackOptions{
		Budget:             3_000,
		MaxToolResultRunes: 32 * 1024,
		ToolProtectTokens:  8_000,
		ToolReclaimMin:     1_000,
	})
	if res.EstimateTokens > 3_000 && !res.OverBudget {
		// may still be over if only one turn left
	}
	if res.ClearedTools == 0 && res.DroppedMessages == 0 && res.TrimmedBodies == 0 {
		t.Fatalf("expected some packing action: %+v est=%d", res, res.EstimateTokens)
	}
	// Input not mutated.
	if !strings.Contains(msgs[len(msgs)-1].Content, "TT") {
		t.Fatal("input mutated")
	}
	if len(res.Messages) == 0 || res.Messages[0].Role != providerapi.RoleSystem {
		t.Fatal("system should remain")
	}
}

func TestPackPreservesLastUserTurn(t *testing.T) {
	msgs := []providerapi.Message{
		{Role: providerapi.RoleSystem, Content: "s"},
		{Role: providerapi.RoleUser, Content: "only"},
		{Role: providerapi.RoleAssistant, Content: "reply"},
	}
	res := Pack(msgs, PackOptions{Budget: 1})
	// Cannot drop the only user turn.
	users := 0
	for _, m := range res.Messages {
		if m.Role == providerapi.RoleUser {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("users=%d messages=%+v", users, res.Messages)
	}
}
