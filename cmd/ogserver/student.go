package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/score"
)

// 学生端:基础查分、逐题对错、错题本、答卷回溯 —— 永久免费,且只能看到本人数据。
// 这是对竞品"付费看分"痛点的直接回答(方案稿第一部分痛点 1)。

// findMyIndex 定位学生账号在考试中的卷位:人工指派优先,其次 OMR 判读考号。
func findMyIndex(ex *Exam, reg string) int {
	for i := range ex.Results {
		if ex.AssignedReg(i+1) == reg {
			return i + 1
		}
	}
	return 0
}

// ClassStat 是按班级聚合的统计(教师/组长看对比,学生仅看本班均分参照)。
type ClassStat struct {
	Class    string  `json:"class"`
	Count    int     `json:"count"`
	Mean     float64 `json:"mean"`
	MeanRate float64 `json:"meanRate"`
}

// computeClassStats 依据名册班级与最终考号聚合班级统计。
func computeClassStats(ex *Exam, rep *score.Report) []ClassStat {
	clsOf := map[string]string{} // 最终考号 → 班级
	for _, st := range ex.Roster {
		if st.Class != "" {
			clsOf[st.Reg] = st.Class
		}
	}
	type acc struct {
		n       int
		sum     float64
		rateSum float64
	}
	byClass := map[string]*acc{}
	for _, line := range rep.Students {
		cls := "未匹配"
		if c, ok := clsOf[line.RegNumber]; ok {
			cls = c
		}
		a := byClass[cls]
		if a == nil {
			a = &acc{}
			byClass[cls] = a
		}
		a.n++
		a.sum += line.Score
		if line.Total > 0 {
			a.rateSum += line.Score / line.Total
		}
	}
	var out []ClassStat
	for cls, a := range byClass {
		cs := ClassStat{Class: cls, Count: a.n, Mean: a.sum}
		if a.n > 0 {
			cs.Mean = a.sum / float64(a.n)
			cs.MeanRate = a.rateSum / float64(a.n)
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

// handleStudentExams GET /api/v1/student/exams → 我的考试列表(含个人得分与本班均分)
func handleStudentExams(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	type item struct {
		ID        string  `json:"id"`
		CreatedAt string  `json:"createdAt"`
		Found     bool    `json:"found"`
		Score     float64 `json:"score"`
		Total     float64 `json:"total"`
		ClassMean float64 `json:"classMean"`
	}
	out := []item{}
	for _, ex := range store.List() {
		it := item{ID: ex.ID, CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04")}
		if idx := findMyIndex(ex, u.Reg); idx > 0 {
			rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
			line := rep.Students[idx-1]
			it.Found = true
			it.Score, it.Total = line.Score, line.Total
			for _, c := range computeClassStats(ex, rep) {
				if c.Class == u.Class {
					it.ClassMean = c.Mean
				}
			}
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStudentExam GET /api/v1/student/exams/{id}
// 个人成绩报告:逐题我的作答 vs 正确答案、主观题得分、全班得分率参照、错题本。
func handleStudentExam(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	idx := findMyIndex(ex, u.Reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("本次考试未找到与账号 %s 关联的答卷", u.Reg))
		return
	}
	res := ex.Results[idx-1]

	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	line := rep.Students[idx-1]

	type qItem struct {
		Index    int     `json:"index"`
		Kind     string  `json:"kind"`
		Max      float64 `json:"max"`
		MyAnswer string  `json:"myAnswer,omitempty"` // 客观题作答
		Correct  string  `json:"correct,omitempty"`  // 客观题正确答案
		Right    bool    `json:"right,omitempty"`    // 客观题是否正确
		MyScore  float64 `json:"myScore"`            // 主观题得分
		Wrong    bool    `json:"wrong"`              // 错题本:丢分即入
	}
	questions := make([]qItem, 0, len(ex.ObjectiveKey)+len(ex.Layout.Subjectives))
	var wrongs []int
	for qi := range ex.ObjectiveKey {
		spec := ex.Layout.Questions[qi]
		max := spec.Score
		if max == 0 {
			max = 1
		}
		my := ""
		if qi < len(res.Answers) {
			my = res.Answers[qi]
		}
		right := my == ex.ObjectiveKey[qi]
		if !right {
			wrongs = append(wrongs, qi+1)
		}
		questions = append(questions, qItem{Index: qi + 1, Kind: spec.Kind, Max: max,
			MyAnswer: my, Correct: ex.ObjectiveKey[qi], Right: right, Wrong: !right})
	}
	for qi, spec := range ex.Layout.Subjectives {
		myScore := 0.0
		if g, ok := ex.Grades[score.GradeKey(idx, qi+1)]; ok && g.Graded {
			myScore = g.Score
		}
		if myScore < spec.Score {
			wrongs = append(wrongs, len(ex.ObjectiveKey)+qi+1)
		}
		questions = append(questions, qItem{Index: len(ex.ObjectiveKey) + qi + 1, Kind: spec.Kind,
			Max: spec.Score, MyScore: myScore, Wrong: myScore < spec.Score})
	}

	classRates := map[string]float64{}
	for _, q := range rep.PerQuestion {
		classRates[fmt.Sprintf("q%d", q.Index)] = q.Rate
	}

	out := map[string]any{
		"id":             ex.ID,
		"createdAt":      ex.CreatedAt.Format("2006-01-02 15:04"),
		"reg":            u.Reg,
		"class":          u.Class,
		"objective":      line.Objective,
		"subjective":     line.Subjective,
		"score":          line.Score,
		"total":          line.Total,
		"regValid":       res.RegValid,
		"questions":      questions,
		"wrongQuestions": wrongs,
		"classRates":     classRates,
		"byClass":        computeClassStats(ex, rep),
	}

	// 主观题作答切片与整卷回溯(仅本人)
	slices := []map[string]string{}
	for qi := range ex.Layout.Subjectives {
		p := filepath.Join(store.SliceDir(ex.ID), fmt.Sprintf("s%02d_q%02d.png", idx, qi+1))
		if fileExists(p) {
			slices = append(slices, map[string]string{
				"question": fmt.Sprintf("%d", len(ex.ObjectiveKey)+qi+1),
				"url":      fmt.Sprintf("/api/v1/student/exams/%s/slice/%d", ex.ID, qi+1),
			})
		}
	}
	out["slices"] = slices
	if fileExists(filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d.jpg", idx))) {
		out["pageURL"] = fmt.Sprintf("/api/v1/student/exams/%s/page", ex.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// handleStudentSlice GET /api/v1/student/exams/{id}/slice/{q} → 我的主观题作答切片
func handleStudentSlice(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	idx := findMyIndex(ex, u.Reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("未找到本人答卷"))
		return
	}
	q, err := strconv.Atoi(r.PathValue("q"))
	if err != nil || q < 1 || q > len(ex.Layout.Subjectives) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("主观题序号不合法"))
		return
	}
	p := filepath.Join(store.SliceDir(ex.ID), fmt.Sprintf("s%02d_q%02d.png", idx, q))
	if !fileExists(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("切片不存在"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, p)
}

// handleStudentPage GET /api/v1/student/exams/{id}/page → 我的整卷回溯(矫正原图)
func handleStudentPage(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	idx := findMyIndex(ex, u.Reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("未找到本人答卷"))
		return
	}
	p := filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d.jpg", idx))
	if !fileExists(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("整卷回溯图不存在"))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

// handleExamPageStaff 教师查看任一学生整卷(讲评用):GET /api/v1/exams/{id}/page/{student}
func handleExamPageStaff(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 整卷含手写姓名,教师按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	idx, err := strconv.Atoi(r.PathValue("student"))
	if err != nil || idx < 1 || idx > len(ex.Results) || ex.Results[idx-1] == nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("无权查看该生答卷"))
		return
	}
	p := filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d.jpg", idx))
	if !fileExists(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("整卷回溯图不存在"))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}
