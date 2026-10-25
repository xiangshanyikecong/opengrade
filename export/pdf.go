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
	"strings"
)

// PagesPDF 把多张位图按指定页面尺寸(mm)生成多页 PDF(兼容包装,见 PagesPDFIter)。
func PagesPDF(imgs []image.Image, pageWmm, pageHmm float64) ([]byte, error) {
	return PagesPDFIter(len(imgs), func(i int) (image.Image, error) { return imgs[i], nil }, pageWmm, pageHmm)
}

// PagesPDFIter 逐页生成多页 PDF(A-6 内存修复):每页位图即编即弃,
// 峰值内存 = 输出缓冲 + 单页。此前全部页面位图同时驻留(千人成绩条 ≈ 500 页
// A4@150DPI 灰度 ≈ 1GB+)再统一 JPEG 编码,班级多的学校一次导出即 OOM。
// PDF 结构与 PagesPDF 相同(Catalog/Pages/逐页 Page+Content+Image),仅把
// 对象写入顺序改为逐页交错 —— xref 按"对象号→偏移"记录,与写入顺序无关。
func PagesPDFIter(pageCount int, pageAt func(i int) (image.Image, error), pageWmm, pageHmm float64) ([]byte, error) {
	n := pageCount
	total := 3*n + 2
	offsets := make([]int, total)
	var out bytes.Buffer
	write := func(s string) { out.WriteString(s) }
	obj := func(id int, body string) {
		offsets[id-1] = out.Len()
		write(body)
	}

	const mmToPt = 72.0 / 25.4
	pw := pageWmm * mmToPt
	ph := pageHmm * mmToPt

	write("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R ", 3+i)
	}
	obj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, ""), n))
	content := fmt.Sprintf("q\n%.2f 0 0 %.2f 0 0 cm\n/Im0 Do\nQ\n", pw, ph)
	for i := 0; i < n; i++ {
		img, err := pageAt(i)
		if err != nil {
			return nil, err
		}
		if img == nil {
			return nil, fmt.Errorf("第 %d 页位图为空", i+1)
		}
		b := img.Bounds()
		var jpg bytes.Buffer
		if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 88}); err != nil {
			return nil, err
		}
		imgID := 2*n + 3 + i
		obj(3+i, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] "+
			"/Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\nendobj\n", pw, ph, imgID, n+3+i))
		obj(n+3+i, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content))
		offsets[imgID-1] = out.Len()
		write(fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			imgID, b.Dx(), b.Dy(), jpg.Len()))
		out.Write(jpg.Bytes())
		write("\nendstream\nendobj\n")
	}

	xrefPos := out.Len()
	write(fmt.Sprintf("xref\n0 %d\n", total+1))
	write("0000000000 65535 f \n")
	for _, off := range offsets {
		write(fmt.Sprintf("%010d 00000 n \n", off))
	}
	write(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", total+1, xrefPos))
	return out.Bytes(), nil
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
