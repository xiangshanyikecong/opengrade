package omr

import (
	"image"

	"github.com/opengrade/opengrade/card"
)

// Slice 是一道主观题在某个学生卷上的切片:阅卷工作台的最小作答单元。
type Slice struct {
	StudentIndex  int         // 1-based 学生卷序号(上传顺序)
	RegNumber     string      // 该卷 OMR 判读考号(可为空)
	QuestionIndex int         // 1-based 主观题序号(对应 Layout.Subjectives 下标)
	QuestionKind  string      // fillblank | subjective
	Image         *image.Gray // 作答区裁剪图(含少量边距)
}

// SliceRectified 从矫正后的整页灰度图中,按共享几何裁出该学生全部主观题切片。
// 外扩 1.5mm 边距,把作答框边线与少量上下文纳入画面,便于教师确认题号。
func SliceRectified(rect *image.Gray, sheet *card.Sheet, studentIndex int, regNumber string) []Slice {
	if len(sheet.SubjectiveBoxes) == 0 {
		return nil
	}
	padF := 1.5 * card.DPI / 25.4 // 1.5mm 边距换算像素
	pad := int(padF + 0.5)
	out := make([]Slice, 0, len(sheet.SubjectiveBoxes))
	for qi, box := range sheet.SubjectiveBoxes {
		r := image.Rect(box.Min.X-pad, box.Min.Y-pad, box.Max.X+pad, box.Max.Y+pad)
		r = r.Intersect(rect.Rect)
		if r.Empty() {
			continue
		}
		w, h := r.Dx(), r.Dy()
		img := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(img.Pix[y*w:(y+1)*w],
				rect.Pix[(r.Min.Y+y)*rect.Rect.Dx()+r.Min.X:(r.Min.Y+y)*rect.Rect.Dx()+r.Min.X+w])
		}
		kind := ""
		if qi < len(sheet.Layout.Subjectives) {
			kind = sheet.Layout.Subjectives[qi].Kind
		}
		out = append(out, Slice{
			StudentIndex:  studentIndex,
			RegNumber:     regNumber,
			QuestionIndex: qi + 1,
			QuestionKind:  kind,
			Image:         img,
		})
	}
	return out
}
