package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/export"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

// handleAssign 人工确认异常卷考号:POST {"examId","student","reg"}(reg="" 撤销指派)。
func handleAssign(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		ExamID  string `json:"examId"`
		Student int    `json:"student"`
		Reg     string `json:"reg"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if ex, ok := store.Get(req.ExamID); !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if err := store.SetAssignment(req.ExamID, req.Student, req.Reg, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSuggest 异常卷自愈:返回某学生的考号判涂明细与名册推荐候选。
// GET /api/v1/exams/{id}/suggest/{student}
func handleSuggest(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, true) // 异常卷推荐属阅卷流,全量
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	student, err := strconv.Atoi(r.PathValue("student"))
	if err != nil || student < 1 || student > len(ex.Results) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("学生序号不合法"))
		return
	}
	res := ex.Results[student-1]
	out := map[string]any{
		"student":  student,
		"reg":      ex.AssignedReg(student),
		"regValid": res.RegValid,
		"regMarks": res.RegMarks,
		"assigned": ex.Assignments[fmt.Sprintf("s%d", student)] != "",
	}
	if ex.Roster != nil && res.RegMarks != nil {
		out["suggestions"] = roster.Suggest(res.RegMarks, ex.Roster, 3)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRosterSearch 名册手动搜索(指派兜底):GET /api/v1/exams/{id}/roster?q=李
func handleRosterSearch(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, true)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	writeJSON(w, http.StatusOK, roster.SuggestByText(ex.Roster, r.URL.Query().Get("q"), 8))
}

// handleDeleteExam 删除考试及全部数据。
func handleDeleteExam(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	if err := store.Delete(r.PathValue("id")); err != nil {
		httpError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// assignedResults 返回应用人工指派后的结果副本(不改动原始 OMR 数据)。
// 教师视图下被过滤的学生为 nil,原位保留以维持索引稳定。
func assignedResults(ex *Exam) []*omr.Result {
	out := make([]*omr.Result, len(ex.Results))
	for i, res := range ex.Results {
		if res == nil {
			continue
		}
		cp := *res
		if reg := ex.Assignments[fmt.Sprintf("s%d", i+1)]; reg != "" {
			cp.RegNumber = reg
			cp.RegValid = true
		}
		out[i] = &cp
	}
	return out
}

// questionLabel 生成逐题显示名。
func questionLabel(ex *Exam, st score.QuestionStat) string {
	if st.Index <= len(ex.Layout.Questions) {
		k := ex.Layout.Questions[st.Index-1].Kind
		return fmt.Sprintf("%d(%s)", st.Index, kindShort(k))
	}
	if st.Elective != "" {
		return fmt.Sprintf("%d(%s·选做)", st.Index, kindShort("subjective"))
	}
	return fmt.Sprintf("%d(%s)", st.Index, kindShort("subjective"))
}

func kindShort(k string) string {
	switch k {
	case card.KindJudge:
		return "判断"
	case card.KindMultiple:
		return "多选"
	case card.KindFillBlank:
		return "填空"
	case card.KindSubjective:
		return "解答"
	default:
		return "单选"
	}
}

// handleExportXlsx 成绩册导出:GET /api/v1/exams/{id}/export.xlsx
// injectRosterInfo 把名册的姓名/班级(走班多班用 | 连接)注入成绩行,导出成绩册用。
func injectRosterInfo(ex *Exam, rep *score.Report) {
	byReg := map[string]roster.Student{}
	for _, st := range ex.Roster {
		byReg[st.Reg] = st
	}
	for i := range rep.Students {
		l := &rep.Students[i]
		if st, ok := byReg[l.RegNumber]; ok {
			l.Name = st.Name
			l.ClassInfo = strings.Join(st.AllClasses(), "|")
		}
	}
}

func handleExportXlsx(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 导出按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	injectRosterInfo(ex, rep)
	labels := make([]string, len(rep.PerQuestion))
	for i, q := range rep.PerQuestion {
		labels[i] = questionLabel(ex, q)
	}
	data, err := export.ScoresXlsxZip(rep, labels, knowledgeStatsOf(ex))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-%s.xlsx", ex.ID))
	_, _ = w.Write(data)
}

// handleExportCSV 成绩册 CSV 导出。
func handleExportCSV(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 导出按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	injectRosterInfo(ex, rep)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-%s.csv", ex.ID))
	_, _ = w.Write(export.ScoresCSV(rep))
}

// handleExportFullZip 开放数据协议 v1:一键打包全部原始数据(版式/答案/OMR 结果/
// 评分日志/报告/名册/切片原图),学校数据随时可整包带走、迁移、审计。
func handleExportFullZip(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 导出按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	dir := store.Dir(ex.ID)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-%s-full.zip", ex.ID))
	zw := zip.NewWriter(w)
	defer zw.Close()

	addFile := func(rel string) error {
		p := filepath.Join(dir, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fh, err := zw.Create("opengrade/" + rel)
		if err != nil {
			return err
		}
		_, err = fh.Write(b)
		return err
	}
	_ = addFile("exam.json")
	// 版式与答案独立成文件,便于第三方工具直接消费
	layoutJSON, _ := json.MarshalIndent(ex.Layout, "", "  ")
	keyJSON, _ := json.MarshalIndent(map[string]any{"version": 1, "answers": ex.ObjectiveKey}, "", "  ")
	report := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	for name, content := range map[string][]byte{
		"opengrade/layout.json": layoutJSON,
		"opengrade/key.json":    keyJSON,
		"opengrade/report.json": reportJSON,
	} {
		if fh, err := zw.Create(name); err == nil {
			_, _ = fh.Write(content)
		}
	}
	// 切片原图
	sliceDir := store.SliceDir(ex.ID)
	entries, _ := os.ReadDir(sliceDir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(sliceDir, n))
		if err != nil {
			continue
		}
		if fh, err := zw.Create("opengrade/slices/" + n); err == nil {
			_, _ = fh.Write(b)
		}
	}
	// 说明文件(开放数据协议 v1)
	readme := `OpenGrade 开放数据包(协议 v1)
================================
layout.json   答题卡版式(几何契约,公开格式)
key.json      客观题参考答案
report.json   合并统分报告(成绩单 + 逐题得分率)
exam.json     会话原始数据:OMR 识别结果(含逐位考号明细)、人工评分日志(含笔迹)、名册与指派记录
slices/       主观题作答切片原图(PNG,300 DPI 矫正后)

本数据包由学校本机系统导出,归学校所有;任何工具均可按上述结构读取,无厂商锁定。
`
	if fh, err := zw.Create("opengrade/README.txt"); err == nil {
		_, _ = fh.Write([]byte(readme))
	}
}

// handleReviewPPTX 讲评课件一键生成(开源基础版):
// 自动聚合得分率低于阈值的题目 —— 客观题给正确答案与选项人数分布,
// 主观题抓取"典型满分"与"待改进"作答切片(切片不含姓名,天然脱敏)。
// GET /api/v1/exams/{id}/review.pptx?low=0.6
func handleReviewPPTX(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 导出按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	low, err := strconv.ParseFloat(r.URL.Query().Get("low"), 64)
	if err != nil || low <= 0 || low >= 1 {
		low = 0.6
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)

	overview := []string{
		fmt.Sprintf("卷数 %d · 卷面满分 %.0f · 均分 %.1f(%.0f%%)", rep.Count, rep.Total, rep.Mean, 100*rep.Mean/max(1, rep.Total)),
		fmt.Sprintf("得分率低于 %.0f%% 的题目 %d 道(本课件逐题讲评)", 100*low, len(score.LowRateQuestions(rep, low))),
	}
	var slides []export.ReviewSlide
	for _, q := range score.LowRateQuestions(rep, low) {
		if len(slides) >= 12 {
			slides = append(slides, export.ReviewSlide{Title: "更多低分率题目", Lines: []string{"其余题目见成绩册细目表,本轮课件截取前 12 题"}})
			break
		}
		rs := export.ReviewSlide{
			Title: fmt.Sprintf("第 %d 题(%s)得分率 %.0f%%", q.Index, questionLabel(ex, q), q.Rate*100),
		}
		if q.Index <= len(ex.Layout.Questions) { // 客观题:正确答案 + 选项分布
			qi := q.Index - 1
			spec := ex.Layout.Questions[qi]
			letters := card.OptionLetters(spec.Kind, spec.Options)
			dist := map[string]int{}
			for _, res := range ex.Results {
				if res == nil || res.Absent || qi >= len(res.Answers) {
					continue
				}
				if ans := res.Answers[qi]; ans != "" {
					dist[ans]++
				}
			}
			rs.Lines = append(rs.Lines,
				fmt.Sprintf("满分 %s · 正确答案:%s", trimMax(spec.Score), ex.ObjectiveKey[qi]),
				fmt.Sprintf("作答 %d 人,答对 %d 人", q.Attempted, q.Correct))
			for _, l := range letters {
				note := ""
				if l == ex.ObjectiveKey[qi] {
					note = " ← 正确"
				}
				rs.Lines = append(rs.Lines, fmt.Sprintf("选 %s:%d 人%s", l, dist[l], note))
			}
		} else { // 主观题:典型满分 + 待改进切片
			sq := q.Index - len(ex.Layout.Questions)
			best, worst := pickTypicalSlices(ex, sq)
			for _, p := range []struct {
				student int
				label   string
			}{best, worst} {
				if p.student == 0 {
					continue
				}
				b, err := os.ReadFile(filepath.Join(store.SliceDir(ex.ID),
					fmt.Sprintf("s%02d_q%02d.png", p.student, sq)))
				if err != nil {
					continue
				}
				rs.Images = append(rs.Images, export.ReviewImage{Data: b, Label: p.label})
			}
			rs.Lines = append(rs.Lines, fmt.Sprintf("满分 %s · 已阅 %d/%d · 均分 %s",
				trimMax(q.Max), q.Graded, q.Count, trimFloat(q.Mean)))
		}
		slides = append(slides, rs)
	}
	if len(slides) == 0 {
		slides = append(slides, export.ReviewSlide{Title: "本卷无低分率题目", Lines: []string{"全部题目得分率达到讲评线以上"}})
	}
	data, err := export.ReviewPPTXZip(fmt.Sprintf("试卷讲评 · 考试 %s", ex.ID), overview, slides)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-%s-review.pptx", ex.ID))
	_, _ = w.Write(data)
}

// pickTypicalSlices 选出某主观题的最高分与最低分已阅学生(讲评典型样本)。
// 拆分小问的题目按小问之和计(全部小问已阅才进入样本)。
func pickTypicalSlices(ex *Exam, question int) (best, worst struct {
	student int
	label   string
}) {
	type sg struct {
		student int
		s       float64
	}
	var graded []sg
	for si := range ex.Results {
		if s, done, _ := score.SubjectiveScore(ex.Layout.Subjectives[question-1], ex.Grades, si+1, question); done {
			graded = append(graded, sg{si + 1, s})
		}
	}
	if len(graded) == 0 {
		return
	}
	sort.Slice(graded, func(i, j int) bool { return graded[i].s > graded[j].s })
	best.student = graded[0].student
	best.label = fmt.Sprintf("典型满分:考号 %s(%.1f 分)", ex.AssignedReg(graded[0].student), graded[0].s)
	worst.student = graded[len(graded)-1].student
	worst.label = fmt.Sprintf("待改进:考号 %s(%.1f 分)", ex.AssignedReg(graded[len(graded)-1].student), graded[len(graded)-1].s)
	return
}

func trimMax(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
