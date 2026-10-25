package main

// 错题重组练习卡(CE 轨道 G8):从一次考试里抽出某学生答错/丢分的题目,
// 重新编译成一张只含这些题的新版式(可打印 SVG 再练)。这是智学网错题本
// "只做图片拼接"之外的差异化闭环:错题 → 新答题卡 → 再练。
//
// 实现上不新增渲染代码:返回一个标准 card.Layout,前端交给既有的
// /api/v1/cards 渲染成可打印 SVG(几何同源,练习卡天然可被 OMR 继续识别)。

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/score"
)

// practiceResult 是错题练习卡的构建结果。
type practiceResult struct {
	Title      string      `json:"title"`
	Layout     card.Layout `json:"layout"`
	Questions  []int       `json:"questions"`  // 原卷题号(1-based),与练习卡顺序一致
	Objective  int         `json:"objective"`  // 抽出的客观题数
	Subjective int         `json:"subjective"` // 抽出的主观题数
}

// buildPractice 依据一份"逐题是否错"的判定,重建一张练习卡版式。
// wrong[qi] = true 表示原卷第 qi 题(1-based)需要重练。
func buildPractice(ex *Exam, title string, wrong map[int]bool) practiceResult {
	nObj := len(ex.Layout.Questions)
	var qs []card.QuestionSpec
	var qIdx []int
	for qi, q := range ex.Layout.Questions {
		if !wrong[qi+1] {
			continue
		}
		qs = append(qs, q) // 保留题型/选项/分值/知识点,答题区留空
		qIdx = append(qIdx, qi+1)
	}
	var subs []card.SubjectiveSpec
	nSub := 0
	for si, s := range ex.Layout.Subjectives {
		if !wrong[nObj+si+1] {
			continue
		}
		subs = append(subs, s)
		qIdx = append(qIdx, nObj+si+1)
		nSub++
	}
	// 选做组残缺(练习卡只抽到组内一题)时退化为普通题:组规则要求至少两道
	gsize := map[string]int{}
	for _, s := range subs {
		if s.Elective != "" {
			gsize[s.Elective]++
		}
	}
	for i := range subs {
		if subs[i].Elective != "" && gsize[subs[i].Elective] < 2 {
			subs[i].Elective = ""
		}
	}
	layout := card.Layout{
		Version: 2, Title: title,
		RegDigits: 0, // 练习卡不设考号区,直接做题
		Columns:   ex.Layout.Columns,
		Questions: qs, Subjectives: subs,
	}
	if layout.Columns == 0 {
		layout.Columns = 2
	}
	return practiceResult{Title: title, Layout: layout, Questions: qIdx, Objective: len(qs), Subjective: nSub}
}

// wrongSetOfStudent 汇总某生在某场考试的错题集合:客观题答错、主观题未满分。
// 空答案键题不判对错、未评完的主观题不进错题(与学生端逐题明细同口径)。
func wrongSetOfStudent(ex *Exam, idx int) map[int]bool {
	wrong := map[int]bool{}
	res := ex.Results[idx-1]
	for qi := range ex.ObjectiveKey {
		if ex.ObjectiveKey[qi] == "" {
			continue // 答案未录:该题不判分,不进错题重练
		}
		ans := ""
		if res != nil && qi < len(res.Answers) {
			ans = res.Answers[qi]
		}
		if ans != ex.ObjectiveKey[qi] {
			wrong[qi+1] = true
		}
	}
	base := len(ex.ObjectiveKey)
	for qi, spec := range ex.Layout.Subjectives {
		max := score.SubjectiveMax(spec)
		if s, done, _ := score.SubjectiveScore(spec, ex.Grades, idx, qi+1); done && s < max {
			wrong[base+qi+1] = true
		}
	}
	return wrong
}

// handleStudentPracticeSVG 学生端错题练习卡直接出图:GET /api/v1/student/exams/{id}/practice.svg?child=
// 版式在服务端按本人错题重建(不接受前端提交的任意版式),学生拿到即可打印;
// 前端无需再借道 staff-only 的 /api/v1/cards 渲染接口。
func handleStudentPracticeSVG(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !ex.Published {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或成绩未发布"))
		return
	}
	reg, ok := pickTarget(w, r, u)
	if !ok {
		return
	}
	idx := findIndex(ex, reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("未找到本人答卷"))
		return
	}
	if ex.Results[idx-1] != nil && ex.Results[idx-1].Absent {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("缺考无错题可重练"))
		return
	}
	res := buildPractice(ex, fmt.Sprintf("错题重练 · 考试 %s", ex.ID), wrongSetOfStudent(ex, idx))
	if res.Objective+res.Subjective == 0 {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("本场无错题,无需重练"))
		return
	}
	sheet, err := res.Layout.Build()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(card.RenderSVG(sheet, nil))
}

// handleStaffPractice 教师端生成某生错题练习卡:GET /api/v1/exams/{id}/practice?student=N
func handleStaffPractice(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	idx, err := strconv.Atoi(r.URL.Query().Get("student"))
	if err != nil || idx < 1 || idx > len(ex.Results) || ex.Results[idx-1] == nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("student 参数不合法"))
		return
	}
	res := buildPractice(ex, fmt.Sprintf("错题重练 · 考试 %s", ex.ID), wrongSetOfStudent(ex, idx))
	if res.Objective+res.Subjective == 0 {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("该生本场无错题,无需重练"))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
