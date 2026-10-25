package export

// 全班成绩条连排批量 PDF(P2 家长签字回执):每页 A4 纵向排 2 张成绩条,
// 条间画裁切线;每条含考试名/学科/日期、姓名/班级/考号、总分/满分、
// 年级与班级排名、均分参照与家长签字线。零依赖 raster/font 栈渲染。

import (
	"fmt"
	"image"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// SlipRow 是一张成绩条的渲染输入(字段已格式化好)。
type SlipRow struct {
	Title     string // 考试标题(卷名·学科)
	Date      string
	Student   string
	Class     string
	Reg       string
	Score     string // 如 "86 / 100"
	Rank      string // 年级排名
	ClassRank string // 班级排名
	Mean      string // 年级均分
}

const (
	slipsDPI   = 150.0
	slipsPageW = 210.0
	slipsPageH = 297.0
	slipsPerPg = 2
	slipsGapMM = 6.0 // 两张成绩条之间的裁切带
)

// slipsMMToPx int(mm → px @150DPI)
func slipsMMToPx(mm float64) int { return int(mm/25.4*slipsDPI + 0.5) }

// RenderSlip 把一张成绩条渲染为灰度位图(宽 A4 内容区,高约半页)。
func RenderSlip(row SlipRow) *image.Gray {
	margin := 14.0
	w := slipsMMToPx(slipsPageW)
	h := slipsMMToPx((slipsPageH-slipsGapMM)/slipsPerPg - 6)
	c := raster.NewCanvas(w, h)
	tf := font.Default()
	black := uint8(0)

	draw := func(xMM, yMM, sizeMM float64, s string) {
		px := slipsMMToPx(sizeMM)
		if tf != nil {
			tf.Draw(c, slipsMMToPx(xMM), slipsMMToPx(yMM)-px, s, px, black)
			return
		}
		c.Text(slipsMMToPx(xMM), slipsMMToPx(yMM)-px+px/4, asciiOrDots(s), maxInt(1, px/6), black)
	}
	drawCenter := func(xMM, yMM, sizeMM float64, s string) {
		px := slipsMMToPx(sizeMM)
		if tf != nil {
			tf.DrawCenter(c, slipsMMToPx(xMM), slipsMMToPx(yMM)-px/2, s, px, black)
			return
		}
		c.TextCenter(slipsMMToPx(xMM), slipsMMToPx(yMM), asciiOrDots(s), maxInt(1, px/6), black)
	}
	drawRight := func(xMM, yMM, sizeMM float64, s string) {
		px := slipsMMToPx(sizeMM)
		if tf != nil {
			tf.DrawRight(c, slipsMMToPx(xMM), slipsMMToPx(yMM)-px, s, px, black)
			return
		}
		c.TextRight(slipsMMToPx(xMM), slipsMMToPx(yMM), asciiOrDots(s), maxInt(1, px/6), black)
	}

	// 标题行 + 分隔线
	drawCenter(slipsPageW/2, 9, 5.2, nonEmpty(row.Title, "成绩条"))
	if row.Date != "" {
		drawRight(slipsPageW-margin, 9, 3.4, row.Date)
	}
	line := func(yMM float64) {
		c.FillRect(slipsMMToPx(margin), slipsMMToPx(yMM), w-2*slipsMMToPx(margin), 1, black)
	}
	line(12.5)

	// 基本信息
	y := 18.5
	draw(margin, y, 4, fmt.Sprintf("姓名:%s", nonEmpty(row.Student, "-")))
	draw(margin+52, y, 4, fmt.Sprintf("班级:%s", nonEmpty(row.Class, "-")))
	draw(margin+100, y, 4, fmt.Sprintf("考号:%s", nonEmpty(row.Reg, "-")))
	y += 8
	// 成绩行
	draw(margin, y, 5.2, fmt.Sprintf("总分:%s", nonEmpty(row.Score, "-")))
	draw(margin+62, y, 4, fmt.Sprintf("年级排名:%s", nonEmpty(row.Rank, "-")))
	draw(margin+104, y, 4, fmt.Sprintf("班级排名:%s", nonEmpty(row.ClassRank, "-")))
	if row.Mean != "" {
		drawRight(slipsPageW-margin, y, 4, fmt.Sprintf("年级均分 %s", row.Mean))
	}
	y += 10
	line(y - 4)

	// 家长签字线
	draw(margin, y+1.5, 4, "家长签字:________________________")
	drawRight(slipsPageW-margin, y+1.5, 3, "如有疑问请联系任课教师复核")
	return c.ToGray()
}

// SlipsPDF 把成绩条逐张渲染并连排为多页 PDF:每页 2 张,条间留 6mm 裁切带。
// 逐页即渲即弃(PagesPDFIter):全部页面位图同时驻留会随班级规模线性膨胀
// (千人 ≈ 500 页 × ~2.2MB 位图)直至 OOM。
func SlipsPDF(rows []SlipRow) ([]byte, error) {
	if len(rows) == 0 {
		rows = []SlipRow{}
	}
	n := (len(rows) + slipsPerPg - 1) / slipsPerPg
	if n == 0 {
		n = 1 // 空班也出一张空版,避免空 PDF 边界
	}
	return PagesPDFIter(n, func(i int) (image.Image, error) {
		lo := i * slipsPerPg
		if lo >= len(rows) {
			return composeSlipPage(nil), nil
		}
		return composeSlipPage(rows[lo:min(lo+slipsPerPg, len(rows))]), nil
	}, slipsPageW, slipsPageH)
}

// composeSlipPage 把最多 2 张成绩条合成一页 A4(中间画裁切虚线)。
func composeSlipPage(rows []SlipRow) *image.Gray {
	w := slipsMMToPx(slipsPageW)
	h := slipsMMToPx(slipsPageH)
	c := raster.NewCanvas(w, h) // 全白画布
	black := uint8(0)
	for i, row := range rows {
		slipsH := (slipsPageH - slipsGapMM) / slipsPerPg
		yTopMM := float64(i) * (slipsH + slipsGapMM)
		slips := RenderSlip(row)
		// 位图等宽,逐行搬像素
		b := slips.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				c.Set(slipsMMToPx(0)+x, slipsMMToPx(yTopMM)+y, slips.GrayAt(x, y).Y)
			}
		}
	}
	// 裁切虚线(两张之间)
	if len(rows) > 1 {
		yMM := (slipsPageH-slipsGapMM)/slipsPerPg + slipsGapMM/2
		y := slipsMMToPx(yMM)
		for x := slipsMMToPx(8); x < w-slipsMMToPx(8); x += 8 {
			c.FillRect(x, y, 4, 1, black)
		}
	}
	return c.ToGray()
}
