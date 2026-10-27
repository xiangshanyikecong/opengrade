package omr

import (
	"image"
	"math"
	"testing"
)

// drawRing 在二值图上以 (cx,cy) 为圆心画宽约 2px 的实心圆环(模拟印刷泡圈)。
func drawRing(b *bitmap, cx, cy, r int) {
	for dy := -r - 2; dy <= r+2; dy++ {
		for dx := -r - 2; dx <= r+2; dx++ {
			d := math.Sqrt(float64(dx*dx + dy*dy))
			if d >= float64(r-1) && d <= float64(r+1) {
				x, y := cx+dx, cy+dy
				if x >= 0 && y >= 0 && x < b.W && y < b.H {
					b.D[y*b.W+x] = 1
				}
			}
		}
	}
}

// TestEstimateRowShift 验证印刷漂移估计:环心整体偏移 (3,2) 时应量出同值;
// 无印刷环(全白)时返回不可信。
func TestEstimateRowShift(t *testing.T) {
	b := &bitmap{W: 400, H: 200, D: make([]byte, 400*200)}
	pts := []image.Point{{60, 60}, {160, 60}, {260, 60}, {60, 140}, {160, 140}}
	const r = 18
	for _, p := range pts {
		drawRing(b, p.X+3, p.Y+2, r)
	}
	dx, dy, ok := estimateRowShift(b, pts, r)
	if !ok || math.Abs(dx-3) > 0.6 || math.Abs(dy-2) > 0.6 {
		t.Fatalf("漂移估计 = (%.2f, %.2f, %v), 期望 ≈(3, 2, true)", dx, dy, ok)
	}
	empty := &bitmap{W: 400, H: 200, D: make([]byte, 400*200)}
	if _, _, ok := estimateRowShift(empty, pts, r); ok {
		t.Fatal("全白图应返回不可信")
	}
	// 已涂满的泡:环带全墨,质心仍应落在(偏移后的)圆心
	filled := &bitmap{W: 400, H: 200, D: make([]byte, 400*200)}
	for _, p := range pts {
		drawRing(filled, p.X+3, p.Y+2, r)
		fillDisk(filled, p.X+3, p.Y+2, r-2)
	}
	dx, dy, ok = estimateRowShift(filled, pts, r)
	if !ok || math.Abs(dx-3) > 0.6 || math.Abs(dy-2) > 0.6 {
		t.Fatalf("涂满泡的漂移估计 = (%.2f, %.2f, %v), 期望 ≈(3, 2, true)", dx, dy, ok)
	}
}

// fillDisk 填充实心圆。
func fillDisk(b *bitmap, cx, cy, r int) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				x, y := cx+dx, cy+dy
				if x >= 0 && y >= 0 && x < b.W && y < b.H {
					b.D[y*b.W+x] = 1
				}
			}
		}
	}
}

func TestShiftPoints(t *testing.T) {
	pts := []image.Point{{10, 20}}
	out := shiftPoints(pts, 2.4, -1.6)
	if out[0].X != 12 || out[0].Y != 18 {
		t.Fatalf("shiftPoints = %v", out[0])
	}
}
