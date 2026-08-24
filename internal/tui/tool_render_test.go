package tui

import (
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

func TestFormatToolCallLineFSWrite(t *testing.T) {
	line := formatToolCallLine(gatewayclient.TranscriptToolCall{
		Name:      "fs_write",
		Arguments: `{"path":"/home/u/proj/src/main.go","content":"..."}`,
	})
	if !strings.Contains(line, "fs_write") || !strings.Contains(line, "main.go") {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, "content") {
		t.Fatalf("should not dump full args: %q", line)
	}
}

func TestFormatToolCallLineProcess(t *testing.T) {
	line := formatToolCallLine(gatewayclient.TranscriptToolCall{
		Name:      "process_exec",
		Arguments: `{"command":"go","args":["test","./..."]}`,
	})
	if !strings.Contains(line, "process_exec") || !strings.Contains(line, "go") {
		t.Fatalf("line = %q", line)
	}
}

func TestFormatToolCallLineTask(t *testing.T) {
	line := formatToolCallLine(gatewayclient.TranscriptToolCall{
		Name:      "task",
		Arguments: `{"prompt":"summarize the module boundaries"}`,
	})
	if !strings.Contains(line, "task") || !strings.Contains(line, "summarize") {
		t.Fatalf("line = %q", line)
	}
}

func TestPaintProcessToolResult(t *testing.T) {
	bl := contentBlock{
		Kind:     blockToolResult,
		ToolName: "process_exec",
		ToolID:   "c1",
		Text:     `{"command":"go","arguments":["test","./..."],"exit_code":1,"stdout":"FAIL pkg"}`,
	}
	got := renderToolResultBlock(bl, false, 72)
	if !strings.Contains(got, "$") || !strings.Contains(got, "go test") || !strings.Contains(got, "exit 1") {
		t.Fatalf("process card = %q", got)
	}
}

func TestPaintProcessToolResultCommandOnly(t *testing.T) {
	bl := contentBlock{
		Kind:     blockToolResult,
		ToolName: "process_shell",
		Text:     `{"command":"go test ./internal/tui","exit_code":0,"stdout":"ok"}`,
	}
	got := renderToolResultBlock(bl, false, 72)
	if !strings.Contains(got, "go test ./internal/tui") || !strings.Contains(got, "exit 0") {
		t.Fatalf("process card = %q", got)
	}
}

func TestFormatToolResultTitle(t *testing.T) {
	if got := formatToolResultTitle("call-abcdef", "fs_read"); !strings.Contains(got, "fs_read") {
		t.Fatalf("got %q", got)
	}
}

func TestTranscriptToItemTypedTools(t *testing.T) {
	msgs := []gatewayclient.TranscriptMessage{
		{
			Role: "assistant",
			ToolCalls: []gatewayclient.TranscriptToolCall{
				{ID: "c1", Name: "fs_write", Arguments: `{"path":"README.md"}`},
			},
		},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	names := toolNameByCallID(msgs)
	item := transcriptToItem(msgs[0], names, 0)
	var found bool
	for _, bl := range item.Blocks {
		if bl.Kind == blockToolCall && bl.ToolName == "fs_write" {
			found = true
			if !strings.Contains(bl.Text, "README.md") {
				t.Fatalf("tool preview = %q", bl.Text)
			}
		}
	}
	if !found {
		t.Fatalf("assistant blocks = %#v", item.Blocks)
	}
	tool := transcriptToItem(msgs[1], names, 1)
	if tool.Kind != tlTool || !strings.Contains(tool.Title, "fs_write") {
		t.Fatalf("tool item = %#v", tool)
	}
	rendered := renderTimeline([]timelineItem{item, tool})
	if !strings.Contains(rendered, "fs_write") {
		t.Fatalf("render = %s", rendered)
	}
}
