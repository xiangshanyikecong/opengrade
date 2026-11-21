package omr_test

import (
	"image"
	"math/rand"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
	"github.com/opengrade/opengrade/synth"
)

// imposeFixture 造一份 A3 横放 2 拼靶场:左右两份同版式 A4 卡,考号/作答各不相同。
// rightBlank = 右半印纯白(名册奇数末张的空白半版)。
func imposeFixture(t *testing.T, rightBlank bool) (*card.Sheet, *image.Gray, []string, []string) {
	t.Helper()
	l := card.Layout{Version: 2, Title: "IMPOSE", RegDigits: 8, Columns: 2,
		Questions:   repQuestions(6, card.QuestionSpec{Kind: card.KindChoice, Options: 4, Score: 1}),
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 40}}}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	fillA := &card.Filling{Reg: []int{1, 2, 3, 4, 5, 6, 7, 8}, Answers: []string{"A", "B", "C", "D", "A", "B"}}
	fillB := &card.Filling{Reg: []int{2, 1, 4, 3, 6, 5, 8, 7}, Answers: []string{"D", "C", "B", "A", "B", "A"}}
	ca := card.RenderPNG(sheet, fillA)
	cb := card.RenderPNG(sheet, fillB)
	var imposed *image.Gray
	if rightBlank {
		blank := raster.NewCanvas(ca.W, ca.H) // 纯白右半(名册奇数末张)
		imposed = card.ImposePNG(ca, blank).ToGray()
	} else {
		imposed = card.ImposePNG(ca, cb).ToGray()
	}
	wantA := []string{"A", "B", "C", "D", "A", "B"}
	wantB := []string{"D", "C", "B", "A", "B", "A"}
	return sheet, imposed, wantA, wantB
}

func repQuestions(n int, q card.QuestionSpec) []card.QuestionSpec {
	out := make([]card.QuestionSpec, n)
	for i := range out {
		out[i] = q
	}
	return out
}

func readHalf(t *testing.T, sheet *card.Sheet, g *image.Gray, wantReg string, wantAns []string) {
	t.Helper()
	res, _, err := omr.Read(sheet, g, omr.Options{})
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if res.RegNumber != wantReg {
		t.Fatalf("考号 = %q,期望 %q(warnings=%v)", res.RegNumber, wantReg, res.Warnings)
	}
	// 先断言数量相等:缺末题/多题/全空都应显式失败,而非被循环跳过(评审 impose_test.go:60)
	if len(res.Answers) != len(wantAns) {
		t.Fatalf("客观题判读 %d 题,期望 %d 题(answers=%v)", len(res.Answers), len(wantAns), res.Answers)
	}
	for i, w := range wantAns {
		if res.Answers[i] != w {
			t.Fatalf("第 %d 题判读 %q,期望 %q", i+1, res.Answers[i], w)
		}
	}
}

func TestSplitImposeClean(t *testing.T) {
	sheet, imposed, wantA, wantB := imposeFixture(t, false)
	left, right, notes := omr.SplitImpose(imposed)
	if left == nil || right == nil {
		t.Fatalf("清晰拼版应切出两半: left=%v right=%v notes=%v", left != nil, right != nil, notes)
	}
	if len(notes) != 0 {
		t.Fatalf("清晰拼版不应有 note: %v", notes)
	}
	readHalf(t, sheet, left, "12345678", wantA)
	readHalf(t, sheet, right, "21436587", wantB)
}

func TestSplitImposeDistorted(t *testing.T) {
	sheet, imposed, wantA, wantB := imposeFixture(t, false)
	rng := rand.New(rand.NewSource(7))
	distorted := synth.Distort(imposed, rng)
	left, right, notes := omr.SplitImpose(distorted)
	if left == nil || right == nil {
		t.Fatalf("畸变拼版应切出两半: notes=%v", notes)
	}
	readHalf(t, sheet, left, "12345678", wantA)
	readHalf(t, sheet, right, "21436587", wantB)
}

func TestSplitImposeBlankHalf(t *testing.T) {
	sheet, imposed, wantA, _ := imposeFixture(t, true)
	left, right, notes := omr.SplitImpose(imposed)
	if left == nil || right != nil {
		t.Fatalf("空白半版应只返回左卡: left=%v right=%v", left != nil, right != nil)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, ""), "空白半版") {
		t.Fatalf("空白半版应有 note: %v", notes)
	}
	readHalf(t, sheet, left, "12345678", wantA)
}

func TestSplitImposeSingleCard(t *testing.T) {
	// 单卡页误传进拼版考试:整页原样返回,不切坏
	l := card.Layout{Version: 2, Title: "SINGLE", RegDigits: 8, Columns: 2,
		Questions:   repQuestions(6, card.QuestionSpec{Kind: card.KindChoice, Options: 4, Score: 1}),
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 40}}}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	g := card.RenderPNG(sheet, &card.Filling{Reg: []int{9, 8, 7, 6, 5, 4, 3, 2}}).ToGray()
	left, right, notes := omr.SplitImpose(g)
	if left == nil || right != nil {
		t.Fatalf("单卡页应整页返回: right=%v notes=%v", right != nil, notes)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, ""), "单卡") {
		t.Fatalf("单卡误传应有 note: %v", notes)
	}
	readHalf(t, sheet, left, "98765432", []string{"", "", "", "", "", ""})
}
