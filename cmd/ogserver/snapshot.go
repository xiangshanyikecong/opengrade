package main

// Exam 快照(读路径并发安全,A-1 修复的核心):
// Store.Get/List 返回深拷贝而非内部指针 —— 此前所有读接口在锁外遍历
// ex.Grades/ex.Results/ex.Assignments 等,与锁内写路径(SetGrade/AppendScans/…)
// 并发即触发 Go 运行时「concurrent map read and map write」致命错误,
// 该错误不可 recover,一人批改 + 一人刷新列表就会杀死整个服务。
// 拷贝策略:可变 map/切片全部克隆;Layout/LayoutB 创建后不可变,直接共享;
// Results 逐卷深拷贝(Electives 是 map,RegMarks/Warnings/Answers 是切片)。
// 写入必须走 Store 的变更方法(锁内操作原会话);对快照的修改不会落盘。

import (
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

// 克隆助手:保持 nil/空切片语义(nil → nil,非 nil 空 → 非 nil 空)——
// 空切片被克隆成 nil 的话,JSON 输出会从 [] 变 null,前端/测试按数组解析即崩。
func cloneStrSlice(src []string) []string {
	if src == nil {
		return nil
	}
	out := make([]string, 0, len(src))
	return append(out, src...)
}

func cloneIntSlice(src []int) []int {
	if src == nil {
		return nil
	}
	out := make([]int, 0, len(src))
	return append(out, src...)
}

func cloneStrMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneResult(res *omr.Result) *omr.Result {
	if res == nil {
		return nil
	}
	rc := *res
	rc.Answers = cloneStrSlice(res.Answers)
	rc.Warnings = cloneStrSlice(res.Warnings)
	rc.PageNos = cloneIntSlice(res.PageNos)
	if res.RegMarks != nil {
		rc.RegMarks = make([][]int, len(res.RegMarks))
		for i, m := range res.RegMarks {
			rc.RegMarks[i] = cloneIntSlice(m)
		}
	}
	if res.Electives != nil {
		rc.Electives = make(map[int][]int, len(res.Electives))
		for k, v := range res.Electives {
			rc.Electives[k] = cloneIntSlice(v)
		}
	}
	return &rc
}

// snapshot 返回考试会话的深拷贝(调用方持 Store.mu)。
func (ex *Exam) snapshot() *Exam {
	cp := *ex
	cp.ObjectiveKey = cloneStrSlice(ex.ObjectiveKey)
	cp.KeyB = cloneStrSlice(ex.KeyB)
	cp.OrderB = cloneIntSlice(ex.OrderB)
	if ex.Results != nil {
		cp.Results = make([]*omr.Result, len(ex.Results))
		for i, res := range ex.Results {
			cp.Results[i] = cloneResult(res)
		}
	}
	if ex.Grades != nil {
		cp.Grades = make(map[string]score.Grade, len(ex.Grades))
		for k, v := range ex.Grades {
			cp.Grades[k] = v
		}
	}
	cp.Assignments = cloneStrMap(ex.Assignments)
	cp.Acked = cloneBoolMap(ex.Acked)
	cp.AssignVariant = cloneStrMap(ex.AssignVariant)
	cp.Tasks = cloneStrMap(ex.Tasks)
	cp.ClassTasks = cloneStrMap(ex.ClassTasks)
	cp.TaskDue = cloneStrMap(ex.TaskDue)
	if ex.Roster != nil {
		cp.Roster = make([]roster.Student, len(ex.Roster))
		copy(cp.Roster, ex.Roster)
	}
	if ex.Audit != nil {
		cp.Audit = make([]AuditEntry, len(ex.Audit))
		copy(cp.Audit, ex.Audit)
	}
	if ex.Appeals != nil {
		cp.Appeals = make([]Appeal, len(ex.Appeals))
		copy(cp.Appeals, ex.Appeals)
	}
	if ex.Rooms != nil {
		rp := *ex.Rooms
		rp.Rooms = make([]Room, len(ex.Rooms.Rooms))
		for i, rm := range ex.Rooms.Rooms {
			rm.Seats = append([]Seat(nil), rm.Seats...)
			rp.Rooms[i] = rm
		}
		cp.Rooms = &rp
	}
	return &cp
}
