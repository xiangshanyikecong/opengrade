package score

import (
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

func TestGradeBasics(t *testing.T) {
	key := []string{"A", "B", "C", "D"}
	results := []*omr.Result{
		{RegNumber: "001", RegValid: true, Answers: []string{"A", "B", "C", "D"}}, // 满分
		{RegNumber: "002", RegValid: true, Answers: []string{"A", "B", "C", ""}},  // 漏答第 4 题
		{RegNumber: "", RegValid: false, Answers: []string{"B", "B", "AB", ""}},   // 错 1、多涂 1、漏答 1
	}
	rep := GradeObjective(key, results)
	if rep.Students[0].Score != 4 || rep.Students[1].Score != 3 || rep.Students[2].Score != 1 {
		t.Fatalf("成绩错误: %+v", rep.Students)
	}
	if rep.Mean < 2.66 || rep.Mean > 2.67 {
		t.Fatalf("均分 = %f, 期望 2.67", rep.Mean)
	}
	// 第 1 题:A/A/B → 2 对 1 错
	if got := rep.PerQuestion[0].Rate; got < 0.66 || got > 0.67 {
		t.Fatalf("第 1 题得分率 = %f, 期望 0.667", got)
	}
	// 第 3 题:C/C/AB → 2 对 1 错(多涂计错)
	if got := rep.PerQuestion[2].Rate; got < 0.66 || got > 0.67 {
		t.Fatalf("第 3 题得分率 = %f, 期望 0.667", got)
	}
	low := LowRateQuestions(rep, 0.6)
	if len(low) != 1 || low[0].Index != 4 {
		// 前三卷第 4 题仅 1 人作答且正确 → 1/3
		t.Fatalf("低分率题应仅为第 4 题,得到 %+v", low)
	}
}

func TestMergeObjectiveAndSubjective(t *testing.T) {
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 2},
		{Kind: card.KindJudge, Score: 2},
		{Kind: card.KindMultiple, Options: 4, Score: 3},
	}
	subjectives := []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10}}
	results := []*omr.Result{
		{RegNumber: "001", RegValid: true, Answers: []string{"A", "T", "AC"}}, // 客观全对 7 分
		{RegNumber: "002", RegValid: true, Answers: []string{"A", "F", "AC"}}, // 客观 2+0+3=5
	}
	grades := map[string]Grade{
		GradeKey(1, 1): {Score: 9, Mark: "check", Graded: true},
		GradeKey(2, 1): {Score: 0, Mark: "cross", Graded: true},
		// 学生 2 的主观题未在列表中出现过 → 未阅
	}
	rep := Merge(questions, []string{"A", "T", "AC"}, results, subjectives, grades)
	if rep.Total != 17 { // 2+2+3 + 10
		t.Fatalf("卷面满分 = %f, 期望 17", rep.Total)
	}
	if rep.Students[0].Score != 16 || rep.Students[1].Score != 5 {
		t.Fatalf("总分错误: %+v", rep.Students)
	}
	if rep.Students[0].Flagged {
		t.Fatal("学生 1 不应有 flag 标记")
	}
	// 主观题统计:均分 4.5,rate 0.45
	st := rep.PerQuestion[3]
	if st.Kind != card.KindSubjective || st.Graded != 2 || st.Mean != 4.5 {
		t.Fatalf("主观题统计错误: %+v", st)
	}
	// 客观加权判断题:F 答案错 → correct=1
	if got := rep.PerQuestion[1].Correct; got != 1 {
		t.Fatalf("判断题答对人数 = %d, 期望 1", got)
	}
}
