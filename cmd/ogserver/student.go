package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/export"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

// 学生端:基础查分、逐题对错、错题本、答卷回溯 —— 永久免费,且只能看到本人数据。
// 家长端:只能看到已绑定子女的数据,同样只读。二者共用同一组处理器。
// 成绩发布控制:教师确认发布前(unpublished),学生/家长一律不可查。

// myRegs 返回当前账号可见的考号列表(学生=本人;家长=绑定的子女)。
func myRegs(u *auth.User) []string {
	if u.Role == auth.RoleParent {
		return u.Children
	}
	if u.Reg == "" {
		return nil
	}
	return []string{u.Reg}
}

// findIndex 在考试中定位某个考号的卷位:人工指派优先,其次 OMR 判读考号。
// 作废卷跳过(对外语义:这份卷不存在)。
func findIndex(ex *Exam, reg string) int {
	if reg == "" {
		return 0
	}
	for i := range ex.Results {
		res := ex.Results[i]
		if res == nil || res.Voided {
			continue
		}
		if ex.AssignedReg(i+1) == reg {
			return i + 1
		}
	}
	return 0
}

// pickTarget 解析本次请求要查看的考号:
//   - 学生:固定本人考号,忽略 child 参数;
//   - 家长:必须显式指定 child 且属于已绑定子女,否则 403(防止横向越权)。
func pickTarget(w http.ResponseWriter, r *http.Request, u *auth.User) (string, bool) {
	if u.Role != auth.RoleParent {
		if u.Reg == "" {
			httpError(w, http.StatusForbidden, fmt.Errorf("账号未绑定考号"))
			return "", false
		}
		return u.Reg, true
	}
	child := r.URL.Query().Get("child")
	if child == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("家长账号需指定 child 参数(子女考号)"))
		return "", false
	}
	for _, c := range u.Children {
		if c == child {
			return child, true
		}
	}
	httpError(w, http.StatusForbidden, fmt.Errorf("考号 %s 不在已绑定子女范围内", child))
	return "", false
}

// ClassStat 是按班级聚合的统计(教师/组长看对比,学生仅看本班均分参照)。
type ClassStat struct {
	Class    string  `json:"class"`
	Count    int     `json:"count"`
	Mean     float64 `json:"mean"`
	MeanRate float64 `json:"meanRate"`
}

// computeClassStats 依据名册班级与最终考号聚合班级统计。
// 走班双归属:学生计入其所属的每一个班(行政班+教学班)——按教学班看走班课的
// 均分,按行政班看整班对比,同一份成绩两种口径。
func computeClassStats(ex *Exam, rep *score.Report) []ClassStat {
	clsOf := map[string][]string{} // 最终考号 → 全部班级
	for _, st := range ex.Roster {
		if cs := st.AllClasses(); len(cs) > 0 {
			clsOf[st.Reg] = cs
		}
	}
	type acc struct {
		n       int
		sum     float64
		rateSum float64
	}
	byClass := map[string]*acc{}
	add := func(cls string, line *score.StudentLine) {
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
	for i := range rep.Students {
		line := &rep.Students[i]
		if line.Absent {
			continue // 缺考不进班级统计
		}
		if cs, ok := clsOf[line.RegNumber]; ok {
			for _, c := range cs {
				add(c, line)
			}
			continue
		}
		add("未匹配", line)
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

// handleStudentExams GET /api/v1/student/exams[?child=] → 我的(或子女的)考试列表。
// 未发布成绩的考试不出现在列表中(教师确认发布后才可见)。
// 家长多孩:同一场考试每个参加的子女各一行(item.child 标注),不再只显示第一个命中的孩子;
// 带 ?child= 时只列该子女(前端切换子女下拉时带参重拉,列表与详情口径一致)。
// 已发布但无关联答卷的考试不逐条刷「未找到关联答卷」(考号没绑定的学生此前看到的是满屏噪音,
// 分不清"没考"和"账号没绑号"),改为一条汇总数。
func handleStudentExams(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	type item struct {
		ID        string  `json:"id"`
		CreatedAt string  `json:"createdAt"`
		ExamDate  string  `json:"examDate,omitempty"` // 考试日期(教务口径;缺省回退扫描时间)
		Child     string  `json:"child,omitempty"`    // 家长视图下对应的子女考号
		Subject   string  `json:"subject,omitempty"`
		Found     bool    `json:"found"`
		Absent    bool    `json:"absent"` // 该场考试登记为缺考
		Score     float64 `json:"score"`
		Total     float64 `json:"total"`
		Rank      int     `json:"rank"`
		ClassMean float64 `json:"classMean"`
		HasReply  bool    `json:"hasReply,omitempty"` // 有复核申请已回复(列表角标)
		HasOpen   bool    `json:"hasOpen,omitempty"`  // 有待处理的复核申请
	}
	regs := myRegs(u)
	if u.Role == auth.RoleParent {
		if child := r.URL.Query().Get("child"); child != "" {
			if !containsStr(u.Children, child) {
				httpError(w, http.StatusForbidden, fmt.Errorf("考号 %s 不在已绑定子女范围内", child))
				return
			}
			regs = []string{child}
		}
	}
	out := []item{}
	unmatched := 0 // 已发布但当前口径下没有关联答卷的场数:可能未参加,或考号未绑定
	for _, ex := range store.List() {
		if !ex.Published {
			continue // 成绩发布控制:未发布不可查
		}
		var rep *score.Report // 懒算:同场多孩只统分一次
		found := false
		for _, reg := range regs {
			if findIndex(ex, reg) == 0 {
				continue
			}
			if rep == nil {
				rep = mergeReport(ex)
			}
			line, ok := lineByReg(rep, reg)
			if !ok {
				continue
			}
			found = true
			it := item{ID: ex.ID, CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), ExamDate: examDateOf(ex),
				Subject: ex.Subject, Found: true, Absent: line.Absent, Score: line.Score, Total: line.Total,
				Rank: line.Rank, Child: reg}
			cls := u.Class
			if cls == "" {
				cls = classOfReg(ex, reg) // 家长无班级:取子女班级
			}
			for _, c := range computeClassStats(ex, rep) {
				if c.Class == cls {
					it.ClassMean = c.Mean
				}
			}
			for _, a := range ex.Appeals {
				if a.By != reg {
					continue
				}
				if a.Status == "resolved" {
					it.HasReply = true
				} else {
					it.HasOpen = true
				}
			}
			out = append(out, it)
		}
		if !found {
			unmatched++
			continue
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"exams": out, "unmatched": unmatched})
}

// studentQuestion 是逐题明细的一行:学生端"我的成绩单"与教师端复核页共用同一口径。
type studentQuestion struct {
	Index      int     `json:"index"`
	Kind       string  `json:"kind"`
	Max        float64 `json:"max"`
	MyAnswer   string  `json:"myAnswer,omitempty"`
	Correct    string  `json:"correct,omitempty"`
	Right      bool    `json:"right,omitempty"`
	MyScore    float64 `json:"myScore"`
	Wrong      bool    `json:"wrong"`
	Pending    bool    `json:"pending,omitempty"`    // 主观题评卷中(未评完不计错题,展示"评卷中")
	Elective   string  `json:"elective,omitempty"`   // 选做组可选题号(如 "23/24");非空即选做题
	NotCounted bool    `json:"notCounted,omitempty"` // 选做题未被所选:不计分、不进错题本
}

// buildStudentQuestions 构建某生本场的逐题明细:客观题"我的作答 vs 参考答案"(对错着色),
// 主观题得分/评卷状态;返回 (明细, 错题全局题号列表)。
func buildStudentQuestions(ex *Exam, res *omr.Result, idx int) ([]studentQuestion, []int) {
	questions := make([]studentQuestion, 0, len(ex.ObjectiveKey)+len(ex.Layout.Subjectives))
	var wrongs []int
	for qi := range ex.ObjectiveKey {
		spec := ex.Layout.Questions[qi]
		rule := ex.Layout.Scoring.RuleFor(qi + 1)
		max := score.FullScore(spec, rule)
		my := ""
		if qi < len(res.Answers) {
			my = res.Answers[qi]
		}
		if ex.ObjectiveKey[qi] == "" { // 答案未录(该题不判分):不判对错、不进错题本
			questions = append(questions, studentQuestion{Index: qi + 1, Kind: spec.Kind, Max: max, MyAnswer: my})
			continue
		}
		// 判定走统一口径(score.Evaluate):未得满分的作答(含漏选部分分)都进错题本
		_, verdict := score.Evaluate(spec, rule, my, ex.ObjectiveKey[qi])
		right := verdict == score.VerdictCorrect
		if !right {
			wrongs = append(wrongs, qi+1)
		}
		questions = append(questions, studentQuestion{Index: qi + 1, Kind: spec.Kind, Max: max,
			MyAnswer: my, Correct: ex.ObjectiveKey[qi], Right: right, Wrong: !right})
	}
	eGroups := score.ElectiveGroups(ex.Layout.Subjectives)
	excluded := score.ElectiveExcluded(eGroups, ex.Layout.Subjectives, ex.Grades, res, idx)
	for qi, spec := range ex.Layout.Subjectives {
		max := score.SubjectiveMax(spec)
		qNo := len(ex.ObjectiveKey) + qi + 1
		elective := ""
		if g := score.GroupOf(eGroups, qi+1); g != nil { // 组员按主观题序号索引,标签转全局题号
			labels := make([]string, len(g.Members))
			for mi, mq := range g.Members {
				labels[mi] = fmt.Sprintf("%d", len(ex.ObjectiveKey)+mq)
			}
			elective = strings.Join(labels, "/")
		}
		if excluded[qi+1] { // 选做题未被所选:不计分,标注后跳过
			questions = append(questions, studentQuestion{Index: qNo, Kind: spec.Kind, Max: max,
				Elective: elective, NotCounted: true})
			continue
		}
		myScore := 0.0
		done := false
		if s, d, _ := score.SubjectiveScore(spec, ex.Grades, idx, qi+1); d {
			myScore, done = s, true
		}
		if !done {
			// 未评完(复核改分窗口/撤回重阅):不按 0 分进错题本,明示「评卷中」
			questions = append(questions, studentQuestion{Index: qNo, Kind: spec.Kind, Max: max,
				Pending: true, Elective: elective})
			continue
		}
		if myScore < max {
			wrongs = append(wrongs, qNo)
		}
		questions = append(questions, studentQuestion{Index: qNo, Kind: spec.Kind,
			Max: max, MyScore: myScore, Wrong: myScore < max, Elective: elective})
	}
	return questions, wrongs
}

// handleStudentExam GET /api/v1/student/exams/{id}?child=<reg>
// 个人成绩报告:总分/排名/均分参照、逐题"我的作答 vs 参考答案"(错题本)、主观题得分、切片与整卷回溯。
func handleStudentExam(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !ex.Published {
		httpError(w, http.StatusNotFound, fmt.Errorf("成绩尚未发布"))
		return
	}
	reg, ok := pickTarget(w, r, u)
	if !ok {
		return
	}
	idx := findIndex(ex, reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("本次考试未找到与考号 %s 关联的答卷", reg))
		return
	}
	res := ex.Results[idx-1]

	rep := mergeReport(ex)
	line, ok := lineByReg(rep, reg)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("本次考试未找到与考号 %s 关联的答卷", reg))
		return
	}
	if line.Absent { // 缺考:只有登记信息,无成绩明细
		writeJSON(w, http.StatusOK, map[string]any{
			"id": ex.ID, "createdAt": ex.CreatedAt.Format("2006-01-02 15:04"),
			"subject": ex.Subject, "reg": reg, "class": u.Class,
			"absent": true, "total": line.Total,
		})
		return
	}

	questions, wrongs := buildStudentQuestions(ex, res, idx)

	classRates := map[string]float64{}
	for _, q := range rep.PerQuestion {
		classRates[fmt.Sprintf("q%d", q.Index)] = q.Rate
	}

	// 本班排名:只统计本班学生(并列同名次;家长无班级时取子女班级)
	classRank := line.Rank
	cls := u.Class
	if cls == "" {
		cls = classOfReg(ex, reg)
	}
	if cls != "" {
		same := []score.StudentLine{}
		for _, l := range rep.Students {
			if l.Absent {
				continue // 缺考不参与班级排名
			}
			if inClassOfReg(ex, l.RegNumber, cls) {
				same = append(same, l)
			}
		}
		rank := 1
		for _, l := range same {
			if l.Score > line.Score {
				rank++
			}
		}
		classRank = rank
	}

	out := map[string]any{
		"id":             ex.ID,
		"createdAt":      ex.CreatedAt.Format("2006-01-02 15:04"),
		"subject":        ex.Subject,
		"reg":            reg,
		"class":          cls,
		"objective":      line.Objective,
		"subjective":     line.Subjective,
		"score":          line.Score,
		"total":          line.Total,
		"rank":           line.Rank,
		"classRank":      classRank,
		"count":          rep.Count,
		"mean":           rep.Mean,
		"regValid":       res.RegValid,
		"questions":      questions,
		"wrongQuestions": wrongs,
		"classRates":     classRates,
		"byClass":        computeClassStats(ex, rep),
	}

	// 主观题作答切片与整卷回溯(仅本人/绑定子女)
	slices := []map[string]string{}
	childQ := ""
	if u.Role == auth.RoleParent {
		childQ = "?child=" + reg
	}
	for qi := range ex.Layout.Subjectives {
		p := filepath.Join(store.SliceDir(ex.ID), fmt.Sprintf("s%02d_q%02d.png", idx, qi+1))
		if fileExists(p) {
			slices = append(slices, map[string]string{
				"question": fmt.Sprintf("%d", len(ex.ObjectiveKey)+qi+1),
				"url":      fmt.Sprintf("/api/v1/student/exams/%s/slice/%d%s", ex.ID, qi+1, childQ),
			})
		}
	}
	out["slices"] = slices
	out["knowledge"] = studentKnowledgeOf(ex, idx, res)
	// 我的复核申请(发布后可发起,教师裁决后在此看到回复)
	myAppeals := []map[string]any{}
	for _, a := range ex.Appeals {
		if a.By != reg {
			continue
		}
		myAppeals = append(myAppeals, map[string]any{
			"time": a.Time.Format("2006-01-02 15:04"), "reason": a.Reason,
			"status": a.Status, "reply": a.Reply,
		})
	}
	out["appeals"] = myAppeals
	if fileExists(filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d.jpg", idx))) {
		out["pageURL"] = fmt.Sprintf("/api/v1/student/exams/%s/page%s", ex.ID, childQ)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStudentPractice GET /api/v1/student/exams/{id}/practice?child=
// 错题重组练习卡:把本人本场的错题编译成一张新版式(前端交 /api/v1/cards 渲染打印)。
func handleStudentPractice(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
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
	writeJSON(w, http.StatusOK, res)
}

// handleStudentReportPDF GET /api/v1/student/exams/{id}/report.pdf?child=
// 服务端生成个人成绩报告单 PDF(离线可下载/打印的回执)。
func handleStudentReportPDF(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
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
	rep := mergeReport(ex)
	line, found := lineByReg(rep, reg)
	if !found {
		httpError(w, http.StatusNotFound, fmt.Errorf("未找到本人成绩"))
		return
	}
	if line.Absent {
		httpError(w, http.StatusUnprocessableEntity, fmt.Errorf("缺考无成绩报告单"))
		return
	}
	res := ex.Results[idx-1]

	// 班级排名(并列同名次;家长无班级时取子女班级)
	classRank := 0
	cls := u.Class
	if cls == "" {
		cls = classOfReg(ex, reg)
	}
	if cls != "" {
		greater := 0
		for _, l := range rep.Students {
			if !l.Absent && inClassOfReg(ex, l.RegNumber, cls) && l.Score > line.Score {
				greater++
			}
		}
		classRank = greater + 1
	}

	kindName := func(k string) string {
		return map[string]string{"choice": "单选", "judge": "判断", "multiple": "多选",
			"fillblank": "填空", "subjective": "解答"}[k]
	}
	var rows []export.QRow
	for qi := range ex.ObjectiveKey {
		spec := ex.Layout.Questions[qi]
		rule := ex.Layout.Scoring.RuleFor(qi + 1)
		max := score.FullScore(spec, rule)
		my := ""
		if qi < len(res.Answers) {
			my = res.Answers[qi]
		}
		if ex.ObjectiveKey[qi] == "" { // 答案未录:不判对错(与 Web 端逐题明细同口径)
			if my == "" {
				my = "(未答)"
			}
			rows = append(rows, export.QRow{Index: qi + 1, Kind: kindName(spec.Kind), Max: trimMaxF(max), Mine: my, Wrong: false})
			continue
		}
		_, verdict := score.Evaluate(spec, rule, my, ex.ObjectiveKey[qi])
		right := verdict == score.VerdictCorrect
		if my == "" {
			my = "(未答)"
		} else if !right {
			my += "(错)"
		}
		rows = append(rows, export.QRow{
			Index: qi + 1, Kind: kindName(spec.Kind), Max: trimMaxF(max),
			Mine: my, Correct: ex.ObjectiveKey[qi], Wrong: !right,
		})
	}
	eGroups := score.ElectiveGroups(ex.Layout.Subjectives)
	excluded := score.ElectiveExcluded(eGroups, ex.Layout.Subjectives, ex.Grades, res, idx)
	for qi, spec := range ex.Layout.Subjectives {
		max := score.SubjectiveMax(spec)
		qNo := len(ex.ObjectiveKey) + qi + 1
		if excluded[qi+1] { // 选做题未被所选:不计分
			rows = append(rows, export.QRow{
				Index: qNo, Kind: kindName(spec.Kind), Max: trimMaxF(max),
				Mine: "(未选做)", Wrong: false,
			})
			continue
		}
		// 未评完(复核改分窗口/撤回重阅):明示评卷中,不按 0 分进"错题"列(与 Web 端同口径)
		if s, done, _ := score.SubjectiveScore(spec, ex.Grades, idx, qi+1); done {
			rows = append(rows, export.QRow{
				Index: qNo, Kind: kindName(spec.Kind),
				Max: trimMaxF(max), Mine: fmt.Sprintf("%s 分", trimMaxF(s)),
				Wrong: s < max,
			})
		} else {
			rows = append(rows, export.QRow{
				Index: qNo, Kind: kindName(spec.Kind),
				Max: trimMaxF(max), Mine: "评卷中", Wrong: false,
			})
		}
	}

	var know []export.KV
	for _, ks := range studentKnowledgeOf(ex, idx, res) {
		know = append(know, export.KV{Key: ks.Name, Value: fmt.Sprintf("%.0f%%", ks.Rate*100)})
	}

	rankText := fmt.Sprintf("第 %d 名 / 共 %d 人", line.Rank, rep.Count)
	card := export.ReportCard{
		Title: ex.Layout.Title, Subject: ex.Subject,
		Student: nameOfReg(ex, reg), Reg: reg, Class: nonEmptyStr(cls, "-"),
		Date:      ex.CreatedAt.Format("2006-01-02"),
		Score:     fmt.Sprintf("%s / %s", trimMaxF(line.Score), trimMaxF(line.Total)),
		Rank:      rankText,
		ClassRank: rankStr(classRank),
		Mean:      trimMaxF(rep.Mean),
		Knowledge: know,
		Questions: rows,
		Footer:    "本报告单由学校本机系统生成,成绩数据归学校与学生本人所有。OpenGrade 开元智阅。",
	}
	img := export.RenderReportCard(card)
	data, err := export.ImagePDF(img, 210, 297)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-report-%s.pdf", reg))
	_, _ = w.Write(data)
}

func rankStr(r int) string {
	if r <= 0 {
		return "-"
	}
	return fmt.Sprintf("第 %d 名", r)
}

func nonEmptyStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func trimMaxF(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// handleStudentSlice GET /api/v1/student/exams/{id}/slice/{q} → 我的主观题作答切片
func handleStudentSlice(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
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
	p := pageImagePath(store, ex.ID, idx, r.URL.Query().Get("p"))
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
	p := pageImagePath(store, ex.ID, idx, r.URL.Query().Get("p"))
	if !fileExists(p) {
		httpError(w, http.StatusNotFound, fmt.Errorf("整卷回溯图不存在"))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

// pageImagePath 多页版式的整卷回溯文件名:第 1 页兼容旧名,第 2+ 页带 _p 页号。
func pageImagePath(store *Store, examID string, idx int, pageParam string) string {
	name := fmt.Sprintf("s%02d.jpg", idx)
	if pv := pageParam; pv != "" {
		if k, err := strconv.Atoi(pv); err == nil && k > 1 {
			name = fmt.Sprintf("s%02d_p%02d.jpg", idx, k)
		}
	}
	return filepath.Join(store.PageDir(examID), name)
}
