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
)

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
