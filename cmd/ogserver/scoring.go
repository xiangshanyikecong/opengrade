package main

// 评分方案接口(吸收自 OMRChecker 的计分模型):建考后调整漏选部分分/倒扣/
// 不答计分/分节规则。方案挂在 Layout 上随开放数据格式走,判分在读取时经
// score.Merge 现场重算,无缓存失效问题。

import (
	"fmt"
	"net/http"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
)

// scoringEditable 判定当前用户可否编辑评分方案(与知识点/参考答案同一权限口径)。
func scoringEditable(u *auth.User, ex *Exam) bool {
	return u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username
}

// handleScoringGet 读取评分方案:GET /api/v1/exams/{id}/scoring
func handleScoringGet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	scheme := ex.Layout.Scoring
	if scheme == nil {
		scheme = &card.ScoringScheme{} // 零值方案 = 传统精确匹配语义,便于前端直渲染
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       ex.ID,
		"scoring":  scheme,
		"nObj":     len(ex.Layout.Questions),
		"editable": scoringEditable(u, ex),
	})
}

// handleScoringSet 写入评分方案:POST /api/v1/exams/{id}/scoring
// {"scoring":{...}};传 null/空对象即恢复传统精确匹配语义。仅管理员/组长/建卷教师。
func handleScoringSet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !scoringEditable(u, ex) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可编辑评分方案"))
		return
	}
	var req struct {
		Scoring *card.ScoringScheme `json:"scoring"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := store.SetScoring(ex.ID, req.Scoring, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
