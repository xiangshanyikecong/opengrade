package main

// 卷生命周期与考试元数据(现实对齐批):
//   - 作废/恢复:重扫替换后弃用旧卷、重复卷裁决保留其一 —— 作废卷不进报告/统计/阅卷队列;
//   - 元数据:考试日期/学期/类型/同场分组/匿名阅卷(趋势轴、列表过滤、年级总表依赖);
//   - 任务截止:按题/按班任务设定截止日期,我的任务 overdue 提示;
//   - 成绩复核申请:学生/家长发布后发起,教师裁决回复(改分走阅卷工作台留痕)。

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/opengrade/opengrade/auth"
)

// handleVoidResult 作废/恢复某张卷:POST /api/v1/exams/{id}/results/{student}/void {"voided":true}
func handleVoidResult(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	student, err := strconv.Atoi(r.PathValue("student"))
	if err != nil || student < 1 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("卷号不合法"))
		return
	}
	var req struct {
		Voided *bool `json:"voided"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Voided == nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 voided 字段"))
		return
	}
	// 权限收窄:作废影响统计与阅卷队列,限建卷人/组长/管理员(与改元数据同口径);
	// 普通阅卷教师发现问题卷走 ⚑ 待复核,由有裁决权的人处理。
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷人可作废或恢复卷"))
		return
	}
	if err := store.SetVoid(ex.ID, student, *req.Voided, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleExamMeta 更新考试元数据:POST /api/v1/exams/{id}/meta
// {"examDate":"2026-10-20","term":"2026-2026上","examType":"期中","examGroup":"2026期中","anonymous":true}
// 指针字段缺省 = 不变更;权限口径与修正参考答案一致(管理员/组长/建卷人)。
func handleExamMeta(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷人可修改考试信息"))
		return
	}
	var req struct {
		ExamDate  *string `json:"examDate"`
		Term      *string `json:"term"`
		ExamType  *string `json:"examType"`
		ExamGroup *string `json:"examGroup"`
		Session   *string `json:"session"` // 场次(全校场次表之一;空 = 不参与监考冲突检测)
		Anonymous *bool   `json:"anonymous"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	// 场次必须存在于场次表,防止手滑造出永远匹配不上冲突的幽灵时段
	if req.Session != nil && *req.Session != "" && !store.School.HasSession(*req.Session) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("场次 %q 不在全校场次表中(先到用户管理/组织架构维护场次)", *req.Session))
		return
	}
	if err := store.SetMeta(ex.ID, req.ExamDate, req.Term, req.ExamType, req.ExamGroup, req.Session, req.Anonymous, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTaskDue 设置任务截止时间:POST /api/v1/exams/{id}/task-due {"key":"q2","due":"2026-10-30"}
func handleTaskDue(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	var req struct {
		Key string `json:"key"`
		Due string `json:"due"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := store.SetTaskDue(ex.ID, req.Key, req.Due, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCreateAppeal 学生/家长发起复核申请:POST /api/v1/student/exams/{id}/appeals {"reason":"..."}
func handleCreateAppeal(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	reg, ok := pickTarget(w, r, u)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	n, err := store.AddAppeal(ex.ID, reg, req.Reason)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"n": n})
}

// handleResolveAppeal 教师裁决复核申请:POST /api/v1/exams/{id}/appeals/{n}/resolve {"reply":"..."}
func handleResolveAppeal(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("申请序号不合法"))
		return
	}
	// 裁决(对外回复口径)限建卷人/组长/管理员;改分本身仍走阅卷工作台,权限不变。
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员/年级组长/建卷人可裁决复核申请"))
		return
	}
	var req struct {
		Reply string `json:"reply"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := store.ResolveAppeal(ex.ID, n, req.Reply, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
