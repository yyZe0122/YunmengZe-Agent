package tui

import (
	"fmt"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

// buildChatTimeline prefers transcript messages (OpenCode/Crush style).
// Falls back to the workflow shell when no messages are loaded yet.
func buildChatTimeline(
	messages []gatewayclient.TranscriptMessage,
	task *gatewayclient.Task,
	plan *gatewayclient.Plan,
	runs []gatewayclient.Run,
) []timelineItem {
	if len(messages) > 0 {
		toolNames := toolNameByCallID(messages)
		items := make([]timelineItem, 0, len(messages)+4)
		for i, msg := range messages {
			items = append(items, transcriptToItem(msg, toolNames, i))
		}
		// Status footer when task is mid-flight.
		if task != nil {
			switch task.State {
			case gatewayclient.TaskStateRunning:
				items = append(items, timelineItem{
					Kind: tlSystem, At: task.UpdatedAt, Title: "running", State: task.State,
				})
			case gatewayclient.TaskStateCompleted:
				items = append(items, timelineItem{
					Kind: tlDone, At: task.UpdatedAt, Title: "done · idle", State: task.State,
				})
			case gatewayclient.TaskStateFailed:
				items = append(items, timelineItem{
					Kind: tlError, At: task.UpdatedAt, Title: "failed", State: task.State,
				})
			case gatewayclient.TaskStateCancelled:
				items = append(items, timelineItem{
					Kind: tlSystem, At: task.UpdatedAt, Title: "cancelled", State: task.State,
				})
			}
		}
		return items
	}
	return buildTimeline(task, plan, runs)
}

// runningStatusTitle returns the mid-flight status line (permission-aware).
func runningStatusTitle(task *gatewayclient.Task, pendingPerms int) string {
	if task == nil {
		return ""
	}
	if task.State != gatewayclient.TaskStateRunning {
		return string(task.State)
	}
	if pendingPerms > 0 {
		return fmt.Sprintf("waiting permission (%d) · 1–4 or /perm", pendingPerms)
	}
	return "running"
}

// patchRunningStatus updates the last system "running" row title when present.
func patchRunningStatus(items []timelineItem, title string) []timelineItem {
	if title == "" || len(items) == 0 {
		return items
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind == tlSystem && (items[i].State == gatewayclient.TaskStateRunning ||
			strings.HasPrefix(items[i].Title, "running") ||
			strings.HasPrefix(items[i].Title, "waiting permission")) {
			items[i].Title = title
			break
		}
	}
	return items
}

func upsertLiveDraft(items []timelineItem, thinking, content string, liveTools []contentBlock) []timelineItem {
	think := strings.TrimSpace(thinking)
	content = strings.TrimRight(content, "\n")
	if think == "" && content == "" && len(liveTools) == 0 {
		return dropLiveDraft(items)
	}
	blocks := make([]contentBlock, 0, 2+len(liveTools))
	if think != "" {
		blocks = append(blocks, contentBlock{
			Kind: blockThinking, Text: think, Key: "live-thinking", Live: true,
		})
	}
	blocks = append(blocks, liveTools...)
	if content != "" {
		blocks = append(blocks, contentBlock{
			Kind: blockReply, Text: content, Key: "live-reply", Live: true,
		})
	}
	live := timelineItem{
		Kind: tlRun, Title: "assistant…", State: "streaming", Blocks: blocks, Key: "live",
	}
	if n := len(items); n > 0 && items[n-1].Key == "live" {
		items[n-1] = live
		return items
	}
	return append(items, live)
}

func transcriptToItem(msg gatewayclient.TranscriptMessage, toolNames map[string]string, index int) timelineItem {
	switch strings.ToLower(msg.Role) {
	case "user":
		return timelineItem{
			Kind: tlUser, At: msg.CreatedAt, Title: "you", Body: msg.Content,
			Key: fmt.Sprintf("msg:%d:user", index),
		}
	case "assistant":
		blocks := make([]contentBlock, 0, 2+len(msg.ToolCalls))
		baseKey := fmt.Sprintf("msg:%d", index)
		if think := strings.TrimSpace(msg.Thinking); think != "" {
			blocks = append(blocks, contentBlock{
				Kind: blockThinking, Text: think, Key: baseKey + ":thinking",
			})
		}
		if msg.Content != "" {
			blocks = append(blocks, contentBlock{
				Kind: blockReply, Text: msg.Content,
			})
		}
		for _, tc := range msg.ToolCalls {
			preview := toolCallPreview(tc.Name, tc.Arguments)
			if preview == "" {
				raw := strings.TrimSpace(tc.Arguments)
				if raw != "" && raw != "{}" {
					preview = truncate(raw, 100)
				}
			}
			blocks = append(blocks, contentBlock{
				Kind:     blockToolCall,
				Text:     preview,
				ToolName: strings.TrimSpace(tc.Name),
				ToolID:   tc.ID,
				Key:      baseKey + ":tc:" + tc.ID,
			})
		}
		title := "assistant"
		if len(msg.ToolCalls) > 0 && strings.TrimSpace(msg.Content) == "" && strings.TrimSpace(msg.Thinking) == "" {
			title = "assistant · tools"
		}
		if len(blocks) == 0 {
			return timelineItem{
				Kind: tlRun, At: msg.CreatedAt, Title: title, Body: "(empty assistant message)",
				Key: baseKey,
			}
		}
		return timelineItem{
			Kind: tlRun, At: msg.CreatedAt, Title: title, Blocks: blocks, Key: baseKey,
		}
	case "tool":
		name := ""
		if toolNames != nil {
			name = toolNames[msg.ToolCallID]
		}
		title := formatToolResultTitle(msg.ToolCallID, name)
		key := fmt.Sprintf("msg:%d:tool:%s", index, msg.ToolCallID)
		return timelineItem{
			Kind: tlTool, At: msg.CreatedAt, Title: title, Key: key,
			Blocks: []contentBlock{{
				Kind:     blockToolResult,
				Text:     msg.Content,
				ToolName: name,
				ToolID:   msg.ToolCallID,
				Key:      key,
			}},
		}
	default:
		return timelineItem{
			Kind: tlSystem, At: msg.CreatedAt, Title: msg.Role, Body: msg.Content,
			Key: fmt.Sprintf("msg:%d:sys", index),
		}
	}
}
