package tui

import "math"

const (
	brushViewW = 1.0
	brushViewH = 1.0
)

func drawBrush(c canvas, mood mascotMood, frame int) {
	xf := identityXform()
	xf.cx, xf.cy = 0.50, 0.42
	phase := float32(frame)
	switch mood {
	case moodIdle:
		xf.sy = 1 + 0.08*sin01(phase/3)
		xf.ang = 0.14 * sin01(phase/5)
		xf.ty = 0.03 * sin01(phase/2)
	case moodThink:
		xf.ang = -0.35 - 0.06*sin01(phase/2)
		xf.tx = -0.05
		xf.ty = 0.04 * sin01(phase)
	case moodWrite:
		xf.ang = 0.22 * sin01(phase)
		xf.ty = 0.05 + 0.05*abs32(sin01(phase))
		xf.sx = 1.04 + 0.03*sin01(phase)
	case moodTool:
		xf.ty = 0.12 * abs32(sin01(phase))
		xf.sy = 1 - 0.10*abs32(sin01(phase))
	case moodWait:
		xf.ang = 0.28 * sin01(phase/2)
		xf.tx = 0.04 * sin01(phase/2)
	case moodPause:
		xf.ang = 0.42
		xf.ty = 0.08
		xf.sy = 0.94 + 0.02*sin01(phase/3)
	case moodStuck:
		if frame%2 == 0 {
			xf.ang = 0.18
		} else {
			xf.ang = -0.18
		}
		xf.ty = 0.03
	case moodLift:
		xf.ty = -0.16 - 0.04*sin01(phase/2)
		xf.sy = 1.08
	}
	c.xf = xf

	c.line(0.50, 0.04, 0.50, 0.60, 0.038, 0.038)
	c.fillTeardrop(0.50, 0.66, 0.92, 0.10)

	if mood == moodWrite {
		c.overlay = true
		if frame%4 < 2 {
			c.disc(0.50, 0.94, 0.028)
		}
		if frame%4 == 1 {
			c.disc(0.56, 0.96, 0.018)
		}
	}
}

func (c canvas) fillTeardrop(cx, bellyY, tipY, bellyR float32) {
	steps := 18
	dy := tipY - bellyY
	for i := 0; i <= steps; i++ {
		t := float32(i) / float32(steps)
		y := bellyY + dy*t
		r := bellyR * (1 - t*t)
		if r < 0.018 {
			r = 0.018
		}
		c.disc(cx, y, r)
	}
}

func sin01(t float32) float32 {
	return float32(math.Sin(float64(t) * math.Pi / 2))
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
