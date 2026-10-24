package main

// 全班成绩条连排批量 PDF(P2 家长签字回执刚需):GET /api/v1/exams/{id}/slips.pdf
// 每页 2 张、条间裁切线;教师一次导出全班,不必逐人打印 50 次。

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/export"
	"github.com/opengrade/opengrade/score"
)

// handleSlipsPDF 全班成绩条连排 PDF。
func handleSlipsPDF(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 权限 + 教师按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	// 排名与均分一律取全卷口径(真实年级名次/年级均分):教师按可见范围只"领取"
	// 其学生的成绩条,不改变名次分母 —— 否则单班教师印出的"第 N 名"实为班内名次,
	// 与班主任/组长导出的数字对不上。
	full, ok2 := store.Get(r.PathValue("id"))
	if !ok2 {
		full = ex
	}
	rep := mergeReport(full)
	injectRosterInfo(full, rep)
	visible := map[string]bool{}
	for _, l := range mergeReport(ex).Students {
		visible[l.RegNumber] = true
	}

	// 班级排名:按名册班级逐班排(缺考不计)
	classRank := map[string]int{}
	for _, cls := range examClasses(full) {
		same := []score.StudentLine{}
		for _, l := range rep.Students {
			if !l.Absent && inClassOfReg(full, l.RegNumber, cls) {
				same = append(same, l)
			}
		}
		sort.Slice(same, func(i, j int) bool { return same[i].Score > same[j].Score })
		for i, l := range same {
			classRank[l.RegNumber] = i + 1
		}
	}

	rows := []export.SlipRow{}
	for _, l := range rep.Students {
		if l.Absent || !visible[l.RegNumber] {
			continue
		}
		title := full.Layout.Title
		if full.Subject != "" {
			title = strings.TrimSpace(title + " · " + full.Subject)
		}
		rows = append(rows, export.SlipRow{
			Title:     nonEmptyStr(title, "成绩条"),
			Date:      examDateOf(full),
			Student:   l.Name,
			Class:     l.ClassInfo,
			Reg:       l.RegNumber,
			Score:     fmt.Sprintf("%s / %s", trimMaxF(l.Score), trimMaxF(l.Total)),
			Rank:      fmt.Sprintf("第 %d 名", l.Rank),
			ClassRank: rankStr(classRank[l.RegNumber]),
			Mean:      trimMaxF(rep.Mean),
		})
	}
	data, err := export.SlipsPDF(rows)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-slips-%s.pdf", ex.ID))
	_, _ = w.Write(data)
}
