package contextpack

import (
	"strings"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

const (
	// DefaultContextWindow is packing/TUI window when the model window is still unset after catalog fill.
	DefaultContextWindow int64 = 1_048_576
	// DefaultMaxOutputTokens is the packing output term when model maxTokens is unset.
	DefaultMaxOutputTokens int64 = 128_000
	// HistoryBudgetShare is the fraction of usable window reserved for Tail packing.
	HistoryBudgetShare = 60
	// MinHistoryBudget floors a positive usable window's tail budget.
	MinHistoryBudget int64 = 2_048
)

// ContextView is the single provider-facing assembly (ADR-051).
// Messages() = Prefix + Summary + Tail + Ephemeral.
type ContextView struct {
	Prefix    []providerapi.Message
	Summary   []providerapi.Message
	Tail      []providerapi.Message
	Ephemeral []providerapi.Message
	Packed    PackResult
	Compacted bool
}

// Messages concatenates the four segments in contract order.
func (v ContextView) Messages() []providerapi.Message {
	n := len(v.Prefix) + len(v.Summary) + len(v.Tail) + len(v.Ephemeral)
	if n == 0 {
		return nil
	}
	out := make([]providerapi.Message, 0, n)
	out = append(out, v.Prefix...)
	out = append(out, v.Summary...)
	out = append(out, v.Tail...)
	out = append(out, v.Ephemeral...)
	return out
}

// BuildInput is the raw segments for one Build. History must not include the current user.
type BuildInput struct {
	Prefix    []providerapi.Message
	History   []providerapi.Message
	Ephemeral []providerapi.Message
	Summary   string
}

// BuildOptions controls Tail packing. Budget 0 means no L2/L3.
type BuildOptions struct {
	Budget             int64
	Model              string
	MaxToolResultRunes int
	ToolProtectTokens  int64
	ToolReclaimMin     int64
}

// Build packs History only (L1–L3). Prefix is never passed to dropOldestTurn.
func Build(in BuildInput, opt BuildOptions) ContextView {
	prefix := cloneMessages(in.Prefix)
	ephemeral := cloneMessages(in.Ephemeral)
	var summary []providerapi.Message
	if text := strings.TrimSpace(in.Summary); text != "" {
		if !strings.HasPrefix(text, "[Prior") {
			text = "[Prior session context — compacted]\n" + text
		}
		summary = []providerapi.Message{{Role: providerapi.RoleSystem, Content: text}}
	}
	packed := Pack(in.History, PackOptions{
		Budget:             opt.Budget,
		Model:              opt.Model,
		MaxToolResultRunes: opt.MaxToolResultRunes,
		ToolProtectTokens:  opt.ToolProtectTokens,
		ToolReclaimMin:     opt.ToolReclaimMin,
	})
	compacted := len(summary) > 0 || packed.DroppedMessages > 0 || packed.ClearedTools > 0
	return ContextView{
		Prefix:    prefix,
		Summary:   summary,
		Tail:      packed.Messages,
		Ephemeral: ephemeral,
		Packed:    packed,
		Compacted: compacted,
	}
}

// HistoryBudget returns the tail token budget from a usable window.
// Never exceeds usable (small windows used to floor at 2048 > usable 1024).
func HistoryBudget(usable int64) int64 {
	if usable <= 0 {
		return 0
	}
	budget := usable * HistoryBudgetShare / 100
	if budget < MinHistoryBudget {
		if usable < MinHistoryBudget {
			return usable
		}
		return MinHistoryBudget
	}
	if budget > usable {
		return usable
	}
	return budget
}

// ClampMaxOutput returns the packing output term. Zero/negative → 128k; configured values are kept.
func ClampMaxOutput(maxOutput int64) int64 {
	if maxOutput <= 0 {
		return DefaultMaxOutputTokens
	}
	return maxOutput
}

// SplitLeadingSystem splits a leading RoleSystem run (Prefix / summary) from the rest.
func SplitLeadingSystem(messages []providerapi.Message) (prefix, rest []providerapi.Message) {
	i := 0
	for i < len(messages) && messages[i].Role == providerapi.RoleSystem {
		i++
	}
	if i == 0 {
		return nil, cloneMessages(messages)
	}
	return cloneMessages(messages[:i]), cloneMessages(messages[i:])
}

// TodoSystemPrefix marks the QE session-todo block in Ephemeral (not Prefix).
const TodoSystemPrefix = "Session todos:"

// IsTodoSystem reports whether msg is the injected session-todo system block.
func IsTodoSystem(msg providerapi.Message) bool {
	return msg.Role == providerapi.RoleSystem && strings.HasPrefix(msg.Content, TodoSystemPrefix)
}

// SplitCodingEphemeral pulls todo system blocks and the last user turn (plus
// anything after it) into Ephemeral so mid-turn rebuild cannot summarize them.
func SplitCodingEphemeral(rest []providerapi.Message) (history, ephemeral []providerapi.Message) {
	userIdx := LastUserIndex(rest)
	end := userIdx
	if end < 0 {
		end = len(rest)
	}
	var todos []providerapi.Message
	for i := 0; i < end; i++ {
		if IsTodoSystem(rest[i]) {
			todos = append(todos, rest[i])
			continue
		}
		history = append(history, rest[i])
	}
	if userIdx >= 0 {
		ephemeral = append(todos, rest[userIdx:]...)
	} else {
		ephemeral = todos
	}
	return history, ephemeral
}

// LastUserIndex returns the last RoleUser index, or -1.
func LastUserIndex(messages []providerapi.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == providerapi.RoleUser {
			return i
		}
	}
	return -1
}

// TrimToolBodies applies L1 trim to tool (and plain assistant) bodies from start onward.
func TrimToolBodies(messages []providerapi.Message, start, maxRunes int) int {
	if maxRunes <= 0 {
		maxRunes = 32 * 1024
	}
	if start < 0 {
		start = 0
	}
	trimmed := 0
	for i := start; i < len(messages); i++ {
		switch messages[i].Role {
		case providerapi.RoleTool:
			before := messages[i].Content
			messages[i].Content = trimRunes(messages[i].Content, maxRunes)
			if messages[i].Content != before {
				trimmed++
			}
		case providerapi.RoleAssistant:
			if len(messages[i].ToolCalls) == 0 {
				before := messages[i].Content
				messages[i].Content = trimRunes(messages[i].Content, maxRunes)
				if messages[i].Content != before {
					trimmed++
				}
			}
		}
	}
	return trimmed
}

// ClearOldToolResults is the L2 pass for incremental mid-turn packing.
func ClearOldToolResults(messages []providerapi.Message, protect, reclaimMin int64) int {
	if protect <= 0 {
		protect = DefaultToolProtectTokens
	}
	if reclaimMin <= 0 {
		reclaimMin = DefaultToolReclaimMin
	}
	return clearOldToolResults(messages, protect, reclaimMin)
}
