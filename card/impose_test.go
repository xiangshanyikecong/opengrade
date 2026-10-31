package card

import (
	"bytes"
	"strings"
	"testing"
)

func testImposeLayout() Layout {
	return Layout{Version: 2, Title: "IMPOSE", RegDigits: 6, Columns: 2,
		Questions:   repQuestion(4, QuestionSpec{Kind: KindChoice, Options: 4, Score: 1}),
		Subjectives: []SubjectiveSpec{{Kind: KindSubjective, Score: 10, HeightMM: 40}},
	}
}

func TestImposeSVG(t *testing.T) {
	l := testImposeLayout()
	sa, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	// 双卡:两份平移分组 + A3 画幅 + 裁切线
	svg := ImposeSVG(sa, sa, nil, nil)
	if !strings.Contains(string(svg), `viewBox="0 0 420 297"`) || !strings.Contains(string(svg), `width="420mm"`) {
		t.Fatalf("拼版 SVG 应为 A3 横放画幅: %s", firstLine(string(svg)))
	}
	if n := strings.Count(string(svg), `transform="translate(210,0)"`); n != 1 {
		t.Fatalf("右卡平移分组数 = %d,期望 1", n)
	}
	if n := bytes.Count(svg, []byte("IMPOSE")); n != 2 {
		t.Fatalf("标题出现次数 = %d,期望 2(两份卡各印一次)", n)
	}
	if !strings.Contains(string(svg), "stroke-dasharray") {
		t.Fatal("拼版 SVG 应印虚线裁切线")
	}
	// Filling 各印各的:左卡涂考号 123456,右卡不涂,实心圆 = 考号 6 位
	// (只数 <circle 实心:定位块/文本/裁切线也是 fill/stroke="black")
	fa := &Filling{Reg: []int{1, 2, 3, 4, 5, 6}}
	svg = ImposeSVG(sa, sa, fa, nil)
	solid := 0
	for _, ln := range strings.Split(string(svg), "\n") {
		if strings.Contains(ln, "<circle") && strings.Contains(ln, `fill="black"`) {
			solid++
		}
	}
	if solid != 6 {
		t.Fatalf("左右卡 Filling 独立:实心圆数 = %d,期望 6", solid)
	}
	// 单卡(nil 右半):仍 A3 画幅 + 裁切线,无右卡分组
	svg = ImposeSVG(sa, nil, nil, nil)
	if strings.Contains(string(svg), "translate(210,0)") {
		t.Fatal("右半 nil 不应有平移分组")
	}
	if !strings.Contains(string(svg), "stroke-dasharray") {
		t.Fatal("奇数末张也应印裁切线")
	}
}

func TestImposePNG(t *testing.T) {
	l := testImposeLayout()
	sa, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	ca := RenderPNG(sa, nil)
	m := ImposePNG(ca, ca)
	if m.W != 2*ca.W || m.H != ca.H {
		t.Fatalf("拼版画布 = %dx%d,期望 %dx%d", m.W, m.H, 2*ca.W, ca.H)
	}
	// 左半与原卡逐像素一致
	for _, y := range []int{0, ca.H / 2, ca.H - 1} {
		if got, want := m.Pix[y*m.W:y*m.W+ca.W], ca.Pix[y*ca.W:(y+1)*ca.W]; !bytes.Equal(got, want) {
			t.Fatalf("第 %d 行左半 blit 不一致", y)
		}
	}
	// 裁切线:中缝应有黑像素(虚线段上),且两半交界外侧一列是白纸
	found := false
	for y := 0; y < m.H; y++ {
		if m.Pix[y*m.W+ca.W-1] == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("中缝 x=W-1 应有裁切线墨迹")
	}
	// 单卡(nil 右半):尺寸不翻倍
	m = ImposePNG(ca, nil)
	if m.W != ca.W || m.H != ca.H {
		t.Fatalf("单卡拼版画布 = %dx%d,期望 %dx%d", m.W, m.H, ca.W, ca.H)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
