package omr_test

// 扫描偏移/裁边容错:黑边、定位块贴边裁切场景下的读卡鲁棒性。

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
)

func cropGray(src *image.Gray, left, top, right, bottom int) *image.Gray {
	w := src.Rect.Dx() - left - right
	h := src.Rect.Dy() - top - bottom
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(out.Pix[y*w:(y+1)*w], src.Pix[(top+y)*src.Rect.Dx()+left:(top+y)*src.Rect.Dx()+left+w])
	}
	return out
}

func addBlackFrame(src *image.Gray, px int) *image.Gray {
	w, h := src.Rect.Dx()+2*px, src.Rect.Dy()+2*px
	out := image.NewGray(image.Rect(0, 0, w, h))
	for i := range out.Pix {
		out.Pix[i] = 0
	}
	for y := 0; y < src.Rect.Dy(); y++ {
		copy(out.Pix[(px+y)*w+px:(px+y)*w+px+src.Rect.Dx()], src.Pix[y*src.Rect.Dx():(y+1)*src.Rect.Dx()])
	}
	return out
}

func TestReadCroppedAndFramed(t *testing.T) { // nolint: gocritic // 表驱动多场景回归
	l := card.DefaultLayout()
	sheet, _ := l.Build()
	f := card.Filling{Answers: []string{"A", "B", "C", "D"}, Reg: []int{2, 0, 2, 5, 0, 7, 1, 3}}
	distorted := synth.Distort(card.RenderPNG(sheet, &f).ToGray(), rand.New(rand.NewSource(9)))
	W := distorted.Rect.Dx() // 210mm
	H := distorted.Rect.Dy() // 297mm
	mmX := func(mm int) int { return mm * W / 210 }
	mmY := func(mm int) int { return mm * H / 297 }

	cases := []struct {
		name                  string
		l, t, r, b            int
		frame                 int
	}{
		{"无裁切基准", 0, 0, 0, 0, 0},
		{"裁边6mm(定位块内缩10mm内)", mmX(6), mmY(6), mmX(6), mmY(6), 0},
		{"裁边10mm(贴到定位块边缘)", mmX(10), mmY(10), mmX(10), mmY(10), 0},
		{"裁边13mm(定位块被切一半)", mmX(13), mmY(13), mmX(13), mmY(13), 0},
		{"扫描仪黑边20px", 0, 0, 0, 0, 20},
		{"黑边+裁边组合", mmX(8), mmY(8), mmX(8), mmY(8), 30},
	}
	for _, c := range cases {
		img := image.Image(distorted)
		g := distorted
		if c.l+c.r+c.t+c.b > 0 {
			g = cropGray(distorted, c.l, c.t, c.r, c.b)
		}
		if c.frame > 0 {
			g = addBlackFrame(g, c.frame)
		}
		img = g
		var jbuf bytes.Buffer
		jpeg.Encode(&jbuf, img, &jpeg.Options{Quality: 85})
		dec, _ := jpeg.Decode(bytes.NewReader(jbuf.Bytes()))
		res, _, err := omr.Read(sheet, dec, omr.Options{})
		if err != nil {
			t.Errorf("%-32s 读卡失败: %v", c.name, err)
			continue
		}
		if res.RegNumber != "20250713" || !res.RegValid {
			t.Errorf("%-32s 考号 = %q valid=%v,期望 20250713", c.name, res.RegNumber, res.RegValid)
		}
		if len(res.Answers) != 30 {
			t.Errorf("%-32s 客观题判读 %d 题,期望 30", c.name, len(res.Answers))
		}
	}
	}
