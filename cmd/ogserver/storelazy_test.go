package main

// 懒加载存储语义测试(摘要索引 + LRU 驻留上限):
//   - 启动只载全量摘要,全量考试不进内存;首次访问懒加载;
//   - 驻留上限内逐干净缓存,脏考试钉住;
//   - 摘要缺失/过期在启动时一次性重建;
//   - VisitExams 流式全扫越过驻留上限;
//   - 摘要粗筛口径与全量考试一致(列表/学生端/监考冲突的字段来源)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

func newLazyExam(t *testing.T, s *Store, i int) *Exam {
	t.Helper()
	ex, err := s.Create(NewExam{
		Layout: card.Layout{Version: 2, Title: "LAZY", RegDigits: 4, Columns: 2,
			Questions: []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 2}},
			Subjectives: []card.SubjectiveSpec{
				{Kind: card.KindSubjective, Score: 6},
			},
		},
		Key:       []string{"A"},
		Results:   []*omr.Result{{RegNumber: "0001", RegValid: true, Answers: []string{"A"}}},
		Roster:    []roster.Student{{Reg: "0001", Name: "甲", Classes: []string{"一班"}}},
		CreatedBy: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ex
}

func TestLazyBootSummariesOnly(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, newLazyExam(t, s1, i).ID)
	}
	if _, err := s1.SetGrade(ids[0], 1, 1, score.Grade{Score: 5, Graded: true}, "t1", 0); err != nil {
		t.Fatal(err)
	}
	s1.FlushAll()
	s1.Stop()

	// 重开:全量考试不进内存,摘要齐全
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	if len(s2.exams) != 0 {
		t.Fatalf("重启后全量考试不应驻留内存,得到 %d 场", len(s2.exams))
	}
	if len(s2.summaries) != 3 {
		t.Fatalf("重启后摘要数 = %d,期望 3", len(s2.summaries))
	}
	// 摘要承载列表字段(含阶段机数字与评分状态)
	var graded int
	s2.VisitSummaries(func(sm *ExamSummary) {
		if sm.Students != 1 || sm.Objective != 1 || sm.Subjective != 1 {
			t.Fatalf("摘要字段异常: %+v", sm)
		}
		if sm.Stage.Total != 1 {
			t.Fatalf("摘要阶段 Total = %d,期望 1", sm.Stage.Total)
		}
		if sm.Stage.Ungraded == 0 {
			graded++
		}
	})
	if graded != 1 {
		t.Fatalf("已评分考试的摘要 Ungraded 应为 0,得到 %d 场", graded)
	}
	// 首次访问懒加载,数据完整
	ex, ok := s2.Get(ids[0])
	if !ok {
		t.Fatal("懒加载失败")
	}
	if g := ex.Grades["s1-q1"]; !g.Graded || g.Score != 5 {
		t.Fatalf("懒加载后评分丢失: %+v", g)
	}
	if len(s2.exams) != 1 {
		t.Fatalf("Get 后应恰有 1 场进缓存,得到 %d", len(s2.exams))
	}
}

func TestLazyEvictionBound(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	s.cacheMax = 2
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, newLazyExam(t, s, i).ID)
	}
	if len(s.exams) > 2 {
		t.Fatalf("建考 5 场后缓存应收敛到上限 2,得到 %d", len(s.exams))
	}
	// 越过上限的考试仍可按需加载且数据完整(逐出的是干净缓存,零损失)
	for i, id := range ids {
		ex, ok := s.Get(id)
		if !ok {
			t.Fatalf("第 %d 场懒加载失败", i)
		}
		if len(ex.Results) != 1 {
			t.Fatalf("第 %d 场数据不完整", i)
		}
	}
	if len(s.exams) > 2 {
		t.Fatalf("遍历 5 场后缓存仍应在上限内,得到 %d", len(s.exams))
	}
}

func TestDirtyPinnedFromEviction(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	s.cacheMax = 1
	ex1 := newLazyExam(t, s, 1)
	if _, err := s.SetGrade(ex1.ID, 1, 1, score.Grade{Score: 5, Graded: true}, "t1", 0); err != nil {
		t.Fatal(err)
	}
	newLazyExam(t, s, 2) // 第二场建考触发驱逐:脏的 ex1 必须钉住
	if s.dirty[ex1.ID] == nil {
		t.Fatal("脏考试不得被驱逐(待落盘状态丢失 = 评分丢失)")
	}
	s.FlushAll()
	if s.dirty[ex1.ID] != nil {
		t.Fatal("FlushAll 后脏标记应摘除")
	}
	// 两场考试的评分都在盘上(重启口径)
	s.Stop()
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	n := 0
	s2.VisitSummaries(func(sm *ExamSummary) {
		if sm.Stage.Ungraded == 0 {
			n++
		}
	})
	if n != 1 {
		t.Fatalf("重启后已评分考试数 = %d,期望 1(ex1 评分经脏钉住+冲刷存活;ex2 未评)", n)
	}
	if g := func() score.Grade { e, _ := s2.Get(ex1.ID); return e.Grades["s1-q1"] }(); !g.Graded || g.Score != 5 {
		t.Fatalf("重启后 ex1 评分丢失: %+v", g)
	}
}

func TestSummaryRebuildOnBoot(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ex := newLazyExam(t, s1, 1)
	s1.Stop()

	// 场景一:摘要文件被删 → 启动重建
	if err := os.Remove(filepath.Join(dir, ex.ID, "summary.json")); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.summaries) != 1 {
		t.Fatalf("摘要删除后启动应重建,得到 %d 条", len(s2.summaries))
	}
	if sm := s2.summaries[ex.ID]; sm == nil || sm.Stage.Total != 1 {
		t.Fatalf("重建摘要内容异常: %+v", s2.summaries[ex.ID])
	}
	s2.Stop()

	// 场景二:exam.json 比摘要新(写 exam 后写 summary 前断电)→ 启动重建
	s3, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	examPath := filepath.Join(dir, ex.ID, "exam.json")
	st, _ := os.Stat(filepath.Join(dir, ex.ID, "summary.json"))
	future := st.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(examPath, future, future); err != nil {
		t.Fatal(err)
	}
	s3.Stop()
	s4, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s4.Stop()
	if len(s4.exams) != 0 {
		t.Fatalf("重建路径不应把全量考试驻留内存,得到 %d", len(s4.exams))
	}
	if len(s4.summaries) != 1 {
		t.Fatalf("摘要过期后启动应重建,得到 %d 条", len(s4.summaries))
	}
}

func TestVisitExamsStreamsBeyondCache(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	s.cacheMax = 2
	for i := 0; i < 5; i++ {
		newLazyExam(t, s, i)
	}
	n := 0
	s.VisitExams(func(ex *Exam) bool {
		if len(ex.Results) != 1 {
			t.Fatalf("流式遍历第 %d 场数据不完整", n)
		}
		n++
		return true
	})
	if n != 5 {
		t.Fatalf("VisitExams 应流式遍历全部 5 场(越过驻留上限),得到 %d", n)
	}
	if len(s.exams) > 2 {
		t.Fatalf("流式遍历不得污染工作集缓存,得到 %d", len(s.exams))
	}
}

func TestSummaryRegsMatchFindIndex(t *testing.T) {
	// 摘要 Regs 与 findIndex 同口径(未作废卷的最终考号),且随作废/恢复即时刷新
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	ex := newLazyExam(t, s, 1)
	if sm := s.summaries[ex.ID]; len(sm.Regs) != 1 || sm.Regs[0] != "0001" {
		t.Fatalf("摘要 Regs 初始 = %v,期望 [0001]", sm.Regs)
	}
	if err := s.SetVoid(ex.ID, 1, true, "t1"); err != nil {
		t.Fatal(err)
	}
	if sm := s.summaries[ex.ID]; len(sm.Regs) != 0 {
		t.Fatalf("作废后摘要 Regs 应即时清空: %v", sm.Regs)
	}
	if err := s.SetVoid(ex.ID, 1, false, "t1"); err != nil {
		t.Fatal(err)
	}
	if sm := s.summaries[ex.ID]; len(sm.Regs) != 1 {
		t.Fatalf("恢复后摘要 Regs 应即时恢复: %v", sm.Regs)
	}
}

func TestSummaryRegsSortedAndBinarySearch(t *testing.T) {
	// 乱序上传(扫描乱序入库)→ 摘要 Regs 必须升序,二分粗筛口径正确
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := s.Create(NewExam{
		Layout: card.Layout{Version: 2, Title: "SORT", RegDigits: 4, Columns: 2,
			Questions:   []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 2}},
			Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 6}},
		},
		Key: []string{"A"},
		Results: []*omr.Result{
			{RegNumber: "0003", RegValid: true, Answers: []string{"A"}},
			{RegNumber: "0001", RegValid: true, Answers: []string{"A"}},
			{RegNumber: "0002", RegValid: true, Answers: []string{"A"}},
		},
		Roster: []roster.Student{
			{Reg: "0001", Name: "甲", Classes: []string{"一班"}},
			{Reg: "0002", Name: "乙", Classes: []string{"一班"}},
			{Reg: "0003", Name: "丙", Classes: []string{"一班"}},
		},
		CreatedBy: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	sm := s.summaries[ex.ID]
	if len(sm.Regs) != 3 || sm.Regs[0] != "0001" || sm.Regs[1] != "0002" || sm.Regs[2] != "0003" {
		t.Fatalf("摘要 Regs 应升序: %v", sm.Regs)
	}
	for _, reg := range []string{"0001", "0002", "0003"} {
		if !summaryHasReg(sm, reg) {
			t.Fatalf("二分漏判 %s", reg)
		}
	}
	if summaryHasReg(sm, "0009") {
		t.Fatal("二分误判 0009 在册")
	}
	s.Stop()

	// 旧版摘要(Regs 卷序)载入归一化:手工写乱序摘要,重启后二分口径仍正确
	var old ExamSummary
	b, _ := os.ReadFile(filepath.Join(dir, ex.ID, "summary.json"))
	json.Unmarshal(b, &old)
	old.Regs = []string{"0003", "0001", "0002"}
	ob, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, ex.ID, "summary.json"), ob, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	sm2 := s2.summaries[ex.ID]
	if sm2.Regs[0] != "0001" || !summaryHasReg(sm2, "0002") {
		t.Fatalf("旧摘要载入应归一化排序: %v", sm2.Regs)
	}
}
