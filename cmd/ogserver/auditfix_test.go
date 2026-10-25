package main

// 深度排查修复批(2026-10)的回归测试:
//   - 知识点编辑必须经 Store.SetKnowledge(锁内写时复制):审计条目不得被后续保存抹掉;
//   - 按班过滤视图必须连同删除小问评分键;
//   - 班级逐题率与讲评课件口径必须排除作废/卷种待指派卷。

import (
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

func regResult(reg string, answers []string) *omr.Result {
	return &omr.Result{RegNumber: reg, RegValid: true, Answers: answers}
}

func newAuditExam(t *testing.T) (*Store, *Exam) {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := s.Create(NewExam{
		Layout: card.Layout{Version: 2, Title: "T", RegDigits: 4, Columns: 2,
			Questions: []card.QuestionSpec{
				{Kind: card.KindChoice, Options: 4, Score: 2},
				{Kind: card.KindChoice, Options: 4, Score: 2},
			},
			Subjectives: []card.SubjectiveSpec{{
				Kind: card.KindSubjective, Score: 6,
				Parts: []card.SubjectivePart{{Score: 3}, {Score: 3}},
			}},
		},
		Key:     []string{"A", "B"},
		Results: []*omr.Result{regResult("0001", []string{"A", "B"}), regResult("0002", []string{"A", "C"})},
		Roster: []roster.Student{
			{Reg: "0001", Name: "甲", Classes: []string{"一班"}},
			{Reg: "0002", Name: "乙", Classes: []string{"二班"}},
		},
		CreatedBy: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, ex
}

// SetKnowledge 后再发生其它保存,审计条目与标签必须同时存活(锁住"锁外改快照落盘"回归)。
func TestAuditKnowledgeSurvivesLaterSaves(t *testing.T) {
	s, ex := newAuditExam(t)
	if _, err := s.SetKnowledge(ex.ID, map[int]string{1: "函数", 3: "导数"}, "teacher1"); err != nil {
		t.Fatal(err)
	}
	// 之后任何人改分触发下一次 save(旧行为:这次 save 会把 setKnowledge 审计从磁盘抹掉);
	// 题 1 拆了小问,改分走 SetGradePart
	if _, err := s.SetGradePart(ex.ID, 1, 1, 1, score.Grade{Score: 3, Graded: true}, "teacher2", 0); err != nil {
		t.Fatal(err)
	}
	// 从磁盘重载,两条审计都在、知识点标签还在
	s2, err := NewStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	re, ok := s2.Get(ex.ID)
	if !ok {
		t.Fatal("考试在重载后消失")
	}
	var hasKnowledge, hasGrade bool
	for _, a := range re.Audit {
		if a.Action == "setKnowledge" {
			hasKnowledge = true
		}
		if a.Action == "setGrade" {
			hasGrade = true
		}
	}
	if !hasKnowledge || !hasGrade {
		t.Fatalf("审计条目缺失: setKnowledge=%v setGrade=%v (audit=%v)", hasKnowledge, hasGrade, re.Audit)
	}
	if re.Layout.Questions[0].Knowledge != "函数" {
		t.Fatalf("知识点标签未持久化: %q", re.Layout.Questions[0].Knowledge)
	}
	// 越界题号拒绝
	if _, err := s.SetKnowledge(ex.ID, map[int]string{99: "x"}, "teacher1"); err == nil {
		t.Fatal("越界题号应报错")
	}
}

// 按班过滤必须连小问评分键一起删:二班教师的视图中不得残留一班学生 s1 的任何评分键。
func TestAuditScopeDropsPartGradeKeys(t *testing.T) {
	_, ex := newAuditExam(t)
	ex.Grades = map[string]score.Grade{
		score.GradeKey(1, 1):          {Score: 6, Graded: true},
		score.GradePartKey(1, 1, 1):   {Score: 3, Graded: true},
		score.GradePartKey(1, 1, 2):   {Score: 3, Graded: true},
		score.GradePartKey(2, 1, 1):   {Score: 1, Graded: true},
	}
	scoped := scopeExamToClasses(ex, []string{"二班"})
	for k := range scoped.Grades {
		if len(k) >= 4 && k[:4] == "s1-q" {
			t.Fatalf("过滤后仍残留一班学生的评分键 %q", k)
		}
	}
	if _, ok := scoped.Grades[score.GradePartKey(2, 1, 1)]; !ok {
		t.Fatal("二班学生的评分键被误删")
	}
}

// 班级逐题率:作废卷与卷种待指派卷不得计入(与全体统计同口径)。
func TestAuditPerClassRatesExcludeVoidAndPending(t *testing.T) {
	_, ex := newAuditExam(t)
	ex.Results[0].Voided = true          // 0001 作废
	ex.Results[1].PendingVariant = true  // 0002 卷种待指派
	rates := perClassQuestionRates(ex)
	for cls, rs := range rates {
		if rs[0] != -1 || rs[1] != -1 {
			t.Fatalf("班级 %s 的逐题率应无样本(-1),得到 %v", cls, rs)
		}
	}
	// 恢复两卷:一班(0001 全对)、二班(0002 对 1 错 2)各自独立;主观题未阅为 -1
	ex.Results[0].Voided = false
	ex.Results[1].PendingVariant = false
	rates = perClassQuestionRates(ex)
	if r := rates["一班"]; r == nil || r[0] != 1 || r[1] != 1 || r[2] != -1 {
		t.Fatalf("一班逐题率 = %v,应为 [1 1 -1](仅 0001)", r)
	}
	if r := rates["二班"]; r == nil || r[0] != 1 || r[1] != 0 || r[2] != -1 {
		t.Fatalf("二班逐题率 = %v,应为 [1 0 -1](仅 0002)", r)
	}
}
