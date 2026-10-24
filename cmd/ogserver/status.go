package main

// 考试阶段状态机(常规网上阅卷流程):待扫描 → 扫描中 → 异常待处理 → 阅卷中 → 待发布 → 已发布。
// 阶段一律从会话数据派生而非落盘字段 —— 数据即状态,不存在"忘了推进"的脏状态:
//   - 已扫 0 卷 → 待扫描(建考=建档,扫描是考后独立阶段);
//   - 名册已知且已扫不足 → 扫描中(还差几张一目了然);
//   - 存在未裁决异常卷(重答/空白卡/考号异常,且未人工指派考号、未标缺考)→ 异常待处理;
//   - 主观题还有切片未评 → 阅卷中;
//   - 全部评完未发布 → 待发布;发布 → 已发布。

import (
	"fmt"
	"sort"
)

// 阶段常量(前端按 stage 上色,按 label 直接展示)。
const (
	stageAwaiting = "awaiting_scan" // 待扫描
	stageScanning = "scanning"      // 扫描中
	stageAnomaly  = "anomalies"     // 异常待处理
	stageGrading  = "grading"       // 阅卷中
	stageReady    = "ready"         // 待发布
	stagePub      = "published"     // 已发布
)

// stageView 是阶段视图:列表与详情共用,数字供工作台"已扫/未评/异常"看板直接展示。
type stageView struct {
	Stage    string `json:"stage"`
	Label    string `json:"label"`
	Students int    `json:"students"`  // 已扫卷数
	Expected int    `json:"expected"`  // 应扫卷数(名册人数;0=名册未提供)
	Warnings int    `json:"warnings"`  // 未裁决异常卷数
	Absent   int    `json:"absent"`    // 缺考人数
	Manual   int    `json:"manual"`    // 手工补录卷数
	Ungraded int    `json:"ungraded"`  // 未评主观切片(份)
	Total    int    `json:"total"`     // 主观切片总数(份;纯客观卷为 0)
	Flagged  int    `json:"flagged"`   // 待复核(⚑)切片数
	Graded   bool   `json:"gradedAll"` // 主观题是否全部评完
}

// sheetResolved 判定一张异常卷是否已被人工裁决:
// 考号人工指派过(采纳推荐/手工确认)或已标缺考,视为处理完成,不再计入"异常待处理"。
func sheetResolved(ex *Exam, student int) bool {
	if ex.Results[student-1].Absent {
		return true
	}
	if ex.Assignments != nil && ex.Assignments[fmt.Sprintf("s%d", student)] != "" {
		return true
	}
	return false
}

// examStage 派生一场考试当前所处阶段与看板数字。
func examStage(ex *Exam) stageView {
	v := stageView{Students: len(ex.Results), Expected: len(ex.Roster)}
	gradedTotal := 0
	for qi := range ex.Layout.Subjectives {
		v.Total += len(ex.Results)
		for si := range ex.Results {
			if subjectiveGraded(ex, si+1, qi+1) {
				gradedTotal++
			}
		}
	}
	for si, res := range ex.Results {
		if res == nil {
			continue
		}
		if res.Absent {
			v.Absent++
		}
		if res.Manual {
			v.Manual++
		}
		if len(res.Warnings) > 0 && !sheetResolved(ex, si+1) {
			v.Warnings++
		}
	}
	for _, g := range ex.Grades {
		if g.Mark == "flag" {
			v.Flagged++
		}
	}
	v.Ungraded = v.Total - gradedTotal
	v.Graded = v.Total == 0 || v.Ungraded == 0

	switch {
	case ex.Published:
		v.Stage, v.Label = stagePub, "已发布"
	case len(ex.Results) == 0:
		v.Stage, v.Label = stageAwaiting, "待扫描"
	case v.Expected > 0 && v.Students < v.Expected:
		v.Stage, v.Label = stageScanning, fmt.Sprintf("扫描中 %d/%d", v.Students, v.Expected)
	case v.Warnings > 0:
		v.Stage, v.Label = stageAnomaly, fmt.Sprintf("异常待处理 %d", v.Warnings)
	case !v.Graded:
		v.Stage, v.Label = stageGrading, fmt.Sprintf("阅卷中 %d%%", pct(gradedTotal, v.Total))
	default:
		v.Stage, v.Label = stageReady, "待发布"
	}
	return v
}

func pct(a, b int) int {
	if b == 0 {
		return 100
	}
	return a * 100 / b
}

// flaggedView 是待复核队列的一行:第 q 题第 student 卷被标 ⚑,待组长/教师复核改分。
type flaggedView struct {
	Student  int     `json:"student"`
	Reg      string  `json:"reg,omitempty"`
	Question int     `json:"question"`
	Score    float64 `json:"score"`
}

// flaggedQueue 汇总 ⚑ 待复核切片(按题号排序)——阅卷标记必须有出口,否则闭环不存在。
func flaggedQueue(ex *Exam) []flaggedView {
	out := []flaggedView{}
	for key, g := range ex.Grades {
		if g.Mark != "flag" {
			continue
		}
		var s, q int
		if _, err := fmt.Sscanf(key, "s%d-q%d", &s, &q); err != nil {
			continue // 小问键 s1-q2p3 不进队(以大题复核入口处理)
		}
		out = append(out, flaggedView{Student: s, Question: q, Reg: ex.AssignedReg(s), Score: g.Score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Question != out[j].Question {
			return out[i].Question < out[j].Question
		}
		return out[i].Student < out[j].Student
	})
	return out
}
