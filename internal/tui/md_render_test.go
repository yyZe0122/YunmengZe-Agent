package tui

import (
	"strings"
	"testing"
)

func TestUnclosedFence(t *testing.T) {
	if !unclosedFence("hi\n```go\nfunc") {
		t.Fatal("want unclosed")
	}
	if unclosedFence("hi\n```go\nfunc\n```") {
		t.Fatal("want closed")
	}
}

func TestStreamingMDKeepsUnclosedPlain(t *testing.T) {
	var s streamingMD
	src := "```md\n**bold**"
	if got := s.render(src, 80, ThemeNight); got != src {
		t.Fatalf("got %q", got)
	}
}

func TestSafeMarkdownCutBlankLine(t *testing.T) {
	src := "hello\n\nworld still growing"
	cut := safeMarkdownCut(src)
	if cut != len("hello\n\n") {
		t.Fatalf("cut = %d want %d (%q)", cut, len("hello\n\n"), src[:cut])
	}
}

func TestSafeMarkdownCutSkipsOpenFence(t *testing.T) {
	src := "intro\n\n```go\nfunc main() {\n"
	cut := safeMarkdownCut(src)
	if cut != len("intro\n\n") {
		t.Fatalf("cut = %d (%q)", cut, src[:cut])
	}
}

func TestSafeMarkdownCutCrossesList(t *testing.T) {
	src := "intro\n\n- item\n\nmore growing"
	cut := safeMarkdownCut(src)
	if cut != len("intro\n\n- item\n\n") {
		t.Fatalf("cut = %d (%q)", cut, src[:cut])
	}
}

func TestStreamingMDFreezesPrefixTrailPlain(t *testing.T) {
	var s streamingMD
	a := s.render("hello\n\nwor", 80, ThemeNight)
	b := s.render("hello\n\nworld", 80, ThemeNight)
	if a == "" || b == "" {
		t.Fatal("empty render")
	}
	first, _, _ := strings.Cut(a, "\n")
	if first != "" && !strings.Contains(b, first) {
		t.Fatalf("prefix drifted:\n%q\n%q", a, b)
	}
	if !strings.Contains(b, "world") {
		t.Fatalf("trail missing: %q", b)
	}
}

func TestStreamingMDTrailStaysPlain(t *testing.T) {
	var s streamingMD
	got := s.render("hello\n\n**wo", 80, ThemeNight)
	if !strings.Contains(got, "**wo") {
		t.Fatalf("trail should stay raw: %q", got)
	}
}

func TestMDStyleNightHeadingsAreBright(t *testing.T) {
	s := mdStyle(ThemeNight)
	if s.Heading.Color == nil || *s.Heading.Color != hexNightHead {
		t.Fatalf("heading = %v want %s", s.Heading.Color, hexNightHead)
	}
	if s.H1.BackgroundColor == nil || *s.H1.BackgroundColor != hexNightH1Bg {
		t.Fatalf("h1 bg = %v want %s", s.H1.BackgroundColor, hexNightH1Bg)
	}
	if *s.H1.BackgroundColor == hexNightInk {
		t.Fatal("h1 must not share ink block")
	}
	if s.Strong.Color == nil || *s.Strong.Color != hexNightEmph {
		t.Fatalf("strong = %v want %s", s.Strong.Color, hexNightEmph)
	}
	if s.Code.Color == nil || *s.Code.Color != hexNightCode {
		t.Fatalf("code = %v want %s", s.Code.Color, hexNightCode)
	}
}

func TestMDStyleDayHeadingsAreBright(t *testing.T) {
	s := mdStyle(ThemeDay)
	if s.Heading.Color == nil || *s.Heading.Color != hexDayHead {
		t.Fatalf("heading = %v want %s", s.Heading.Color, hexDayHead)
	}
	if s.H1.BackgroundColor == nil || *s.H1.BackgroundColor != hexDayH1Bg {
		t.Fatalf("h1 bg = %v want %s", s.H1.BackgroundColor, hexDayH1Bg)
	}
}
