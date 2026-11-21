package omr

// paperan 合成靶场:用与模板同源的字体渲染已知结构的模拟试卷 →(畸变档)旋转/噪声
// + JPEG 压缩回读 → 断言检测出的题目构成与真值一致。
// 模板与靶图同源(font.Default 或内置 5x7 点阵),测的是算法与流水线本身;
// 真实试卷的字体/排版差异靠多字体模板集缓解 + 确认页人工兜底(半自动契约)。
// synth.Distort(透视/光照梯度/更强畸变)的压力档见 paperan_stress_test.go(外部测试包)。

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"strings"
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

// paperCanvas 测试试卷渲染助手(font.Default + 内置点阵双路径)。
type paperCanvas struct {
	cv   *raster.Canvas
	f    *font.Font
	size int
}

func newPaperCanvas(w, h, size int) *paperCanvas {
	return &paperCanvas{cv: raster.NewCanvas(w, h), f: font.Default(), size: size}
}

func (p *paperCanvas) draw(x, y int, s string) {
	if p.f != nil {
		p.f.Draw(p.cv, x, y, s, p.size, 0)
	} else {
		p.cv.Text(x, y, s, p.size/7, 0) // 内置 5x7 点阵
	}
}

func (p *paperCanvas) textW(s string) int {
	if p.f != nil {
		return p.f.WidthPx(s, p.size)
	}
	return len([]rune(s)) * 6 * (p.size / 7)
}

func (p *paperCanvas) gray() *image.Gray {
	return &image.Gray{Pix: p.cv.Pix, Stride: p.cv.W, Rect: image.Rect(0, 0, p.cv.W, p.cv.H)}
}

// drawQuestion 画一道题:题干文字行 + 选项行(选项 contents[k] 前自动带 A~F 前缀,
// 绘于 optXs 固定位;题号前缀 num>0 时输出「num.」)。
func (p *paperCanvas) drawQuestion(x, y, num, opts int, contents []string, optXs []int) {
	if num > 0 {
		p.draw(x, y+45, fmt.Sprintf("%d.", num))
	}
	letters := "ABCDEF"
	for k := 0; k < opts && k < len(contents) && k < len(optXs); k++ {
		p.draw(optXs[k], y+45, string(letters[k])+"."+contents[k])
	}
}

const (
	testW = 1488 // 210mm * 180dpi / 25.4
	testH = 2105
)

var defaultOptXs = []int{200, 560, 920, 1240, 200, 560} // 单栏 4 选项位;紧排/双栏测试自带

// renderTestPaper 渲染一页模拟试卷(A4 @180dpi):
// 9 道客观题(6 单选 4 选项 + 2 判断 A/B + 1 单选,均带题干行,题号 1~9 连续)
// + 2 个留白解答区(40mm / 30mm)。
func renderTestPaper(t *testing.T) *image.Gray {
	t.Helper()
	p := newPaperCanvas(testW, testH, 30)
	p.draw(80, 40, "高一月考数学试卷")
	y := 110
	for i := 1; i <= 9; i++ {
		p.draw(80, y, fmt.Sprintf("第%d题题干:下列说法正确的是。", i))
		contents := []string{"选项甲内容", "选项乙内容", "选项丙内容", "选项丁内容"}
		n := 4
		if i == 7 || i == 8 {
			contents, n = []string{"对", "错"}, 2 // 判断题仍印 A/B(对/错识别另有专项)
		}
		p.drawQuestion(80, y, i, n, contents, defaultOptXs)
		y += 90 // 题干行 + 选项行,行距 45px ≈ 6.3mm(<12mm 段落阈)
	}
	p.draw(80, y, "计算下列各题:")
	y += 40
	mmPx := float64(testW) / 210.0
	y += int(40 * mmPx) // 40mm 留白
	p.draw(80, y, "证明如下命题:")
	y += 40
	y += int(30 * mmPx) // 30mm 留白
	p.draw(80, y, "(完)请核对姓名与考号填写无误后再交卷。") // 收尾行:否则 30mm 留白会并入页尾空白
	y += 45
	for y < 1950 { // 页脚填充:内容铺到页底附近,页尾余白 <25mm 不产生主观区
		p.draw(80, y, "本页以下无正文")
		y += 45
	}
	return p.gray()
}

func TestAnalyzePaperClean(t *testing.T) {
	if !glyphSetsUsable() {
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
		if q.Number != i+1 {
			t.Fatalf("第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
		if q.StemImg == nil {
			t.Fatalf("第 %d 题题干截图缺失", i+1)
		}
		if q.Note != "" {
			t.Fatalf("清晰件第 %d 题不应有低置信说明:%s", i+1, q.Note)
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
	if res.Overlay == nil || len(res.Rects) == 0 || a.Width == 0 || a.Height == 0 {
		t.Fatal("叠加图/检测矩形/坐标系尺寸不应为空")
	}
	for _, n := range a.Notes {
		if strings.Contains(n, "疑似漏检") || strings.Contains(n, "跳变") {
			t.Fatalf("清晰件不应有漏检告警:%s", n)
		}
	}
}

func TestAnalyzePaperDistorted(t *testing.T) {
	if !glyphSetsUsable() {
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

// renderTwoColPaper 构造双栏排版页:左右两栏内容流彼此独立(右栏节距 58px、行起点错开
// 23px,真实分栏两栏行网格不会全程对齐),左栏 3 题 + 右栏 2 题,题号 1~5 跨栏连续。
func renderTwoColPaper(t *testing.T) *image.Gray {
	t.Helper()
	p := newPaperCanvas(testW, testH, 22)
	leftOptXs := []int{85, 220, 355, 490}
	rightOptXs := []int{805, 940, 1075, 1210}
	y := 60
	for i := 1; i <= 3; i++ { // 左栏(节距 45px)
		p.draw(40, y, fmt.Sprintf("第%d题题干:下列说法正确的是。", i))
		p.drawQuestion(40, y, i, 4, []string{"选项甲", "选项乙", "选项丙", "选项丁"}, leftOptXs)
		y += 90
	}
	y = 83
	for i := 4; i <= 5; i++ { // 右栏(节距 58px,行起点错开 23px)
		p.draw(760, y, fmt.Sprintf("第%d题题干:下列说法正确的是。", i))
		p.drawQuestion(760, y, i, 4, []string{"选项甲", "选项乙", "选项丙", "选项丁"}, rightOptXs)
		y += 116
	}
	y = 340
	ry := 363
	for y < testH-160 || ry < testH-160 { // 两栏页脚填充(节距随各自内容流:左 45 右 58),避免页尾大空白
		if y < testH-160 {
			p.draw(40, y, "本栏以下无正文")
			y += 45
		}
		if ry < testH-160 {
			p.draw(760, ry, "本栏以下无正文")
			ry += 58
		}
	}
	return p.gray()
}

func TestAnalyzePaperTwoColumn(t *testing.T) {
	// 双栏排版:左右栏分别识别再拼接,题号跨栏连续
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderTwoColPaper(t))
	if err != nil {
		t.Fatalf("双栏件应可识别,却报错:%v", err)
	}
	a := res.Analysis
	if len(a.Questions) != 5 {
		t.Fatalf("双栏件客观题数应 5,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Options != 4 || q.Kind != "choice" {
			t.Fatalf("双栏件第 %d 题应 4 选项单选,得到 %d/%s", i+1, q.Options, q.Kind)
		}
		if q.Number != i+1 {
			t.Fatalf("双栏件第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
	}
	twoCol := false
	for _, n := range a.Notes {
		if strings.Contains(n, "双栏") {
			twoCol = true
		}
	}
	if !twoCol {
		t.Fatalf("双栏件应出「已按双栏识别」提示:%v", a.Notes)
	}
}

// renderNumberedPaper 按给定题号序列渲染试卷(每题 = 题干行 + 4 选项行)。
func renderNumberedPaper(t *testing.T, nums []int) *image.Gray {
	t.Helper()
	p := newPaperCanvas(testW, testH, 30)
	p.draw(80, 40, "测试卷")
	y := 110
	for _, n := range nums {
		p.draw(80, y, fmt.Sprintf("第%d题题干:下列说法正确的是。", n))
		p.drawQuestion(80, y, n, 4, []string{"选项甲内容", "选项乙内容", "选项丙内容", "选项丁内容"}, defaultOptXs)
		y += 90
		if y > testH-400 {
			break
		}
	}
	for y < 1950 {
		p.draw(80, y, "本页以下无正文")
		y += 45
	}
	return p.gray()
}

func TestAnalyzePaperNumberGaps(t *testing.T) {
	// 题号 1,2,5:第 3、4 题漏检 → 自动补占位题,让漏检显式可见
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderNumberedPaper(t, []int{1, 2, 5}))
	if err != nil {
		t.Fatal(err)
	}
	a := res.Analysis
	if len(a.Questions) != 5 {
		t.Fatalf("补占位后应 5 题,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Number != i+1 {
			t.Fatalf("第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
	}
	for _, i := range []int{2, 3} {
		if !strings.Contains(a.Questions[i].Note, "疑似漏检") {
			t.Fatalf("占位题 %d 应带漏检说明:%s", i+1, a.Questions[i].Note)
		}
	}
}

func TestAnalyzePaperMultiDigit(t *testing.T) {
	// 多位题号:一簇内连续数字(10、11)与三位数(105);11→105 跳变 93 > 上限 → 只告警不补题
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderNumberedPaper(t, []int{9, 10, 11, 105}))
	if err != nil {
		t.Fatal(err)
	}
	a := res.Analysis
	if len(a.Questions) != 4 {
		t.Fatalf("应 4 题,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, want := range []int{9, 10, 11, 105} {
		if a.Questions[i].Number != want {
			t.Fatalf("第 %d 题题号应 %d,得到 %d", i+1, want, a.Questions[i].Number)
		}
	}
	if !strings.Contains(a.Questions[3].Note, "逐题核对") {
		t.Fatalf("超大跳变应只告警:%s", a.Questions[3].Note)
	}
}

// renderTightPaper 紧排选项卷:选项间 ~1.2mm 间隙(数学卷「A.1 B.2 C.3 D.4」典型排印),
// 默认档(1.8mm)会把整行并成一簇,须细档重切才能识别。
func renderTightPaper(t *testing.T) *image.Gray {
	t.Helper()
	p := newPaperCanvas(testW, testH, 30)
	p.draw(80, 40, "紧排选项测试卷")
	gapPx := 3 // 墨迹间隙 ≈1mm(<1.8mm 默认档,须细档重切)
	y := 110
	for i := 1; i <= 5; i++ {
		p.draw(80, y, fmt.Sprintf("第%d题题干:下列说法正确的是。", i))
		p.draw(80, y+45, fmt.Sprintf("%d.", i))
		letters := "ABCD"
		x := 200
		for k := 0; k < 4; k++ {
			s := string(letters[k]) + ".3"
			p.draw(x, y+45, s)
			x += p.textW(s) + gapPx
		}
		y += 90
	}
	for y < 1950 {
		p.draw(80, y, "本页以下无正文")
		y += 45
	}
	return p.gray()
}

func TestAnalyzePaperTightOptions(t *testing.T) {
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderTightPaper(t))
	if err != nil {
		t.Fatal(err)
	}
	a := res.Analysis
	if len(a.Questions) != 5 {
		t.Fatalf("紧排件应 5 题,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Options != 4 || q.Kind != "choice" {
			t.Fatalf("紧排件第 %d 题应 4 选项单选,得到 %d/%s", i+1, q.Options, q.Kind)
		}
		if q.Number != i+1 {
			t.Fatalf("紧排件第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
		if !strings.Contains(q.Note, "紧") {
			t.Fatalf("紧排件第 %d 题应带紧排提示:%s", i+1, q.Note)
		}
	}
}

// renderDuiCuoPaper 判断题印「对/错」(无 A/B 前缀)的卷面。
func renderDuiCuoPaper(t *testing.T) *image.Gray {
	t.Helper()
	p := newPaperCanvas(testW, testH, 30)
	p.draw(80, 40, "判断题测试卷")
	y := 110
	for i := 1; i <= 3; i++ {
		p.draw(80, y, fmt.Sprintf("第%d题题干:判断下列说法正误。", i))
		p.draw(80, y+45, fmt.Sprintf("%d.", i))
		p.draw(200, y+45, "对.说法甲完全正确")
		p.draw(700, y+45, "错.说法乙存在明显错误")
		y += 90
	}
	for y < 1950 {
		p.draw(80, y, "本页以下无正文")
		y += 45
	}
	return p.gray()
}

func TestAnalyzePaperDuiCuo(t *testing.T) {
	// 判断题印「对/错」:按对/错字形识别为 judge(印 A/B 的走字母通道,见 Clean)
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	res, err := AnalyzePaper(renderDuiCuoPaper(t))
	if err != nil {
		t.Fatal(err)
	}
	a := res.Analysis
	if len(a.Questions) != 3 {
		t.Fatalf("对错件应 3 题,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Kind != "judge" || q.Options != 2 {
			t.Fatalf("对错件第 %d 题应 2 选项判断,得到 %d/%s(%s)", i+1, q.Options, q.Kind, q.Note)
		}
		if !strings.Contains(q.Note, "对/错") {
			t.Fatalf("对错件第 %d 题应带对错识别提示:%s", i+1, q.Note)
		}
		if q.Number != i+1 {
			t.Fatalf("对错件第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
	}
}

func TestAnalyzePaperPhotoBackground(t *testing.T) {
	// 手机照片形态:试卷周围带暗色桌面背景 → 纸面裁剪后应得到与贴边扫描一致的结果
	if !glyphSetsUsable() {
		t.Skip("字形模板渲染异常(环境无可用字体),跳过靶场")
	}
	paper := renderTestPaper(t)
	cw, ch := paper.Rect.Dx()+400, paper.Rect.Dy()+400
	cv := raster.NewCanvas(cw, ch)
	cv.FillRect(0, 0, cw, ch, 130) // 桌面灰
	for y := 0; y < paper.Rect.Dy(); y++ {
		copy(cv.Pix[(y+200)*cw+200:(y+200)*cw+200+paper.Rect.Dx()], paper.Pix[y*paper.Stride:(y+1)*paper.Stride])
	}
	res, err := AnalyzePaper(cv.ToGray())
	if err != nil {
		t.Fatalf("照片背景件分析失败:%v", err)
	}
	a := res.Analysis
	if len(a.Questions) != 9 {
		t.Fatalf("照片背景件应 9 题,得到 %d:%+v", len(a.Questions), a.Questions)
	}
	for i, q := range a.Questions {
		if q.Number != i+1 {
			t.Fatalf("照片背景件第 %d 题题号应 %d,得到 %d", i+1, i+1, q.Number)
		}
	}
}

func TestAnalyzePaperEmpty(t *testing.T) {
	blank := image.NewGray(image.Rect(0, 0, testW, testH))
	for i := range blank.Pix { // NewGray 零值是全黑,这里要全白纸面
		blank.Pix[i] = 255
	}
	if _, err := AnalyzePaper(blank); err == nil {
		t.Fatal("空白页应报错(未检测到文字行)")
	}
	// 全黑页(反色/黑版):防御性报错,不产出垃圾草稿
	black := image.NewGray(image.Rect(0, 0, testW, testH))
	if _, err := AnalyzePaper(black); err == nil {
		t.Fatal("全黑页应报错")
	}
}
