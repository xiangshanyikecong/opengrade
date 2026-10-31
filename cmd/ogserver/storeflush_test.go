package main

// 存储增量化(去抖落盘)语义测试:
//   - 评分类热路径(SetGrade/SetGradePart)不再逐笔全量重写 exam.json,由 flusher 去抖合并;
//   - FlushAll 冲刷后磁盘状态与内存一致(重启恢复口径);
//   - 删除与在途写盘竞争:删除后 FlushAll 不复活目录;
//   - Stop 优雅关停冲刷。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

func newFlushExam(t *testing.T) (*Store, *Exam) {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	ex, err := s.Create(NewExam{
		Layout: card.Layout{Version: 2, Title: "FLUSH", RegDigits: 4, Columns: 2,
			Questions: []card.QuestionSpec{{Kind: card.KindChoice, Options: 4, Score: 2}},
			Subjectives: []card.SubjectiveSpec{
				{Kind: card.KindSubjective, Score: 6},
			},
		},
		Key:        []string{"A"},
		Results:    []*omr.Result{{RegNumber: "0001", RegValid: true, Answers: []string{"A"}}},
		Roster:     []roster.Student{{Reg: "0001", Name: "甲", Classes: []string{"一班"}}},
		CreatedBy:  "t",
		Imposition: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, ex
}

func diskExam(t *testing.T, dir, id string) *Exam {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id, "exam.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ex Exam
	if err := json.Unmarshal(b, &ex); err != nil {
		t.Fatal(err)
	}
	return &ex
}

func TestDebouncedFlushSemantics(t *testing.T) {
	s, ex := newFlushExam(t)
	if _, err := s.SetGrade(ex.ID, 1, 1, score.Grade{Score: 5, Graded: true}, "t1", 0); err != nil {
		t.Fatal(err)
	}
	// 去抖窗口内:内存可读,磁盘尚未写(写放大已摊平)
	if g := s.exams[ex.ID].Grades["s1-q1"]; !g.Graded {
		t.Fatal("内存应立即可见评分")
	}
	if de := diskExam(t, s.dir, ex.ID); len(de.Grades) != 0 {
		t.Fatalf("去抖窗口内 exam.json 不应已含评分,得到 %d 条", len(de.Grades))
	}
	// 冲刷后磁盘一致(重启恢复口径)
	s.FlushAll()
	de := diskExam(t, s.dir, ex.ID)
	if g := de.Grades["s1-q1"]; !g.Graded || g.Score != 5 {
		t.Fatalf("FlushAll 后 exam.json 应含评分 5 分: %+v", g)
	}
	if !de.AuditHas("setGrade") {
		t.Fatal("审计条目应随评分落盘")
	}
}

// AuditHas 是测试辅助:exam.json 的审计里是否出现过某动作。
func (ex *Exam) AuditHas(action string) bool {
	for _, a := range ex.Audit {
		if a.Action == action {
			return true
		}
	}
	return false
}

func TestDeleteBeatsInFlightFlush(t *testing.T) {
	s, ex := newFlushExam(t)
	if _, err := s.SetGrade(ex.ID, 1, 1, score.Grade{Score: 5, Graded: true}, "t1", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ex.ID); err != nil {
		t.Fatal(err)
	}
	s.FlushAll() // 挂起的脏考试已随 Delete 撤销;在途写盘复核后清理
	if _, err := os.Stat(filepath.Join(s.dir, ex.ID, "exam.json")); !os.IsNotExist(err) {
		t.Fatal("删除后 FlushAll 不得复活考试目录")
	}
}

func TestStopFlushes(t *testing.T) {
	s, ex := newFlushExam(t)
	if _, err := s.SetGrade(ex.ID, 1, 1, score.Grade{Score: 5, Graded: true}, "t1", 0); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	if g := diskExam(t, s.dir, ex.ID).Grades["s1-q1"]; !g.Graded {
		t.Fatal("Stop 应冲刷未落盘评分(优雅关停口径)")
	}
	s2, err := NewStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	got, ok := s2.Get(ex.ID)
	if !ok {
		t.Fatal("重启后考试应仍在")
	}
	if g := got.Grades["s1-q1"]; !g.Graded || g.Score != 5 {
		t.Fatalf("重启后评分丢失: %+v", g)
	}
}

func TestSyncOpsStillImmediate(t *testing.T) {
	// 关键操作(发布/改答案/元数据)保持同步落盘:响应返回即持久
	s, ex := newFlushExam(t)
	if err := s.SetPublished(ex.ID, true, "t1"); err != nil {
		t.Fatal(err)
	}
	if !diskExam(t, s.dir, ex.ID).Published {
		t.Fatal("发布应同步落盘")
	}
	if err := s.SetMeta(ex.ID, &[]string{"2026-10-31"}[0], nil, nil, nil, nil, nil, "t1"); err != nil {
		t.Fatal(err)
	}
	if de := diskExam(t, s.dir, ex.ID); de.ExamDate != "2026-10-31" {
		t.Fatalf("元数据应同步落盘: %q", de.ExamDate)
	}
}
