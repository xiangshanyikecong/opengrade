package card

import (
	"bytes"
	"fmt"
	"strings"
)

// RenderSVG 输出矢量版答题卡(mm 精度,可直接送打印),供浏览器打印或印刷。
// SVG 文本交由渲染方字体解析,天然支持中文卷头与题干。
func RenderSVG(s *Sheet, f *Filling) []byte {
	g := mustGeometry(s)
	n := s.Layout
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%.0fmm\" height=\"%.0fmm\" viewBox=\"0 0 %.0f %.0f\">\n", PageWMM, PageHMM, PageWMM, PageHMM)
	fmt.Fprintf(&b, "<rect x=\"0\" y=\"0\" width=\"%.0f\" height=\"%.0f\" fill=\"white\"/>\n", PageWMM, PageHMM)

	// 四角定位方块
	const half = locatorSideMM / 2
	for _, p := range g.locators {
		fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"black\"/>\n", p.x-half, p.y-half, locatorSideMM, locatorSideMM)
	}

	// 客观题涂卡圆
	for qi, opts := range g.choice {
		letters := OptionLetters(n.Questions[qi].Kind, len(opts))
		for oi, p := range opts {
			if f != nil && qi < len(f.Answers) && strings.Contains(f.Answers[qi], letters[oi]) {
				fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"black\"/>\n", p.x, p.y, choiceRMM)
			} else {
				fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x, p.y, choiceRMM, strokeMM)
			}
		}
	}

	// 考号涂卡圆(纯码制通道不印)
	if n.RegHasFill() {
		for ci, col := range g.reg {
			for d, p := range col {
				if f.regFilledAt(ci, d) {
					fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"black\"/>\n", p.x, p.y, regRMM)
				} else {
					fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x, p.y, regRMM, strokeMM)
				}
			}
		}
	}

	// 考号码区(条码/二维码通道):学生卡印实际码,母版只印区框与提示
	if cg := g.code; cg != nil {
		renderCodeSVG(&b, cg, n, f)
	}

	// 文本标签(每页右上角标页码,多页卡逐页可归属)
	text(&b, g.title.x, g.title.y, 6, xmlEscape(n.Title))
	// 卷种点(AB 卷):A 卷第 1 点实心、B 卷第 2 点实心,旁印 A/B 卷标签供人眼分堆
	if len(g.variantDots) > 0 {
		const halfDot = pageDotSide / 2
		solidIdx := -1
		if n.Variant == "A" {
			solidIdx = 0
		} else if n.Variant == "B" {
			solidIdx = 1
		}
		for i, p := range g.variantDots {
			if i == solidIdx {
				fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"black\"/>\n", p.x-halfDot, p.y-halfDot, pageDotSide, pageDotSide)
			} else {
				fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x-halfDot, p.y-halfDot, pageDotSide, pageDotSide, strokeMM)
			}
		}
		textEnd(&b, g.variantDots[0].x-pageDotPitch*0.9, g.variantDots[0].y, 3.5, n.Variant+"卷")
	}
	if s.PageCount > 1 {
		// 右端留在 190mm:紧贴定位块会被连成连通域,破坏角标定位
		textEnd(&b, PageWMM-marginX-6, g.title.y, 3.5, fmt.Sprintf("第 %d/%d 页", s.PageNo, s.PageCount))
		// 页点:第 k 点实心 = 第 k 页(OMR 机器判读页号,按页归组/缺页告警用)
		const halfDot = pageDotSide / 2
		for i, p := range g.pageDots {
			if i == s.PageNo-1 {
				fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"black\"/>\n", p.x-halfDot, p.y-halfDot, pageDotSide, pageDotSide)
			} else {
				fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x-halfDot, p.y-halfDot, pageDotSide, pageDotSide, strokeMM)
			}
		}
	}
	if n.RegHasFill() && len(g.reg) > 0 {
		text(&b, g.regLabel.x, g.regLabel.y, 3.5, "REG NO")
		for d, p := range g.regRowLbl {
			text(&b, p.x, p.y, 3, digitName(d))
		}
	}
	text(&b, g.nameLabel.x, g.nameLabel.y, 3.5, "NAME")
	fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"0.4\"/>\n",
		g.nameBoxX, g.nameBoxY, g.nameBoxW, g.nameBoxH)
	for i, p := range g.qNumLbl {
		text(&b, p.x, p.y, 3.5, itoa(i+1))
	}
	for qi, lbls := range g.optLbls {
		letters := OptionLetters(n.Questions[qi].Kind, len(lbls))
		for j, p := range lbls {
			text(&b, p.x, p.y, 3.5, letters[j])
		}
	}

	// 主观题区块:标签行(左对齐)+ 作答框;拆分小问时标签列出小问分值,作答区右上角画逐问得分框;
	// 选做题(版式 v3)在组内第一道题的标签行右端印题号区涂点
	for i, sg := range g.subjectives {
		gi := i // 全局主观题下标(0-based):多页版式下第 2+ 页的 i 是页内序号,须还原
		if i < len(s.pageSubs) {
			gi = s.pageSubs[i]
		}
		label := itoa(len(n.Questions)+gi+1) + ". "
		if gi < len(n.Subjectives) && n.Subjectives[gi].Label != "" {
			label += n.Subjectives[gi].Label + " "
		}
		label += "(" + kindName(sg.kind) + " " + trimScore(sg.score) + "分"
		if len(sg.parts) > 0 {
			label += ":" + partsBreakdown(sg.parts)
		}
		label += ")"
		if sg.elective != nil {
			label += " 选做 " + strings.Join(sg.elective.labels, "/") + "(任选一题,涂所选题号)"
		}
		textStart(&b, marginX+1, sg.qNum.y, 3.5, xmlEscape(label))
		fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"0.4\"/>\n",
			marginX, sg.boxTop, PageWMM-2*marginX, sg.boxH)
		for pi, r := range partScoreBoxes(sg) {
			fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"0.3\"/>\n", r.x, r.y, r.w, r.h)
			text(&b, r.x+r.w/2, r.y+r.h/2, 2.8, xmlEscape(trimScore(sg.parts[pi].Score)+"分"))
		}
		if sg.elective != nil {
			text(&b, sg.elective.prefix.x, sg.elective.prefix.y, 3, "题号")
			for oi, p := range sg.elective.points {
				filled := f != nil && f.Electives != nil && f.Electives[sg.elective.members[0]] == oi
				if filled {
					fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"black\"/>\n", p.x, p.y, electiveRMM)
				} else {
					fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x, p.y, electiveRMM, strokeMM)
				}
				text(&b, p.x-electiveRMM-2.8, p.y, 3, sg.elective.labels[oi])
			}
		}
	}

	b.WriteString("</svg>\n")
	return b.Bytes()
}

func kindName(kind string) string {
	if kind == KindFillBlank {
		return "填空"
	}
	return "解答"
}

func trimScore(v float64) string {
	if v == float64(int(v)) {
		return itoa(int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// text 输出居中文本;(x,y) 为文本包围盒中心,换算为基线位置。
func text(b *bytes.Buffer, x, y, size float64, s string) {
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"%.2f\" text-anchor=\"middle\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n", x, y, size, s)
}

// textEnd 输出右对齐文本;(x,y) 为文本包围盒右缘中点。
func textEnd(b *bytes.Buffer, x, y, size float64, str string) {
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"%.2f\" text-anchor=\"end\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n", x, y, size, xmlEscape(str))
}

// textStart 输出左对齐文本;(x,y) 为文本包围盒左缘中点。
func textStart(b *bytes.Buffer, x, y, size float64, s string) {
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"%.2f\" text-anchor=\"start\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n", x, y, size, s)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
