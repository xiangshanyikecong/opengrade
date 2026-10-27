package omr_test

import (
	"image"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// TestReadAdaptiveThresholdFadedMark 验证逐题自适应阈值的核心收益:
// 同一张卡上个别涂得过轻的选项(墨迹占比低于全局阈值,但行内"涂/未涂"
// 台阶依然清晰)不应丢判 —— 模拟铅笔压力轻/局部褪色的场景。
func TestReadAdaptiveThresholdFadedMark(t *testing.T) {
	sheet, f := makeFixture(t)
	gray := card.RenderPNG(sheet, f).ToGray()

	// 把第 5 题(0-based 4)A 选项的涂点冲淡到约 1/3 墨迹密度:
	// 低于全局阈值 0.42,但与同行空白泡(≈0)的台阶仍可分。
	p := sheet.Choice[4][0]
	diluteDisk(gray, p.X, p.Y, int(float64(sheet.ChoiceR)*0.8))

	res, dbg, err := omr.Read(sheet, gray, omr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answers[4] != "A" {
		t.Fatalf("淡化涂点应被自适应阈值找回: 第 5 题识别 = %q, 警告=%v", res.Answers[4], res.Warnings)
	}
	if dbg.Thresholds[4] >= 0.42 {
		t.Fatalf("第 5 题应下探自适应阈值: got %v", dbg.Thresholds[4])
	}
	// 干净行保持全局阈值(零回归):饱满涂点行的行内中点被夹持回全局。
	if dbg.Thresholds[0] > 0.42+1e-9 {
		t.Fatalf("干净行不应抬高阈值: got %v", dbg.Thresholds[0])
	}
	for qi, want := range f.Answers {
		if res.Answers[qi] != want {
			t.Fatalf("第 %d 题识别 = %q, 期望 %q", qi+1, res.Answers[qi], want)
		}
	}
}

// diluteDisk 把 (cx,cy) 半径 r 圆域内约 2/3 的像素置为纸白,保留 1/3 墨迹,
// 模拟压力偏轻/褪色的浅淡涂点。
func diluteDisk(g *image.Gray, cx, cy, r int) {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	r2 := r * r
	for dy := -r; dy <= r; dy++ {
		y := cy + dy
		if y < 0 || y >= h {
			continue
		}
		for dx := -r; dx <= r; dx++ {
			x := cx + dx
			if x < 0 || x >= w || dx*dx+dy*dy > r2 {
				continue
			}
			if (x+y)%3 != 0 {
				g.Pix[y*w+x] = 255
			}
		}
	}
}
