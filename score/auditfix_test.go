package score

import (
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// 深度排查修复批(2026-10)回归:区分度列对齐、小问统计过滤、阈值边界。

// 空答案键(答案后补窗口)时,后续题的区分度必须取自自身列,不得左移错位。
func TestAuditDiscriminationAlignedWithEmptyKeys(t *testing.T) {
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 2},
		{Kind: card.KindChoice, Options: 4, Score: 2},
		{Kind: card.KindChoice, Options: 4, Score: 2},
		{Kind: card.KindChoice, Options: 4, Score: 2},
	}
	key := []string{"A", "", "C", "D"} // 第 2 题未录答案
	answers := [][]string{
		{"A", "B", "C", "D"}, {"A", "A", "C", "D"}, {"A", "B", "C", "C"}, {"A", "A", "C", "C"},
		{"B", "B", "A", "D"}, {"B", "A", "A", "D"}, {"C", "B", "B", "C"}, {"C", "A", "B", "C"},
	}
	results := make([]*omr.Result, len(answers))
	for i, ans := range answers {
		results[i] = &omr.Result{Answers: append([]string(nil), ans...), RegValid: true}
	}
	rep := Merge(questions, key, results, nil, nil, nil)
	if len(rep.PerQuestion) != 3 {
		t.Fatalf("PerQuestion 应只含 3 道有键题,得到 %d", len(rep.PerQuestion))
	}
	byIndex := map[int]QuestionStat{}
	for _, st := range rep.PerQuestion {
		byIndex[st.Index] = st
	}
	// q1/q3:高分组合对、低分组全错 → 区分度 1;q4 作答模式与 q3 相同(高分 D 低分 C)→ 同为 1。
	// 错位回归的判别点是 q3:修复前它读空键列恒无效,区分度被归零。
	if d := byIndex[1].Discrimination; d <= 0.99 {
		t.Fatalf("第 1 题区分度 = %v,应 ≈1", d)
	}
	if d := byIndex[3].Discrimination; d <= 0.99 {
		t.Fatalf("第 3 题区分度 = %v,应 ≈1(修复前因错位恒 0)", d)
	}
	if d := byIndex[4].Discrimination; d <= 0.99 {
		t.Fatalf("第 4 题区分度 = %v,应 ≈1", d)
	}
}

// 小问统计:缺考/作废/卷种待指派卷的小问评分不得进 PartStat。
func TestAuditPartsStatsExcludeInvalidResults(t *testing.T) {
	questions := []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 2}}
	key := []string{"A"}
	subs := []card.SubjectiveSpec{{Kind: card.KindSubjective, Parts: []card.SubjectivePart{{Score: 3}, {Score: 3}}}}
	mk := func(state string) *omr.Result {
		return &omr.Result{RegValid: true, Answers: []string{"A"},
			Absent: state == "absent", Voided: state == "voided", PendingVariant: state == "pending"}
	}
	results := []*omr.Result{mk(""), mk(""), mk("absent"), mk("voided"), mk("pending")}
	grades := map[string]Grade{}
	for si := 1; si <= 5; si++ {
		for pi := 1; pi <= 2; pi++ {
			grades[GradePartKey(si, 1, pi)] = Grade{Score: 3, Graded: true}
		}
	}
	rep := Merge(questions, key, results, subs, grades, nil)
	st := rep.PerQuestion[len(rep.PerQuestion)-1] // 主观题(第 2 题)
	if st.Index != 2 {
		t.Fatalf("主观题统计应在第 2 位,得到 %d", st.Index)
	}
	for pi, p := range st.Parts {
		if p.Graded != 2 {
			t.Fatalf("小问 %d 计入 %d 人,应为 2(缺考/作废/待指派各 1 人不得进)", pi+1, p.Graded)
		}
		if p.Mean != 3 {
			t.Fatalf("小问 %d 均分 = %v,应为 3", pi+1, p.Mean)
		}
	}
}

// 阈值边界:1.0(满分及格)应被接受,而不是静默回落 0.6。
func TestAuditThresholdsAcceptOne(t *testing.T) {
	th := Thresholds{Pass: 1.0, Good: 1.0, Low: 1.0}
	if th.pass() != 1.0 || th.good() != 1.0 || th.low() != 1.0 {
		t.Fatalf("1.0 应被接受:pass=%v good=%v low=%v", th.pass(), th.good(), th.low())
	}
	if (Thresholds{Pass: 1.5}).pass() != 0.6 {
		t.Fatal("越界值(>1)仍应回落 0.6")
	}
}
