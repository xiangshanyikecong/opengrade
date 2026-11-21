package score

import (
	"math"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// 评分方案(评分规则/漏选部分分/倒扣/分节)回归:判分口径唯一(Evaluate),
// Merge 的卷面满分、逐题统计与班级/错题侧共用同一解析。

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEvaluateLegacySemantics(t *testing.T) {
	spec := card.QuestionSpec{Kind: card.KindChoice, Score: 2}
	// 零值规则 = 传统精确匹配:对得满分,错/未答 0 分
	if e, v := Evaluate(spec, card.ScoringRule{}, "A", "A"); e != 2 || v != VerdictCorrect {
		t.Fatalf("答对 = %v/%v, 期望 2/correct", e, v)
	}
	if e, v := Evaluate(spec, card.ScoringRule{}, "B", "A"); e != 0 || v != VerdictIncorrect {
		t.Fatalf("答错 = %v/%v", e, v)
	}
	if e, v := Evaluate(spec, card.ScoringRule{}, "", "A"); e != 0 || v != VerdictUnanswered {
		t.Fatalf("未答 = %v/%v", e, v)
	}
}

func TestEvaluateMultiPartial(t *testing.T) {
	multi := card.QuestionSpec{Kind: card.KindMultiple, Options: 4, Score: 4}
	cases := []struct {
		name  string
		rule  card.ScoringRule
		ans   string
		earn  float64
		verd  string
	}{
		{"精确命中给满分(不受漏选规则影响)", card.ScoringRule{MultiPartial: card.MultiPartialRatio}, "ABC", 4, VerdictCorrect},
		{"漏选按占比", card.ScoringRule{MultiPartial: card.MultiPartialRatio}, "AB", 8.0 / 3, VerdictPartial},
		{"漏选得半分", card.ScoringRule{MultiPartial: card.MultiPartialHalf}, "AB", 2, VerdictPartial},
		{"默认漏选不得分", card.ScoringRule{}, "AB", 0, VerdictIncorrect},
		{"错选归零优先于漏选规则", card.ScoringRule{MultiPartial: card.MultiPartialRatio}, "ABD", 0, VerdictIncorrect},
		{"错选按 Incorrect 倒扣", card.ScoringRule{MultiWrong: card.MultiWrongIncorrect, Incorrect: -1}, "ABD", -1, VerdictIncorrect},
	}
	for i := range cases {
		c := &cases[i]
		e, v := Evaluate(multi, c.rule, c.ans, "ABC")
		if !approx(e, c.earn) || v != c.verd {
			t.Fatalf("%s: Evaluate = %v/%v, 期望 %v/%v", c.name, e, v, c.earn, c.verd)
		}
	}
	// 加权:OptionScores = A:2 B:3 C:4(和为 9),答案 ABC,选 AB → 满分 × 5/9
	weighted := card.QuestionSpec{Kind: card.KindMultiple, Options: 4, Score: 4, OptionScores: []float64{2, 3, 4, 0}}
	e, v := Evaluate(weighted, card.ScoringRule{MultiPartial: card.MultiPartialWeighted}, "AB", "ABC")
	if !approx(e, 4*5.0/9.0) || v != VerdictPartial {
		t.Fatalf("加权漏选 = %v/%v, 期望 %v/partial", e, v, 4*5.0/9.0)
	}
	// 未配置 OptionScores 时 weighted 退化为 ratio
	e, _ = Evaluate(multi, card.ScoringRule{MultiPartial: card.MultiPartialWeighted}, "AB", "ABC")
	if !approx(e, 8.0/3) {
		t.Fatalf("weighted 退化 ratio = %v", e)
	}
}

func TestEvaluateUnmarkedAndCorrectOverride(t *testing.T) {
	// 不答计分 + 规则 Correct 覆盖题面分值
	rule := card.ScoringRule{Correct: 3, Unmarked: 0.5, Incorrect: -1}
	if e, v := Evaluate(card.QuestionSpec{Kind: card.KindChoice, Score: 2}, rule, "", "A"); e != 0.5 || v != VerdictUnanswered {
		t.Fatalf("不答计分 = %v/%v", e, v)
	}
	if e, _ := Evaluate(card.QuestionSpec{Kind: card.KindChoice, Score: 2}, rule, "A", "A"); e != 3 {
		t.Fatalf("规则 Correct 应覆盖题面分值 = %v", e)
	}
	if e, _ := Evaluate(card.QuestionSpec{Kind: card.KindChoice, Score: 2}, rule, "B", "A"); e != -1 {
		t.Fatalf("倒扣 = %v", e)
	}
}

func TestSchemeRuleFor(t *testing.T) {
	s := &card.ScoringScheme{
		Default: card.ScoringRule{MultiPartial: card.MultiPartialHalf},
		Sections: []card.ScoringSection{
			{From: 1, To: 10, Rule: card.ScoringRule{Correct: 2}},
			{From: 11, To: 40, Rule: card.ScoringRule{MultiPartial: card.MultiPartialRatio}},
		},
	}
	if r := s.RuleFor(5); r.Correct != 2 {
		t.Fatalf("题 5 应命中第一分节: %+v", r)
	}
	if r := s.RuleFor(20); r.MultiPartial != card.MultiPartialRatio {
		t.Fatalf("题 20 应命中第二分节: %+v", r)
	}
	if r := s.RuleFor(50); r.MultiPartial != card.MultiPartialHalf {
		t.Fatalf("区间外应落回 Default: %+v", r)
	}
	var nilScheme *card.ScoringScheme
	if r := nilScheme.RuleFor(1); r != (card.ScoringRule{}) {
		t.Fatalf("nil 方案应返回零值规则: %+v", r)
	}
	if err := (&card.ScoringScheme{Sections: []card.ScoringSection{{From: 3, To: 2}}}).Validate(10); err == nil {
		t.Fatal("区间倒置应校验失败")
	}
	if err := (&card.ScoringScheme{Default: card.ScoringRule{MultiPartial: "bogus"}}).Validate(10); err == nil {
		t.Fatal("非法 multiPartial 应校验失败")
	}
}

// TestMergeWithScheme 验证方案贯通统分全链路:卷面满分同源、部分分/倒扣进总分、
// 逐题统计出现 Partial/ScoreRate 且 Difficulty=得分率。
func TestMergeWithScheme(t *testing.T) {
	// 4 题:1-2 单选(规则 Correct=2,错倒扣 -1);3 多选漏选按占比;4 单选
	questions := []card.QuestionSpec{
		{Kind: card.KindChoice, Options: 4, Score: 1},
		{Kind: card.KindChoice, Options: 4, Score: 1},
		{Kind: card.KindMultiple, Options: 4, Score: 4},
		{Kind: card.KindChoice, Options: 4, Score: 1},
	}
	scheme := &card.ScoringScheme{
		Sections: []card.ScoringSection{
			{From: 1, To: 2, Rule: card.ScoringRule{Correct: 2, Incorrect: -1}},
			{From: 3, To: 3, Rule: card.ScoringRule{MultiPartial: card.MultiPartialRatio}},
		},
	}
	key := []string{"A", "B", "ABC", "D"}
	mk := func(ans []string) *omr.Result { return &omr.Result{RegValid: true, Answers: ans} }
	results := []*omr.Result{
		mk([]string{"A", "B", "AB", "D"}),  // 2 + 2 + 4*(2/3) + 2 = 6.667
		mk([]string{"C", "B", "ABC", ""}),  // -1 + 2 + 4 + 0.5 = 5.5
		mk([]string{"A", "C", "ABC", "D"}), // 2 - 1 + 4 + 2 = 7
	}
	rep := Merge(questions, key, results, nil, nil, scheme)
	if !approx(rep.Total, 2+2+4+1) { // q4 无分节覆盖 → Default → 满分=题面 1 分
		t.Fatalf("卷面满分应按规则 Correct 解析 = %v, 期望 9", rep.Total)
	}
	want := []float64{2 + 2 + 4*2.0/3 + 1, -1 + 2 + 4 + 0, 2 - 1 + 4 + 1}
	for i, l := range rep.Students {
		if !approx(l.Objective, want[i]) {
			t.Fatalf("学生 %d 客观得分 = %v, 期望 %v", i, l.Objective, want[i])
		}
	}
	byIndex := map[int]QuestionStat{}
	for _, st := range rep.PerQuestion {
		byIndex[st.Index] = st
	}
	if p := byIndex[3].Partial; p != 1 {
		t.Fatalf("第 3 题部分分人数 = %d, 期望 1", p)
	}
	if !approx(byIndex[3].ScoreRate, (4*2.0/3+4+4)/(4*3)) {
		t.Fatalf("第 3 题得分率 = %v", byIndex[3].ScoreRate)
	}
	if !approx(byIndex[3].Difficulty, byIndex[3].ScoreRate) {
		t.Fatalf("难度系数应等于得分率: %v vs %v", byIndex[3].Difficulty, byIndex[3].ScoreRate)
	}
	if r := byIndex[1].Rate; !approx(r, 2.0/3) {
		t.Fatalf("对率口径不变(全对人数/实考) = %v", r)
	}
}
