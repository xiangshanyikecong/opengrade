package omr

// paperan 合成靶场:用与模板同源的字体渲染已知结构的模拟试卷 →(畸变档)synth.Distort
// + JPEG 压缩回读 → 断言检测出的题目构成与真值一致。
// 模板与靶图同源(font.Default 或内置 5x7 点阵),测的是算法与流水线本身;
// 真实试卷的字体/排版差异靠确认页人工兜底(半自动契约)。

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// distortForTest 轻量畸变(固定倾斜 + 高斯噪声):synth 包反向依赖 omr,包内测试不能
// import(环),此处等价复刻最影响 paperan 的两类损伤——旋转与 JPEG 压缩在测试体内做。
func distortForTest(t *testing.T, src *image.Gray, seed int64) *image.Gray {
	t.Helper()
	rot := rotateGray(src, 2.5)
	rng := rand.New(rand.NewSource(seed))
	out := image.NewGray(rot.Rect)
	for i, p := range rot.Pix {
		v := int(p) + rng.Intn(21) - 10
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		out.Pix[i] = uint8(v)
	}
	return out
}

// renderTestPaper 渲染一页模拟试卷(A4 @180dpi):
// 6 道单选(4 选项)+ 2 道判断(A/B)+ 1 道单选 + 2 个留白解答区(40mm / 30mm)。
func renderTestPaper(t *testing.T) *image.Gray {
	t.Helper()
	const (
		W = 1488 // 210mm * 180dpi / 25.4
		H = 2105
	)
	cv := raster.NewCanvas(W, H)
	f := font.Default()
	size := 30 // 字号 px(≈4.2mm)
	draw := func(x, y int, s string) {
		if f != nil {
			f.Draw(cv, x, y, s, size, 0)
		} else {
			cv.Text(x, y, s, size/7, 0) // 内置 5x7 点阵
		}
	}
	draw(80, 40, "高一月考数学试卷")
	// 选项分绘在固定 x 位(模拟真实试卷的选项排版;连排字符串的词间距 <簇分割阈,不真实)
	optXs := []int{200, 560, 920, 1240}
	letters := "ABCD"
	y := 110
	for i := 1; i <= 9; i++ {
		draw(80, y, fmt.Sprintf("%d.", i))
		contents := []string{"选项甲内容", "选项乙内容", "选项丙内容", "选项丁内容"}
		n := 4
		if i == 7 || i == 8 {
			contents, n = []string{"对", "错"}, 2
		}
		for k := 0; k < n; k++ {
			draw(optXs[k], y, string(letters[k])+"."+contents[k])
		}
		y += 45 // 行距 45px ≈ 6.3mm(<12mm 段落阈)
	}
	draw(80, y, "计算下列各题:")
	y += 40
	mmPx := float64(W) / 210.0
	y += int(40 * mmPx) // 40mm 留白
	draw(80, y, "证明如下命题:")
	y += 40
	y += int(30 * mmPx) // 30mm 留白
	draw(80, y, "(完)请核对姓名与考号填写无误后再交卷。") // 收尾行:否则 30mm 留白会并入页尾空白
	y += 45
	for y < 1950 { // 页脚填充:内容铺到页底附近,页尾余白 <25mm 不产生主观区
		draw(80, y, "本页以下无正文")
		y += 45
	}
	return &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
}

func TestAnalyzePaperClean(t *testing.T) {
	if glyphTemplates() == nil {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderTestPaper(t))
	if err != nil {
		t.Fatalf("清晰件分析失败:%v", err)
	}
	a := res.Analysis
	wantOpts := []int{4, 4, 4, 4, 4, 4, 2, 2, 4}
	if len(a.Questions) != len(wantOpts) {
		t.Fatalf("客观题数应 %d,得到 %d:%+v", len(wantOpts), len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Options != wantOpts[i] {
			t.Fatalf("第 %d 题选项数应 %d,得到 %d(%s)", i+1, wantOpts[i], q.Options, q.Kind)
		}
		wantKind := "choice"
		if wantOpts[i] == 2 {
			wantKind = "judge"
		}
		if q.Kind != wantKind {
			t.Fatalf("第 %d 题题型应 %s,得到 %s", i+1, wantKind, q.Kind)
		}
	}
	if len(a.Subjectives) != 2 {
		t.Fatalf("主观留白区应 2 处,得到 %d:%+v", len(a.Subjectives), a.Subjectives)
	}
	// 40mm 留白 → 高度 36mm(扣 4mm 书写边距);30mm → 26mm;允许 ±3mm
	for i, want := range []float64{36, 26} {
		if got := a.Subjectives[i].HeightMM; got < want-3 || got > want+3 {
			t.Fatalf("主观区 %d 高度应 %.0f±3mm,得到 %.1f", i+1, want, got)
		}
	}
	if a.SkewDeg != 0 {
		t.Fatalf("清晰件倾斜角应为 0,得到 %.1f", a.SkewDeg)
	}
	if res.Overlay == nil {
		t.Fatal("叠加标注图不应为空")
	}
}

func TestAnalyzePaperDistorted(t *testing.T) {
	if glyphTemplates() == nil {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	src := renderTestPaper(t)
	distorted := distortForTest(t, src, 7)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := AnalyzePaper(ToGray(dec))
	if err != nil {
		t.Fatalf("畸变件分析失败:%v", err)
	}
	a := res.Analysis
	wantOpts := []int{4, 4, 4, 4, 4, 4, 2, 2, 4}
	if len(a.Questions) != len(wantOpts) {
		t.Fatalf("畸变件客观题数应 %d,得到 %d(skew=%.1f):%+v", len(wantOpts), len(a.Questions), a.SkewDeg, a.Questions)
	}
	bad := 0
	for i, q := range a.Questions {
		if q.Options != wantOpts[i] {
			bad++
			t.Logf("第 %d 题选项数 %d,应为 %d", i+1, q.Options, wantOpts[i])
		}
	}
	if bad > 0 {
		t.Fatalf("畸变件选项数错判 %d 题(倾斜 %.1f°)", bad, a.SkewDeg)
	}
	if len(a.Subjectives) != 2 {
		t.Fatalf("畸变件主观留白区应 2 处,得到 %d:%+v", len(a.Subjectives), a.Subjectives)
	}
}

// renderTwoColPaper 构造双栏排版页(左右两栏内容流独立:右栏错位 20px、节距不同)。
func renderTwoColPaper(t *testing.T) *image.Gray {
	t.Helper()
	const W, H = 1488, 2105
	cv := raster.NewCanvas(W, H)
	f := font.Default()
	draw := func(x, y int, s string) {
		if f != nil {
			f.Draw(cv, x, y, s, 26, 0)
		} else {
			cv.Text(x, y, s, 4, 0)
		}
	}
	gutter1 := W/2 + 40 // 右栏起点;左栏内容须在 gutter 之前结束
	y := 60
	for y < H-120 {
		draw(60, y, "A.选项甲内容 B.选项乙内容") // 左栏行(短内容,不越过中缝)
		draw(gutter1+20, y+20, "A.选项甲内容 B.选项乙内容 C.选项丙内容")
		y += 42
		draw(gutter1+20, y+20, "A.选项甲内容 B.选项乙内容 C.选项丙内容")
		y += 51
	}
	return &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
}

func TestAnalyzePaperTwoColumn(t *testing.T) {
	// 双栏排版:必须报错拒绝而不是给出错误草稿
	if _, err := AnalyzePaper(renderTwoColPaper(t)); err == nil {
		t.Fatal("双栏排版应报错拒绝")
	}
}

func TestAnalyzePaperEmpty(t *testing.T) {
	blank := image.NewGray(image.Rect(0, 0, 1488, 2105))
	for i := range blank.Pix { // NewGray 零值是全黑,这里要全白纸面
		blank.Pix[i] = 255
	}
	if _, err := AnalyzePaper(blank); err == nil {
		t.Fatal("空白页应报错(未检测到文字行)")
	}
	// 全黑页(反色/黑版):防御性报错,不产出垃圾草稿
	black := image.NewGray(image.Rect(0, 0, 1488, 2105))
	if _, err := AnalyzePaper(black); err == nil {
		t.Fatal("全黑页应报错")
	}
}
