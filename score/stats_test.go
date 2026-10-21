package score

import (
	"math"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// mkResult 构造一位学生的 OMR 结果,answers 每个字符对应一道客观题的作答。
func mkResult(reg, answers string) *omr.Result {
	ans := make([]string, 0, len(answers))
	for _, r := range answers {
		ans = append(ans, string(r))
	}
	return &omr.Result{RegNumber: reg, RegValid: true, Answers: ans}
}

// buildReport 用 5 名学生的两题客观卷生成报告(第 1 题 A 对,第 2 题答案各异)。
func buildReport(t *testing.T) *Report {
	t.Helper()
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 60},
		{Kind: card.KindChoice, Options: 4, Score: 40},
	}
	key := []string{"A", "B"}
	// 两名并列 100 分,一名 60,两名 0 分 —— 排名应为 1,1,3,4,4
	results := []*omr.Result{
		mkResult("00000001", "AB"), // 60+40 = 100
		mkResult("00000002", "AB"), // 100
		mkResult("00000003", "AC"), // 60
		mkResult("00000004", "CC"), // 0
		mkResult("00000005", "CD"), // 0
	}
	_ = results
	r := Merge(questions, key, results, nil, nil)
	if r.Count != 5 {
		t.Fatalf("count = %d", r.Count)
	}
	return r
}

func TestRanks(t *testing.T) {
	r := buildReport(t)
	want := []int{1, 1, 3, 4, 4}
	for i, w := range want {
		if got := r.Students[i].Rank; got != w {
			t.Fatalf("学生 %d 排名 = %d,期望 %d", i+1, got, w)
		}
	}
}

func TestStatsSummary(t *testing.T) {
	r := buildReport(t)
	s := r.Stats
	if s == nil {
		t.Fatal("stats 缺失")
	}
	if s.Total != 100 {
		t.Fatalf("满分 = %v", s.Total)
	}
	if s.Max != 100 || s.Min != 0 || s.Range != 100 {
		t.Fatalf("max/min/range = %v/%v/%v", s.Max, s.Min, s.Range)
	}
	if s.Mean != 52 { // (100+100+60+0+0)/5
		t.Fatalf("均分 = %v,期望 52", s.Mean)
	}
	if s.Median != 60 {
		t.Fatalf("中位数 = %v,期望 60", s.Median)
	}
	// 方差 = ((48)^2*2 + (8)^2 + (52)^2*2)/5 = (4608+64+5408)/5 = 2016
	if math.Abs(s.StdDev-math.Sqrt(2016)) > 1e-9 {
		t.Fatalf("标准差 = %v", s.StdDev)
	}
	if s.PassLine != 60 || s.GoodLine != 85 || s.LowLine != 40 {
		t.Fatalf("阈值线 = %v/%v/%v", s.PassLine, s.GoodLine, s.LowLine)
	}
	if s.PassRate != 0.6 { // 100,100,60 及格;0,0 不及格
		t.Fatalf("及格率 = %v", s.PassRate)
	}
	if s.GoodRate != 0.4 {
		t.Fatalf("优秀率 = %v", s.GoodRate)
	}
	if s.LowRate != 0.4 { // 低分线 40,两名 0 分低于线
		t.Fatalf("低分率 = %v", s.LowRate)
	}
}

func TestSegments(t *testing.T) {
	r := buildReport(t)
	seg := r.Stats.Segments
	if len(seg) != 10 {
		t.Fatalf("分数段数 = %d", len(seg))
	}
	total := 0
	for _, s := range seg {
		total += s.Count
	}
	if total != 5 {
		t.Fatalf("分数段人数合计 = %d,期望 5", total)
	}
	if seg[9].Count != 2 { // 两名 100 分在 [90,100] 段
		t.Fatalf("最高段人数 = %d,期望 2", seg[9].Count)
	}
}

func TestOptionDistribution(t *testing.T) {
	r := buildReport(t)
	q1 := r.PerQuestion[0]
	// 答案:A,A,A,C,C → A=3 C=2,无未答
	if q1.Options["A"] != 3 || q1.Options["C"] != 2 || q1.Options["未答"] != 0 {
		t.Fatalf("第 1 题选项分布 = %v", q1.Options)
	}
	if q1.Options["B"] != 0 {
		t.Fatalf("未选的 B 应为 0:%v", q1.Options)
	}
	if q1.Difficulty != 0.6 {
		t.Fatalf("难度系数 = %v", q1.Difficulty)
	}
}

func TestDiscrimination(t *testing.T) {
	r := buildReport(t)
	// 高分组为并列满分的两人(第 1 题均答对),低分组为两名 0 分(第 1 题均答错)→ 区分度 > 0
	if r.PerQuestion[0].Discrimination <= 0 {
		t.Fatalf("第 1 题区分度应 > 0:%v", r.PerQuestion[0].Discrimination)
	}
	// 反例:高分组 Q1 全错、低分组 Q1 全对 → 区分度为负(-1)
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 1},
		{Kind: card.KindChoice, Options: 4, Score: 1},
		{Kind: card.KindChoice, Options: 4, Score: 1},
	}
	key := []string{"A", "A", "A"}
	results := []*omr.Result{
		mkResult("01", "BAA"), // 总分 2,Q1 错
		mkResult("02", "BAA"), // 总分 2,Q1 错
		mkResult("03", "ABB"), // 总分 1,Q1 对
		mkResult("04", "ABB"), // 总分 1,Q1 对
	}
	r2 := Merge(questions, key, results, nil, nil)
	if d := r2.PerQuestion[0].Discrimination; d != -1 {
		t.Fatalf("高分组错、低分组对的题区分度应为 -1:%v", d)
	}
}

func TestEmptyAndFiltered(t *testing.T) {
	// 全部学生被过滤(nil)→ 不 panic,统计为空
	questions := []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 1}}
	r := Merge(questions, []string{"A"}, []*omr.Result{nil, nil}, nil, nil)
	if r.Count != 0 || r.Stats.Count != 0 {
		t.Fatalf("空报告异常: %+v", r.Stats)
	}
	if len(r.Stats.Segments) != 10 {
		t.Fatal("空报告也应有分数段骨架")
	}
	// 中位数:偶数取均值
	questions2 := []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 10}}
	r3 := Merge(questions2, []string{"A"}, []*omr.Result{
		mkResult("01", "A"), mkResult("02", "B"), mkResult("03", "A"), mkResult("04", "A"),
	}, nil, nil)
	if r3.Stats.Median != 10 { // 排序后 0,10,10,10 → (10+10)/2 = 10
		t.Fatalf("偶数中位数 = %v", r3.Stats.Median)
	}
}

func TestAbsentStudents(t *testing.T) {
	// 缺考行保留(供名册/趋势对齐),但不计入统计、排名与选项分布
	questions := []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 10}}
	key := []string{"A"}
	results := []*omr.Result{
		mkResult("00000001", "A"),                             // 10
		mkResult("00000002", "B"),                             // 0
		{RegNumber: "00000003", RegValid: true, Absent: true}, // 缺考
	}
	r := Merge(questions, key, results, nil, nil)
	if r.Count != 2 || r.AbsentCount != 1 {
		t.Fatalf("count/absentCount = %d/%d,期望 2/1", r.Count, r.AbsentCount)
	}
	if len(r.Students) != 3 || !r.Students[2].Absent {
		t.Fatalf("缺考行应保留在报告中: %+v", r.Students)
	}
	if r.Students[2].Rank != 0 {
		t.Fatalf("缺考行不应有排名: %d", r.Students[2].Rank)
	}
	if r.Students[0].Rank != 1 || r.Students[1].Rank != 2 {
		t.Fatalf("排名应跳过缺考: %d/%d", r.Students[0].Rank, r.Students[1].Rank)
	}
	if r.Stats.Count != 2 || r.Stats.Mean != 5 {
		t.Fatalf("统计应只含实考: count=%d mean=%v", r.Stats.Count, r.Stats.Mean)
	}
	// 选项分布:只统计实考卷(缺考卷空作答不计入"未答")
	if q := r.PerQuestion[0]; q.Attempted != 2 || q.Options["A"] != 1 || q.Options["B"] != 1 || q.Options["未答"] != 0 {
		t.Fatalf("作答统计应只含实考: attempted=%d options=%v", q.Attempted, q.Options)
	}
}

func TestSubjectivePartsMerge(t *testing.T) {
	// 版式 v3 小题切片:一道 17 分解答题拆 6+5+6;全部小问已阅才计入大题
	subjectives := []card.SubjectiveSpec{
		{Kind: card.KindSubjective, Score: 17, Parts: []card.SubjectivePart{{Score: 6}, {Score: 5}, {Score: 6}}},
		{Kind: card.KindSubjective, Score: 10},
	}
	results := []*omr.Result{mkResult("00000001", ""), mkResult("00000002", "")}
	grades := map[string]Grade{
		// 学生1:三问全阅(第 3 问带 ⚑)→ 大题 17
		GradePartKey(1, 1, 1): {Score: 6, Graded: true},
		GradePartKey(1, 1, 2): {Score: 5, Graded: true},
		GradePartKey(1, 1, 3): {Score: 6, Graded: true, Mark: "flag"},
		// 学生2:只阅第 1 问 → 大题不计入(半阅不进统计)
		GradePartKey(2, 1, 1): {Score: 3, Graded: true},
		// 学生2:整题 10 分题得 7
		GradeKey(2, 2): {Score: 7, Graded: true},
	}
	r := Merge(nil, []string{}, results, subjectives, grades)
	if r.Total != 27 {
		t.Fatalf("满分 = %v,期望 27", r.Total)
	}
	s1, s2 := r.Students[0], r.Students[1]
	if s1.Subjective != 17 || !s1.Flagged {
		t.Fatalf("学生1 主观 = %v flagged=%v,期望 17/true", s1.Subjective, s1.Flagged)
	}
	if s2.Subjective != 7 {
		t.Fatalf("学生2 主观 = %v,期望 7(半阅大题不计入)", s2.Subjective)
	}
	q1 := r.PerQuestion[0]
	if q1.Max != 17 || q1.Graded != 1 || q1.Mean != 17 {
		t.Fatalf("大题统计 = max %v graded %d mean %v", q1.Max, q1.Graded, q1.Mean)
	}
	if len(q1.Parts) != 3 {
		t.Fatalf("小问统计缺失: %d", len(q1.Parts))
	}
	if q1.Parts[0].Graded != 2 || q1.Parts[0].Mean != 4.5 { // (6+3)/2
		t.Fatalf("第 1 问统计 = %+v", q1.Parts[0])
	}
	if q1.Parts[2].Graded != 1 || q1.Parts[2].Mean != 6 {
		t.Fatalf("第 3 问统计 = %+v", q1.Parts[2])
	}
}
