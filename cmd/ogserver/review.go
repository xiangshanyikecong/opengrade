package main

// 教师端学生个体复核视图:单个学生本场全部作答 + 主观题改分 + 整卷/切片回溯 + 复核申请联动。
// 复核申请"去改分"从列表页无深链、教师要在阅卷工作台手动翻卷找人的断头路由此收口:
//   GET /api/v1/exams/{id}/students/{reg} → 逐题明细(与学生端同口径)+ 主观题评分状态(ver 乐观锁)
//   + 教师版切片 URL + 整卷页数 + 该生复核申请 + 两级拉黑状态;改分仍走 POST /api/v1/grades。

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/score"
)

// subjectiveState 是复核页某道主观题的可编辑评分状态(与阅卷工作台切片行同构,ver 供乐观锁)。
type subjectiveState struct {
	Question int            `json:"question"` // 主观题号(1-based)
	Kind     string         `json:"kind"`
	Label    string         `json:"label,omitempty"`
	Max      float64        `json:"max"`
	Graded   bool           `json:"graded"`
	Score    float64        `json:"score"`
	Ver      int            `json:"ver,omitempty"` // 整题评分乐观锁(拆分小问时无题级锁,读第 1 问)
	Mark     string         `json:"mark"`
	Parts    []partState    `json:"parts,omitempty"` // 拆分小问(版式 v3),各问独立 ver
	Ink      []score.Stroke `json:"ink,omitempty"`   // 批注笔迹(复核页只读回显)
	Elective string         `json:"elective,omitempty"`
	SliceURL string         `json:"sliceURL,omitempty"`
	Missing  bool           `json:"missing"` // 无切片图(手工补录卷),复核页显示占位
}

// handleStudentDetailStaff GET /api/v1/exams/{id}/students/{reg}
// 教师查看单个学生的本场个体情况(复核改分/讲评):可见性与阅卷工作台一致(教师全量)。
func handleStudentDetailStaff(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, true) // 复核供阅卷:教师全量视图
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	reg := strings.TrimSpace(r.PathValue("reg"))
	if reg == "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("考号不能为空"))
		return
	}
	idx := findIndex(ex, reg)
	if idx == 0 {
		httpError(w, http.StatusNotFound, fmt.Errorf("本场没有考号 %s 的答卷", reg))
		return
	}
	res := ex.Results[idx-1]

	name := nameOfReg(ex, reg)
	classes := []string{}
	for _, st := range ex.Roster {
		if st.Reg == reg {
			classes = st.AllClasses()
			break
		}
	}

	out := map[string]any{
		"id": ex.ID, "createdAt": ex.CreatedAt.Format("2006-01-02 15:04"), "examDate": examDateOf(ex),
		"subject": ex.Subject, "title": ex.Layout.Title,
		"reg": reg, "name": name, "classes": classes, "student": idx,
		"published": ex.Published, "dirty": ex.Dirty, "anonymous": ex.Anonymous,
		"manualMode": ex.ManualFull > 0,
		"canEdit":    u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username,
		"objN":       len(ex.ObjectiveKey),
	}
	// 两级拉黑状态(复核横幅上的"拉黑本场"按钮据此显示)
	blocked := map[string]any{"blocked": false, "reason": "", "global": false, "globalReason": ""}
	if why, hit := ex.AppealBlocked[reg]; hit {
		blocked["blocked"], blocked["reason"] = true, why
	}
	for _, b := range store.School.AppealBansSnapshot() {
		if b.Reg == reg {
			blocked["global"], blocked["globalReason"] = true, b.Reason
			break
		}
	}
	out["appealBlocked"] = blocked
	// 该生的复核申请(含裁决人/时间,复核页裁决联动用)
	mine := []map[string]any{}
	for _, a := range ex.Appeals {
		if a.By != reg {
			continue
		}
		m := map[string]any{"time": a.Time.Format("2006-01-02 15:04"), "reason": a.Reason,
			"status": a.Status, "reply": a.Reply, "resolvedBy": a.ResolvedBy}
		if a.ResolvedAt != nil {
			m["resolvedAt"] = a.ResolvedAt.Format("2006-01-02 15:04")
		}
		mine = append(mine, m)
	}
	out["appeals"] = mine

	if res.Absent { // 缺考:只有登记信息,无成绩明细
		out["absent"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}

	rep := mergeReport(ex)
	line, found := lineByReg(rep, reg)
	if !found {
		httpError(w, http.StatusNotFound, fmt.Errorf("本场没有考号 %s 的成绩", reg))
		return
	}
	questions, wrongs := buildStudentQuestions(ex, res, idx)
	out["objective"], out["subjective"] = line.Objective, line.Subjective
	out["score"], out["total"], out["rank"], out["mean"], out["count"] = line.Score, line.Total, line.Rank, rep.Mean, rep.Count
	out["questions"], out["wrongQuestions"] = questions, wrongs

	// 主观题可编辑评分状态(改分走 /api/v1/grades,带各问 ver)
	eGroups := score.ElectiveGroups(ex.Layout.Subjectives)
	states := []subjectiveState{}
	for qi, spec := range ex.Layout.Subjectives {
		st := subjectiveState{Question: qi + 1, Kind: spec.Kind, Label: spec.Label,
			Max:      score.SubjectiveMax(spec),
			SliceURL: fmt.Sprintf("/api/v1/exams/%s/slice/%d/%d", ex.ID, idx, qi+1)}
		if g := score.GroupOf(eGroups, qi+1); g != nil {
			labels := make([]string, len(g.Members))
			for mi, mq := range g.Members {
				labels[mi] = fmt.Sprintf("%d", len(ex.ObjectiveKey)+mq)
			}
			st.Elective = strings.Join(labels, "/")
		}
		if len(spec.Parts) > 0 {
			st.Parts = make([]partState, len(spec.Parts))
			var sum float64
			for pi := range spec.Parts {
				ps := partState{Part: pi + 1}
				if g, ok := ex.Grades[score.GradePartKey(idx, qi+1, pi+1)]; ok {
					ps.Score, ps.Graded, ps.Ver = g.Score, g.Graded, g.Ver
					if g.Graded {
						sum += g.Score
					}
				}
				st.Parts[pi] = ps
			}
			st.Score = sum
			// 小问拆分题:题级标注(✓✗⚑/笔迹)同步存在每个小问上,读第 1 问
			if g, ok := ex.Grades[score.GradePartKey(idx, qi+1, 1)]; ok {
				st.Mark, st.Ink, st.Ver = g.Mark, g.Ink, g.Ver
			}
			_, st.Graded, _ = score.SubjectiveScore(spec, ex.Grades, idx, qi+1)
		} else if g, ok := ex.Grades[score.GradeKey(idx, qi+1)]; ok {
			st.Graded, st.Score, st.Ver, st.Mark, st.Ink = g.Graded, g.Score, g.Ver, g.Mark, g.Ink
		}
		st.Missing = !fileExists(filepath.Join(store.SliceDir(ex.ID), fmt.Sprintf("s%02d_q%02d.png", idx, qi+1)))
		states = append(states, st)
	}
	out["subjectives"] = states

	// 整卷回溯:探测页数(第 1 页旧名 sNN.jpg,第 2+ 页 sNN_pKK.jpg)
	pages := 0
	if fileExists(filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d.jpg", idx))) {
		pages = 1
		for p := 2; p < 100; p++ {
			if fileExists(filepath.Join(store.PageDir(ex.ID), fmt.Sprintf("s%02d_p%02d.jpg", idx, p))) {
				pages = p
				continue
			}
			break
		}
	}
	out["pages"] = pages
	out["pageURL"] = fmt.Sprintf("/api/v1/exams/%s/page/%d", ex.ID, idx)
	writeJSON(w, http.StatusOK, out)
}
