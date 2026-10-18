package omr_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
)

func makeFixture(t *testing.T) (*card.Sheet, *card.Filling) {
	t.Helper()
	l := card.Layout{Version: 1, Title: "OMR TEST", RegDigits: 6, ChoiceCount: 20, Options: 4, Columns: 2}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	f := card.Filling{Answers: []string{"A", "B", "C", "D", "A", "B", "C", "D", "A", "B", "C", "D", "A", "B", "C", "D", "A", "B", "C", "D"}}
	for d := 0; d < 6; d++ {
		f.Reg = append(f.Reg, []int{2, 0, 2, 5, 0, 7}[d])
	}
	return sheet, &f
}

func decode(t *testing.T, b []byte) (image.Image, error) {
	t.Helper()
	return png.Decode(bytes.NewReader(b))
}

func TestReadCleanRoundtrip(t *testing.T) {
	sheet, f := makeFixture(t)
	canvas := card.RenderPNG(sheet, f)
	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas.ToGray()); err != nil {
		t.Fatal(err)
	}
	img, err := decode(t, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := omr.Read(sheet, img, omr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.RegNumber != "202507" || !res.RegValid {
		t.Fatalf("考号识别错误: %q valid=%v", res.RegNumber, res.RegValid)
	}
	for qi, want := range f.Answers {
		if res.Answers[qi] != want {
			t.Fatalf("第 %d 题识别 = %q, 期望 %q", qi+1, res.Answers[qi], want)
		}
	}
}

func TestReadDistortedRoundtrip(t *testing.T) {
	sheet, f := makeFixture(t)
	canvas := card.RenderPNG(sheet, f)

	rng := rand.New(rand.NewSource(42))
	distorted := synth.Distort(canvas.ToGray(), rng)

	// 模拟扫描导出:JPEG 有损压缩
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 82}); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(jbuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	res, _, err := omr.Read(sheet, img, omr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.RegValid || res.RegNumber != "202507" {
		t.Fatalf("畸变下考号识别错误: %q valid=%v 警告=%v", res.RegNumber, res.RegValid, res.Warnings)
	}
	bad := 0
	for qi, want := range f.Answers {
		if res.Answers[qi] != want {
			bad++
			t.Logf("第 %d 题识别 = %q, 期望 %q", qi+1, res.Answers[qi], want)
		}
	}
	if bad > 1 { // 允许极端畸变下 1 题以内抖动
		t.Fatalf("畸变下误判题数 %d 超过容限", bad)
	}
}

// TestReadV2Kinds 验证 v2 版式:判断题 T/F、多选题合法多涂、自适应二值化路径。
func TestReadV2Kinds(t *testing.T) {
	l := card.DefaultLayout() // 20 单选 + 5 判断 + 5 多选 + 3 主观
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Choice) != 30 || len(sheet.SubjectiveBoxes) != 3 {
		t.Fatalf("v2 版式编译错误: 客观 %d 题,主观 %d 区", len(sheet.Choice), len(sheet.SubjectiveBoxes))
	}
	f := card.Filling{Answers: make([]string, 30)}
	for d := 0; d < 8; d++ {
		f.Reg = append(f.Reg, []int{3, 1, 4, 1, 5, 9, 2, 6}[d])
	}
	for qi := range f.Answers {
		switch {
		case qi < 20: // 单选
			f.Answers[qi] = string(rune('A' + qi%4))
		case qi < 25: // 判断
			f.Answers[qi] = []string{"T", "F"}[qi%2]
		default: // 多选:两题涂两项,一题涂一项
			switch qi {
			case 26:
				f.Answers[qi] = "AC"
			case 28:
				f.Answers[qi] = "ABD"
			default:
				f.Answers[qi] = "B"
			}
		}
	}

	canvas := card.RenderPNG(sheet, &f)
	rng := rand.New(rand.NewSource(7))
	distorted := synth.Distort(canvas.ToGray(), rng)
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(jbuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := omr.Read(sheet, img, omr.Options{Threshold: "adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RegNumber != "31415926" || !res.RegValid {
		t.Fatalf("v2 考号识别错误: %q valid=%v", res.RegNumber, res.RegValid)
	}
	if res.Answers[26] != "AC" || res.Answers[28] != "ABD" {
		t.Fatalf("多选题识别错误: %v", res.Answers[20:])
	}
	if res.Answers[20] != "T" || res.Answers[21] != "F" {
		t.Fatalf("判断题识别错误: %v", res.Answers[20:25])
	}
	bad := 0
	for qi, want := range f.Answers {
		if res.Answers[qi] != want {
			bad++
			t.Logf("第 %d 题识别 = %q, 期望 %q", qi+1, res.Answers[qi], want)
		}
	}
	if bad > 1 {
		t.Fatalf("v2 畸变下误判题数 %d 超过容限", bad)
	}
}
