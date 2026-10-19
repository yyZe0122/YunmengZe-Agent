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

	out := RepairToolMessages(cloneMessages(messages))
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

	if opt.Budget > 0 {
		// L2: clear older tool results if reclaim is worthwhile.
		cleared := 0
		if EstimateMessages(out) > opt.Budget {
			cleared = clearOldToolResults(out, min(opt.ToolProtectTokens, opt.Budget/3), min(opt.ToolReclaimMin, opt.Budget/4))
		}
		result.ClearedTools = cleared

		// L3: drop oldest complete turns while over budget (keep system prefix).
		for EstimateMessages(out) > opt.Budget {
			n, ok := dropOldestTurn(out)
			if !ok {
				break
			}
			out = n
			result.DroppedMessages++
		}
	}

	result.EstimateTokens = EstimateMessages(out)
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

// dropOldestTurn removes the oldest non-system turn block (user… until next user or end),
// preserving tool-call pairing within remaining suffix. Returns false if nothing droppable.
func dropOldestTurn(messages []providerapi.Message) ([]providerapi.Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	// Keep leading system messages.
	start := 0
	for start < len(messages) && (messages[start].Role == providerapi.RoleSystem || messages[start].Replay != nil && messages[start].Replay.Canonical) {
		start++
	}
	if start >= len(messages) {
		return messages, false
	}
	// Find first user after system, or first message if no user.
	turnStart := start
	if messages[turnStart].Role != providerapi.RoleUser {
		return messages, false
	}
	// End of turn = next user (exclusive) or EOF; always keep at least the last user turn.
	turnEnd := turnStart + 1
	for turnEnd < len(messages) && messages[turnEnd].Role != providerapi.RoleUser {
		turnEnd++
	}
	// Count remaining user turns after this drop.
	usersAfter := 0
	for i := turnEnd; i < len(messages); i++ {
		if messages[i].Role == providerapi.RoleUser {
			usersAfter++
		}
	}
	if usersAfter < 1 {
		// Would drop the only remaining user turn.
		return messages, false
	}
	out := append(append([]providerapi.Message{}, messages[:turnStart]...), messages[turnEnd:]...)
	return out, true
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
	head := keep / 2
	tail := keep - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail >= len(runes) {
		return content
	}
	omitted := len(runes) - head - tail
	marker = fmt.Sprintf(trimMarkerFmt, omitted)
	markerLen = utf8.RuneCountInString(marker)
	if head+markerLen+tail < maxRunes {
		extra := maxRunes - head - markerLen - tail
		head += extra
	}
	if head+markerLen+tail > maxRunes {
		overflow := head + markerLen + tail - maxRunes
		if tail > overflow {
			tail -= overflow
		} else {
			overflow -= tail
			tail = 0
			if head > overflow {
				head -= overflow
			} else {
				head = 0
			}
		}
	}
	if head+tail >= len(runes) || head < 0 || tail < 0 {
		return string(runes[:maxRunes])
	}
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}
