package card

import (
	"strings"
	"testing"
)

// 多页版式渲染:第 2+ 页主观题必须印全局题号、取对应全局题的 Label,
// 不得按页内序号重置为 1(与客观题 1..N 撞号、与选做组涂点全局题号对不上)。
func TestAuditMultipageGlobalNumbering(t *testing.T) {
	l := DefaultLayout()
	l.Questions = nil
	for i := 0; i < 30; i++ {
		l.Questions = append(l.Questions, QuestionSpec{Kind: KindChoice, Options: 4, Score: 2})
	}
	labels := []string{"标签A", "标签B", "标签C", "标签D", "标签E", "标签F"}
	l.Subjectives = nil
	for i := 0; i < 6; i++ {
		l.Subjectives = append(l.Subjectives, SubjectiveSpec{
			Kind: KindSubjective, Score: 10, HeightMM: 60, Label: labels[i],
		})
	}
	book, err := l.BuildBook()
	if err != nil {
		t.Fatalf("BuildBook: %v", err)
	}
	if len(book) < 3 {
		t.Fatalf("预期至少 3 页,实际 %d", len(book))
	}
	// 第 2 页承载全局主观题 2~4(印刷题号 32/33/34,标签 B/C/D)
	want2 := []string{"32. 标签B", "33. 标签C", "34. 标签D"}
	got := labelLines(t, RenderSVG(book[1], nil))
	for i, w := range want2 {
		if i >= len(got) || !strings.Contains(got[i], w) {
			t.Fatalf("第 2 页标签行[%d] = %v,应含 %q(整行:%v)", i, got, w, got)
		}
	}
	// 第 3 页承载全局主观题 5~6(印刷题号 35/36,标签 E/F)
	want3 := []string{"35. 标签E", "36. 标签F"}
	got = labelLines(t, RenderSVG(book[2], nil))
	for i, w := range want3 {
		if i >= len(got) || !strings.Contains(got[i], w) {
			t.Fatalf("第 3 页标签行[%d]应含 %q(整行:%v)", i, w, got)
		}
	}
}

func labelLines(t *testing.T, svg []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(svg), "\n") {
		if strings.Contains(line, `text-anchor="start"`) {
			out = append(out, line)
		}
	}
	return out
}
