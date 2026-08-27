package tui

import "strings"

// timelineRenderCache avoids re-styling the full timeline when items are unchanged
// (Crush cachedMessageItem pattern, simplified for our single-string viewport).
// Finished rows use a stable fingerprint so live drafts do not invalidate them.
// C2: stable prefix of finished rows is cached; only the live tail is re-rendered.
type timelineRenderCache struct {
	key       string
	output    string
	prefixKey string
	prefixOut string
	prefixN   int
}

func (c *timelineRenderCache) render(items []timelineItem, exp expandState, opts renderOpts) string {
	key := timelineCacheKey(items, exp, opts)
	if c != nil && key == c.key && c.output != "" {
		return c.output
	}

	// C2: reuse stable finished prefix when only the live tail changed.
	// Prefix fingerprint ignores Anim so a running spinner does not restyle finished bubbles.
	if c != nil && c.prefixN > 0 && c.prefixN <= len(items) {
		n := countFinishedPrefix(items)
		if n == c.prefixN && n < len(items) {
			pk := timelineCacheKeyAnim(items[:n], exp, opts, false)
			if pk == c.prefixKey && c.prefixOut != "" {
				tail := renderTimelineUncached(items[n:], exp, opts)
				out := c.prefixOut
				if out != "" && !strings.HasSuffix(out, "\n") {
					out += "\n"
				}
				if len(items) > n {
					out += "\n" + tail
				}
				c.key = key
				c.output = out
				return out
			}
		}
	}

	out := renderTimelineUncached(items, exp, opts)
	if c != nil {
		c.key = key
		c.output = out
		n := countFinishedPrefix(items)
		c.prefixN = n
		if n > 0 {
			c.prefixKey = timelineCacheKeyAnim(items[:n], exp, opts, false)
			c.prefixOut = renderTimelineUncached(items[:n], exp, opts)
		} else {
			c.prefixKey = ""
			c.prefixOut = ""
		}
	}
	return out
}

func countFinishedPrefix(items []timelineItem) int {
	n := 0
	for _, it := range items {
		if !timelineItemFinished(it) {
			break
		}
		n++
	}
	return n
}

func timelineCacheKey(items []timelineItem, exp expandState, opts renderOpts) string {
	return timelineCacheKeyAnim(items, exp, opts, true)
}

func timelineCacheKeyAnim(items []timelineItem, exp expandState, opts renderOpts, withAnim bool) string {
	var b strings.Builder
	b.Grow(len(items)*48 + 32)
	b.WriteString(itoa(opts.Width))
	b.WriteByte('|')
	b.WriteString(string(opts.Theme))
	b.WriteByte('|')
	if withAnim {
		b.WriteString(itoa(opts.Anim))
	} else {
		b.WriteString("-")
	}
	b.WriteByte('|')
	if exp.all {
		b.WriteString("A1|")
	} else {
		b.WriteString("A0|")
	}
	if exp.keys != nil {
		for k, v := range exp.keys {
			if v {
				b.WriteString(k)
				b.WriteByte(',')
			}
		}
		b.WriteByte('|')
	}
	for _, it := range items {
		b.WriteString(string(it.Kind))
		b.WriteByte('|')
		b.WriteString(it.Title)
		b.WriteByte('|')
		b.WriteString(it.State)
		b.WriteByte('|')
		b.WriteString(it.At)
		b.WriteByte('|')
		b.WriteString(it.Key)
		b.WriteByte('|')
		if timelineItemFinished(it) {
			b.WriteString("F")
			b.WriteString(itemFingerprint(it, 24))
		} else {
			b.WriteString("L")
			b.WriteString(itemFingerprint(it, 48))
		}
		b.WriteByte(';')
	}
	return b.String()
}

func itemFingerprint(it timelineItem, prefix int) string {
	if len(it.Blocks) > 0 {
		var b strings.Builder
		for _, bl := range it.Blocks {
			b.WriteString(string(bl.Kind))
			b.WriteByte(':')
			b.WriteString(bl.Key)
			b.WriteByte('#')
			b.WriteString(itoa(len(bl.Text)))
			if !timelineItemFinished(it) {
				if len(bl.Text) > 0 {
					start := 0
					if len(bl.Text) > prefix {
						start = len(bl.Text) - prefix
					}
					b.WriteString(bl.Text[start:])
				}
			} else if len(bl.Text) > 0 {
				b.WriteString(bl.Text[:min(prefix, len(bl.Text))])
			}
			b.WriteByte(';')
		}
		return b.String()
	}
	body := it.Body
	if timelineItemFinished(it) {
		return body[:min(prefix, len(body))] + "#" + itoa(len(body))
	}
	start := 0
	if len(body) > prefix {
		start = len(body) - prefix
	}
	return body[start:] + "#" + itoa(len(body))
}

// timelineItemFinished is true for settled transcript rows (not live typewriter draft).
func timelineItemFinished(it timelineItem) bool {
	if it.State == "streaming" || it.Title == "assistant…" {
		return false
	}
	for _, bl := range it.Blocks {
		if bl.Live {
			return false
		}
	}
	if it.Kind == tlUser || it.Kind == tlPlan || it.Kind == tlDone || it.Kind == tlJourney {
		return true
	}
	switch it.State {
	case "completed", "failed", "cancelled":
		return true
	case "running", "paused":
		return false
	default:
		return it.Kind == tlSystem || it.Kind == tlError || it.Kind == tlTool || it.State == ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
