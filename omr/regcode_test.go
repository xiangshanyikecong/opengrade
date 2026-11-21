package omr_test

// 考号码制通道(条码/二维码)端到端:渲染 → 畸变+JPEG → Read 解码。
// 覆盖:纯条码 / 纯二维码 / 双通道(涂卡正常、涂卡漏涂由码修复)。

import (
	"bytes"
	"image/jpeg"
	"math/rand"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
)

func codeFixture(t *testing.T, regMode string) *card.Sheet {
	t.Helper()
	l := card.Layout{
		Version: 2, Title: "CODE TEST", RegDigits: 8, RegMode: regMode, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 42}},
	}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	return sheet
}

func digitsReg(s string) []int {
	out := make([]int, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = int(s[i] - '0')
	}
	return out
}

func readDistorted(t *testing.T, sheet *card.Sheet, f *card.Filling, seed int64) *omr.Result {
	t.Helper()
	canvas := card.RenderPNG(sheet, f)
	rng := rand.New(rand.NewSource(seed))
	distorted := synth.Distort(canvas.ToGray(), rng)
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
	return res
}

func answers4() []string { return []string{"A", "B", "C", "D"} }

// TestBarcodeChannel 纯条码通道:卡面不印填涂区,畸变下条码解码即考号。
func TestBarcodeChannel(t *testing.T) {
	sheet := codeFixture(t, card.RegBarcode)
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		f := &card.Filling{Reg: digitsReg("00420013"), Answers: answers4()}
		res := readDistorted(t, sheet, f, seed)
		if !res.RegValid || res.RegNumber != "00420013" {
			t.Fatalf("seed=%d 条码考号识别错误: %q valid=%v 警告=%v", seed, res.RegNumber, res.RegValid, res.Warnings)
		}
		if res.RegSource != "barcode" {
			t.Fatalf("seed=%d 考号来源 %q,期望 barcode", seed, res.RegSource)
		}
	}
}

// TestQRChannel 纯二维码通道。
func TestQRChannel(t *testing.T) {
	sheet := codeFixture(t, card.RegQR)
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		f := &card.Filling{Reg: digitsReg("31415926"), Answers: answers4()}
		res := readDistorted(t, sheet, f, seed)
		if !res.RegValid || res.RegNumber != "31415926" {
			t.Fatalf("seed=%d 二维码考号识别错误: %q valid=%v 警告=%v", seed, res.RegNumber, res.RegValid, res.Warnings)
		}
		if res.RegSource != "qr" {
			t.Fatalf("seed=%d 考号来源 %q,期望 qr", seed, res.RegSource)
		}
	}
}

// TestDualChannelBarcode 双通道:涂卡正常时码与涂卡一致;
// 涂卡漏涂(第 4 位空)时由条码修复,RegMarks 保留原始明细。
func TestDualChannelBarcode(t *testing.T) {
	sheet := codeFixture(t, card.RegFillBarcode)
	// 正常涂卡
	f := &card.Filling{Reg: digitsReg("20260101"), Answers: answers4()}
	res := readDistorted(t, sheet, f, 11)
	if !res.RegValid || res.RegNumber != "20260101" {
		t.Fatalf("双通道正常卷识别错误: %q valid=%v 警告=%v", res.RegNumber, res.RegValid, res.Warnings)
	}
	if res.RegSource != "barcode" {
		t.Fatalf("双通道来源 %q,期望 barcode", res.RegSource)
	}
	// 漏涂一位:填涂区第 4 位不印实心圆,条码仍印真实考号 → 码修复
	f2 := &card.Filling{Reg: digitsReg("20260101"), Answers: answers4()}
	f2.RegUnfilled = make([]bool, 8)
	f2.RegUnfilled[3] = true
	res2 := readDistorted(t, sheet, f2, 12)
	if !res2.RegValid || res2.RegNumber != "20260101" {
		t.Fatalf("双通道漏涂卷识别错误: %q valid=%v 警告=%v", res2.RegNumber, res2.RegValid, res2.Warnings)
	}
	if res2.RegSource != "barcode" {
		t.Fatalf("漏涂修复后来源 %q,期望 barcode", res2.RegSource)
	}
	foundRepair := false
	for _, w := range res2.Warnings {
		if w == "考号填涂异常,已按条码修复" {
			foundRepair = true
		}
	}
	if !foundRepair {
		t.Fatalf("漏涂卷缺少修复告警: %v", res2.Warnings)
	}
	if len(res2.RegMarks) != 8 || len(res2.RegMarks[3]) != 0 {
		t.Fatalf("RegMarks 应保留漏涂明细: %v", res2.RegMarks)
	}
}

// TestQRChannelBadFill 纯二维码模式不读填涂(卡面没有涂卡圆也不产生漏涂告警)。
func TestQRChannelNoFillWarnings(t *testing.T) {
	sheet := codeFixture(t, card.RegQR)
	f := &card.Filling{Reg: digitsReg("87654321"), Answers: answers4()}
	res := readDistorted(t, sheet, f, 21)
	for _, w := range res.Warnings {
		if len(w) > 6 && w[:6] == "考号第" {
			t.Fatalf("纯码制不应产生涂卡漏涂告警: %v", res.Warnings)
		}
	}
	if !res.RegValid || res.RegNumber != "87654321" {
		t.Fatalf("识别错误: %q valid=%v", res.RegNumber, res.RegValid)
	}
}
