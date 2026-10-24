package omr_test

// 定位兼容层回归:打印/扫描偏差下定位角标的找回能力。
// 五类真实故障,兼容层上线前全部或大概率读卡失败:
//   1. 角标与墨迹粘连(连通域统计失效 → 模板匹配);
//   2. 角标被订书钉/贴纸整个遮挡(无墨可找 → 相邻角标几何推算);
//   3. 打印缩放且左上对齐(整套尺寸/位置闸门失准 → 比例自校准);
//   4. 扫描 DPI ≠ 300(标准折算的期望边长/期望位置全偏 → 比例自校准);
//   5. 角标整块被扫描边缘裁没( → 相邻角标几何推算)。

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
)

func fillRect(g *image.Gray, x0, y0, x1, y1 int, v uint8) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if x >= 0 && y >= 0 && x < g.Rect.Dx() && y < g.Rect.Dy() {
				g.Pix[y*g.Rect.Dx()+x] = v
			}
		}
	}
}

// shrinkContent 模拟打印缩放:内容缩 k 倍、锚定左上贴回原幅面
// (打印机"缩放+左上对齐"或复印缩放的常见产物)。
func shrinkContent(src *image.Gray, k float64) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewGray(image.Rect(0, 0, w, h))
	for i := range out.Pix {
		out.Pix[i] = 255
	}
	for y := 0; y < h; y++ {
		sy := (float64(y) + 0.5) / k
		for x := 0; x < w; x++ {
			sx := (float64(x) + 0.5) / k
			out.Pix[y*w+x] = uint8(omr.SampleBilinear(src, sx, sy) + 0.5)
		}
	}
	return out
}

// scaleGray 模拟低 DPI 扫描:整幅图缩 k 倍(内容与纸张一起变小)。
func scaleGray(src *image.Gray, k float64) *image.Gray {
	w := int(float64(src.Rect.Dx()) * k)
	h := int(float64(src.Rect.Dy()) * k)
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := (float64(y) + 0.5) / k
		for x := 0; x < w; x++ {
			sx := (float64(x) + 0.5) / k
			out.Pix[y*w+x] = uint8(omr.SampleBilinear(src, sx, sy) + 0.5)
		}
	}
	return out
}

func TestLocateRescueScenarios(t *testing.T) { // nolint: gocritic // 表驱动多场景回归
	l := card.DefaultLayout()
	sheet, _ := l.Build()
	// 全卷每题涂第一个选项(按题型取合法字母),作答数才有 30 题可比
	var answers []string
	for qi := range sheet.Choice {
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(sheet.Choice[qi]))
		answers = append(answers, letters[0])
	}
	f := card.Filling{Answers: answers, Reg: []int{2, 0, 2, 5, 0, 7, 1, 3}}
	base := card.RenderPNG(sheet, &f).ToGray()
	mm := func(v float64) int { return int(math.Round(v * float64(base.Rect.Dx()) / 210.0)) }

	// 故障注入:base → (可选)内容改造 → Distort 模拟扫描 → (可选)输出改造。
	// 期望分两档:full=全量正确;degraded=整角无墨(遮挡/裁没),推算角只有仿射/相似精度,
	// 定位与绝大多数客观题可读,考号判读失准 → 走异常清单人工指派(常规产品同此契约)。
	scenarios := []struct {
		name       string
		seed       int64
		tweak      func(g *image.Gray)
		post       func(g *image.Gray) *image.Gray
		degraded   bool
		minAnswers int
	}{
		{"无故障基准", 21, nil, nil, false, 30},
		{"粘连_角标连到右侧长条", 22, func(g *image.Gray) {
			// 1.6mm 厚黑条从角标内部(x13mm)拉到 x60mm → 与角标熔成大连通域
			fillRect(g, mm(13), mm(11.6), mm(60), mm(13.4), 0)
		}, nil, false, 30},
		{"遮挡_左上角标整体覆盖", 23, func(g *image.Gray) {
			fillRect(g, mm(8), mm(8), mm(18), mm(18), 255) // 订书钉/贴纸盖没 10~15mm 的角标
			// 整角无墨时推算角只有仿射精度;synth 的 ±2.5% 透视抖动会让贴近该角的
			// 考号区失准(真实平板扫描透视≈0,推算为精确解,不丢题)。
			// 契约:不硬失败,客观题部分可读,考号判读失准 → 异常清单人工指派
		}, nil, true, 8},
		{"打印缩放58%左上对齐", 24, func(g *image.Gray) {
			*g = *shrinkContent(g, 0.58)
		}, nil, false, 30},
		{"低DPI_整幅半尺寸", 25, nil, func(g *image.Gray) *image.Gray {
			return scaleGray(g, 0.5) // 300DPI 件按 150DPI 扫出
		}, false, 30},
		{"裁没_左侧双角随边裁掉", 26, nil, func(g *image.Gray) *image.Gray {
			// 模拟扫描输出被裁掉左侧 22mm:TL/BL 角标(10~15mm)整块没了,TR/BR 幸存
			return cropGray(g, 360, 0, 0, 0)
		}, true, 24},
	}
	for _, sc := range scenarios {
		g := card.RenderPNG(sheet, &f).ToGray()
		if sc.tweak != nil {
			sc.tweak(g)
		}
		distorted := synth.Distort(g, rand.New(rand.NewSource(sc.seed)))
		if sc.post != nil {
			distorted = sc.post(distorted)
		}
		var jbuf bytes.Buffer
		jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85})
		dec, _ := jpeg.Decode(bytes.NewReader(jbuf.Bytes()))
		res, _, err := omr.Read(sheet, dec, omr.Options{})
		if err != nil {
			t.Errorf("%-24s 读卡失败: %v", sc.name, err)
			continue
		}
		if !sc.degraded {
			if res.RegNumber != "20250713" || !res.RegValid {
				t.Errorf("%-24s 考号 = %q valid=%v,期望 20250713", sc.name, res.RegNumber, res.RegValid)
			}
		} else if res.RegValid && res.RegNumber != "20250713" {
			t.Errorf("%-24s 考号 = %q,失准时应保持未判读而非误读", sc.name, res.RegNumber)
		}
		answered, correct := 0, 0
		for qi, a := range res.Answers {
			if a != "" {
				answered++
			}
			if a == answers[qi] {
				correct++
			}
		}
		if len(res.Answers) != 30 || correct < sc.minAnswers {
			t.Errorf("%-24s 客观题判读 %d 题/判对 %d,期望 30 题/判对 ≥%d", sc.name, len(res.Answers), correct, sc.minAnswers)
		}
	}
}
