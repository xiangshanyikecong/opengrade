package omr

import (
	"image"
	"image/color"
	"math"

	"github.com/opengrade/opengrade/card"
)

// RenderDebugOverlay 在矫正图上叠加判读结果:绿圈=已涂,黄圈=涂写偏浅,
// 红圈=定位角标,蓝圈=判读出的考号数字。供排障与人工复核。
func RenderDebugOverlay(d *Debug, sheet *card.Sheet, th float64) *image.RGBA {
	if d.Rectified == nil {
		return nil
	}
	th = Options{MarkThreshold: th}.withDefaults().MarkThreshold
	w, h := d.Rectified.Rect.Dx(), d.Rectified.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, y, color.Gray{Y: d.Rectified.Pix[y*w+x]})
		}
	}

	green := color.RGBA{R: 0, G: 170, B: 0, A: 255}
	amber := color.RGBA{R: 210, G: 140, B: 0, A: 255}
	red := color.RGBA{R: 200, G: 0, B: 0, A: 255}
	blue := color.RGBA{R: 0, G: 90, B: 220, A: 255}
	ring := func(cx, cy, r int, c color.RGBA) {
		for a := 0; a < 360; a += 2 {
			rad := float64(a) * math.Pi / 180
			x := cx + int(float64(r)*math.Cos(rad))
			y := cy + int(float64(r)*math.Sin(rad))
			if x >= 0 && y >= 0 && x < w && y < h {
				out.Set(x, y, c)
				out.Set(x+1, y, c)
			}
		}
	}

	// 行阈值:优先取判读时的逐行自适应阈值,缺省(旧 Debug/回退行)用全局 th。
	rowTh := func(i int, ths []float64) float64 {
		if i < len(ths) && ths[i] > 0 {
			return ths[i]
		}
		return th
	}

	for qi, opts := range sheet.Choice {
		thQ := rowTh(qi, d.Thresholds)
		for oi, p := range opts {
			ratio := d.ChoiceRatio[qi][oi]
			switch {
			case ratio >= thQ:
				ring(p.X, p.Y, sheet.ChoiceR+4, green)
			case ratio >= thQ-0.12:
				ring(p.X, p.Y, sheet.ChoiceR+4, amber)
			}
		}
	}
	for ci, col := range sheet.Reg {
		thC := rowTh(ci, d.RegThresholds)
		for di, p := range col {
			if ci < len(d.RegRatio) && di < len(d.RegRatio[ci]) && d.RegRatio[ci][di] >= thC {
				ring(p.X, p.Y, sheet.RegR+4, blue)
			}
		}
	}
	for _, p := range d.Locators {
		ring(p.X, p.Y, sheet.LocatorSidePx, red)
	}
	return out
}
