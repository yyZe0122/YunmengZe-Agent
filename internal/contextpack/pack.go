package contextpack

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// Default tool-result protection window (estimated tokens of newest tool bodies).
const (
	DefaultToolProtectTokens int64 = 40_000
	DefaultToolReclaimMin    int64 = 20_000
	toolClearPlaceholderFmt        = "[cleared tool result: id=%s; full output retained in local history]"
)

// PackOptions controls deterministic provider-view packing.
type PackOptions struct {
	// Budget is the max estimated tokens for the packed message list (0 = no budget).
	Budget int64
	// MaxToolResultRunes caps single tool/assistant content (L1). 0 uses 32*1024.
	MaxToolResultRunes int
	// ToolProtectTokens keeps newest tool result tokens before L2 clear. 0 = default.
	ToolProtectTokens int64
	// ToolReclaimMin requires reclaimable tokens before L2 runs. 0 = default.
	ToolReclaimMin int64
}

// PackResult is the packed provider view plus diagnostics.
type PackResult struct {
	Messages        []providerapi.Message
	EstimateTokens  int64
	TrimmedBodies   int
	ClearedTools    int
	DroppedMessages int
	OverBudget      bool
}

// Pack builds a non-destructive provider view: L1 body trim → L2 old tool clear → L3 drop oldest turns.
func Pack(messages []providerapi.Message, opt PackOptions) PackResult {
	if opt.MaxToolResultRunes <= 0 {
		opt.MaxToolResultRunes = 32 * 1024
	}
	if opt.ToolProtectTokens <= 0 {
		opt.ToolProtectTokens = DefaultToolProtectTokens
	}
	if opt.ToolReclaimMin <= 0 {
		opt.ToolReclaimMin = DefaultToolReclaimMin
	}

	out := RepairToolMessages(messages)
	var result PackResult

	// L1: per-message body trim.
	for i := range out {
		switch out[i].Role {
		case providerapi.RoleTool:
			before := out[i].Content
			out[i].Content = trimRunes(out[i].Content, opt.MaxToolResultRunes)
			if out[i].Content != before {
				result.TrimmedBodies++
			}
		case providerapi.RoleAssistant:
			if len(out[i].ToolCalls) == 0 && out[i].Replay == nil {
				before := out[i].Content
				out[i].Content = trimRunes(out[i].Content, opt.MaxToolResultRunes)
				if out[i].Content != before {
					result.TrimmedBodies++
				}
			}
		}
	}

	result.EstimateTokens = EstimateMessages(out)
	if opt.Budget > 0 {
		// L2: clear older tool results if reclaim is worthwhile.
		cleared := 0
		if result.EstimateTokens > opt.Budget {
			cleared = clearOldToolResults(out, min(opt.ToolProtectTokens, opt.Budget/3), min(opt.ToolReclaimMin, opt.Budget/4))
		}
		result.ClearedTools = cleared
		if cleared > 0 {
			result.EstimateTokens = EstimateMessages(out)
		}

		// L3: drop oldest complete turns while over budget (keep system prefix).
		if result.EstimateTokens > opt.Budget {
			out, result.DroppedMessages, result.EstimateTokens = dropOldestTurns(out, opt.Budget, result.EstimateTokens)
		}
	}

	result.OverBudget = opt.Budget > 0 && result.EstimateTokens > opt.Budget
	result.Messages = out
	return result
}

func cloneMessages(in []providerapi.Message) []providerapi.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]providerapi.Message, len(in))
	for i, msg := range in {
		out[i] = msg
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = append([]providerapi.ToolCall(nil), msg.ToolCalls...)
		}
	}
	return out
}

func clearOldToolResults(messages []providerapi.Message, protect, reclaimMin int64) int {
	// Walk newest→oldest tool messages; protect newest protect tokens; clear older if reclaim > reclaimMin.
	type idxEst struct {
		i           int
		est         int64
		reclaim     int64
		placeholder string
	}
	var tools []idxEst
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != providerapi.RoleTool {
			continue
		}
		id := strings.TrimSpace(messages[i].ToolCallID)
		if id == "" {
			id = "?"
		}
		cleared := messages[i]
		cleared.Content = fmt.Sprintf(toolClearPlaceholderFmt, id)
		cleared.Images = nil
		est := EstimateMessage(messages[i])
		tools = append(tools, idxEst{i: i, est: est, reclaim: max(0, est-EstimateMessage(cleared)), placeholder: cleared.Content})
	}
	var kept int64
	var reclaim int64
	clearFrom := -1
	for n, t := range tools {
		if kept < protect {
			kept += t.est
			continue
		}
		if t.reclaim > 4 {
			reclaim += t.reclaim
		}
		if clearFrom < 0 {
			clearFrom = n
		}
	}
	if reclaim < reclaimMin || clearFrom < 0 {
		return 0
	}
	cleared := 0
	for n := clearFrom; n < len(tools); n++ {
		t := tools[n]
		if t.reclaim <= 4 {
			continue
		}
		messages[t.i].Content = t.placeholder
		messages[t.i].Images = nil
		cleared++
	}
	return cleared
}

// dropOldestTurns removes complete turns in one pass, keeping the prefix and last user turn.
// messages belongs to the packed view, so its backing array can be compacted in place.
func dropOldestTurns(messages []providerapi.Message, budget, tokens int64) ([]providerapi.Message, int, int64) {
	start := 0
	for start < len(messages) && (messages[start].Role == providerapi.RoleSystem || messages[start].Replay != nil && messages[start].Replay.Canonical) {
		start++
	}
	cut, dropped := start, 0
	for tokens > budget && cut < len(messages) && messages[cut].Role == providerapi.RoleUser {
		end := cut + 1
		for end < len(messages) && messages[end].Role != providerapi.RoleUser {
			end++
		}
		if end == len(messages) {
			break
		}
		tokens -= EstimateMessages(messages[cut:end])
		cut = end
		dropped++
	}
	if dropped > 0 {
		end := start + copy(messages[start:], messages[cut:])
		clear(messages[end:])
		messages = messages[:end]
	}
	return messages, dropped, tokens
}

const trimMarkerFmt = "\n...[trimmed %d runes for provider context]...\n"

func trimRunes(content string, maxRunes int) string {
	if maxRunes < 16 {
		maxRunes = 16
	}
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}
	marker := fmt.Sprintf(trimMarkerFmt, len(runes))
	markerLen := utf8.RuneCountInString(marker)
	if markerLen >= maxRunes {
		return string(runes[:maxRunes])
	}
	keep := maxRunes - markerLen
	head := (keep + 1) / 2
	tail := keep - head
	// Reserving digits for the original length also bounds the shorter omitted count.
	marker = fmt.Sprintf(trimMarkerFmt, len(runes)-keep)
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}
