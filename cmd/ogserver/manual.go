package main

// 手工成绩科目(P1:体音美/实验/听力/纯纸笔考试进同一套统计与趋势):
//   - 免版式建考:manual=1 + 学科 + 满分 + 名册,不渲染答题卡、无扫描环节;
//   - 每位名册学生预建一张"待录入"卷(Manual 卷,无切片原图);
//   - 分数即主观题评分(合成单题版式,评分/统分/报告/导出/趋势全部复用既有机器);
//   - 教师在考试详情的成绩录入表逐格打分,或整表粘贴「考号 分数」批量导入。

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

// manualScoreQuestion 是手工成绩考试的固定主观题号(1)。
const manualScoreQuestion = 1

// createManualExam 免版式建考:multipart manual=1, subject, full(满分), roster(必填)
// 以及可选的 examDate/term/examType/examGroup 元数据。
func createManualExam(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	subject := strings.TrimSpace(r.FormValue("subject"))
	full, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("full")), 64)
	if err != nil || full <= 0 || full > 10000 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("满分 full 须为 0~10000 的数值"))
		return
	}
	rb := strings.TrimSpace(r.FormValue("roster"))
	if rb == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("手工成绩考试必须提供学生名册(roster;可从班级勾选带出或粘贴「考号,姓名,班级」)"))
		return
	}
	st, err := roster.Load([]byte(rb))
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("名册解析失败: %w", err))
		return
	}
	if len(st) == 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("名册为空"))
		return
	}

	layout := card.Layout{
		Version:   2,
		Title:     nonEmptyStr(subject, "手工成绩") + "(直录)",
		Questions: []card.QuestionSpec{},
		Subjectives: []card.SubjectiveSpec{{
			Kind: card.KindSubjective, Score: full, Label: "成绩(直录)",
		}},
	}
	// 每位名册学生预建一张待录入卷:考号有效、无扫描原图与切片(阅卷端有占位提示)
	results := make([]*omr.Result, 0, len(st))
	seen := map[string]bool{}
	for _, s := range st {
		if s.Reg == "" || seen[s.Reg] {
			continue
		}
		seen[s.Reg] = true
		results = append(results, &omr.Result{RegNumber: s.Reg, RegValid: true, Manual: true})
	}
	session := strings.TrimSpace(r.FormValue("session"))
	if session != "" && !store.School.HasSession(session) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("场次 %q 不在全校场次表中(先维护场次表)", session))
		return
	}
	ex, err := store.Create(NewExam{
		Layout: layout, Key: []string{}, Results: results,
		CreatedBy: u.Username, Subject: subject, Roster: st,
		ExamDate:   strings.TrimSpace(r.FormValue("examDate")),
		Term:       strings.TrimSpace(r.FormValue("term")),
		ExamType:   strings.TrimSpace(r.FormValue("examType")),
		ExamGroup:  strings.TrimSpace(r.FormValue("examGroup")),
		Session:    session,
		ManualFull: full,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	stage := examStage(ex)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": ex.ID, "students": len(results), "manualFull": full,
		"subject": subject, "stage": stage.Stage, "stageLabel": stage.Label,
	})
}

// handleManualScores 批量直录/修改分数:POST /api/v1/exams/{id}/manual-scores
// {"scores":[{"reg":"00000001","score":92.5,"absent":false}]}
// reg 不存在时报错列出;分数越界拒绝;absent 可选(登记缺考)。逐条返回结果。
func handleManualScores(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
		return
	}
	if ex.ManualFull <= 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("该考试不是手工成绩科目(manual-scores 仅限免版式直录考试;扫描考试的评分走 /api/v1/grades)"))
		return
	}
	var req struct {
		Scores []struct {
			Reg    string   `json:"reg"`
			Score  *float64 `json:"score"`
			Absent *bool    `json:"absent"`
		} `json:"scores"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	type result struct {
		Reg  string `json:"reg"`
		OK   bool   `json:"ok"`
		Name string `json:"name,omitempty"`
		Err  string `json:"error,omitempty"`
	}
	out := []result{}
	anyOK := false
	for _, sc := range req.Scores {
		res := result{Reg: sc.Reg}
		idx := findIndex(ex, sc.Reg)
		if idx == 0 {
			res.Err = "名册/已录名单中无此考号"
			out = append(out, res)
			continue
		}
		res.Name = nameOfReg(ex, sc.Reg)
		if sc.Absent != nil {
			if err := store.SetAbsent(ex.ID, idx, *sc.Absent, u.Username); err != nil && !strings.Contains(err.Error(), "无变化") {
				res.Err = err.Error()
				out = append(out, res)
				continue
			}
		}
		if sc.Score != nil {
			g := score.Grade{Score: *sc.Score, Graded: true}
			// 直录改分是常态:携带当前版本号通过乐观锁(真正的并发互写仍会 409 提示)
			ver := 0
			if old, exists := ex.Grades[score.GradeKey(idx, manualScoreQuestion)]; exists && old.Graded {
				ver = old.Ver
			}
			if _, err := store.SetGrade(ex.ID, idx, manualScoreQuestion, g, u.Username, ver); err != nil {
				res.Err = err.Error()
				out = append(out, res)
				continue
			}
		}
		res.OK = true
		anyOK = true
		out = append(out, res)
	}
	// 空态/全失败时仍返回结构化结果;HTTP 200,前端按 ok 字段渲染
	if !anyOK && len(out) == 0 {
		out = []result{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reg < out[j].Reg })
	writeJSON(w, http.StatusOK, map[string]any{"results": out, "students": len(ex.Results)})
}
