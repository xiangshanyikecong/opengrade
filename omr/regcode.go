// regcode.go:考号码制通道(条码/二维码)在矫正后标准网格上的解码与双通道融合。
package omr

import (
	"image"

	"github.com/opengrade/opengrade/barcode"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/qrcode"
)

// decodeRegCode 按 Sheet 声明的码制解码考号区。
// 输入是透视矫正后的产物:灰度标准网格 + 其二值化位图,几何即出卡时的码区矩形。
func decodeRegCode(rectGray *image.Gray, bin *bitmap, sheet *card.Sheet) (string, bool) {
	switch sheet.RegCodeKind {
	case "barcode":
		return decodeBarcodeVotes(bin, sheet.RegCode)
	case "qr":
		s, err := qrcode.DecodeRegion(rectGray, sheet.RegCode)
		return s, err == nil
	}
	return "", false
}

// decodeBarcodeVotes 在码区矩形内取 13 条水平扫描线分别解码,
// 多数票(≥2 票)胜出;单线噪声被 mod-103 校验与投票双重过滤。
func decodeBarcodeVotes(bin *bitmap, r image.Rectangle) (string, bool) {
	const lines = 13
	votes := map[string]int{}
	for i := 0; i < lines; i++ {
		y := r.Min.Y + r.Dy()*(i+1)/(lines+1)
		if y < 0 || y >= bin.H {
			continue
		}
		line := make([]bool, r.Dx())
		for x := 0; x < r.Dx(); x++ {
			line[x] = bin.D[y*bin.W+r.Min.X+x] == 1
		}
		if s, err := barcode.Decode(line); err == nil {
			votes[s]++
		}
	}
	best, bestN := "", 0
	for s, n := range votes {
		if n > bestN {
			best, bestN = s, n
		}
	}
	return best, bestN >= 2
}

// mergeRegCode 把码制解码结果融合进填涂判读结果:
//   - 码成功:以码为准(码制可靠性高于涂卡),涂卡有效但不一致/涂卡异常时给告警;
//   - 码失败:回退涂卡结果并告警;纯码制且码失败时进入异常指派流(RegMarks 为空)。
func mergeRegCode(res *Result, sheet *card.Sheet, codeVal string, codeOK bool) {
	if sheet.RegCodeKind == "" {
		if res.RegValid {
			res.RegSource = "fill"
		}
		return
	}
	kindName := "条码"
	if sheet.RegCodeKind == "qr" {
		kindName = "二维码"
	}
	if !codeOK {
		if res.RegValid { // 双通道:码失败但涂卡有效
			res.RegSource = "fill"
			res.Warnings = append(res.Warnings, kindName+"解码失败,已按填涂考号")
		} else {
			res.Warnings = append(res.Warnings, kindName+"与填涂均未识别,请人工定位")
		}
		return
	}
	fillVal, fillValid := res.RegNumber, res.RegValid
	res.RegNumber = codeVal
	res.RegValid = true
	res.RegSource = sheet.RegCodeKind
	switch {
	case !sheet.Layout.RegHasFill():
		// 纯码制:无涂卡可比较
	case fillValid && fillVal != codeVal:
		res.Warnings = append(res.Warnings,
			"考号填涂 "+fillVal+" 与"+kindName+" "+codeVal+" 不一致,已按"+kindName)
	case !fillValid:
		res.Warnings = append(res.Warnings, "考号填涂异常,已按"+kindName+"修复")
	}
}
