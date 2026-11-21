// exif.go 处理手机/相机照片的 EXIF Orientation:解码时把像素转到"观看方向"。
// 老师用手机拍答题卡是很常见的上传路径,横持拍摄的照片像素是横向存放的,
// 仅靠 Orientation 标记摆正 —— Go 的 image/jpeg 不会自动应用,不解正则
// 整卷躺倒,OMR 定位与切片展示全错。
// 解析为手写的最小 JPEG APP1/TIFF 遍历(只取 Orientation 一个 tag,零第三方依赖),
// 对损坏/非标数据一律按"无旋转"处理,绝不 panic。
package raster

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/jpeg" // 注册解码器:上传主流格式
	_ "image/png"
	"io"
)

// DecodeImage 解码 PNG/JPEG 并应用 EXIF Orientation(若有)。
// 替代裸 image.Decode 的统一入口:所有上传/CLI 的图像读取都应走这里。
func DecodeImage(r io.Reader) (image.Image, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	orient := 1
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xD8 { // SOI:仅 JPEG 可能带 EXIF
		orient = jpegOrientation(b)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if orient > 1 {
		img = applyOrientation(img, orient)
	}
	return img, nil
}

// jpegOrientation 遍历 JPEG 段找到 APP1 Exif,返回 Orientation(1~8,无则 1)。
// 段结构破坏/超界一律返回 1:EXIF 必在 SOS 之前的段区里,遍历到即止。
func jpegOrientation(b []byte) int {
	i := 2 // 跳过 SOI
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xFF { // 部分编码器输出的填充字节
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) { // TEM/RSTn/EOI:无长度域
			i += 2
			continue
		}
		if marker == 0xDA { // SOS:之后是熵编码数据
			return 1
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		if segLen < 2 || i+2+segLen > len(b) {
			return 1
		}
		payload := b[i+4 : i+2+segLen]
		if marker == 0xE1 && len(payload) >= 14 && string(payload[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(payload[6:]); o > 1 {
				return o
			}
		}
		i += 2 + segLen
	}
	return 1
}

// tiffOrientation 从 TIFF 头解析 IFD0 的 Orientation(0x0112)。
// 两种字节序都支持;Orientation 恒为内联 SHORT,个别设备误写 LONG 也兼容。
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch {
	case t[0] == 'I' && t[1] == 'I':
		bo = binary.LittleEndian
	case t[0] == 'M' && t[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	ifd0 := int(bo.Uint32(t[4:8]))
	if ifd0 < 8 || ifd0+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd0 : ifd0+2]))
	for e := 0; e < n; e++ {
		off := ifd0 + 2 + e*12
		if off+12 > len(t) {
			return 1
		}
		entry := t[off : off+12]
		if bo.Uint16(entry[0:2]) != 0x0112 {
			continue
		}
		if bo.Uint32(entry[4:8]) != 1 { // count
			return 1
		}
		var v uint32
		switch bo.Uint16(entry[2:4]) { // type
		case 3: // SHORT:值内联在 4 字节值域前 2 字节
			v = uint32(bo.Uint16(entry[8:10]))
		case 4: // LONG
			v = bo.Uint32(entry[8:12])
		default:
			return 1
		}
		if v >= 1 && v <= 8 {
			return int(v)
		}
		return 1
	}
	return 1
}

// applyOrientation 按 EXIF Orientation 把像素重排到观看方向:
// 2=水平镜像 3=旋转180° 4=垂直镜像 5=主对角转置 6=顺时针90° 7=反对角镜像 8=逆时针90°。
func applyOrientation(src image.Image, orient int) image.Image {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw, dh := sw, sh
	if orient >= 5 && orient <= 8 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orient {
			case 2:
				sx, sy = sw-1-x, y
			case 3:
				sx, sy = sw-1-x, sh-1-y
			case 4:
				sx, sy = x, sh-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, sh-1-x
			case 7:
				sx, sy = sw-1-y, sh-1-x
			case 8:
				sx, sy = sw-1-y, x
			default:
				return src
			}
			dst.Set(x, y, src.At(sb.Min.X+sx, sb.Min.Y+sy))
		}
	}
	return dst
}
