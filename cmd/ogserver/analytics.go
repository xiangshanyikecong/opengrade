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

// handleSetKey 修正客观题参考答案:POST /api/v1/exams/{id}/key {"key":["A","B",...],"variant":"A"|"B"}
// 管理员/年级组长/建卷教师可操作;修正后全班客观题分自动重统。
// variant="B" 修正 B 卷答案(建考时「留空=后补」的出口;key 按规范题号序)。
func handleSetKey(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Key     []string `json:"key"`
		Variant string   `json:"variant"` // ""/"A" = A 卷;“B” = B 卷
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
		return
	}
	// 仅管理员/年级组长/建卷人可改标准答案(普通任课教师不可动他人卷的答案键)
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可修正参考答案"))
		return
	}
	if err := store.SetObjectiveKey(ex.ID, req.Key, u.Username, req.Variant); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePublish 成绩发布控制:POST /api/v1/exams/{id}/publish {"published":true}
// 仅管理员/年级组长/建卷教师可发布或撤回(他人考试不可代发布)。
// 发布门禁(数据即状态的收口):异常卷未裁决/主观题未评完(含半阅)/客观题答案未录齐
// 一律 422 拒绝并说明差什么 —— 未评按 0 分进排名与错题本,是最伤学生的错数。
func handlePublish(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Published *bool `json:"published"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Published == nil {
		// 发布决定必须显式给出:提交 {} 会把缺失字段当 false 悄悄撤回成绩(评审 analytics.go:58)
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少发布决定:请求体须包含 published(true 发布 / false 撤回)"))
		return
	}
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
		return
	}
	if !(u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅管理员、年级组长或建卷教师可控制成绩发布"))
		return
	}
	// 门禁闭包在 Store 锁内对最新快照执行:检查与提交原子,不再可能发布出
	// "检查通过后又被撤评分"的未完卷(评审 analytics.go:102)。手工成绩科目没有
	// 异常/主观/答案概念,不设门禁。
	gate := func(cur *Exam) error {
		if cur.ManualFull != 0 {
			return nil
		}
		stage := examStage(cur)
		if stage.Students == 0 {
			return fmt.Errorf("这场考试还没有任何卷(已扫 0/名册 %d 人),发布空考试没有意义——先上传扫描件,或对全员登记缺考", stage.Expected)
		}
		if stage.Warnings > 0 {
			return fmt.Errorf("还有 %d 张异常卷未裁决(%s),处理完才能发布——考试详情「异常清单」", stage.Warnings, examBlockingWarningSamples(cur, 3))
		}
		if stage.Ungraded > 0 {
			return fmt.Errorf("主观题还有 %d 份切片未评完(含半阅),评完才能发布——未评按 0 分进排名与错题本会误导学生", stage.Ungraded)
		}
		for i, k := range cur.ObjectiveKey {
			if k == "" {
				return fmt.Errorf("第 %d 题客观题参考答案未录入,补齐后才能发布(考试详情「客观题参考答案」;空答案题不判分)", i+1)
			}
		}
		return nil
	}
	if err := store.SetPublishedChecked(ex.ID, *req.Published, u.Username, gate); err != nil {
		httpError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"published": *req.Published, "dirty": false})
}

// handleAudit 操作审计日志:GET /api/v1/exams/{id}/audit(管理员/年级组长)。
func handleAudit(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
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
	// ContentLength==-1 表示长度未知而非空:按实际读取结果区分空正文与有效配置;
	// 非空正文解码失败返回 400,不再吞错后静默用默认档(评审 analytics.go:154/155)
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("赋分配置无法解码: %w", err))
			return
		}
	}
	scale := req.GradeScale
	if req.Fine && len(scale.Bands) == 0 {
		scale = score.FineGradeScale()
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades, ex.Layout.Scoring)
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
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades, ex.Layout.Scoring)
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
		"id":               ex.ID,
		"subject":          ex.Subject,
		"total":            rep.Total,
		"stats":            rep.Stats,
		"byClass":          computeClassStats(ex, rep),
		"questions":        qs,
		"knowledge":        knowledgeStatsOf(ex),
		"perClassQuestion": perClassQuestionRates(ex),
	})
}

// perClassQuestionRates 按班级计算逐题得分率(与全体同口径:客观=对率/非缺考,
// 主观=已阅均分/满分;选做题只计所选该题者),供「与年级差值」列。
// 无数据的题位为 -1。键 "全体" 为全卷口径,便于前端直接做差。
func perClassQuestionRates(ex *Exam) map[string][]float64 {
	nObj, nSub := len(ex.Layout.Questions), len(ex.Layout.Subjectives)
	nQ := nObj + nSub
	if nQ == 0 {
		return nil
	}
	regClasses := map[string][]string{}
	for _, st := range ex.Roster {
		regClasses[st.Reg] = st.AllClasses()
	}
	type acc struct {
		count, correct int
		sum            float64
		graded         int
	}
	agg := map[string][]*acc{}
	get := func(c string) []*acc {
		if agg[c] == nil {
			agg[c] = make([]*acc, nQ)
			for i := range agg[c] {
				agg[c][i] = &acc{}
			}
		}
		return agg[c]
	}
	groups := score.ElectiveGroups(ex.Layout.Subjectives)
	for si, res := range ex.Results {
		// 与全体统计(score.Merge)同口径:作废/卷种待指派卷不进班级逐题率
		if res == nil || res.Absent || res.Voided || res.PendingVariant {
			continue
		}
		cls := regClasses[ex.AssignedReg(si+1)]
		if len(cls) == 0 {
			cls = []string{"未匹配"}
		}
		// 选做口径同统分层(涂点 + 批阅回退,自带涂点下标防护),替代裸 marks 下标比对
		excluded := score.ElectiveExcluded(groups, ex.Layout.Subjectives, ex.Grades, res, si+1)
		// 全体基准桶(评审 analytics.go:258):每张有效卷只累计一次(多班归属学生
		// 不重复计入全体),班级桶仍逐班累计 —— 前端按「全体」键取全体得分率
		all := get("全体")
		for qi := 0; qi < nObj && qi < len(res.Answers); qi++ {
			if ex.ObjectiveKey[qi] == "" {
				continue
			}
			_, verdict := score.Evaluate(ex.Layout.Questions[qi], ex.Layout.Scoring.RuleFor(qi+1),
				res.Answers[qi], ex.ObjectiveKey[qi])
			all[qi].count++
			if verdict == score.VerdictCorrect {
				all[qi].correct++
			}
		}
		for qi := 0; qi < nSub; qi++ {
			if excluded[qi+1] {
				continue
			}
			if sc, done, _ := score.SubjectiveScore(ex.Layout.Subjectives[qi], ex.Grades, si+1, qi+1); done {
				all[nObj+qi].graded++
				all[nObj+qi].sum += sc
			}
		}
		for _, c := range cls {
			accs := get(c)
			for qi := 0; qi < nObj && qi < len(res.Answers); qi++ {
				if ex.ObjectiveKey[qi] == "" {
					continue // 空键题不判分:不进班级逐题口径(与全体统计一致)
				}
				accs[qi].count++
				// 判定走 score.Evaluate(与统分同口径):只计得满分的"对",
				// 部分分不计入对率 —— 对率口径与 score.Merge 的 Rate 一致
				_, verdict := score.Evaluate(ex.Layout.Questions[qi], ex.Layout.Scoring.RuleFor(qi+1),
					res.Answers[qi], ex.ObjectiveKey[qi])
				if verdict == score.VerdictCorrect {
					accs[qi].correct++
				}
			}
			for qi := 0; qi < nSub; qi++ {
				spec := ex.Layout.Subjectives[qi]
				if excluded[qi+1] {
					continue // 未被所选(含漏涂/多涂且无批阅回退):不进本题统计
				}
				// 评分键按主观题序号 qi+1 查(评分存储键即主观题序号);nObj+qi 只是
				// 输出数组定位 —— 此前传 qNo(nObj+qi+1)查分,样本恒空(评审 analytics.go:279)
				if sc, done, _ := score.SubjectiveScore(spec, ex.Grades, si+1, qi+1); done {
					accs[nObj+qi].graded++
					accs[nObj+qi].sum += sc
				}
			}
		}
	}
	rate := func(a *acc, max float64) float64 {
		if a.count > 0 {
			return float64(a.correct) / float64(a.count)
		}
		if a.graded > 0 && max > 0 {
			return (a.sum / float64(a.graded)) / max
		}
		return -1
	}
	out := map[string][]float64{}
	for c, accs := range agg {
		rs := make([]float64, nQ)
		for qi := 0; qi < nQ; qi++ {
			// 主观题才查版式分值;客观题满分恒为 1(qi-nObj 为负时不可下标)
			max := 1.0
			if qi >= nObj {
				max = ex.Layout.Subjectives[qi-nObj].Score
			}
			rs[qi] = rate(accs[qi], max)
		}
		out[c] = rs
	}
	return out
}

var _ = strings.TrimSpace
