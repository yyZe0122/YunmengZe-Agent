package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/gatewayclient"
)

type timelineKind string

const (
	tlUser    timelineKind = "user"
	tlSystem  timelineKind = "system"
	tlPlan    timelineKind = "plan"
	tlRun     timelineKind = "run"
	tlError   timelineKind = "error"
	tlTool    timelineKind = "tool"
	tlDone    timelineKind = "done"
	tlJourney timelineKind = "journey"
)

type blockKind string

const (
	blockThinking   blockKind = "thinking"
	blockReply      blockKind = "reply"
	blockToolCall   blockKind = "tool_call"
	blockToolResult blockKind = "tool_result"
	blockPlain      blockKind = "plain"
)

// contentBlock is a typed segment inside a timeline row (presentation only).
type contentBlock struct {
	Kind     blockKind
	Text     string
	ToolName string
	ToolID   string
	Key      string // expand key; empty = not foldable
	Live     bool   // streaming: fold shows tail
}

type timelineItem struct {
	Kind   timelineKind
	At     string
	Title  string
	Body   string // plain body when Blocks empty (legacy / simple rows)
	Blocks []contentBlock
	State  string
	Key    string // item-level expand key
}

// expandState controls which folded blocks are fully shown.
type expandState struct {
	all  bool
	keys map[string]bool
}

func (e expandState) open(key string) bool {
	if key == "" {
		return false
	}
	if e.all {
		return true
	}
	if e.keys == nil {
		return false
	}
	return e.keys[key]
}

func (e *expandState) toggle(key string) {
	if key == "" {
		return
	}
	if e.keys == nil {
		e.keys = make(map[string]bool)
	}
	e.keys[key] = !e.keys[key]
	e.all = false
}

func (e *expandState) setAll(open bool) {
	e.all = open
	if open {
		e.keys = nil
	} else {
		e.keys = make(map[string]bool)
	}
}

func buildTimeline(task *gatewayclient.Task, plan *gatewayclient.Plan, runs []gatewayclient.Run) []timelineItem {
	if task == nil {
		return nil
	}
	items := make([]timelineItem, 0, 8+len(runs))

	obj := strings.TrimSpace(task.Objective)
	if obj == "" {
		obj = task.Title
	}
	items = append(items, timelineItem{
		Kind: tlUser, At: task.CreatedAt, Title: "you", Body: obj,
	})

	items = append(items, timelineItem{
		Kind: tlSystem, At: task.CreatedAt, Title: "task created",
		Body: fmt.Sprintf("%s", task.ID), State: gatewayclient.TaskStateCreated,
	})

	switch task.State {
	case gatewayclient.TaskStateRunning:
		items = append(items, timelineItem{
			Kind: tlSystem, At: task.UpdatedAt, Title: "running", State: task.State,
		})
	case gatewayclient.TaskStatePaused:
		items = append(items, timelineItem{
			Kind: tlSystem, At: task.UpdatedAt, Title: "paused", State: task.State,
		})
	case gatewayclient.TaskStateCompleted:
		items = append(items, timelineItem{
			Kind: tlDone, At: task.UpdatedAt, Title: "done", State: task.State,
		})
	case gatewayclient.TaskStateFailed:
		items = append(items, timelineItem{
			Kind: tlError, At: task.UpdatedAt, Title: "task failed", State: task.State,
		})
	case gatewayclient.TaskStateCancelled:
		items = append(items, timelineItem{
			Kind: tlSystem, At: task.UpdatedAt, Title: "cancelled", State: task.State,
		})
	}

	if plan != nil {
		items = append(items, timelineItem{
			Kind: tlPlan, At: plan.UpdatedAt, Title: fmt.Sprintf("plan %s", plan.ID),
			Body:  fmt.Sprintf("rev=%d state=%s · %d step(s)", plan.Revision, plan.State, len(plan.Steps)),
			State: plan.State,
		})
	}

	for _, run := range runs {
		if run.ParentRunID != nil && strings.TrimSpace(string(*run.ParentRunID)) != "" {
			continue
		}
		step := "plan"
		if run.StepID != nil {
			step = string(*run.StepID)
		}
		title := fmt.Sprintf("run %s [%s]", shortID(string(run.ID)), step)
		body := ""
		kind := tlRun
		if run.Error != nil && strings.TrimSpace(*run.Error) != "" {
			kind = tlError
			body = strings.TrimSpace(*run.Error)
		} else if run.Result != nil && strings.TrimSpace(*run.Result) != "" {
			body = strings.TrimSpace(*run.Result)
		}
		at := run.StartedAt
		if run.FinishedAt != nil {
			at = *run.FinishedAt
		}
		key := "run:" + string(run.ID)
		item := timelineItem{
			Kind: kind, At: at, Title: title, State: run.State, Key: key,
		}
		if body != "" {
			item.Blocks = []contentBlock{{
				Kind: blockReply, Text: body,
			}}
		}
		items = append(items, item)
	}
	return items
}

// foldBody truncates large run output (head-first). Used for simple strings.
func foldBody(s string) string {
	return foldHead(s, timelineBodyMaxLines, timelineBodyMaxChars)
}

// foldHead keeps the first maxLines / maxChars.
func foldHead(s string, maxLines, maxChars int) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	truncated := false
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	runes := []rune(out)
	if maxChars > 0 && len(runes) > maxChars {
		out = string(runes[:maxChars])
		truncated = true
	}
	if truncated {
		out = strings.TrimRight(out, "\n") + "\n… (truncated · e expand)"
	}
	return out
}

// foldTail keeps the last maxLines (and last maxChars of that window).
func foldTail(s string, maxLines, maxChars int) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	// Drop trailing empty line from split of trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	truncated := false
	if maxLines > 0 && total > maxLines {
		lines = lines[total-maxLines:]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	runes := []rune(out)
	if maxChars > 0 && len(runes) > maxChars {
		out = string(runes[len(runes)-maxChars:])
		truncated = true
	}
	if truncated {
		return "… (" + itoa(total) + " lines · e expand)\n" + out
	}
	return out
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	if n < 1 {
		return 1
	}
	return n
}

func renderTimeline(items []timelineItem) string {
	return renderTimelineExpanded(items, expandState{})
}

func renderTimelineExpanded(items []timelineItem, exp expandState) string {
	return renderTimelineUncached(items, exp, defaultRenderOpts())
}

func renderTimelineUncached(items []timelineItem, exp expandState, opts renderOpts) string {
	if len(items) == 0 {
		return styleDim.Render("No activity yet.")
	}
	if opts.Width <= 0 {
		opts = defaultRenderOpts()
	}
	var b strings.Builder
	for i, item := range items {
		b.WriteString(renderTimelineItem(item, exp, opts))
		if i < len(items)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func renderTimelineItem(item timelineItem, exp expandState, opts renderOpts) string {
	w := opts.Width
	if item.Kind == tlDone {
		return renderDoneBanner(item.Title, item.State, w)
	}

	// System / plan / error / journey: compact rows (not full chat bubbles).
	switch item.Kind {
	case tlSystem, tlPlan, tlError, tlJourney:
		prefix, titleStyle := itemChrome(item)
		line := renderSystemLine(prefix, item.Title, item.State, titleStyle, opts.Anim)
		if item.Body == "" && len(item.Blocks) == 0 {
			return line
		}
		var b strings.Builder
		b.WriteString(line)
		b.WriteByte('\n')
		if len(item.Blocks) > 0 {
			for _, bl := range item.Blocks {
				b.WriteString(renderBlock(bl, exp, opts, item.Kind))
				b.WriteByte('\n')
			}
			return strings.TrimRight(b.String(), "\n")
		}
		text := item.Body
		if !exp.open(item.Key) {
			text = foldHead(text, timelineBodyMaxLines, timelineBodyMaxChars)
		}
		for _, ln := range strings.Split(wrapBody(text, w-4), "\n") {
			b.WriteString(styleDim.Render("  "+ln) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	if len(item.Blocks) > 0 {
		var parts []string
		for _, bl := range item.Blocks {
			parts = append(parts, renderBlock(bl, exp, opts, item.Kind))
		}
		return strings.Join(parts, "\n")
	}

	text := item.Body
	if item.Key != "" && !exp.open(item.Key) && needsFold(text, timelineBodyMaxLines, timelineBodyMaxChars) {
		text = foldHead(text, timelineBodyMaxLines, timelineBodyMaxChars)
	} else if item.Key == "" {
		text = foldHead(text, timelineBodyMaxLines, timelineBodyMaxChars)
	}
	switch item.Kind {
	case tlUser:
		return renderUserBlock(text, w)
	case tlRun:
		return renderAssistantBlock(text, w)
	case tlTool:
		title := item.Title
		if title == "" {
			title = "tool"
		}
		return renderToolCard(styleTLTool.Render(blockTitleTool(title, text)), w)
	}
	return renderAssistantBlock(text, w)
}

func itemChrome(item timelineItem) (prefix string, titleStyle lipgloss.Style) {
	switch item.Kind {
	case tlUser:
		return "▸", styleTLUser
	case tlPlan:
		return "◇", styleTLPlan
	case tlRun:
		if item.State == "streaming" {
			return "◌", styleTLRun
		}
		return "●", styleTLRun
	case tlError:
		return "✖", styleTLErr
	case tlTool:
		return "⚙", styleTLTool
	case tlJourney:
		return "◆", styleTLJourney
	default:
		return "·", styleTLSys
	}
}

func renderBlock(bl contentBlock, exp expandState, opts renderOpts, parent timelineKind) string {
	w := opts.Width
	open := exp.open(bl.Key)
	switch bl.Kind {
	case blockThinking:
		text := bl.Text
		lines := lineCount(text)
		folded := false
		if !open {
			if bl.Live {
				text = foldTail(text, thinkingLiveMaxLines, thinkingLiveMaxChars)
			} else if needsFold(text, thinkingFoldMaxLines, thinkingFoldMaxChars) {
				folded = true
				text = ""
			}
		}
		title := blockTitleThinking(lines)
		if folded {
			title += fmt.Sprintf("  %d lines collapsed · press e", lines)
		}
		line := renderThinkingLine(title, w)
		if folded || text == "" {
			return line
		}
		return line + "\n" + styleDim.Render(wrapBody(text, max(12, w-2)))

	case blockToolCall:
		return renderToolCard(styleTLTool.Render(blockTitleTool(bl.ToolName, bl.Text)), w)

	case blockToolResult:
		return renderToolCard(renderToolResultBlock(bl, open, w), w)

	case blockReply:
		text := bl.Text
		if bl.Live {
			if text == "" {
				return renderAssistantBlock(text, w)
			}
			if opts.Stream == nil {
				return renderAssistantBlock(text, w)
			}
			return renderAssistantBlock(opts.Stream.render(text, w, opts.Theme), w)
		}
		if text != "" {
			return renderAssistantBlock(renderMarkdown(text, w, opts.Theme), w)
		}
		return renderAssistantBlock(text, w)

	default:
		text := bl.Text
		if !open {
			text = foldHead(text, timelineBodyMaxLines, timelineBodyMaxChars)
		}
		return renderAssistantBlock(text, w)
	}
}

func needsFold(s string, maxLines, maxChars int) bool {
	if s == "" {
		return false
	}
	if maxLines > 0 && lineCount(s) > maxLines {
		return true
	}
	if maxChars > 0 && len([]rune(s)) > maxChars {
		return true
	}
	return false
}

func looksLikeUnifiedDiff(s string) bool {
	return strings.Contains(s, "\n--- a/") || strings.HasPrefix(strings.TrimSpace(s), "--- a/") ||
		strings.Contains(s, `"diff":`) && strings.Contains(s, "@@ ")
}

func firstDiffHunkLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "@@") || strings.HasPrefix(trim, "---") || strings.HasPrefix(trim, "+++") {
			return trim
		}
	}
	return firstLine(s)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// collectExpandKeys returns foldable keys in display order (for e = last foldable).
func collectExpandKeys(items []timelineItem) []string {
	var keys []string
	seen := make(map[string]struct{})
	add := func(k string) {
		if k == "" {
			return
		}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for _, it := range items {
		if len(it.Blocks) == 0 {
			add(it.Key)
			continue
		}
		for _, bl := range it.Blocks {
			if bl.Kind == blockReply {
				continue
			}
			add(bl.Key)
		}
	}
	return keys
}
