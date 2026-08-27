package tui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	dotsX        = 2
	dotsY        = 4
	brailleBase  = 0x2800
	coverOn      = float32(0.45)
	minStrokeDot = float32(0.55)
)

type vec2 struct{ x, y float32 }

type bitmap struct {
	w, h    int
	cov     []float32
	overlay []float32
}

func newBitmap(cellsW, cellsH int) *bitmap {
	if cellsW < 1 {
		cellsW = 1
	}
	if cellsH < 1 {
		cellsH = 1
	}
	w, h := cellsW*dotsX, cellsH*dotsY
	return &bitmap{
		w:       w,
		h:       h,
		cov:     make([]float32, w*h),
		overlay: make([]float32, w*h),
	}
}

func (b *bitmap) add(x, y int, a float32, overlay bool) {
	if a <= 0 || x < 0 || y < 0 || x >= b.w || y >= b.h {
		return
	}
	i := y*b.w + x
	if overlay {
		s := b.overlay[i] + a
		if s > 1 {
			s = 1
		}
		b.overlay[i] = s
		if s >= coverOn {
			b.cov[i] = 0
		}
		return
	}
	s := b.cov[i] + a
	if s > 1 {
		s = 1
	}
	b.cov[i] = s
}

func coverDisc(d, r float32) float32 {
	if r <= 0 {
		return 0
	}
	if d >= r+0.55 {
		return 0
	}
	if d <= r-0.55 {
		return 1
	}
	return (r + 0.55 - d) / 1.1
}

func (b *bitmap) stamp(cx, cy, r float32, overlay bool) {
	if r <= 0 {
		return
	}
	minx := int(math.Floor(float64(cx - r - 1)))
	maxx := int(math.Ceil(float64(cx + r + 1)))
	miny := int(math.Floor(float64(cy - r - 1)))
	maxy := int(math.Ceil(float64(cy + r + 1)))
	for y := miny; y <= maxy; y++ {
		for x := minx; x <= maxx; x++ {
			dx := float32(x) + 0.5 - cx
			dy := float32(y) + 0.5 - cy
			b.add(x, y, coverDisc(float32(math.Hypot(float64(dx), float64(dy))), r), overlay)
		}
	}
}

func (b *bitmap) stroke(x0, y0, x1, y1, r0, r1 float32, overlay bool) {
	dx := x1 - x0
	dy := y1 - y0
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	n := int(dist*2.4) + 2
	if n < 2 {
		n = 2
	}
	for i := 0; i <= n; i++ {
		t := float32(i) / float32(n)
		b.stamp(x0+(x1-x0)*t, y0+(y1-y0)*t, r0+(r1-r0)*t, overlay)
	}
}

type xform struct {
	cx, cy float32
	ang    float32
	sx, sy float32
	tx, ty float32
}

func identityXform() xform {
	return xform{sx: 1, sy: 1, cx: 0.5, cy: 0.5}
}

func (x xform) apply(p vec2) vec2 {
	px := p.x - x.cx
	py := p.y - x.cy
	if x.sx != 1 {
		px *= x.sx
	}
	if x.sy != 1 {
		py *= x.sy
	}
	if x.ang != 0 {
		c := float32(math.Cos(float64(x.ang)))
		s := float32(math.Sin(float64(x.ang)))
		px, py = px*c-py*s, px*s+py*c
	}
	return vec2{x: px + x.cx + x.tx, y: py + x.cy + x.ty}
}

type canvas struct {
	b       *bitmap
	scaleX  float32
	scaleY  float32
	padX    float32
	padY    float32
	xf      xform
	overlay bool
}

func newCanvas(cellsW, cellsH int, viewW, viewH float32) canvas {
	b := newBitmap(cellsW, cellsH)
	padX := float32(1.0)
	padY := float32(1.0)
	innerW := float32(b.w) - 2*padX
	innerH := float32(b.h) - 2*padY
	if viewW < 0.01 {
		viewW = 1
	}
	if viewH < 0.01 {
		viewH = 1
	}
	return canvas{
		b:      b,
		scaleX: innerW / viewW,
		scaleY: innerH / viewH,
		padX:   padX,
		padY:   padY,
		xf:     identityXform(),
	}
}

func (c canvas) mapPt(p vec2) vec2 {
	p = c.xf.apply(p)
	return vec2{x: c.padX + p.x*c.scaleX, y: c.padY + p.y*c.scaleY}
}

func (c canvas) mapR(r float32) float32 {
	s := (c.scaleX + c.scaleY) * 0.5
	out := r * s
	if !c.overlay && out < minStrokeDot {
		return minStrokeDot
	}
	return out
}

func (c canvas) line(x0, y0, x1, y1, r0, r1 float32) {
	a := c.mapPt(vec2{x0, y0})
	b := c.mapPt(vec2{x1, y1})
	c.b.stroke(a.x, a.y, b.x, b.y, c.mapR(r0), c.mapR(r1), c.overlay)
}

func (c canvas) disc(x, y, r float32) {
	p := c.mapPt(vec2{x, y})
	c.b.stamp(p.x, p.y, c.mapR(r), c.overlay)
}

var brailleDot = [dotsY][dotsX]int{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

func (b *bitmap) encodeBraille(paper, ink, accent color.Color) string {
	cellsW := b.w / dotsX
	cellsH := b.h / dotsY
	var out strings.Builder
	out.Grow(cellsH * (cellsW*8 + 1))
	space := lipgloss.NewStyle().Foreground(paper).Background(paper).Render(" ")
	inkS := lipgloss.NewStyle().Foreground(ink).Background(paper)
	accentS := lipgloss.NewStyle().Foreground(accent).Background(paper)
	for row := 0; row < cellsH; row++ {
		if row > 0 {
			out.WriteByte('\n')
		}
		for col := 0; col < cellsW; col++ {
			bodyBits, accentBits := 0, 0
			for dy := 0; dy < dotsY; dy++ {
				for dx := 0; dx < dotsX; dx++ {
					i := (row*dotsY+dy)*b.w + (col*dotsX + dx)
					bit := brailleDot[dy][dx]
					if b.overlay[i] >= coverOn {
						accentBits |= bit
					} else if b.cov[i] >= coverOn {
						bodyBits |= bit
					}
				}
			}
			switch {
			case accentBits != 0:
				out.WriteString(accentS.Render(string(rune(brailleBase + accentBits))))
			case bodyBits != 0:
				out.WriteString(inkS.Render(string(rune(brailleBase + bodyBits))))
			default:
				out.WriteString(space)
			}
		}
	}
	return out.String()
}

func hasBraille(s string) bool {
	for _, r := range s {
		if r >= brailleBase && r <= brailleBase+0xFF {
			return true
		}
	}
	return false
}
