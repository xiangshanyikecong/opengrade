package omr_test

import (
	"image"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
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

// TestReadPrintDrift 验证印刷漂移自对齐:逐行走位的卡面经单应矫正后仍残留
// 条带级偏移 —— 饱满涂点仍可读回(回归),而淡涂点会因此掉出判读阈值,
// 自对齐把每行"量出来"的偏移修掉后应能找回。
func TestReadPrintDrift(t *testing.T) {
	sheet, f := makeFixture(t)

	// 纯漂移回归:饱满涂点在 ±6px 走位下不丢题
	rng := rand.New(rand.NewSource(99))
	drifted := synth.Drift(card.RenderPNG(sheet, f).ToGray(), rng, 6)
	res, _, err := omr.Read(sheet, drifted, omr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for qi, want := range f.Answers {
		if res.Answers[qi] != want {
			t.Fatalf("漂移下第 %d 题识别 = %q, 期望 %q", qi+1, res.Answers[qi], want)
		}
	}

	// 漂移 + 淡涂点:第 5 题冲淡到 1/3 密度,自对齐修正条带偏移后应可读回
	gray := card.RenderPNG(sheet, f).ToGray()
	p := sheet.Choice[4][0]
	diluteDisk(gray, p.X, p.Y, int(float64(sheet.ChoiceR)*0.8))
	drifted2 := synth.Drift(gray, rand.New(rand.NewSource(99)), 6)
	res2, _, err := omr.Read(sheet, drifted2, omr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Answers[4] != "A" {
		t.Fatalf("漂移下淡涂点应被自对齐找回: 第 5 题识别 = %q, 警告=%v", res2.Answers[4], res2.Warnings)
	}
	for qi, want := range f.Answers {
		if res2.Answers[qi] != want {
			t.Fatalf("漂移+淡涂第 %d 题识别 = %q, 期望 %q", qi+1, res2.Answers[qi], want)
		}
	}
}
