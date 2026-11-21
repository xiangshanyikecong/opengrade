package main

// 全班成绩条连排批量 PDF(P2 家长签字回执刚需):GET /api/v1/exams/{id}/slips.pdf
// 每页 2 张、条间裁切线;教师一次导出全班,不必逐人打印 50 次。

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/export"
)

// handleSlipsPDF 全班成绩条连排 PDF。
func handleSlipsPDF(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	// 复用成绩册导出口径(exportReport):可见性 + 教师裁剪行 + 全卷排名 + 匿名保护
	// 一并继承;裁剪视图与全卷视图出自同一次快照读取 —— 此前两次 Get 取交集,
	// 期间成绩/作废变更会让可见集合落在"考试从未存在过"的状态上(评审 slips.go:25/30)。
	full, rep, _, ok := store.exportReport(r, u)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	// 匿名且非特权账号时 exportReport 已清空 RegNumber/Name/ClassInfo:
	// 成绩条不再注入姓名/班级/考号(与成绩册导出同限制,评审 slips.go:30 / CWE-200)。
	anonymous := len(rep.Students) > 0 && rep.Students[0].RegNumber == ""
	// 均分沿用"全卷真实口径":排名与均分都取全卷,单班教师印出的数字与组长导出对得上
	mean := rep.Mean
	if fullRep := mergeReport(full); fullRep != nil {
		mean = fullRep.Mean
	}

	// 班级排名:按行政班逐班排(缺考不计;并列同名次)——走班学生挂教学班会让
	// "班级名次"跨班混排,与行政班语义不符(评审 slips.go:54)
	classRank := map[string]int{}
	if !anonymous {
		adminClasses := map[string]bool{}
		for _, st := range full.Roster {
			if st.Class != "" {
				adminClasses[st.Class] = true
			}
		}
		for cls := range adminClasses {
			var scores []float64
			mine := map[string]float64{}
			for _, l := range rep.Students {
				if !l.Absent && classOfReg(full, l.RegNumber) == cls {
					scores = append(scores, l.Score)
					mine[l.RegNumber] = l.Score
				}
			}
			for reg, sc := range mine {
				rank := 1
				for _, s := range scores {
					if s > sc {
						rank++
					}
				}
				classRank[reg] = rank
			}
		}
	}

	rows := []export.SlipRow{}
	for _, l := range rep.Students {
		if l.Absent {
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
			Mean:      trimMaxF(mean),
		})
	}
	data, err := export.SlipsPDF(rows)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=opengrade-slips-%s.pdf", full.ID))
	_, _ = w.Write(data)
}
