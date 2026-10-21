package score

// 选做题判分回归(版式 v3):只取所选、漏涂回退、多涂拒绝、
// 统计口径(选做人数为分母)与卷面满分(每组只计一题)。

import (
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

func electiveFixture() ([]card.QuestionSpec, []string, []card.SubjectiveSpec) {
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 4},
		{Kind: card.KindChoice, Options: 4, Score: 4},
	}
	key := []string{"A", "B"}
	// 主观题 3(填空 5 分)、4 与 5(选做组 A,各 10 分)
	subjectives := []card.SubjectiveSpec{
		{Kind: card.KindFillBlank, Score: 5},
		{Kind: card.KindSubjective, Score: 10, Elective: "A"},
		{Kind: card.KindSubjective, Score: 10, Elective: "A"},
	}
	return questions, key, subjectives
}

// 基准:两人各涂一题,总分只计所选;卷面满分 = 8 + 5 + 10(组内一题)。
func TestElectiveMarkedChoice(t *testing.T) {
	questions, key, subjectives := electiveFixture()
	results := []*omr.Result{
		{RegNumber: "1", RegValid: true, Answers: []string{"A", "B"},
			Electives: map[int][]int{2: {0}}}, // 选做题组题号区印在主观题 2 上,涂第 1 个选项
		{RegNumber: "2", RegValid: true, Answers: []string{"A", "B"},
			Electives: map[int][]int{2: {1}}},
	}
	grades := map[string]Grade{
		// 学生 1:选做题 4 得 8 分(所选);未选的题 5 即使被误批 7 分也不计
		GradeKey(1, 1): {Score: 4, Graded: true},
		GradeKey(1, 2): {Score: 8, Graded: true},
		GradeKey(1, 3): {Score: 7, Graded: true},
		// 学生 2:选做题 5 得 6 分
		GradeKey(2, 1): {Score: 2, Graded: true},
		GradeKey(2, 3): {Score: 6, Graded: true},
	}
	rep := Merge(questions, key, results, subjectives, grades)

	if rep.Total != 8+5+10 {
		t.Fatalf("卷面满分 = %v,期望 23(选做组只计一题)", rep.Total)
	}
	s1, s2 := rep.Students[0], rep.Students[1]
	if s1.Score != 8+4+8 || s1.Subjective != 4+8 {
		t.Fatalf("学生 1 总分 = %v(主观 %v),期望 20(主观 12,未选题 7 分不计)", s1.Score, s1.Subjective)
	}
	if s2.Score != 8+2+6 {
		t.Fatalf("学生 2 总分 = %v,期望 16", s2.Score)
	}
	// 逐题统计:分母 = 实际选做人数
	q4, q5 := rep.PerQuestion[3], rep.PerQuestion[4]
	if q4.Elective != "A" || q4.Chosen != 1 || q4.Graded != 1 || q4.Mean != 8 {
		t.Fatalf("选做题 4 统计异常: %+v", q4)
	}
	if q5.Elective != "A" || q5.Chosen != 1 || q5.Graded != 1 || q5.Mean != 6 {
		t.Fatalf("选做题 5 统计异常: %+v", q5)
	}
}

// 漏涂回退:组内恰一题已批阅 → 取该题;多涂无效 → 不计。
func TestElectiveUnmarkedFallback(t *testing.T) {
	questions, key, subjectives := electiveFixture()
	results := []*omr.Result{
		{RegNumber: "1", RegValid: true, Answers: []string{"A", "B"}}, // 题号区漏涂
		{RegNumber: "2", RegValid: true, Answers: []string{"A", "B"},
			Electives: map[int][]int{2: {0, 1}}}, // 多涂
		{RegNumber: "3", RegValid: true, Answers: []string{"A", "B"}}, // 漏涂且组内两题都被批
	}
	grades := map[string]Grade{
		GradeKey(1, 3): {Score: 9, Graded: true}, // 仅题 3 批阅 → 回退取题 3
		GradeKey(2, 2): {Score: 5, Graded: true},
		GradeKey(2, 3): {Score: 5, Graded: true}, // 多涂且两题都被批 → 整组不计
		GradeKey(3, 2): {Score: 10, Graded: true},
		GradeKey(3, 3): {Score: 10, Graded: true}, // 两题均批 → 整组不计
		GradeKey(3, 1): {Score: 5, Graded: true},
	}
	rep := Merge(questions, key, results, subjectives, grades)
	if got := rep.Students[0].Score; got != 8+9 {
		t.Fatalf("漏涂回退:总分 = %v,期望 17(计入题 3 的 9 分)", got)
	}
	if got := rep.Students[1].Score; got != 8 {
		t.Fatalf("多涂无效:总分 = %v,期望 8(选做组整组不计,填空未批)", got)
	}
	if got := rep.Students[2].Score; got != 8+5 {
		t.Fatalf("漏涂且双题被批:总分 = %v,期望 13(选做组不计)", got)
	}
	// 回退命中题 3(主观题 3,PerQuestion[4])的学生 1 应计入该题统计
	q3, q4 := rep.PerQuestion[4], rep.PerQuestion[3]
	if q3.Chosen != 1 || q3.Graded != 1 || q3.Mean != 9 {
		t.Fatalf("题 3(回退命中)统计 = %+v", q3)
	}
	if q4.Chosen != 0 || q4.Graded != 0 {
		t.Fatalf("题 4 无人计入: %+v", q4)
	}
}

// 缺考与被过滤(nil)的卷不进选做统计;手工补录卷无涂点时按批阅回退。
func TestElectiveAbsentAndManual(t *testing.T) {
	questions, key, subjectives := electiveFixture()
	results := []*omr.Result{
		nil, // 被视图过滤的学生
		{RegNumber: "2", RegValid: true, Answers: []string{"A", "B"}, Absent: true},
		{RegNumber: "3", RegValid: true, Answers: []string{"A", "B"}, Manual: true}, // 手工补录:无题号区数据
	}
	grades := map[string]Grade{
		GradeKey(3, 3): {Score: 8, Graded: true},
	}
	rep := Merge(questions, key, results, subjectives, grades)
	if rep.Count != 1 || rep.Students[0].Absent != true {
		t.Fatalf("缺考/过滤行处理异常: count=%d", rep.Count)
	}
	if got := rep.Students[1].Score; got != 8+8 {
		t.Fatalf("手工补录回退:总分 = %v,期望 16", got)
	}
	if q := rep.PerQuestion[4]; q.Chosen != 1 || q.Graded != 1 || q.Mean != 8 {
		t.Fatalf("选做题统计 = %+v", q)
	}
}

// ElectiveGroups 汇总与 GroupOf 定位。
func TestElectiveGroupsIndex(t *testing.T) {
	_, _, subjectives := electiveFixture()
	groups := ElectiveGroups(subjectives)
	if len(groups) != 1 || groups[0].Key != "A" || len(groups[0].Members) != 2 ||
		groups[0].Members[0] != 2 || groups[0].Members[1] != 3 {
		t.Fatalf("选做组汇总异常: %+v", groups)
	}
	if GroupOf(groups, 3) == nil || GroupOf(groups, 1) != nil {
		t.Fatal("GroupOf 定位异常")
	}
	// 排除口径:学生 1 涂第 1 选项 → 题 3 被排除
	ex := ElectiveExcluded(groups, subjectives, map[string]Grade{},
		&omr.Result{Electives: map[int][]int{2: {0}}}, 1)
	if !ex[3] || ex[2] {
		t.Fatalf("排除集合异常: %v", ex)
	}
}
