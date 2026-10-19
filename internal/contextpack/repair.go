package contextpack

import (
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/pkg/providerapi"
)

// RepairToolMessages fixes orphan tool protocol edges for provider views:
//   - drops tool results with no matching prior assistant tool_call id
//   - injects synthetic error tool results for calls missing a result before the
//     next user/assistant boundary (Crush-style orphan repair)
//
// Non-destructive to durable history: callers pass a clone or pack view only.
func RepairToolMessages(messages []providerapi.Message) []providerapi.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]providerapi.Message, 0, len(messages)+4)
	pending := make(map[string]providerapi.ToolCall)
	order := make([]string, 0)

	flushMissing := func() {
		for _, id := range order {
			tc, ok := pending[id]
			if !ok {
				continue
			}
			name := strings.TrimSpace(tc.Name)
			if name == "" {
				name = "unknown"
			}
			out = append(out, providerapi.Message{
				Role:       providerapi.RoleTool,
				ToolCallID: id,
				Content:    fmt.Sprintf(`{"error":"orphan_tool_call","tool":%q,"hint":"No tool result was recorded; synthetic error injected for provider protocol integrity."}`, name),
			})
			delete(pending, id)
		}
		order = order[:0]
	}

	for _, msg := range messages {
		switch msg.Role {
		case providerapi.RoleAssistant:
			if len(msg.ToolCalls) == 0 {
				flushMissing()
				out = append(out, msg)
				continue
			}
			// New assistant tool batch: prior pending calls never got results.
			flushMissing()
			cloned := msg
			cloned.ToolCalls = append([]providerapi.ToolCall(nil), msg.ToolCalls...)
			out = append(out, cloned)
			for _, tc := range msg.ToolCalls {
				id := strings.TrimSpace(tc.ID)
				if id == "" {
					continue
				}
				pending[id] = tc
				order = append(order, id)
			}
		case providerapi.RoleTool:
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" {
				continue
			}
			if _, ok := pending[id]; !ok {
				// Orphan result — drop from provider view.
				continue
			}
			delete(pending, id)
			// Keep order compact.
			for i, oid := range order {
				if oid == id {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
			out = append(out, msg)
		case providerapi.RoleUser, providerapi.RoleSystem:
			flushMissing()
			out = append(out, msg)
		default:
			out = append(out, msg)
		}
	}
	flushMissing()
	return out
}
