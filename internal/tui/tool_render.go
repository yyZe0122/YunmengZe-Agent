package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

// formatToolCallLine renders a typed tool-call summary for the timeline (presentation only).
// No import of internal/tools — name + JSON args only.
func formatToolCallLine(tc gatewayclient.TranscriptToolCall) string {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = "tool"
	}
	preview := toolCallPreview(name, tc.Arguments)
	if preview == "" {
		raw := strings.TrimSpace(tc.Arguments)
		if raw == "" || raw == "{}" {
			return fmt.Sprintf("⚙ %s", name)
		}
		return fmt.Sprintf("⚙ %s(%s)", name, truncate(raw, 100))
	}
	return fmt.Sprintf("⚙ %s · %s", name, preview)
}

// formatToolResultTitle labels a tool result row with call id (and optional name if known).
func renderToolResultBlock(bl contentBlock, open bool, width int) string {
	text := bl.Text
	label := bl.ToolName
	if label == "" {
		label = "result"
	}
	if bl.ToolID != "" {
		label = label + " " + shortID(bl.ToolID)
	}
	if painted := paintToolResult(label, text, open, width); painted != "" {
		return painted
	}
	if !open && looksLikeUnifiedDiff(text) {
		n := lineCount(text)
		preview := firstDiffHunkLine(text)
		line := styleTLTool.Render("· " + label + " · diff " + fmt.Sprintf("%d lines · e expand", n))
		if preview != "" {
			line += "\n" + styleDim.Render("  "+truncate(preview, 80))
		}
		return line
	}
	if !open && needsFold(text, toolResultMaxLines, toolResultMaxChars) {
		n := lineCount(text)
		preview := firstLine(text)
		line := styleTLTool.Render("· " + label + " · " + fmt.Sprintf("%d lines · e expand", n))
		if preview != "" {
			line += "\n" + styleDim.Render("  "+truncate(preview, 80))
		}
		return line
	}
	body := text
	if !open {
		body = foldHead(text, toolResultMaxLines, toolResultMaxChars)
	}
	head := styleTLTool.Render("· " + label)
	return head + "\n" + styleDim.Render(wrapBody(body, max(12, width-2)))
}

func paintToolResult(label, text string, open bool, width int) string {
	name := strings.TrimSpace(label)
	if i := strings.IndexByte(name, ' '); i > 0 {
		name = name[:i]
	}
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		payload = nil
	}
	switch {
	case strings.HasPrefix(name, "process_"):
		cmd := processToolPreview(payload)
		code := stringField(payload, "exit_code")
		out := stringField(payload, "stdout")
		errOut := stringField(payload, "stderr")
		head := styleTLTool.Render("$ " + truncate(firstNonEmpty(cmd, name), 72))
		if code != "" {
			head += "  " + styleDim.Render("exit "+code)
		}
		if !open {
			preview := firstLine(firstNonEmpty(out, errOut, raw))
			if preview != "" {
				return head + "\n" + styleDim.Render("  "+truncate(preview, 80))
			}
			return head
		}
		body := firstNonEmpty(out, errOut, raw)
		return head + "\n" + styleDim.Render(wrapBody(body, max(12, width-2)))
	case name == "fs_patch" || looksLikeUnifiedDiff(raw):
		n := lineCount(raw)
		head := styleTLTool.Render("· " + label + " · diff")
		if !open {
			head += " " + styleDim.Render(fmt.Sprintf("%d lines · e expand", n))
			if preview := firstDiffHunkLine(raw); preview != "" {
				return head + "\n" + styleDim.Render("  "+truncate(preview, 80))
			}
			return head
		}
		return head + "\n" + styleDim.Render(wrapBody(raw, max(12, width-2)))
	case strings.HasPrefix(name, "fs_grep") || name == "fs_glob":
		count := stringField(payload, "count", "matches", "total")
		head := styleTLTool.Render("· " + label)
		if count != "" {
			head += "  " + styleDim.Render(count+" hits")
		}
		if !open {
			return head + "\n" + styleDim.Render("  "+truncate(firstLine(raw), 80))
		}
		return head + "\n" + styleDim.Render(wrapBody(raw, max(12, width-2)))
	case name == "todo_list" || name == "todo_write":
		head := styleTLTool.Render("· " + label)
		if !open {
			return head
		}
		return head + "\n" + styleDim.Render(wrapBody(raw, max(12, width-2)))
	default:
		return ""
	}
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			return s
		}
	}
	return ""
}

func formatToolResultTitle(toolCallID, toolName string) string {
	id := strings.TrimSpace(toolCallID)
	name := strings.TrimSpace(toolName)
	switch {
	case name != "" && id != "":
		return fmt.Sprintf("%s %s", name, shortID(id))
	case name != "":
		return name
	case id != "":
		return "tool " + shortID(id)
	default:
		return "tool"
	}
}

func toolCallPreview(name, arguments string) string {
	args := parseToolArgs(arguments)
	switch {
	case name == "task":
		if p := stringField(args, "prompt", "objective", "description"); p != "" {
			return "prompt " + truncate(p, 72)
		}
		return ""
	case strings.HasPrefix(name, "fs_"):
		return fsToolPreview(name, args)
	case strings.HasPrefix(name, "git_"):
		return gitToolPreview(name, args)
	case strings.HasPrefix(name, "process_"):
		return processToolPreview(args)
	case strings.HasPrefix(name, "http_"):
		if u := stringField(args, "url", "uri"); u != "" {
			return truncate(u, 80)
		}
		return ""
	case strings.HasPrefix(name, "memory_"):
		if k := stringField(args, "kind", "key", "query"); k != "" {
			return truncate(k, 60)
		}
		return ""
	default:
		if p := stringField(args, "path", "file", "filepath"); p != "" {
			return pathPreview(p)
		}
		if c := stringField(args, "command", "cmd"); c != "" {
			return truncate(c, 72)
		}
		return ""
	}
}

func fsToolPreview(name string, args map[string]any) string {
	path := stringField(args, "path", "file", "filepath", "target", "dest", "destination")
	switch name {
	case "fs_write", "fs_patch", "fs_mkdir", "fs_remove":
		if path != "" {
			return pathPreview(path)
		}
		if from := stringField(args, "from", "src", "source"); from != "" {
			to := stringField(args, "to", "dest", "destination")
			if to != "" {
				return pathPreview(from) + " → " + pathPreview(to)
			}
			return pathPreview(from)
		}
	case "fs_grep", "fs_glob":
		pat := stringField(args, "pattern", "glob", "query")
		if path != "" && pat != "" {
			return pathPreview(path) + " · " + truncate(pat, 40)
		}
		if pat != "" {
			return truncate(pat, 60)
		}
	}
	if path != "" {
		return pathPreview(path)
	}
	return ""
}

func gitToolPreview(name string, args map[string]any) string {
	if msg := stringField(args, "message"); msg != "" && (name == "git_commit" || strings.Contains(name, "commit")) {
		return truncate(msg, 60)
	}
	if ref := stringField(args, "ref", "branch", "remote"); ref != "" {
		return truncate(ref, 48)
	}
	if path := stringField(args, "path", "paths"); path != "" {
		return pathPreview(path)
	}
	return ""
}

func processToolPreview(args map[string]any) string {
	cmd := stringField(args, "command", "cmd")
	if cmd == "" {
		if argv := stringSliceField(args, "arguments", "argv", "args"); len(argv) > 0 {
			cmd = strings.Join(argv, " ")
		}
	} else if extra := stringSliceField(args, "arguments", "args", "argv"); len(extra) > 0 {
		cmd = cmd + " " + strings.Join(extra, " ")
	}
	if cmd == "" {
		return ""
	}
	return truncate(cmd, 80)
}

func pathPreview(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Prefer basename for long absolute paths; keep short relative paths.
	if len(p) > 48 && (strings.HasPrefix(p, "/") || filepath.IsAbs(p)) {
		base := filepath.Base(p)
		dir := filepath.Base(filepath.Dir(p))
		if dir != "" && dir != "." && dir != "/" {
			return truncate(dir+"/"+base, 48)
		}
		return truncate(base, 48)
	}
	return truncate(p, 48)
}

func parseToolArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

func stringField(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case float64:
			return fmt.Sprintf("%g", t)
		case bool:
			return fmt.Sprintf("%v", t)
		case []any:
			parts := make([]string, 0, len(t))
			for _, item := range t {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, " ")
			}
		}
	}
	return ""
}

func stringSliceField(m map[string]any, keys ...string) []string {
	if m == nil {
		return nil
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case []any:
			out := make([]string, 0, len(t))
			for _, item := range t {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		case []string:
			if len(t) > 0 {
				return t
			}
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return []string{s}
			}
		}
	}
	return nil
}

// toolNameByCallID maps tool_call_id → tool name from prior assistant messages.
func toolNameByCallID(messages []gatewayclient.TranscriptMessage) map[string]string {
	out := make(map[string]string)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			id := strings.TrimSpace(tc.ID)
			if id == "" {
				continue
			}
			if name := strings.TrimSpace(tc.Name); name != "" {
				out[id] = name
			}
		}
	}
	return out
}
