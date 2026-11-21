package main

// 阅卷工作量统计(P2:算补贴用):按评分操作人聚合已评份数。
// 口径:一条评分记录(Graded && By 非空)= 1 份;拆分小问按小问各计 1(与评卷动作一致)。
// 历史数据(未记录操作人)归入 "(历史)" 桶,新评分自动记录 By。

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/opengrade/opengrade/auth"
)

// handleWorkload GET /api/v1/workload → 按教师聚合阅卷量(可见考试口径)。
func handleWorkload(w http.ResponseWriter, r *http.Request, store *Store, authStore *auth.Store, u *auth.User) {
	type row struct {
		Username    string         `json:"username"`
		DisplayName string         `json:"displayName,omitempty"`
		Graded      int            `json:"graded"`           // 已评仂数(条)
		Flagged     int            `json:"flagged"`          // 当前挂 ⚑ 待复核数
		ByExam      map[string]int `json:"byExam,omitempty"` // 考试 ID → 评了多条
	}
	byUser := map[string]*row{}
	get := func(name string) *row {
		if byUser[name] == nil {
			byUser[name] = &row{Username: name, ByExam: map[string]int{}}
		}
		return byUser[name]
	}
	display := map[string]string{}
	for _, su := range authStore.List() {
		display[su.Username] = su.DisplayName
	}
	// 全量流式(VisitExams):工作量必须看每场考试的全部评分记录,无法摘要粗筛;
	// 低频管理页,内存 O(单场),时延 O(考试总数)。
	store.VisitExams(func(ex *Exam) bool {
		if !canOpenExam(u, ex, store.School) {
			return true
		}
		studentOf := func(key string) int {
			var n int
			if _, err := fmt.Sscanf(key, "s%d", &n); err != nil {
				return 0
			}
			return n
		}
		for key, g := range ex.Grades {
			// 作废卷的评分记录不计入(SetVoid 口径:作废卷不进统计;恢复后自然重新计入,
			// 评审 workload.go:40)
			if n := studentOf(key); n >= 1 && n <= len(ex.Results) && ex.Results[n-1] != nil && ex.Results[n-1].Voided {
				continue
			}
			// flagged 与 graded 独立统计:未评完的 flag 标记也是一条待复核,
			// 此前先按 Graded 跳过,半阅 flag 被漏计(评审 workload.go:43)
			if g.Graded {
				name := g.By
				if name == "" {
					name = "(历史)"
				}
				row := get(name)
				row.Graded++
				row.ByExam[ex.ID]++
			}
			if g.Mark == "flag" {
				name := g.By
				if name == "" {
					name = "(历史)"
				}
				row := get(name)
				row.Flagged++
			}
		}
		return true
	})
	out := []row{}
	for name, row := range byUser {
		row.DisplayName = display[name]
		if row.ByExam != nil && len(row.ByExam) == 0 {
			row.ByExam = nil
		}
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Graded > out[j].Graded })
	writeJSON(w, http.StatusOK, out)
}
