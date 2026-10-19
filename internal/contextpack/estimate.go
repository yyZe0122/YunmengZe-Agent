// Package contextpack estimates tokens and packs provider message views
// without mutating durable SQLite transcripts.
package contextpack

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// CharsPerToken is the default heuristic (industry ~4 chars/token).
const CharsPerToken = 4

// PerMessageOverhead accounts for role/framing tokens not in content text.
const PerMessageOverhead int64 = 6

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
	// Native state includes opaque signatures; count conservatively without duplicating visible text.
	if msg.Replay != nil {
		n = max(n, PerMessageOverhead+EstimateText(string(msg.Replay.Data)))
	}
	// Image token cost varies by model/resolution; this is a packing reserve, not billing.
	n += int64(len(msg.Images)) * 4096
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
	selected := make([]string, 0, len(head))
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
		selected = append(selected, text)
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
		b.WriteString(item)
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

// SplitTokenTail permits compaction inside a single long user turn. Cuts occur
// only between complete assistant/tool batches; the newest batch is retained.
func SplitTokenTail(messages []providerapi.Message, tailBudget int64) (head, tail []providerapi.Message) {
	if len(messages) < 3 {
		return nil, cloneMessages(messages)
	}
	var size int64
	cut := len(messages)
	for cut > 0 && size < tailBudget {
		cut--
		size += EstimateMessage(messages[cut])
	}
	if cut == 0 {
		return nil, cloneMessages(messages)
	}
	// A cut before a tool result must include its whole originating batch.
	if messages[cut].Role == providerapi.RoleTool {
		for cut > 0 && messages[cut].Role == providerapi.RoleTool {
			cut--
		}
	}
	if cut <= 0 {
		return nil, cloneMessages(messages)
	}
	return cloneMessages(messages[:cut]), cloneMessages(messages[cut:])
}
