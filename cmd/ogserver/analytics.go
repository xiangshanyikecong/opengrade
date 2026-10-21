package main

// 学情统计与量产阅卷机制的处理器:
//   - 客观题答案修正 → 全班重统(量产事故补救,现在无需删考重建)
//   - 成绩发布控制(教师确认后学生/家长方可查)
//   - 等级赋分换算(新高考再选科目)
//   - 操作审计日志查询(合规追溯)
//   - 多维分析报告(统计摘要 + 分数段 + 逐题难度/区分度)

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/score"
)

// handleSetKey 修正客观题参考答案:POST /api/v1/exams/{id}/key {"key":["A","B",...]}
// 管理员/年级组长/建卷教师可操作;修正后全班客观题分自动重统。
func handleSetKey(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Key []string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	// 仅管理员/年级组长/建卷人可改标准答案(普通任课教师不可动他人卷的答案键)
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可修正参考答案"))
		return
	}
	if err := store.SetObjectiveKey(ex.ID, req.Key, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePublish 成绩发布控制:POST /api/v1/exams/{id}/publish {"published":true}
// 仅管理员/年级组长/建卷教师可发布或撤回(他人考试不可代发布)。
func handlePublish(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Published bool `json:"published"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可控制成绩发布"))
		return
	}
	if err := store.SetPublished(ex.ID, req.Published, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"published": req.Published})
}

// handleAudit 操作审计日志:GET /api/v1/exams/{id}/audit(管理员/年级组长)。
func handleAudit(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	type entry struct {
		Time   string `json:"time"`
		By     string `json:"by"`
		Action string `json:"action"`
		Detail string `json:"detail"`
	}
	out := make([]entry, 0, len(ex.Audit))
	for i := len(ex.Audit) - 1; i >= 0; i-- { // 最新在前
		a := ex.Audit[i]
		out = append(out, entry{a.Time.Format("2006-01-02 15:04:05"), a.By, a.Action, a.Detail})
	}
	writeJSON(w, http.StatusOK, struct {
		Published   bool    `json:"published"`
		PublishedAt string  `json:"publishedAt,omitempty"`
		Entries     []entry `json:"entries"`
	}{ex.Published, pubTime(ex), out})
}

func pubTime(ex *Exam) string {
	if ex.PublishedAt == nil {
		return ""
	}
	return ex.PublishedAt.Format("2006-01-02 15:04")
}

// handleGradeConvert 等级赋分换算:POST /api/v1/exams/{id}/grade-convert
// {"pass":..,"bands":[{"name","ratio","low","high"}...]} → 原始分→赋分对照表。
// 用于再选科目的"原始分—赋分"双轨呈现。
func handleGradeConvert(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	var req struct {
		score.GradeScale
		Fine bool `json:"fine"` // true=21 级细分档
	}
	if r.ContentLength > 0 {
		_ = decodeJSON(r, &req)
	}
	scale := req.GradeScale
	if req.Fine && len(scale.Bands) == 0 {
		scale = score.FineGradeScale()
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	regs := make([]string, 0, len(rep.Students))
	raw := make([]float64, 0, len(rep.Students))
	for _, line := range rep.Students {
		if line.Absent {
			continue // 缺考不参与赋分
		}
		regs = append(regs, line.RegNumber)
		raw = append(raw, line.Score)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scale":     scaleOrDefault(scale),
		"converted": score.ConvertGrades(regs, raw, scale),
	})
}

func scaleOrDefault(s score.GradeScale) score.GradeScale {
	if len(s.Bands) == 0 {
		return score.DefaultGradeScale()
	}
	return s
}

// handleAnalytics 多维分析报告:GET /api/v1/exams/{id}/analytics
// 汇总统计摘要 + 分数段 + 逐题难度/区分度 + 选项分布,供报表页图表化呈现。
func handleAnalytics(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	labels := make([]string, len(rep.PerQuestion))
	for i, q := range rep.PerQuestion {
		labels[i] = questionLabel(ex, q)
	}
	// 逐题附加显示名,便于前端直接渲染
	type qView struct {
		score.QuestionStat
		Label string `json:"label"`
	}
	qs := make([]qView, len(rep.PerQuestion))
	for i, q := range rep.PerQuestion {
		qs[i] = qView{QuestionStat: q, Label: labels[i]}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        ex.ID,
		"subject":   ex.Subject,
		"total":     rep.Total,
		"stats":     rep.Stats,
		"byClass":   computeClassStats(ex, rep),
		"questions": qs,
		"knowledge": knowledgeStatsOf(ex),
	})
}

var _ = strings.TrimSpace
