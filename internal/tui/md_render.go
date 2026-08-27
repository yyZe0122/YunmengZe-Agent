package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

const (
	mdMaxRunes   = 12000
	mdCacheLimit = 64
)

type mdCacheEntry struct {
	key string
	out string
}

var (
	mdMu    sync.Mutex
	mdCache []mdCacheEntry
)

// renderMarkdown renders assistant reply markdown for the terminal.
// Plain short replies without markdown markers stay plain (no glamour padding).
func renderMarkdown(src string, width int, theme ThemeName) string {
	src = strings.TrimRight(src, "\n")
	if src == "" {
		return ""
	}
	if width < 20 {
		width = 40
	}
	// Soft cap: huge dumps stay plain.
	if utf8.RuneCountInString(src) > mdMaxRunes {
		return src
	}
	if !looksLikeMarkdown(src) {
		return src
	}
	key := mdCacheKey(src, width, string(theme))
	if out, ok := mdCacheGet(key); ok {
		return out
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(mdStyle(theme)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	out = strings.TrimRight(out, "\n")
	mdCachePut(key, out)
	return out
}

func looksLikeMarkdown(s string) bool {
	if strings.Contains(s, "```") || strings.Contains(s, "`") {
		return true
	}
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") ||
			strings.HasPrefix(t, "> ") || strings.HasPrefix(t, "1. ") ||
			strings.HasPrefix(t, "[") && strings.Contains(t, "](") {
			return true
		}
		if strings.Contains(t, "**") || strings.Contains(t, "__") {
			return true
		}
	}
	return false
}

func mdCacheKey(src string, width int, theme string) string {
	h := sha256.Sum256([]byte(src))
	return hex.EncodeToString(h[:8]) + "|" + itoa(width) + "|" + theme
}

func mdCacheGet(key string) (string, bool) {
	mdMu.Lock()
	defer mdMu.Unlock()
	for _, e := range mdCache {
		if e.key == key {
			return e.out, true
		}
	}
	return "", false
}

func mdCachePut(key, out string) {
	mdMu.Lock()
	defer mdMu.Unlock()
	for i := range mdCache {
		if mdCache[i].key == key {
			mdCache[i].out = out
			return
		}
	}
	if len(mdCache) >= mdCacheLimit {
		mdCache = mdCache[1:]
	}
	mdCache = append(mdCache, mdCacheEntry{key: key, out: out})
}

func unclosedFence(s string) bool {
	return strings.Count(s, "```")%2 == 1
}

// streamingMD freezes a glamour'd prefix at the last safe blank-line cut.
// The growing trail stays plain (T8). Owned by model, passed via renderOpts.
type streamingMD struct {
	src    string
	cut    int
	prefix string
	width  int
	theme  ThemeName
}

func (s *streamingMD) reset() {
	if s == nil {
		return
	}
	*s = streamingMD{}
}

func (s *streamingMD) render(src string, width int, theme ThemeName) string {
	src = strings.TrimRight(src, "\n")
	if src == "" {
		return ""
	}
	if width < 20 {
		width = 40
	}
	if utf8.RuneCountInString(src) > mdMaxRunes || !looksLikeMarkdown(src) {
		return src
	}
	if s == nil {
		return src
	}
	if s.width != width || s.theme != theme {
		s.reset()
	}
	if s.cut > 0 && (s.cut > len(src) || src[:s.cut] != s.src[:min(s.cut, len(s.src))]) {
		s.reset()
	}

	cut := safeMarkdownCut(src)
	if cut > s.cut {
		s.prefix = renderMarkdown(src[:cut], width, theme)
		s.cut = cut
	}
	s.src = src
	s.width = width
	s.theme = theme

	if s.cut <= 0 || s.prefix == "" {
		return src
	}
	trail := src[s.cut:]
	if trail == "" {
		return s.prefix
	}
	return joinMD(s.prefix, trail)
}

func joinMD(prefix, trail string) string {
	prefix = strings.TrimRight(prefix, "\n")
	trail = strings.TrimLeft(trail, "\n")
	if prefix == "" {
		return trail
	}
	if trail == "" {
		return prefix
	}
	return prefix + "\n\n" + trail
}

// safeMarkdownCut is the last blank-line offset whose prefix has no open fence.
func safeMarkdownCut(src string) int {
	best := 0
	off := 0
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		off += len(line)
		if i < len(lines)-1 {
			off++ // newline
		}
		if i == len(lines)-1 {
			break
		}
		if strings.TrimSpace(line) != "" {
			continue
		}
		if unclosedFence(src[:off]) {
			continue
		}
		best = off
	}
	return best
}

func mdStyle(theme ThemeName) ansi.StyleConfig {
	if theme == ThemeDay {
		s := styles.LightStyleConfig
		s.Document.Color = mdStr(hexDayBone)
		s.Heading.Color = mdStr(hexDayHead)
		s.Heading.Bold = mdBool(true)
		s.H1.Color = mdStr(hexDayBone)
		s.H1.BackgroundColor = mdStr(hexDayH1Bg)
		s.H1.Bold = mdBool(true)
		s.H2.Color = mdStr(hexDayHead)
		s.H2.Bold = mdBool(true)
		s.Strong.Color = mdStr(hexDayEmph)
		s.Strong.Bold = mdBool(true)
		s.Link.Color = mdStr(hexDayHead)
		s.LinkText.Color = mdStr(hexDayHead)
		s.Code.Color = mdStr(hexDayCode)
		s.Code.BackgroundColor = mdStr(hexDayWash)
		s.CodeBlock.Color = mdStr(hexDayBone)
		s.CodeBlock.BackgroundColor = mdStr(hexDayWash)
		s.CodeBlock.Chroma = mdChroma(hexDayBone, hexDayHead, hexDayEmph, hexDayCode, hexDayHair, hexDayWash)
		return s
	}
	s := styles.DarkStyleConfig
	s.Document.Color = mdStr(hexNightBone)
	s.Heading.Color = mdStr(hexNightHead)
	s.Heading.Bold = mdBool(true)
	s.H1.Color = mdStr(hexNightMix)
	s.H1.BackgroundColor = mdStr(hexNightH1Bg)
	s.H1.Bold = mdBool(true)
	s.H2.Color = mdStr(hexNightHead)
	s.H2.Bold = mdBool(true)
	s.Strong.Color = mdStr(hexNightEmph)
	s.Strong.Bold = mdBool(true)
	s.Link.Color = mdStr(hexNightHead)
	s.LinkText.Color = mdStr(hexNightHead)
	s.Code.Color = mdStr(hexNightCode)
	s.Code.BackgroundColor = mdStr(hexNightWash)
	s.CodeBlock.Color = mdStr(hexNightBone)
	s.CodeBlock.BackgroundColor = mdStr(hexNightWash)
	s.CodeBlock.Chroma = mdChroma(hexNightBone, hexNightHead, hexNightEmph, hexNightCode, hexNightFly, hexNightWash)
	return s
}

func mdChroma(text, keyword, str, fn, comment, bg string) *ansi.Chroma {
	return &ansi.Chroma{
		Text:          ansi.StylePrimitive{Color: mdStr(text)},
		Keyword:       ansi.StylePrimitive{Color: mdStr(keyword), Bold: mdBool(true)},
		KeywordType:   ansi.StylePrimitive{Color: mdStr(keyword)},
		LiteralString: ansi.StylePrimitive{Color: mdStr(str)},
		NameFunction:  ansi.StylePrimitive{Color: mdStr(fn)},
		NameClass:     ansi.StylePrimitive{Color: mdStr(fn), Bold: mdBool(true)},
		Comment:       ansi.StylePrimitive{Color: mdStr(comment)},
		Background:    ansi.StylePrimitive{BackgroundColor: mdStr(bg)},
	}
}

func mdStr(s string) *string { return &s }

func mdBool(v bool) *bool { return &v }
