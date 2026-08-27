package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRasterDiscHasCoverage(t *testing.T) {
	b := newBitmap(8, 4)
	b.stamp(8, 8, 3.2, false)
	var sum float32
	for _, v := range b.cov {
		sum += v
	}
	if sum < 4 {
		t.Fatalf("coverage too low: %v", sum)
	}
	out := ansi.Strip(b.encodeBraille(colorPaper, colorBone, colorSeal))
	if !hasBraille(out) {
		t.Fatalf("encode missing braille:\n%s", out)
	}
	if strings.Contains(out, "▀") {
		t.Fatalf("half-block leaked:\n%s", out)
	}
}

func TestRasterStrokeDiffersEmpty(t *testing.T) {
	empty := newBitmap(6, 3)
	drawn := newBitmap(6, 3)
	drawn.stroke(1, 1, 10, 10, 0.8, 0.6, false)
	if ansi.Strip(empty.encodeBraille(colorPaper, colorBone, colorSeal)) == ansi.Strip(drawn.encodeBraille(colorPaper, colorBone, colorSeal)) {
		t.Fatal("stroke should change the bitmap")
	}
}

func TestBrailleCellSize(t *testing.T) {
	b := newBitmap(5, 3)
	if b.w != 10 || b.h != 12 {
		t.Fatalf("bitmap %dx%d want 10x12", b.w, b.h)
	}
}

func TestOverlayPunchesBody(t *testing.T) {
	b := newBitmap(4, 2)
	b.stamp(4, 4, 3, false)
	b.stamp(4, 4, 1.2, true)
	var body, accent int
	for i := range b.cov {
		if b.overlay[i] >= coverOn {
			accent++
			if b.cov[i] >= coverOn {
				t.Fatalf("overlay pixel %d still has body coverage", i)
			}
		}
		if b.cov[i] >= coverOn {
			body++
		}
	}
	if accent == 0 || body == 0 {
		t.Fatalf("accent=%d body=%d", accent, body)
	}
}
