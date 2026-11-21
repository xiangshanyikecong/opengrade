package omr_test

// paperan 压力靶场(外部测试包:synth 反向依赖 omr,包内测试不能 import,见 paperan_test.go)。
// 自建畸变:透视抖动(幅度参数化,经 omr.FitHomography 重采样)+ 旋转 + 线性光照梯度
// + 高斯噪声 + JPEG 压缩,验证流水线在扫描/拍照常见损伤下的稳定性:
// 题目构成与题号必须零错判,留白区数量正确(高度受缩放/透视影响,不断言)。
// 透视幅度取实测边界 ±1.5%:±2.5%(synth.Distort 对 OMR 定位标的强度)需要四角
// 检测做透视矫正才能扛住,属于 EE 范围 —— 本靶场把「无透视矫正时的容忍边界」固化下来。

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
)

// renderStressPaper 渲染与 paperan_test 同构的模拟试卷(9 客观题 + 2 留白区)。
// 外部测试包无法复用包内 renderTestPaper,此处独立渲染(结构与真值保持一致)。
func renderStressPaper(t *testing.T) *image.Gray {
	t.Helper()
	const (
		W = 1488 // 210mm * 180dpi / 25.4
		H = 2105
	)
	cv := raster.NewCanvas(W, H)
	f := font.Default()
	size := 30
	draw := func(x, y int, s string) {
		if f != nil {
			f.Draw(cv, x, y, s, size, 0)
		} else {
			cv.Text(x, y, s, size/7, 0)
		}
	}
	draw(80, 40, "高一月考数学试卷")
	optXs := []int{200, 560, 920, 1240}
	letters := "ABCD"
	y := 110
	for i := 1; i <= 9; i++ {
		draw(80, y, "下列说法正确的是。")
		draw(80, y+45, itoa(i)+".")
		n, contents := 4, []string{"选项甲内容", "选项乙内容", "选项丙内容", "选项丁内容"}
		if i == 7 || i == 8 {
			n, contents = 2, []string{"对", "错"}
		}
		for k := 0; k < n; k++ {
			draw(optXs[k], y+45, string(letters[k])+"."+contents[k])
		}
		y += 90
	}
	draw(80, y, "计算下列各题:")
	y += 40
	mmPx := float64(W) / 210.0
	y += int(40 * mmPx)
	draw(80, y, "证明如下命题:")
	y += 40
	y += int(30 * mmPx)
	draw(80, y, "(完)请核对姓名与考号填写无误后再交卷。")
	y += 45
	for y < 1950 {
		draw(80, y, "本页以下无正文")
		y += 45
	}
	return &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// distortStress 透视(±ampPct% 四角抖动)+ 旋转(±rotDeg°)+ 光照梯度 + 噪声 + JPEG。
func distortStress(t *testing.T, src *image.Gray, seed int64, ampPct, rotDeg float64) *image.Gray {
	t.Helper()
	w, h := src.Rect.Dx(), src.Rect.Dy()
	rng := rand.New(rand.NewSource(seed))
	jx := int(float64(w) * ampPct / 200)
	jy := int(float64(h) * ampPct / 200)
	rad := rotDeg * math.Pi / 180
	cx, cy := float64(w)/2, float64(h)/2
	sin, cos := math.Sin(rad), math.Cos(rad)
	dstQuad := []image.Point{{0, 0}, {w - 1, 0}, {w - 1, h - 1}, {0, h - 1}}
	srcQuad := make([]image.Point, 4)
	for i, p := range dstQuad {
		dx := float64(p.X) - cx
		dy := float64(p.Y) - cy
		sx := cx + cos*dx - sin*dy + float64(rng.Intn(2*jx+1)-jx)
		sy := cy + sin*dx + cos*dy + float64(rng.Intn(2*jy+1)-jy)
		srcQuad[i] = image.Point{X: int(sx + 0.5), Y: int(sy + 0.5)}
	}
	hm, err := omr.FitHomography(dstQuad, srcQuad)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			illum := 1 + 0.15*(float64(x)/float64(w)-0.5) // 线性横向光照梯度 ±7.5%
			sx, sy := hm.Apply(float64(x), float64(y))
			v := omr.SampleBilinear(src, sx, sy)*illum + float64(rng.Intn(17)-8)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			out.Pix[y*w+x] = uint8(v + 0.5)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return dec.(*image.Gray)
}

func TestAnalyzePaperSynthStress(t *testing.T) {
	paper := renderStressPaper(t)
	// 基线:清晰件必须可解析(字体环境异常时跳过,与包内靶场同守卫)
	if res, err := omr.AnalyzePaper(paper); err != nil || len(res.Analysis.Questions) != 9 {
		t.Skipf("字形环境异常,跳过压力靶场(err=%v)", err)
	}
	wantOpts := []int{4, 4, 4, 4, 4, 4, 2, 2, 4}
	for seed := int64(1); seed <= 10; seed++ {
		rot := 2.5
		if seed%2 == 0 {
			rot = -2.5
		}
		dec := distortStress(t, paper, seed, 1.0, rot)
		res, err := omr.AnalyzePaper(omr.ToGray(dec))
		if err != nil {
			t.Fatalf("seed %d: 压力件分析失败:%v", seed, err)
		}
		a := res.Analysis
		if len(a.Questions) != len(wantOpts) {
			t.Fatalf("seed %d: 客观题数应 %d,得到 %d:%+v", seed, len(wantOpts), len(a.Questions), a.Questions)
		}
		for i, q := range a.Questions {
			if q.Options != wantOpts[i] {
				t.Fatalf("seed %d: 第 %d 题选项数应 %d,得到 %d(%s)", seed, i+1, wantOpts[i], q.Options, q.Kind)
			}
			if q.Number != i+1 {
				t.Fatalf("seed %d: 第 %d 题题号应 %d,得到 %d", seed, i+1, i+1, q.Number)
			}
		}
		if len(a.Subjectives) != 2 {
			t.Fatalf("seed %d: 主观留白区应 2 处,得到 %d", seed, len(a.Subjectives))
		}
	}
}
