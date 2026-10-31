package card

import (
	"bytes"
	"fmt"

	"github.com/opengrade/opengrade/raster"
)

// 一卡多人(拼版):一张 A3 横放纸印左右两份完整 A4 卡,各带独立身份区(考号/码),
// 中缝印虚线裁切线。版式模型仍是一份 A4 Layout —— 拼版只是渲染层输出形态,
// OMR 侧(omr.SplitImpose)在定位之前把扫描图切回单卡,再走既有读卡管线。

// ImposeWMM 拼版母版宽度:mm(两份 A4 并排)。高度仍为 PageHMM。
func ImposeWMM() float64 { return 2 * PageWMM }

// ImposeSVG 输出 A3 横放拼版母版 SVG:左右各印一份卡体(平移 <g> 包裹,元素零改动),
// sb/fb 可为 nil(名册奇数时的末张只印左半,裁切线照印便于裁下单卡)。
func ImposeSVG(sa, sb *Sheet, fa, fb *Filling) []byte {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%.0fmm\" height=\"%.0fmm\" viewBox=\"0 0 %.0f %.0f\">\n", ImposeWMM(), PageHMM, ImposeWMM(), PageHMM)
	fmt.Fprintf(&b, "<rect x=\"0\" y=\"0\" width=\"%.0f\" height=\"%.0f\" fill=\"white\"/>\n", ImposeWMM(), PageHMM)
	fmt.Fprintf(&b, "<g>")
	writeSVGBody(&b, sa, fa)
	b.WriteString("</g>\n")
	if sb != nil {
		fmt.Fprintf(&b, "<g transform=\"translate(%.0f,0)\">\n", PageWMM)
		writeSVGBody(&b, sb, fb)
		b.WriteString("</g>\n")
	}
	writeCutLineSVG(&b, PageWMM)
	b.WriteString("</svg>\n")
	return b.Bytes()
}

// writeCutLineSVG 在 x 处印竖向虚线裁切线与上下剪刀位缺口。
func writeCutLineSVG(b *bytes.Buffer, x float64) {
	fmt.Fprintf(b, "<line x1=\"%.1f\" y1=\"3\" x2=\"%.1f\" y2=\"%.0f\" stroke=\"black\" stroke-width=\"0.4\" stroke-dasharray=\"3 2.4\"/>\n", x, x, PageHMM-3)
}

// ImposePNG 把两份已渲染的 A4 画布 blit 到一张 A3 横放画布(逐行 copy),
// sb 可为 nil;中缝按 2mm 段/2mm 空画虚线裁切线。
func ImposePNG(sa, sb *raster.Canvas) *raster.Canvas {
	w, h := sa.W, sa.H
	if sb != nil {
		w += sb.W
		if sb.H > h {
			h = sb.H
		}
	}
	m := raster.NewCanvas(w, h)
	blitCanvas(m, sa, 0, 0)
	if sb != nil {
		blitCanvas(m, sb, sa.W, 0)
	}
	drawCutLinePNG(m, sa.W-1)
	return m
}

func blitCanvas(dst, src *raster.Canvas, ox, oy int) {
	for y := 0; y < src.H; y++ {
		dy := oy + y
		if dy < 0 || dy >= dst.H {
			continue
		}
		copy(dst.Pix[dy*dst.W+ox:], src.Pix[y*src.W:y*src.W+src.W])
	}
}

// drawCutLinePNG 在 x 列画竖向虚线(2mm 实 2mm 空,2px 宽)。
func drawCutLinePNG(c *raster.Canvas, x int) {
	if x < 0 || x >= c.W {
		return
	}
	seg := mmToPx(2)
	for y := mmToPx(3); y < c.H-mmToPx(3); y += 2 * seg {
		for k := 0; k < seg && y+k < c.H; k++ {
			c.Set(x, y+k, 0)
			if x+1 < c.W {
				c.Set(x+1, y+k, 0)
			}
		}
	}
}
