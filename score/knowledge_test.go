package score

import (
	"testing"

	"github.com/opengrade/opengrade/card"
)

// TestKnowledgeStats 验证知识点掌握度按分值加权聚合,且低掌握度排前。
func TestKnowledgeStats(t *testing.T) {
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 10, Knowledge: "函数、导数"},
		{Kind: card.KindChoice, Options: 4, Score: 10, Knowledge: "函数"},
		{Kind: card.KindChoice, Options: 4, Score: 20, Knowledge: "概率"},
	}
	// 逐题统计:题1 得分率 0.5、题2 得分率 1.0、题3 得分率 0.25,各 4 人作答
	perQ := []QuestionStat{
		{Index: 1, Kind: card.KindChoice, Max: 10, Count: 4, Rate: 0.5},
		{Index: 2, Kind: card.KindChoice, Max: 10, Count: 4, Rate: 1.0},
		{Index: 3, Kind: card.KindChoice, Max: 20, Count: 4, Rate: 0.25},
	}
	got := KnowledgeStats(questions, nil, perQ)
	byName := map[string]KnowledgeStat{}
	for _, k := range got {
		byName[k.Name] = k
	}
	// 函数 = 题1+题2:(0.5*10*4 + 1.0*10*4) / (10*4 + 10*4) = 60/80 = 0.75
	if fn := byName["函数"]; fn.Rate != 0.75 || fn.Questions != 2 {
		t.Fatalf("函数 掌握度 = %v(题数 %d),期望 0.75/2", fn.Rate, fn.Questions)
	}
	// 导数 只挂题1 = 0.5;概率 = 0.25
	if byName["导数"].Rate != 0.5 {
		t.Fatalf("导数 掌握度 = %v,期望 0.5", byName["导数"].Rate)
	}
	if byName["概率"].Rate != 0.25 {
		t.Fatalf("概率 掌握度 = %v,期望 0.25", byName["概率"].Rate)
	}
	// 排序:掌握度升序(概率 0.25 → 导数 0.5 → 函数 0.75)
	if got[0].Name != "概率" || got[len(got)-1].Name != "函数" {
		t.Fatalf("排序错误: %v", got)
	}
}

// TestStudentKnowledge 验证个人掌握度只纳入有得分的题目。
func TestStudentKnowledge(t *testing.T) {
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Score: 10, Knowledge: "函数"},
		{Kind: card.KindChoice, Score: 10, Knowledge: "函数"},
	}
	// 该生题1 满分,题2 未作答(不进入映射)
	rates := map[int]float64{1: 1.0}
	got := StudentKnowledge(questions, nil, rates)
	if len(got) != 1 || got[0].Name != "函数" {
		t.Fatalf("知识点数 = %d,期望 1", len(got))
	}
	if got[0].Rate != 1.0 || got[0].Max != 10 {
		t.Fatalf("掌握度 = %v max=%v,期望 1.0/10(未答题不计入分母)", got[0].Rate, got[0].Max)
	}
}

// TestSplitKnowledge 验证标签拆分去重与中文分隔符。
func TestSplitKnowledge(t *testing.T) {
	got := card.SplitKnowledge("函数、导数, 函数 ;概率|统计")
	want := []string{"函数", "导数", "概率", "统计"}
	if len(got) != len(want) {
		t.Fatalf("拆分 = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("拆分[%d] = %q,期望 %q", i, got[i], want[i])
		}
	}
}

// TestFineGradeScale 验证 21 级细分档:档位数、占比合计、赋分区间递增。
func TestFineGradeScale(t *testing.T) {
	sc := FineGradeScale()
	if len(sc.Bands) < 15 {
		t.Fatalf("细分档位数 = %d,期望 >= 15", len(sc.Bands))
	}
	var sum float64
	for _, b := range sc.Bands {
		sum += b.Ratio
		if b.High <= b.Low {
			t.Fatalf("档位 %s 区间非法: [%v,%v]", b.Name, b.Low, b.High)
		}
		if b.Name == "" {
			t.Fatal("档位缺名称")
		}
	}
	if sum < 0.99 || sum > 1.01 {
		t.Fatalf("占比合计 = %v,期望约 1", sum)
	}
	// 换算不越界
	regs := []string{"a", "b", "c"}
	raw := []float64{100, 60, 30}
	rows := ConvertGrades(regs, raw, sc)
	if len(rows) != 3 {
		t.Fatalf("换算行数 = %d", len(rows))
	}
	for _, r := range rows {
		if r.Converted < sc.Start-1 || r.Converted > 100 {
			t.Fatalf("赋分 %v 越界(起点 %v)", r.Converted, sc.Start)
		}
	}
}
