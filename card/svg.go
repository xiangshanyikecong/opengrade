package card

import (
	"bytes"
	"fmt"
	"strings"
)

// RenderSVG 输出矢量版答题卡(mm 精度,可直接送打印),供浏览器打印或印刷。
// SVG 文本交由渲染方字体解析,天然支持中文卷头与题干。
func RenderSVG(s *Sheet, f *Filling) []byte {
	g := mustGeometry(s.Layout)
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

	// 考号涂卡圆
	for ci, col := range g.reg {
		for d, p := range col {
			if f != nil && ci < len(f.Reg) && f.Reg[ci] == d {
				fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"black\"/>\n", p.x, p.y, regRMM)
			} else {
				fmt.Fprintf(&b, "<circle cx=\"%.2f\" cy=\"%.2f\" r=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"%.2f\"/>\n", p.x, p.y, regRMM, strokeMM)
			}
		}
	}

	// 文本标签
	text(&b, g.title.x, g.title.y, 6, xmlEscape(n.Title))
	if len(g.reg) > 0 {
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

	// 主观题区块:标签行(左对齐)+ 作答框
	for i, sg := range g.subjectives {
		label := itoa(len(g.choice)+i+1) + ". "
		if i < len(n.Subjectives) && n.Subjectives[i].Label != "" {
			label += n.Subjectives[i].Label + " "
		}
		label += "(" + kindName(sg.kind) + " " + trimScore(sg.score) + "分)"
		textStart(&b, marginX+1, sg.qNum.y, 3.5, xmlEscape(label))
		fmt.Fprintf(&b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"0.4\"/>\n",
			marginX, sg.boxTop, PageWMM-2*marginX, sg.boxH)
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

// textStart 输出左对齐文本;(x,y) 为文本包围盒左缘中点。
func textStart(b *bytes.Buffer, x, y, size float64, s string) {
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"%.2f\" text-anchor=\"start\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n", x, y, size, s)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
