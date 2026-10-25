package main

// 知识点学情接口(CE 轨道 G2):建考后逐题挂知识点标签,学情分析出班级掌握度,
// 学生端出个人掌握雷达,导出细目表带知识点列。
// 标签存储在考试会话上(而非独立题库),与"学校自录、数据本地"的定位一致;
// 后续校本题库(bank/)落地后可直接迁移为题属性。

import (
	"fmt"
	"net/http"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

// handleKnowledgeGet 读取逐题知识点:GET /api/v1/exams/{id}/knowledge
func handleKnowledgeGet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       ex.ID,
		"tags":     knowledgeTags(ex),
		"stats":    knowledgeStatsOf(ex),
		"editable": u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username,
	})
}

// knowledgeTags 返回逐题标签快照:客观题 1..n,主观题接续编号。
func knowledgeTags(ex *Exam) map[int]string {
	tags := map[int]string{}
	for i, q := range ex.Layout.Questions {
		tags[i+1] = q.Knowledge
	}
	for i, s := range ex.Layout.Subjectives {
		tags[len(ex.Layout.Questions)+i+1] = s.Knowledge
	}
	return tags
}

// handleKnowledgeSet 写入逐题知识点:POST /api/v1/exams/{id}/knowledge
// {"tags":{"1":"函数","2":"函数、导数",...}};仅管理员/组长/建卷教师。
// 变更走 Store.SetKnowledge(锁内写时复制 + 审计落盘)——此前在锁外直接改快照并
// save,审计条目随下一次保存被静默抹除,且与并发写构成数据竞争。
func handleKnowledgeSet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可编辑知识点"))
		return
	}
	var req struct {
		Tags map[int]string `json:"tags"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	changed, err := store.SetKnowledge(ex.ID, req.Tags, u.Username)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "changed": changed})
}

// knowledgeStatsOf 一场考试的班级维度知识点掌握度(空切片而非 null,便于前端直渲染)。
func knowledgeStatsOf(ex *Exam) []score.KnowledgeStat {
	rep := mergeReport(ex)
	stats := score.KnowledgeStats(ex.Layout.Questions, ex.Layout.Subjectives, rep.PerQuestion)
	if stats == nil {
		stats = []score.KnowledgeStat{}
	}
	return stats
}

// studentKnowledgeOf 计算某生个人知识点掌握度(空切片而非 null)。
func studentKnowledgeOf(ex *Exam, idx int, res *omr.Result) []score.KnowledgeStat {
	rates := map[int]float64{}
	for qi := range ex.ObjectiveKey {
		ans := ""
		if res != nil && qi < len(res.Answers) {
			ans = res.Answers[qi]
		}
		if ans == "" {
			continue
		}
		if ans == ex.ObjectiveKey[qi] {
			rates[qi+1] = 1
		} else {
			rates[qi+1] = 0
		}
	}
	base := len(ex.ObjectiveKey)
	eGroups := score.ElectiveGroups(ex.Layout.Subjectives)
	excluded := score.ElectiveExcluded(eGroups, ex.Layout.Subjectives, ex.Grades, res, idx)
	for qi, spec := range ex.Layout.Subjectives {
		if excluded[qi+1] {
			continue // 选做题未被所选:不进个人掌握度口径
		}
		if s, done, _ := score.SubjectiveScore(spec, ex.Grades, idx, qi+1); done {
			if m := score.SubjectiveMax(spec); m > 0 {
				rates[base+qi+1] = s / m
			}
		}
	}
	out := score.StudentKnowledge(ex.Layout.Questions, ex.Layout.Subjectives, rates)
	if out == nil {
		out = []score.KnowledgeStat{}
	}
	return out
}
