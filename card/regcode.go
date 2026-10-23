// regcode.go:考号码区(条码/二维码)的渲染辅助,SVG 与 PNG 两条渲染路径共用。
package card

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/opengrade/opengrade/barcode"
	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/qrcode"
	"github.com/opengrade/opengrade/raster"
)

// regDigitsText 从 Filling 提取考号数字串;Filling 缺失或位数不符时返回 ""
// (母版渲染:只印码区框与提示,不印实际码)。
func regDigitsText(l Layout, f *Filling) string {
	if f == nil || l.RegDigits <= 0 || len(f.Reg) != l.RegDigits {
		return ""
	}
	var sb strings.Builder
	for _, d := range f.Reg {
		if d < 0 || d > 9 {
			return ""
		}
		sb.WriteByte(byte('0' + d))
	}
	return sb.String()
}

// codeLabel 返回码区顶部标签文案。
func codeLabel(kind string) string {
	if kind == "barcode" {
		return "考号条码"
	}
	return "考号二维码"
}

// renderCodeSVG 把码区画进 SVG 母版(mm 坐标)。
func renderCodeSVG(b *bytes.Buffer, cg *codeGeom, l Layout, f *Filling) {
	fmt.Fprintf(b, "<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"none\" stroke=\"black\" stroke-width=\"0.3\"/>\n",
		cg.frame.x, cg.frame.y, cg.frame.w, cg.frame.h)
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"3.5\" text-anchor=\"middle\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n",
		cg.label.x, cg.label.y, codeLabel(cg.kind))
	digits := regDigitsText(l, f)
	if digits == "" {
		fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"3\" text-anchor=\"middle\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n",
			cg.frame.x+cg.frame.w/2, cg.frame.y+cg.frame.h/2, "(按学生生成时印码)")
		return
	}
	switch cg.kind {
	case "barcode":
		if mods, err := barcode.Encode(digits); err == nil {
			// 两侧各留 6mm 静区(Code128 要求 ≥10 模块)
			const quiet = 6.0
			stripW := cg.body.w - 2*quiet
			mw := stripW / float64(len(mods))
			x := cg.body.x + quiet
			for _, m := range mods {
				if m {
					fmt.Fprintf(b, "<rect x=\"%.3f\" y=\"%.2f\" width=\"%.3f\" height=\"%.2f\" fill=\"black\"/>\n", x, cg.body.y, mw, cg.body.h)
				}
				x += mw
			}
		}
	default:
		if m, err := qrcode.EncodeNumeric(digits); err == nil {
			const quiet = 2.0
			mod := cg.body.w / float64(len(m.Dark)+2*quiet)
			for r, row := range m.Dark {
				for c, dark := range row {
					if !dark {
						continue
					}
					fmt.Fprintf(b, "<rect x=\"%.3f\" y=\"%.3f\" width=\"%.3f\" height=\"%.3f\" fill=\"black\"/>\n",
						cg.body.x+(quiet+float64(c))*mod, cg.body.y+(quiet+float64(r))*mod, mod, mod)
				}
			}
		}
	}
	fmt.Fprintf(b, "<text x=\"%.2f\" y=\"%.2f\" font-size=\"4\" text-anchor=\"middle\" dominant-baseline=\"central\" font-family=\"Helvetica, Arial, sans-serif\" fill=\"black\">%s</text>\n",
		cg.text.x, cg.text.y, digits)
}

// drawCodePNG 把码区画进 300 DPI 位图(模块取整像素,条码整体居中于码带)。
func drawCodePNG(c *raster.Canvas, cg *codeGeom, l Layout, f *Filling, tf *font.Font) {
	black := uint8(0)
	drawLabelMM := func(x, y, mmSize float64, s string) {
		px := mmToPx(mmSize)
		if tf != nil {
			tf.DrawCenter(c, mmToPx(x), mmToPx(y)-px/2, s, px, black)
			return
		}
		c.TextCenter(mmToPx(x), mmToPx(y), asciiOrQ(s), max(1, px*5/33), black)
	}
	// 码区装饰框与标签
	drawBox(c, mmToPx(cg.frame.x), mmToPx(cg.frame.y), mmToPx(cg.frame.w), mmToPx(cg.frame.h), 2)
	drawLabelMM(cg.label.x, cg.label.y, 3.5, codeLabel(cg.kind))

	digits := regDigitsText(l, f)
	if digits == "" {
		drawLabelMM(cg.frame.x+cg.frame.w/2, cg.frame.y+cg.frame.h/2, 3, "(按学生生成时印码)")
		return
	}
	bodyW, bodyH := mmToPx(cg.body.w), mmToPx(cg.body.h)
	bx, by := mmToPx(cg.body.x), mmToPx(cg.body.y)
	switch cg.kind {
	case "barcode":
		mods, err := barcode.Encode(digits)
		if err != nil {
			return
		}
		// 两侧各留 6mm 静区(与 SVG 渲染同口径)
		quietPx := mmToPx(6)
		stripW := bodyW - 2*quietPx
		modPx := stripW / len(mods)
		if modPx < 1 {
			return
		}
		totalPx := modPx * len(mods)
		x0 := bx + (bodyW-totalPx)/2
		for i, m := range mods {
			if m {
				c.FillRect(x0+i*modPx, by, modPx, bodyH, black)
			}
		}
	default:
		m, err := qrcode.EncodeNumeric(digits)
		if err != nil {
			return
		}
		const quiet = 2
		modPx := bodyW / (len(m.Dark) + 2*quiet)
		side := modPx * len(m.Dark)
		x0 := bx + (bodyW-side)/2
		y0 := by + (bodyH-side)/2
		for r, row := range m.Dark {
			for col, dark := range row {
				if dark {
					c.FillRect(x0+col*modPx, y0+r*modPx, modPx, modPx, black)
				}
			}
		}
	}
	drawLabelMM(cg.text.x, cg.text.y, 4, digits)
}
