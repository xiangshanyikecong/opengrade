package raster

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// exifTiff 构造最小 TIFF(EXIF 头之后的部分):TIFF 头 + IFD0 仅一个 Orientation 条目。
func exifTiff(orient int, bigEndian bool) []byte {
	var bo binary.ByteOrder = binary.LittleEndian
	magic := "II"
	if bigEndian {
		bo, magic = binary.BigEndian, "MM"
	}
	var buf bytes.Buffer
	put := func(v any) {
		if err := binary.Write(&buf, bo, v); err != nil {
			panic(err) // bytes.Buffer 写入不会失败
		}
	}
	buf.WriteString(magic)
	put(uint16(42))     // TIFF 魔数
	put(uint32(8))      // IFD0 偏移
	put(uint16(1))      // IFD 条目数
	put(uint16(0x0112)) // Orientation tag
	put(uint16(3))      // SHORT
	put(uint32(1))      // count
	put(uint16(orient))
	put(uint16(0)) // 值域填充
	put(uint32(0)) // 下一 IFD
	return buf.Bytes()
}

// app1Segment 把任意 payload 包成 APP1 Exif 段(含 6 字节 Exif 魔数)。
func app1Segment(tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	n := len(payload) + 2
	return append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, payload...)
}

// jpegWithExifAPP1 生成 16×8 四分区灰度 JPEG,并在 SOI 后插入给定的段字节流。
func jpegWithExifAPP1(t *testing.T, segs ...[]byte) (raw []byte, src *image.Gray) {
	t.Helper()
	src = image.NewGray(image.Rect(0, 0, 16, 8))
	quads := [4]uint8{10, 80, 150, 220} // 左上/右上/左下/右下
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			q := 0
			if x >= 8 {
				q |= 1
			}
			if y >= 4 {
				q |= 2
			}
			src.SetGray(x, y, color.Gray{Y: quads[q]})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	raw = buf.Bytes()
	out := append([]byte{}, raw[:2]...) // SOI
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, raw[2:]...), src
}

// TestApplyOrientationAll8 用四角互异的 2×2 图钉死全部 8 种 Orientation 的像素映射。
func TestApplyOrientationAll8(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 2))
	src.SetGray(0, 0, color.Gray{Y: 10})  // 左上
	src.SetGray(1, 0, color.Gray{Y: 80})  // 右上
	src.SetGray(0, 1, color.Gray{Y: 150}) // 左下
	src.SetGray(1, 1, color.Gray{Y: 220}) // 右下
	// 各 Orientation 下目标网格(左上/右上/左下/右下),按变换语义手工推导:
	//   1 原样            2 水平镜像        3 旋转180°        4 垂直镜像
	//   5 主对角转置      6 顺时针90°       7 反对角镜像      8 逆时针90°
	want := map[int][4]int{
		1: {10, 80, 150, 220},
		2: {80, 10, 220, 150},
		3: {220, 150, 80, 10},
		4: {150, 220, 10, 80},
		5: {10, 150, 80, 220},
		6: {150, 10, 220, 80},
		7: {220, 80, 150, 10},
		8: {80, 220, 10, 150},
	}
	grayAt := func(img image.Image, x, y int) int {
		r, _, _, _ := img.At(x, y).RGBA()
		return int(r >> 8)
	}
	for o := 1; o <= 8; o++ {
		dst := applyOrientation(src, o)
		if dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy(); dw != 2 || dh != 2 {
			t.Fatalf("orient=%d 尺寸 %dx%d, 期望 2x2", o, dw, dh)
		}
		got := [4]int{grayAt(dst, 0, 0), grayAt(dst, 1, 0), grayAt(dst, 0, 1), grayAt(dst, 1, 1)}
		if got != want[o] {
			t.Fatalf("orient=%d 网格 %v, 期望 %v", o, got, want[o])
		}
	}
	// 非法值原样返回
	if applyOrientation(src, 9) != image.Image(src) {
		t.Fatal("非法 orientation 应原样返回源图")
	}
}

func TestJPEGOrientationParse(t *testing.T) {
	raw, _ := jpegWithExifAPP1(t)
	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"II 小端 orient=6", spliceAfterSOI(raw, app1Segment(exifTiff(6, false))), 6},
		{"MM 大端 orient=6", spliceAfterSOI(raw, app1Segment(exifTiff(6, true))), 6},
		{"MM 大端 orient=8", spliceAfterSOI(raw, app1Segment(exifTiff(8, true))), 8},
		{"orient=1 无需转正", spliceAfterSOI(raw, app1Segment(exifTiff(1, false))), 1},
		{"无 EXIF 段", raw, 1},
		{"XMP 在前 EXIF 在后", spliceAfterSOI(raw, xmpSegment(), app1Segment(exifTiff(3, false))), 3},
		{"截断的 TIFF 头", spliceAfterSOI(raw, app1Segment([]byte("II\x2a\x00"))), 1},
		{"未知字节序", spliceAfterSOI(raw, app1Segment(append([]byte("XX\x2a\x00\x00\x00\x00\x00"), make([]byte, 20)...))), 1},
		{"APP1 长度越界", spliceAfterSOI(raw, []byte{0xFF, 0xE1, 0xFF, 0xFF, 'E', 'x', 'i', 'f', 0, 0}), 1},
		{"SOI 后直接 SOS", append(append([]byte{}, raw[:2]...), 0xFF, 0xDA), 1},
		{"非 JPEG 数据", []byte{0x89, 'P', 'N', 'G'}, 1},
		{"空数据", nil, 1},
	}
	for _, c := range cases {
		if got := jpegOrientation(c.data); got != c.want {
			t.Errorf("%s: jpegOrientation=%d, 期望 %d", c.name, got, c.want)
		}
	}
}

// TestDecodeImageEndToEnd 真实 JPEG + EXIF 走完整 DecodeImage:尺寸转置 + 象限落位。
func TestDecodeImageEndToEnd(t *testing.T) {
	data, src := jpegWithExifAPP1(t, app1Segment(exifTiff(6, false)))
	img, err := DecodeImage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Dx(); got != 8 {
		t.Fatalf("顺时针 90° 后宽应为 8, 得到 %d", got)
	}
	if got := img.Bounds().Dy(); got != 16 {
		t.Fatalf("顺时针 90° 后高应为 16, 得到 %d", got)
	}
	// 旋转后象限校验:源左下(150)应转到目标左上,采样各象限中心做最近值匹配。
	quads := []uint8{10, 80, 150, 220}
	nearest := func(v uint32) uint8 {
		best := quads[0]
		for _, q := range quads {
			if abs64(int(v)-int(q)) < abs64(int(v)-int(best)) {
				best = q
			}
		}
		return best
	}
	check := func(x, y int, wantSrcQuadrantValue uint8, label string) {
		r, g, b, _ := img.At(x, y).RGBA()
		gray := (299*r + 587*g + 114*b) / 1000 >> 8
		if got := nearest(gray); got != wantSrcQuadrantValue {
			t.Fatalf("%s: (%d,%d)≈%d, 期望 %d", label, x, y, got, wantSrcQuadrantValue)
		}
	}
	check(2, 2, 150, "旋转后左上=源左下")
	check(6, 2, 10, "旋转后右上=源左上")
	check(2, 14, 220, "旋转后左下=源右下")
	check(6, 14, 80, "旋转后右下=源右上")
	_ = src
}

func TestDecodeImagePassthrough(t *testing.T) {
	// 无 EXIF 的 JPEG:原样解码,尺寸不变
	raw, _ := jpegWithExifAPP1(t)
	img, err := DecodeImage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 16 || img.Bounds().Dy() != 8 {
		t.Fatalf("无 EXIF 不应转正: %v", img.Bounds())
	}
	// PNG 走 image.Decode 注册路径,正常解码
	var pbuf bytes.Buffer
	pngSrc := image.NewGray(image.Rect(0, 0, 4, 3))
	if err := png.Encode(&pbuf, pngSrc); err != nil {
		t.Fatal(err)
	}
	pimg, err := DecodeImage(bytes.NewReader(pbuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if pimg.Bounds().Dx() != 4 || pimg.Bounds().Dy() != 3 {
		t.Fatalf("PNG 尺寸错误: %v", pimg.Bounds())
	}
	// 坏数据返回错误而非 panic
	if _, err := DecodeImage(bytes.NewReader([]byte{0xFF, 0xD8, 0xFF})); err == nil {
		t.Fatal("截断 JPEG 应报解码错误")
	}
}

func spliceAfterSOI(jpeg []byte, segs ...[]byte) []byte {
	out := append([]byte{}, jpeg[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, jpeg[2:]...)
}

func xmpSegment() []byte {
	payload := []byte("http://ns.adobe.com/xap/1.0/\x00<x/>")
	n := len(payload) + 2
	return append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, payload...)
}

func abs64(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
