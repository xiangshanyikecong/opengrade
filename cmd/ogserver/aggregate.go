package main

// 年级总分工表(P1 教务第一张表):同场分组(ExamGroup)下的多科汇总。
//   - 一行一名学生:各科得分 + 总分 + 年级排名 + 班级排名(缺考按 0 计入总分,单元格标缺);
//   - 一列一场考试:按考试日期升序,列头附满分/均分/实考数;
//   - 权限沿用可见性:只聚合当前账号可见的考试。
// 同场分组由建考/元数据填写(如 "2026期中"),总分趋势(纵向)由此天然成立。

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/opengrade/opengrade/auth"
)

// aggregateExam 是工表的一列(一场考试)。
type aggregateExam struct {
	ID      string  `json:"id"`
	Subject string  `json:"subject,omitempty"`
	Date    string  `json:"date"`
	Total   float64 `json:"total"`  // 该科满分
	Mean    float64 `json:"mean"`   // 全体均分(可见口径)
	Count   int     `json:"count"`  // 实考人数
	Absent  int     `json:"absent"` // 缺考人数
}

// aggregateCell 是一名学生在一科的成绩格。
type aggregateCell struct {
	Score  float64 `json:"score"`
	Absent bool    `json:"absent,omitempty"`
	Found  bool    `json:"found"` // false = 在册但无卷(按 0 计)
}

// aggregateStudent 是工表的一行。
type aggregateStudent struct {
	Reg       string          `json:"reg"`
	Name      string          `json:"name,omitempty"`
	Class     string          `json:"class,omitempty"`
	Cells     []aggregateCell `json:"cells"` // 与 exams 等长对齐
	Total     float64         `json:"total"` // 各科之和(缺考按 0)
	Rank      int             `json:"rank"`  // 年级排名(并列同名次)
	ClassRank int             `json:"classRank,omitempty"`
}

// handleAggregate GET /api/v1/aggregate?group=2026期中
func handleAggregate(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	if group == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 group 参数(同场分组标签)"))
		return
	}
	var exams []*Exam
	totalExams := 0   // 该分组的全部考试数(不论当前账号是否可见;前端据此提示口径)
	pendingExams := 0 // 其中未发布/发布后有变更的科目数:不进总分排名,未收敛前排名会失真
	for _, ex := range store.List() {
		if ex.ExamGroup != group {
			continue
		}
		totalExams++
		if !ex.Published {
			pendingExams++
			continue // 未发布科目不进总分:阅卷中/扫描中的列会把总分排名拉错,且无「口径未收敛」可言
		}
		if ex.Dirty {
			pendingExams++ // 发布后又有变更:数字可用但提示需重新发布收口
		}
		if canOpenExam(u, ex, store.School) {
			exams = append(exams, ex)
		}
	}
	sort.Slice(exams, func(i, j int) bool { // 日期升序(未填日期按创建时间)
		di, dj := examDateOf(exams[i]), examDateOf(exams[j])
		if di != dj {
			return di < dj
		}
		return exams[i].ID < exams[j].ID
	})
	cols := []aggregateExam{}
	for _, ex := range exams {
		rep := mergeReport(ex)
		absent := 0
		for i := range ex.Results {
			res := ex.Results[i]
			if res == nil || res.Voided || !res.Absent {
				continue
			}
			absent++ // 缺考人数按登记口径全量计(与考试列表/详情一致);考号未指派的缺考卷也是缺考
		}
		cols = append(cols, aggregateExam{
			ID: ex.ID, Subject: ex.Subject, Date: examDateOf(ex),
			Total: rep.Total, Mean: rep.Mean, Count: rep.Count, Absent: absent,
		})
	}

	// 学生维度:考号 → 行;姓名/班级取各场名册(后写覆盖 = 最近一场,同 trends 口径)
	name, class := map[string]string{}, map[string]string{}
	var regOrder []string
	rows := map[string]*aggregateStudent{}
	ensureRow := func(reg string) {
		if reg == "" {
			return
		}
		if _, seen := rows[reg]; !seen {
			rows[reg] = &aggregateStudent{Reg: reg, Cells: make([]aggregateCell, len(exams))}
			regOrder = append(regOrder, reg)
		}
	}
	for _, ex := range exams {
		for _, st := range ex.Roster {
			ensureRow(st.Reg)
			if st.Name != "" {
				name[st.Reg] = st.Name
			}
			if st.Class != "" {
				class[st.Reg] = st.Class
			}
		}
		for i := range ex.Results {
			res := ex.Results[i]
			if res == nil || res.Voided {
				continue
			}
			ensureRow(ex.AssignedReg(i + 1))
		}
	}

	// 逐科填格:报告里的行给分;名册在册但缺卷的按 0(Found=false)
	for ci, ex := range exams {
		rep := mergeReport(ex)
		for _, l := range rep.Students {
			st := rows[l.RegNumber]
			if st == nil {
				continue
			}
			st.Cells[ci] = aggregateCell{Score: l.Score, Absent: l.Absent, Found: true}
			if !l.Absent {
				st.Total += l.Score
			}
		}
		for i := range ex.Results {
			res := ex.Results[i]
			if res == nil || res.Voided {
				continue
			}
			st := rows[ex.AssignedReg(i+1)]
			if st == nil || st.Cells[ci].Found {
				continue
			}
			// 在册无卷:格子保持零值(Found=false,总分不加)
		}
	}

	// 总分与排名:年级并列同名次;班级排名按班级内总分。
	// 缺考口径(教务两派并存,默认"全科未参与不进排名"):
	//   - 默认(absentRank 缺省):所有科目都缺考/无卷的学生保留行(总分 0)但不进排名池,名次 0(前端示"—");
	//   - absentRank=include:全员进池,全科缺考按 0 分垫底(旧口径)。
	rankAbsent := r.URL.Query().Get("absentRank") == "include"
	participated := func(st *aggregateStudent) bool {
		for _, c := range st.Cells {
			if c.Found && !c.Absent {
				return true
			}
		}
		return false
	}
	all := make([]*aggregateStudent, 0, len(regOrder))
	for _, reg := range regOrder {
		st := rows[reg]
		st.Name, st.Class = name[reg], class[reg]
		all = append(all, st)
	}
	classTotals := map[string][]float64{}
	var allTotals []float64
	for _, st := range all {
		if !rankAbsent && !participated(st) {
			continue // 全科缺考:不进排名池、不占名次
		}
		allTotals = append(allTotals, st.Total)
		if st.Class != "" {
			classTotals[st.Class] = append(classTotals[st.Class], st.Total)
		}
	}
	rankOf := func(v float64, pool []float64) int {
		rank := 1
		for _, s := range pool {
			if s > v {
				rank++
			}
		}
		return rank
	}
	for _, st := range all {
		if !rankAbsent && !participated(st) {
			continue
		}
		st.Rank = rankOf(st.Total, allTotals)
		if st.Class != "" {
			st.ClassRank = rankOf(st.Total, classTotals[st.Class])
		}
	}
	// 工表行序:班级 → 总分降序 → 考号
	sort.Slice(all, func(i, j int) bool {
		if all[i].Class != all[j].Class {
			return all[i].Class < all[j].Class
		}
		if all[i].Total != all[j].Total {
			return all[i].Total > all[j].Total
		}
		return all[i].Reg < all[j].Reg
	})
	if cols == nil {
		cols = []aggregateExam{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":        group,
		"exams":        cols,
		"totalExams":   totalExams, // 分组内考试总数;与 exams 数量不一致 = 当前账号不可见部分科目,总分/排名仅按可见科目
		"pendingExams": pendingExams,
		"students":     all,
	})
}

// examDateOf 考试展示日期:元数据考试日期优先,未填用创建时间。
func examDateOf(ex *Exam) string {
	if ex.ExamDate != "" {
		return ex.ExamDate
	}
	return ex.CreatedAt.Format("2006-01-02")
}
