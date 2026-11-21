package card

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildCapacityError(t *testing.T) {
	l := Layout{Version: 1, Title: "T", RegDigits: 8, ChoiceCount: 62, Options: 4, Columns: 2}
	if _, err := l.Build(); err == nil {
		t.Fatal("62 题 2 栏(31 行 > 30 行)应超出单页容量而报错")
	}
}

func TestSVGElementCounts(t *testing.T) {
	l := Layout{Version: 1, Title: "TEST SHEET", RegDigits: 6, ChoiceCount: 20, Options: 4, Columns: 2}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	svg := RenderSVG(sheet, nil)
	got := bytes.Count(svg, []byte("<circle"))
	want := 20*4 + 6*10
	if got != want {
		t.Fatalf("SVG 涂卡圆数量 = %d, 期望 %d", got, want)
	}
	if !strings.Contains(string(svg), "TEST SHEET") {
		t.Fatal("SVG 应包含卷头标题")
	}
}

func TestSubjectiveParts(t *testing.T) {
	l := Layout{Version: 2, Title: "T", RegDigits: 8, Columns: 2,
		Questions: []QuestionSpec{{Kind: KindChoice, Options: 4, Score: 1}},
		Subjectives: []SubjectiveSpec{
			{Kind: KindSubjective, HeightMM: 60, Parts: []SubjectivePart{{Score: 6}, {Score: 5}, {Score: 4}}},
		}}
	n, err := l.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if n.Subjectives[0].Score != 15 { // 大题满分 = 小问之和
		t.Fatalf("拆分小问后大题满分 = %v,期望 15", n.Subjectives[0].Score)
	}
	if n.Subjectives[0].Parts[0].Label != "(1)" {
		t.Fatalf("小问标签应自动编号: %q", n.Subjectives[0].Parts[0].Label)
	}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.SubjectiveBoxes) != 1 {
		t.Fatalf("拆分小问不改变切片几何: %d", len(sheet.SubjectiveBoxes))
	}
	svg := RenderSVG(sheet, nil)
	if !strings.Contains(string(svg), "6+5+4") || !strings.Contains(string(svg), "15分") {
		t.Fatal("SVG 标签应含小问拆解与大题满分")
	}
	// 矩形计数:1 白底 + 4 定位块 + 1 姓名框 + 1 作答框 + 3 小问得分框 = 10
	if got := bytes.Count(svg, []byte("<rect")); got != 10 {
		t.Fatalf("SVG 矩形数 = %d,期望 10", got)
	}
	if RenderPNG(sheet, nil) == nil {
		t.Fatal("位图渲染不应返回空")
	}
	// 非法:小问分值 ≤ 0
	bad := l
	bad.Subjectives[0].Parts[1].Score = 0
	if _, err := bad.Build(); err == nil {
		t.Fatal("小问分值 ≤ 0 应报错")
	}
}

func TestElectiveGroup(t *testing.T) {
	l := Layout{Version: 2, Title: "T", RegDigits: 8, Columns: 2,
		Questions: repQuestion(22, QuestionSpec{Kind: KindChoice, Options: 4, Score: 2}),
		Subjectives: []SubjectiveSpec{
			{Kind: KindFillBlank, Score: 5, HeightMM: 16},
			{Kind: KindSubjective, Score: 10, HeightMM: 40, Elective: "A"},
			{Kind: KindSubjective, Score: 10, HeightMM: 40, Elective: " A "}, // 空白归一后同组
		}}
	n, err := l.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if n.Subjectives[2].Elective != "A" {
		t.Fatalf("选做组标签应去空白: %q", n.Subjectives[2].Elective)
	}
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(sheet.Electives) != 1 {
		t.Fatalf("选做题号区数 = %d,期望 1(印在组内第一道题上)", len(sheet.Electives))
	}
	row := sheet.Electives[0]
	if row.Subjective != 2 || len(row.Members) != 2 || row.Members[0] != 2 || row.Members[1] != 3 {
		t.Fatalf("题号区位置/成员异常: %+v", row)
	}
	if len(row.Points) != 2 || len(row.Labels) != 2 || row.Labels[0] != "24" || row.Labels[1] != "25" {
		t.Fatalf("题号区涂点/卷面题号异常: %+v", row)
	}
	// 涂点必须落在第一道题的标签行(作答框上沿之上),且在版心内
	box := sheet.SubjectiveBoxes[1]
	if p := row.Points[0]; p.Y >= box.Min.Y || p.X <= 0 || p.X >= sheet.Wpx {
		t.Fatalf("题号区涂点应印在标签行: %v vs 作答框 %v", p, box)
	}
	// SVG:题号区 + 任选提示
	svg := RenderSVG(sheet, nil)
	if !strings.Contains(string(svg), "任选一题") || !strings.Contains(string(svg), ">题号<") {
		t.Fatal("SVG 应印题号区与任选提示")
	}
	// Filling 涂选第二个选项 → SVG 第二个圆为实心
	filled := RenderSVG(sheet, &Filling{Electives: map[int]int{2: 1}})
	solid := 0
	for _, ln := range strings.Split(string(filled), "\n") {
		if strings.Contains(ln, "fill=\"black\"") && strings.Contains(ln, "<circle") {
			solid++
		}
	}
	if solid != 1 { // 仅考号未涂,客观题未涂:唯一实心圆 = 所选涂点
		t.Fatalf("涂选后实心圆数 = %d,期望 1", solid)
	}
	// 位图渲染不应 panic
	if RenderPNG(sheet, &Filling{Electives: map[int]int{2: 0}}) == nil {
		t.Fatal("位图渲染不应返回空")
	}

	// 非法:选做组只有 1 道题
	single := l
	single.Subjectives = []SubjectiveSpec{
		{Kind: KindSubjective, Score: 10, HeightMM: 40, Elective: "A"},
		{Kind: KindSubjective, Score: 10, HeightMM: 40},
	}
	if _, err := single.Build(); err == nil {
		t.Fatal("选做组只有 1 道题应报错")
	}
	// 非法:选做组超过 4 道
	five := l
	five.Subjectives = []SubjectiveSpec{
		{Kind: KindSubjective, Score: 6, HeightMM: 20, Elective: "A"},
		{Kind: KindSubjective, Score: 6, HeightMM: 20, Elective: "A"},
		{Kind: KindSubjective, Score: 6, HeightMM: 20, Elective: "A"},
		{Kind: KindSubjective, Score: 6, HeightMM: 20, Elective: "A"},
		{Kind: KindSubjective, Score: 6, HeightMM: 20, Elective: "A"},
	}
	if _, err := five.Build(); err == nil {
		t.Fatal("选做组超过 4 道应报错")
	}
}
