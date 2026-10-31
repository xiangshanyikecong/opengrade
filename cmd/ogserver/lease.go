package main

// 阅卷分片领取租约(协作阅卷的撞片治理):
//   切片队列是全量快照,"下一未评"由各端本地推进 —— 两位老师同时阅同一场同一道题,
//   会以相同顺序推进同一批分片;数据由乐观锁兜底不写坏,但后写者 409 丢分、重复劳动。
//   租约把「谁在阅哪张」变成服务端可观测状态:
//     - POST /api/v1/exams/{id}/slices/claim    领取/续期(claim 兼心跳):TTL 3 分钟;
//       活租约被他人持有 → 409;同账号重复 claim = 续期;已评完的片不建租约,
//       返回 graded + 当前评分,阅卷端据此跳过。
//     - POST /api/v1/exams/{id}/slices/release  显式释放(翻片/退出时);断线不释放由 TTL 自愈。
//   设计红线:租约只驱动阅卷端「跳过」,不硬闸写路径 —— SetGrade 不看租约,正确性
//   永远由乐观锁兜底(阅卷人掉线 3 分钟,不能把复核/改分也堵死)。
//   存储形态:Store 级内存 map(不进 Exam、不落盘、不进快照/摘要)—— 租约是会话态
//   而非考试数据,服务重启/考试驱逐即清空,重新领取即可,零迁移负担。

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/score"
)

// sliceLeaseTTL 租约存活时长:覆盖「细看一道大题」的停顿,阅卷端 90s 心跳续期;
// 断线/杀进程后最长 3 分钟自动释放。测试可调短模拟过期。
var sliceLeaseTTL = 3 * time.Minute

// sliceLease 一张分片的阅卷租约(纯内存会话态)。
type sliceLease struct {
	By    string
	Until time.Time
}

// leaseKey 租约键:"s{学生号}_q{主观题号}"。租约粒度到题 —— 小问评分共用同一张片的租约。
func leaseKey(student, question int) string {
	return fmt.Sprintf("s%d_q%d", student, question)
}

// LeaseHeldError 报告分片正被他人持有(处理器映射为 409)。
type LeaseHeldError struct{ By string }

func (e *LeaseHeldError) Error() string {
	if e.By != "" {
		return fmt.Sprintf("该卷正被教师 %s 阅阅中,请先阅下一份(对方约 3 分钟无动作自动释放)", e.By)
	}
	return "该卷正被他人阅阅中,请先阅下一份"
}

// SliceClaim 是一次领取的结果。
type SliceClaim struct {
	Status string  // "ok"(已领取/续期) | "graded"(载入后已被评完,无需领取)
	Score  float64 // graded 时:当前评分(整题口径;小问题取第 1 问,仅回执展示)
	Ver    int     // graded 时:当前评分版本号
	By     string  // graded 时:评分人
}

// ClaimSlice 领取/续期一张分片的阅卷租约。幂等语义:
//   - 无租约/已过期 → 新领;同账号持有中 → 续期(claim 即心跳);
//   - 他人持有且未过期 → LeaseHeldError;
//   - 该片已评完(整题或全部小问已阅)→ 不建租约,返回 graded。
func (s *Store) ClaimSlice(id string, student, question int, by string) (SliceClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.getLocked(id)
	if !ok {
		return SliceClaim{}, fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return SliceClaim{}, fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return SliceClaim{}, fmt.Errorf("主观题序号 %d 越界(共 %d 道)", question, len(ex.Layout.Subjectives))
	}
	if subjectiveGraded(ex, student, question) {
		sc, ver, gradedBy := sliceCurrentGrade(ex, student, question)
		return SliceClaim{Status: "graded", Score: sc, Ver: ver, By: gradedBy}, nil
	}
	now := time.Now()
	key := leaseKey(student, question)
	if l, exists := s.leases[id][key]; exists && now.Before(l.Until) && l.By != by {
		return SliceClaim{}, &LeaseHeldError{By: l.By}
	}
	if s.leases[id] == nil {
		s.leases[id] = map[string]sliceLease{}
	}
	s.leases[id][key] = sliceLease{By: by, Until: now.Add(sliceLeaseTTL)}
	return SliceClaim{Status: "ok"}, nil
}

// ReleaseSlice 释放本人持有的分片租约(翻片/退出时调用;幂等,他人租约不受影响)。
func (s *Store) ReleaseSlice(id string, student, question int, by string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := leaseKey(student, question)
	if l, ok := s.leases[id][key]; ok && l.By == by {
		delete(s.leases[id], key)
	}
}

// LiveLeases 返回该考试当前全部活租约(键→持有人),顺带清理过期条目。
// 供切片列表回填 lockedBy/locked;与考试快照两次取锁,轻微不一致只影响展示。
func (s *Store) LiveLeases(id string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := map[string]string{}
	for key, l := range s.leases[id] {
		if now.After(l.Until) {
			delete(s.leases[id], key)
			continue
		}
		out[key] = l.By
	}
	return out
}

// sliceCurrentGrade 返回切片当前评分的展示口径:整题评分优先;拆分小问取第 1 问
// (仅用于 claim 的 graded 回执,统分口径在 score.SubjectiveScore)。
func sliceCurrentGrade(ex *Exam, student, question int) (float64, int, string) {
	if g, ok := ex.Grades[score.GradeKey(student, question)]; ok && g.Graded {
		return g.Score, g.Ver, g.By
	}
	for p := 1; p <= len(ex.Layout.Subjectives[question-1].Parts); p++ {
		if g, ok := ex.Grades[score.GradePartKey(student, question, p)]; ok && g.Graded {
			return g.Score, g.Ver, g.By
		}
	}
	return 0, 0, ""
}

// handleSliceClaim 领取阅卷租约:POST /api/v1/exams/{id}/slices/claim
// {"question":N,"student":S} → 200 {"status":"ok"|"graded",...} / 409 {"error","lockedBy"}。
func handleSliceClaim(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Question int `json:"question"`
		Student  int `json:"student"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	// 权限口径与评分写路径一致(阅卷全量,不按班级过滤)
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	claim, err := store.ClaimSlice(ex.ID, req.Student, req.Question, u.Username)
	if err != nil {
		var held *LeaseHeldError
		if errors.As(err, &held) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "lockedBy": held.By})
			return
		}
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": claim.Status, "score": claim.Score, "ver": claim.Ver, "by": claim.By,
	})
}

// handleSliceRelease 释放阅卷租约:POST /api/v1/exams/{id}/slices/release
// {"question":N,"student":S}。恒 200(幂等;释放失败由 TTL 自愈)。
func handleSliceRelease(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Question int `json:"question"`
		Student  int `json:"student"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	store.ReleaseSlice(ex.ID, req.Student, req.Question, u.Username)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
