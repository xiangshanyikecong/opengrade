// Package export 的服务端 PDF 输出:手写最小 PDF(无第三方库),
// 把一张位图(成绩报告单)按 A4 幅面嵌入为单页 PDF。
// 采用 DeviceGray + DCTDecode(JPEG)内嵌,体积小、兼容性最好,
// 浏览器/打印机/办公软件均可直接打开与打印。
package export

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"strings"
)

// PagesPDF 把多张位图按指定页面尺寸(mm)生成多页 PDF(兼容包装,见 StreamPagesPDF)。
func PagesPDF(imgs []image.Image, pageWmm, pageHmm float64) ([]byte, error) {
	return PagesPDFIter(len(imgs), func(i int) (image.Image, error) { return imgs[i], nil }, pageWmm, pageHmm)
}

// PagesPDFIter 逐页生成多页 PDF(A-6 内存修复):每页位图即编即弃,输出整册进内存缓冲。
// 大册输出(千人×多页出卡)请改用 StreamPagesPDF 直接写响应流。
func PagesPDFIter(pageCount int, pageAt func(i int) (image.Image, error), pageWmm, pageHmm float64) ([]byte, error) {
	var out bytes.Buffer
	if err := StreamPagesPDF(&out, pageCount, pageAt, pageWmm, pageHmm); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// StreamPagesPDF 把逐页位图流式写成 PDF 到 w:每页即渲即编即写,峰值内存 = 单页位图 +
// 单页 JPEG。PDF 结构与原实现相同(Catalog/Pages/逐页 Page+Content+Image,xref 按对象号
// 记偏移与写入顺序无关);pageCount 须在调用前已知(/Pages /Count 需要)。
// pageAt 返回错误即中断;w 写失败(客户端断开)同样中断并返回错误。
func StreamPagesPDF(w io.Writer, pageCount int, pageAt func(i int) (image.Image, error), pageWmm, pageHmm float64) error {
	p := &pdfWriter{w: w}
	n := pageCount
	total := 3*n + 2
	offsets := make([]int, total)

	const mmToPt = 72.0 / 25.4
	pw := pageWmm * mmToPt
	ph := pageHmm * mmToPt

	p.writeString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	// 每个对象必须以「N 0 obj\n」开头且 xref 偏移精确指向它:xref 坏了小文件还能靠阅读器
	// 全文重建蒙混过关,220MB 大册重建直接超时打不开(2026-10-25 出卡 PDF 打不开的根因)。
	p.objAt(&offsets[0], "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R ", 3+i)
	}
	p.objAt(&offsets[1], fmt.Sprintf("2 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, ""), n))
	content := fmt.Sprintf("q\n%.2f 0 0 %.2f 0 0 cm\n/Im0 Do\nQ\n", pw, ph)
	for i := 0; i < n && p.err == nil; i++ {
		img, err := pageAt(i)
		if err != nil {
			return err
		}
		if img == nil {
			return fmt.Errorf("第 %d 页位图为空", i+1)
		}
		b := img.Bounds()
		var jpg bytes.Buffer
		if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 88}); err != nil {
			return err
		}
		imgID := 2*n + 3 + i
		p.objAt(&offsets[3+i-1], fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
			"/Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\nendobj\n", 3+i, pw, ph, imgID, n+3+i))
		p.objAt(&offsets[n+3+i-1], fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", n+3+i, len(content), content))
		p.markAt(&offsets[imgID-1])
		p.writeString(fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			imgID, b.Dx(), b.Dy(), jpg.Len()))
		p.writeBytes(jpg.Bytes())
		p.writeString("\nendstream\nendobj\n")
	}
	if p.err != nil {
		return p.err
	}

	xrefPos := p.pos
	p.writeString(fmt.Sprintf("xref\n0 %d\n", total+1))
	p.writeString("0000000000 65535 f \n")
	for _, off := range offsets {
		p.writeString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	p.writeString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total+1, xrefPos))
	return p.err
}

// pdfWriter 是带字节计数与错误黏滞的最小写包装(xref 偏移按累计字节数记录)。
type pdfWriter struct {
	w   io.Writer
	pos int
	err error
}

func (p *pdfWriter) writeString(s string) {
	if p.err != nil {
		return
	}
	n, err := io.WriteString(p.w, s)
	p.pos += n
	p.err = err
}

func (p *pdfWriter) writeBytes(b []byte) {
	if p.err != nil {
		return
	}
	n, err := p.w.Write(b)
	p.pos += n
	p.err = err
}

func (p *pdfWriter) objAt(off *int, body string) {
	if p.err != nil {
		return
	}
	*off = p.pos
	p.writeString(body)
}

func (p *pdfWriter) markAt(off *int) {
	if p.err == nil {
		*off = p.pos
	}
}

// ImagePDF 把一张位图按指定页面尺寸(mm)生成单页 PDF 字节。
// 图像铺满整页(等比不变形由调用方负责)。
func ImagePDF(img image.Image, pageWmm, pageHmm float64) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	jpegBytes := jpg.Bytes()

	// PDF 页面坐标单位 1/72 英寸;mm → pt
	const mmToPt = 72.0 / 25.4
	pw := pageWmm * mmToPt
	ph := pageHmm * mmToPt

	var out bytes.Buffer
	var offsets []int
	write := func(s string) {
		out.WriteString(s)
	}
	obj := func(body string) {
		offsets = append(offsets, out.Len())
		write(body)
	}

	write("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	// 1 Catalog / 2 Pages / 3 Page / 4 Contents / 5 Image XObject
	obj("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	obj("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	obj(fmt.Sprintf("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>\nendobj\n", pw, ph))

	content := fmt.Sprintf("q\n%.2f 0 0 %.2f 0 0 cm\n/Im0 Do\nQ\n", pw, ph)
	obj(fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content))

	// 图像 XObject(DeviceGray + DCTDecode)
	offsets = append(offsets, out.Len())
	write(fmt.Sprintf("5 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
		w, h, len(jpegBytes)))
	out.Write(jpegBytes)
	write("\nendstream\nendobj\n")

	// xref
	xrefPos := out.Len()
	write(fmt.Sprintf("xref\n0 %d\n", len(offsets)+1))
	write("0000000000 65535 f \n")
	for _, off := range offsets {
		write(fmt.Sprintf("%010d 00000 n \n", off))
	}
	write(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xrefPos))
	return out.Bytes(), nil
}
