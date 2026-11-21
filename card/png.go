package card

import (
	"fmt"
	"strings"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// Filling 描述一张"已涂卡"的答题卡:考号各位数字与各题所涂选项。
// 用于模拟学生卷;nil 字段表示留空。
type Filling struct {
	Reg         []int       // 每位考号数字(0~9),长度 = RegDigits;码制通道(条码/二维码)也按它印码
	RegUnfilled []bool      // 考号某位"漏涂"(填涂区不印实心圆,但条码/二维码仍印真实考号;模拟漏涂场景)
	Answers     []string    // 每题如 "A" / "AB" / "T";"" 表示未涂
	Electives   map[int]int // 选做题号区(版式 v3):主观题号(1-based,题号区所在题)→ 涂的第几个选项(0-based);缺省未涂
	CodePrefix  string      // 码内容前缀(AB 卷个性化印刷):考试短号(4 位数字)+卷种位(1=A/2=B),空 = 普通纯考号码
}

// regFilledAt 报告考号第 ci 位是否应涂实心(漏涂位除外)。
func (f *Filling) regFilledAt(ci, d int) bool {
	if f == nil || ci >= len(f.Reg) || f.Reg[ci] != d {
		return false
	}
	return ci >= len(f.RegUnfilled) || !f.RegUnfilled[ci]
}

// RenderPNG 把版式渲染为 300 DPI 灰度位图(可直接打印或送入 OMR)。
// 位图字体仅覆盖 ASCII:卷头/标签中的非 ASCII 字符以 '?' 占位,
// 中文排版请走 SVG 打印路径(或待 M1 嵌入 CJK 字体)。
func RenderPNG(s *Sheet, f *Filling) *raster.Canvas {
	c := raster.NewCanvas(s.Wpx, s.Hpx)
	const black = uint8(0)

	// 四角定位方块
	half := s.LocatorSidePx / 2
	for _, p := range s.Locators {
		c.FillRect(p.X-half, p.Y-half, s.LocatorSidePx, s.LocatorSidePx, black)
	}

	// 客观题涂卡圆:空心描边;被涂的画成实心
	stroke := float64(mmToPx(strokeMM))
	tf := font.Default() // 本机中文字体:缺失时退化为内置 5x7 点阵
	for qi, opts := range s.Choice {
		letters := OptionLetters(s.ChoiceKinds[qi], len(opts))
		for oi, p := range opts {
			r := float64(s.ChoiceR)
			if f != nil && qi < len(f.Answers) && strings.Contains(f.Answers[qi], letters[oi]) {
				c.FillCircle(float64(p.X), float64(p.Y), r-1, black)
			} else {
				c.Ring(float64(p.X), float64(p.Y), r, r-stroke, black)
			}
		}
	}

	// 考号涂卡圆(纯码制通道不印)
	if s.Layout.RegHasFill() {
		for ci, col := range s.Reg {
			for d, p := range col {
				r := float64(s.RegR)
				if f.regFilledAt(ci, d) {
					c.FillCircle(float64(p.X), float64(p.Y), r-1, black)
				} else {
					c.Ring(float64(p.X), float64(p.Y), r, r-stroke, black)
				}
			}
		}
	}

	// 卷头与标签文字
	g := mustGeometry(s)

	// 考号码区(条码/二维码):学生卡印实际码,母版只印区框与提示
	if g.code != nil {
		drawCodePNG(c, g.code, s.Layout, f, tf)
	}

	drawLabel := func(cx, cyMM float64, text string, mmSize float64) {
		px := mmToPx(mmSize)
		if tf != nil {
			tf.DrawCenter(c, mmToPx(cx), mmToPx(cyMM)-px/2, text, px, black)
			return
		}
		c.TextCenter(mmToPx(cx), mmToPx(cyMM), asciiOrQ(text), max(1, px*5/33), black)
	}
	drawLabel(g.title.x, g.title.y, s.Layout.Title, 6)
	// 卷种点(AB 卷):A 卷第 1 点实心、B 卷第 2 点实心,旁印 A/B 卷标签供人眼分堆
	if len(g.variantDots) > 0 {
		halfDot := mmToPx(pageDotSide / 2)
		strokePx := float64(mmToPx(strokeMM))
		solidIdx := -1
		if s.Layout.Variant == "A" {
			solidIdx = 0
		} else if s.Layout.Variant == "B" {
			solidIdx = 1
		}
		for i, p := range g.variantDots {
			cx, cy := mmToPx(p.x), mmToPx(p.y)
			if i == solidIdx {
				c.FillRect(cx-halfDot, cy-halfDot, 2*halfDot, 2*halfDot, black)
			} else {
				c.Ring(float64(cx), float64(cy), float64(halfDot), float64(halfDot)-strokePx, black)
			}
		}
		drawLabel(g.variantDots[0].x-pageDotPitch*1.6, g.variantDots[0].y, s.Layout.Variant+"卷", 3.5)
	}
	if s.PageCount > 1 {
		mk := fmt.Sprintf("第 %d/%d 页", s.PageNo, s.PageCount)
		px := mmToPx(3.5)
		rightPx := mmToPx(PageWMM - marginX - 6) // 避开右上定位块
		if tf != nil {
			tf.DrawRight(c, rightPx, mmToPx(g.title.y)-px/2, mk, px, black)
		} else {
			w := raster.TextWidth(asciiOrQ(mk), max(1, px*5/33))
			c.Text(rightPx-w, mmToPx(g.title.y), asciiOrQ(mk), max(1, px*5/33), black)
		}
		// 页点:第 k 点实心 = 第 k 页(OMR 机器判读页号,按页归组/缺页告警用)
		halfDot := mmToPx(pageDotSide / 2)
		strokePx := float64(mmToPx(strokeMM))
		for i, p := range g.pageDots {
			cx, cy := mmToPx(p.x), mmToPx(p.y)
			if i == s.PageNo-1 {
				c.FillRect(cx-halfDot, cy-halfDot, 2*halfDot, 2*halfDot, black)
			} else {
				c.Ring(float64(cx), float64(cy), float64(halfDot), float64(halfDot)-strokePx, black)
			}
		}
	}
	if s.Layout.RegHasFill() && len(s.Reg) > 0 {
		drawLabel(g.regLabel.x, g.regLabel.y, "考号 REG NO", 3.5)
		for d, p := range g.regRowLbl {
			drawLabel(p.x, p.y, digitName(d), 3)
		}
	}
	drawLabel(g.nameLabel.x, g.nameLabel.y, "姓名 NAME", 3.5)
	drawBox(c, mmToPx(g.nameBoxX), mmToPx(g.nameBoxY), mmToPx(g.nameBoxW), mmToPx(g.nameBoxH), 3)

	for i, p := range g.qNumLbl {
		drawLabel(p.x, p.y, itoa(i+1), 3.5)
	}
	for qi, lbls := range g.optLbls {
		for j, p := range lbls {
			drawLabel(p.x, p.y, optionDisplay(s.ChoiceKinds[qi], j, tf != nil), 3.5)
		}
	}

	// 主观题区块:标签行(左对齐,避免长标签溢出左页边距)+ 作答框;拆分小问时画逐问得分框
	for i, sg := range g.subjectives {
		gi := i // 全局主观题下标(0-based):多页版式下第 2+ 页的 i 是页内序号,须还原(与 svg.go 同口径)
		if i < len(s.pageSubs) {
			gi = s.pageSubs[i]
		}
		label := itoa(len(s.Layout.Questions)+gi+1) + ". " + subjectiveTagCN(sg.kind, tf != nil) + " (" + scoreText(sg.score)
		if len(sg.parts) > 0 {
			label += ": " + partsBreakdown(sg.parts)
		}
		label += ")"
		if sg.elective != nil { // 选做题:标签行提示可选题号,右端印题号区
			label += " 选做 " + strings.Join(sg.elective.labels, "/")
		}
		if tf != nil {
			tf.Draw(c, mmToPx(marginX), mmToPx(sg.qNum.y)-mmToPx(3.5)/2, label, mmToPx(3.5), black)
		} else {
			const scale = 5
			c.Text(mmToPx(marginX), mmToPx(sg.qNum.y)-(7*scale)/2, asciiOrQ(label), scale, black)
		}
		x0, y0 := mmToPx(marginX), mmToPx(sg.boxTop)
		drawBox(c, x0, y0, mmToPx(PageWMM-2*marginX), mmToPx(sg.boxH), 3)
		for pi, r := range partScoreBoxes(sg) {
			bx, by, bw, bh := mmToPx(r.x), mmToPx(r.y), mmToPx(r.w), mmToPx(r.h)
			drawBox(c, bx, by, bw, bh, 2)
			scoreLbl := scoreText(sg.parts[pi].Score)
			if tf != nil {
				tf.DrawCenter(c, bx+bw/2, by+bh/2-mmToPx(2.8)/2, scoreLbl, mmToPx(2.8), black)
			} else {
				c.TextCenter(bx+bw/2, by+bh/2, asciiOrQ(scoreLbl), 3, black)
			}
		}
		if sg.elective != nil { // 题号区:涂点 + 各涂点左侧的卷面题号 + "题号"前缀
			drawLabel(sg.elective.prefix.x, sg.elective.prefix.y, "题号", 3)
			stroke := float64(mmToPx(strokeMM))
			for oi, p := range sg.elective.points {
				r := mmToPx(electiveRMM)
				// nil map 取零值 0 恰是第 1 个选项,必须区分"未涂"与"涂第 1 项"
				filled := f != nil && f.Electives != nil && f.Electives[sg.elective.members[0]] == oi
				if filled {
					c.FillCircle(float64(mmToPx(p.x)), float64(mmToPx(p.y)), float64(r)-1, black)
				} else {
					c.Ring(float64(mmToPx(p.x)), float64(mmToPx(p.y)), float64(r), float64(r)-stroke, black)
				}
				drawLabel(p.x-electiveRMM-2.8, p.y, sg.elective.labels[oi], 3)
			}
		}
	}
	return c
}

// optionDisplay 返回选项的显示标签:有中文字体时判断题显示 √/×,数据契约仍为 T/F。
func optionDisplay(kind string, idx int, cjk bool) string {
	if kind == KindJudge && cjk {
		return []string{"√", "×"}[idx]
	}
	return OptionLetters(kind, idx+1)[idx]
}

func subjectiveTagCN(kind string, cjk bool) string {
	if !cjk {
		return subjectiveTag(kind)
	}
	if kind == KindFillBlank {
		return "填空"
	}
	return "解答题"
}

func subjectiveTag(kind string) string {
	if kind == KindFillBlank {
		return "FILL"
	}
	return "ANSWER"
}

func scoreText(v float64) string {
	if v == float64(int(v)) {
		return itoa(int(v)) + " PTS"
	}
	// 一位小数足够演示;真实分值体系在 M3 报表层处理
	return itoa(int(v*10)/10) + " PTS"
}

// asciiOrQ 把非 ASCII 字符替换为 '?',避免位图渲染出空白。
func asciiOrQ(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

func digitName(d int) string { return string(rune('0' + d)) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// mustGeometry 供渲染器使用:Render 的入参 Sheet 一定来自 BuildBook(),不会失败。
func mustGeometry(sh *Sheet) geom {
	g, err := sh.Layout.geometryFor(sh.PageNo, sh.PageCount, sh.pageSubs)
	if err != nil {
		panic("card: 渲染使用了未经 Build 的版式: " + err.Error())
	}
	return g
}

// drawBox 用四条边画矩形框(手写/作答区)。
func drawBox(c *raster.Canvas, x, y, w, h, t int) {
	c.FillRect(x, y, w, t, 0)
	c.FillRect(x, y+h-t, w, t, 0)
	c.FillRect(x, y, t, h, 0)
	c.FillRect(x+w-t, y, t, h, 0)
}
