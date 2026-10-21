package export

// 个人成绩报告单渲染:用零依赖的 raster/font 栈把一张成绩报告排成 A4 灰度位图,
// 再由 ImagePDF 封装为可下载/打印的 PDF。中文字形走系统中文字体发现,
// 找不到字体时位图降级为 ASCII(仍能出 PDF)。
//
// 这是 M3 路线图"服务端 PDF 生成待做"的落地:家长/学生无需浏览器打印,
// 直接下载一份 PDF 回执。

import (
	"fmt"
	"image"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// ReportCard 是个人成绩报告单的渲染输入(字段皆已格式化好,渲染层不再做业务判断)。
type ReportCard struct {
	Title     string // 卷头标题,如 "高一(3)班 月考"
	Subject   string
	Student   string // 姓名(可空)
	Reg       string // 考号
	Class     string // 班级
	Date      string // 考试日期
	Score     string // 总分文本,如 "86 / 100"
	Rank      string // 年级排名文本
	ClassRank string // 班级排名文本
	Mean      string // 均分文本
	Knowledge []KV   // 知识点掌握度(名称/文本)
	Questions []QRow // 逐题明细
	Footer    string // 页脚说明
}

// KV 是键值展示项。
type KV struct {
	Key   string
	Value string
}

// QRow 是逐题明细的一行。
type QRow struct {
	Index   int
	Kind    string
	Max     string
	Mine    string
	Correct string
	Wrong   bool
}

const (
	rcDPI    = 150.0
	rcPageW  = 210.0 // A4 mm
	rcPageH  = 297.0
	rcMargin = 16.0
)

func mmToPxRC(mm float64, dpi float64) int { return int(mm/25.4*dpi + 0.5) }

// RenderReportCard 把报告单渲染为灰度位图(A4 @150DPI)。
func RenderReportCard(rc ReportCard) image.Image {
	w := mmToPxRC(rcPageW, rcDPI)
	h := mmToPxRC(rcPageH, rcDPI)
	c := raster.NewCanvas(w, h)
	tf := font.Default()

	const black = uint8(0)
	// 统一文本绘制:有中文字体走 font.Draw,否则退化为内置点阵(非 ASCII 变 ?)。
	draw := func(xMM, yMM float64, sizeMM float64, s string) {
		px := mmToPxRC(sizeMM, rcDPI)
		x := mmToPxRC(xMM, rcDPI)
		y := mmToPxRC(yMM, rcDPI)
		if tf != nil {
			tf.Draw(c, x, y-px, s, px, black)
			return
		}
		c.Text(x, y-px+px/4, asciiOrDots(s), maxInt(1, px/6), black)
	}
	drawRight := func(xMM, yMM, sizeMM float64, s string) {
		px := mmToPxRC(sizeMM, rcDPI)
		x := mmToPxRC(xMM, rcDPI)
		y := mmToPxRC(yMM, rcDPI)
		if tf != nil {
			tf.DrawRight(c, x, y-px, s, px, black)
			return
		}
		c.TextRight(x, y-px+px/4, asciiOrDots(s), maxInt(1, px/6), black)
	}
	line := func(yMM float64) {
		y := mmToPxRC(yMM, rcDPI)
		c.FillRect(mmToPxRC(rcMargin, rcDPI), y, w-2*mmToPxRC(rcMargin, rcDPI), 1, black)
	}

	y := rcMargin + 8
	title := rc.Title
	if rc.Subject != "" {
		title += " · " + rc.Subject
	}
	drawCenter(c, tf, w/2, mmToPxRC(y, rcDPI), 6, title)
	y += 9
	drawCenter(c, tf, w/2, mmToPxRC(y, rcDPI), 4, "个人成绩报告单")
	y += 6
	line(y)
	y += 7

	// 基本信息(两栏)
	info := []KV{
		{"姓名", rc.Student}, {"考号", rc.Reg},
		{"班级", rc.Class}, {"考试日期", rc.Date},
	}
	for i, kv := range info {
		col := i % 2
		row := i / 2
		x := rcMargin + 2 + float64(col)*90
		draw(x, y+float64(row)*7, 4, fmt.Sprintf("%s:%s", kv.Key, nonEmpty(kv.Value, "-")))
	}
	y += 7*2 + 4
	line(y)
	y += 7

	// 成绩摘要
	draw(rcMargin+2, y, 4.5, fmt.Sprintf("总分:%s", rc.Score))
	draw(rcMargin+62, y, 4.5, fmt.Sprintf("年级排名:%s", nonEmpty(rc.Rank, "-")))
	draw(rcMargin+122, y, 4.5, fmt.Sprintf("班级排名:%s", nonEmpty(rc.ClassRank, "-")))
	y += 7
	draw(rcMargin+2, y, 4, fmt.Sprintf("年级均分:%s", nonEmpty(rc.Mean, "-")))
	y += 8

	// 知识点掌握度
	if len(rc.Knowledge) > 0 {
		draw(rcMargin+2, y, 4.5, "知识点掌握度")
		y += 6
		for _, kv := range rc.Knowledge {
			draw(rcMargin+4, y, 3.8, kv.Key)
			drawRight(rcPageW-rcMargin-2, y, 3.8, kv.Value)
			y += 5.4
		}
		y += 2
	}

	// 逐题明细
	draw(rcMargin+2, y, 4.5, "逐题明细")
	y += 6
	// 表头
	hdr := []struct {
		x   float64
		txt string
	}{
		{rcMargin + 2, "题号"}, {rcMargin + 18, "题型"}, {rcMargin + 40, "满分"},
		{rcMargin + 60, "我的作答/得分"}, {rcMargin + 110, "参考答案"},
	}
	for _, hd := range hdr {
		draw(hd.x, y, 3.6, hd.txt)
	}
	y += 1.5
	line(y)
	y += 4.5
	rowH := 5.0
	for _, q := range rc.Questions {
		if y > rcPageH-rcMargin-14 {
			break // 单页截断,避免溢出(后续可扩展多页)
		}
		draw(rcMargin+2, y, 3.6, fmt.Sprintf("%d", q.Index))
		draw(rcMargin+18, y, 3.6, q.Kind)
		draw(rcMargin+40, y, 3.6, q.Max)
		draw(rcMargin+60, y, 3.6, q.Mine)
		draw(rcMargin+110, y, 3.6, nonEmpty(q.Correct, "-"))
		y += rowH
	}

	// 页脚
	draw(rcMargin+2, rcPageH-rcMargin, 3, rc.Footer)
	return c.ToGray()
}

// drawCenter 居中绘制一段文本。
func drawCenter(c *raster.Canvas, tf *font.Font, cx, yTop, sizeMM int, s string) {
	if tf != nil {
		tf.DrawCenter(c, cx, yTop, s, sizeMM, 0)
		return
	}
	c.TextCenter(cx, yTop+sizeMM/2, asciiOrDots(s), maxInt(1, sizeMM/6), 0)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// asciiOrDots 把非 ASCII 字符替换为点,避免无字体时整行空白。
func asciiOrDots(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			out = append(out, r)
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
