// Package contextpack estimates tokens and packs provider message views
// without mutating durable SQLite transcripts.
package contextpack

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// CharsPerToken is the default heuristic (industry ~4 chars/token).
const CharsPerToken = 4

// PerMessageOverhead accounts for role/framing tokens not in content text.
const PerMessageOverhead int64 = 6

// Anti-thrash defaults for session LLM compaction (Hermes-style).
const (
	// DefaultAntiThrashWindow is the lookback for counting durable compactions.
	DefaultAntiThrashWindow = 10 * time.Minute
	// DefaultAntiThrashMax is max LLM compact calls allowed inside the window.
	// Beyond this, session packing still runs L1–L3 + extractive, not the model.
	DefaultAntiThrashMax = 3
)

// EstimateText returns a token estimate for plain text.
// CJK runes count as ~1 token each; remaining runes use CharsPerToken (~4).
func EstimateText(text string) int64 {
	if text == "" {
		return 0
	}
	var cjk, other int64
	for _, r := range text {
		if isCJKRune(r) {
			cjk++
		} else {
			other++
		}
	}
	est := cjk
	if other > 0 {
		est += (other + CharsPerToken - 1) / CharsPerToken
	}
	if est < 1 {
		return 1
	}
	return est
}

func isCJKRune(r rune) bool {
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
		return true
	}
	// CJK punctuation / compatibility blocks commonly seen in logs and source comments.
	switch {
	case r >= 0x3000 && r <= 0x303F:
		return true
	case r >= 0xFF00 && r <= 0xFFEF:
		return true
	}
	return false
}

// EstimateMessage estimates one provider message (content + tool calls + thinking).
func EstimateMessage(msg providerapi.Message) int64 {
	n := PerMessageOverhead + EstimateText(msg.Content) + EstimateText(msg.Thinking)
	if msg.ToolCallID != "" {
		n += EstimateText(msg.ToolCallID)
	}
	for _, tc := range msg.ToolCalls {
		n += EstimateText(tc.ID) + EstimateText(tc.Name) + EstimateText(tc.Arguments) + 4
	}
	return n
}

// EstimateMessages sums message estimates.
func EstimateMessages(messages []providerapi.Message) int64 {
	var total int64
	for _, msg := range messages {
		total += EstimateMessage(msg)
	}
	return total
}

// EstimateTools estimates tool definition payload size.
func EstimateTools(tools []providerapi.ToolDefinition) int64 {
	if len(tools) == 0 {
		return 0
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		var n int64
		for _, t := range tools {
			n += EstimateText(t.Name) + EstimateText(t.Description) + int64(len(t.InputSchema))/CharsPerToken + 8
		}
		return n
	}
	return EstimateText(string(raw))
}

// ShouldCompact reports whether LLM/extractive head summary should run.
// Trigger: pressure >= 75% of usable and packing still over budget (or no budget left for tail).
func ShouldCompact(estimate, usable int64, overBudget bool) bool {
	if usable <= 0 {
		return overBudget
	}
	if overBudget {
		return true
	}
	return float64(estimate) >= DefaultPressureTrigger*float64(usable)
}

// SplitHeadTail keeps the last keepUserTurns user turns as tail; rest is head (excluding leading system).
// The cut is adjusted so assistant tool_call batches are not separated from their tool results
// (Hermes/OpenClaw tool-pair floor).
func SplitHeadTail(messages []providerapi.Message, keepUserTurns int) (head, tail []providerapi.Message) {
	if keepUserTurns < 1 {
		keepUserTurns = 2
	}
	if len(messages) == 0 {
		return nil, nil
	}
	sysEnd := 0
	for sysEnd < len(messages) && messages[sysEnd].Role == providerapi.RoleSystem {
		sysEnd++
	}
	// Find user turn starts from the end.
	var userIdx []int
	for i := sysEnd; i < len(messages); i++ {
		if messages[i].Role == providerapi.RoleUser {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= keepUserTurns {
		return nil, cloneMessages(messages)
	}
	cut := userIdx[len(userIdx)-keepUserTurns]
	cut = alignToolPairCut(messages, cut, sysEnd)
	if cut <= sysEnd {
		return nil, cloneMessages(messages)
	}
	head = cloneMessages(messages[sysEnd:cut])
	tail = cloneMessages(append(append([]providerapi.Message{}, messages[:sysEnd]...), messages[cut:]...))
	return head, tail
}

// alignToolPairCut moves cut left so we never split an assistant tool_call from its tool results.
func alignToolPairCut(messages []providerapi.Message, cut, sysEnd int) int {
	if cut <= sysEnd || cut >= len(messages) {
		return cut
	}
	// If cut lands on a tool result, walk back to the assistant that owns those calls.
	if messages[cut].Role == providerapi.RoleTool {
		for cut > sysEnd && messages[cut-1].Role == providerapi.RoleTool {
			cut--
		}
		if cut > sysEnd && messages[cut-1].Role == providerapi.RoleAssistant && len(messages[cut-1].ToolCalls) > 0 {
			cut--
		}
		return cut
	}
	// If cut is mid-batch: previous messages are tools still pending for an earlier assistant.
	// Walk back while previous is tool; if that leaves an assistant with tools immediately before tools, include it in tail.
	i := cut
	for i > sysEnd && messages[i-1].Role == providerapi.RoleTool {
		i--
	}
	if i < cut && i > sysEnd && messages[i-1].Role == providerapi.RoleAssistant && len(messages[i-1].ToolCalls) > 0 {
		return i - 1
	}
	return cut
}

// ExtractiveSummary builds a non-LLM summary from head messages (fallback).
// Lines are selected newest-first so recent paths/errors survive a tight rune budget,
// then emitted oldest-first for readable chronology.
func ExtractiveSummary(head []providerapi.Message, maxRunes int) string {
	if maxRunes < 256 {
		maxRunes = 256
	}
	const preface = "Session context summary (local extractive; prior turns compacted):\n"
	budget := maxRunes - len([]rune(preface))
	if budget < 64 {
		budget = 64
	}
	type line struct {
		text string
		idx  int
	}
	selected := make([]line, 0, len(head))
	used := 0
	for i := len(head) - 1; i >= 0; i-- {
		msg := head[i]
		text := string(msg.Role) + ": " + truncateRunes(msg.Content, 240)
		if msg.Role == providerapi.RoleAssistant && len(msg.ToolCalls) > 0 {
			names := make([]string, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				names = append(names, tc.Name)
			}
			text += " tools=" + strings.Join(names, ",")
		}
		need := len([]rune(text)) + 1
		if used+need > budget && len(selected) > 0 {
			continue
		}
		selected = append(selected, line{text: text, idx: i})
		used += need
		if used >= budget {
			break
		}
	}
	// Restore chronological order.
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	var b strings.Builder
	b.WriteString(preface)
	for _, item := range selected {
		b.WriteString(item.text)
		b.WriteByte('\n')
	}
	return truncateRunes(b.String(), maxRunes)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// UsableWindow is context capacity left for prompt after output and reserve.
// Zero contextWindow means unknown → no hard pack limit (callers should pass DefaultContextWindow).
func UsableWindow(contextWindow, maxOutput, reserve int64) int64 {
	if contextWindow <= 0 {
		return 0
	}
	if maxOutput < 0 {
		maxOutput = 0
	}
	if reserve < 0 {
		reserve = 0
	}
	if reserve == 0 {
		reserve = defaultReserve(maxOutput)
	}
	usable := contextWindow - maxOutput - reserve
	if usable < 1024 {
		usable = 1024
	}
	return usable
}

func defaultReserve(maxOutput int64) int64 {
	r := maxOutput
	if r < 1024 {
		r = 1024
	}
	if r > 8192 {
		r = 8192
	}
	return r
}
