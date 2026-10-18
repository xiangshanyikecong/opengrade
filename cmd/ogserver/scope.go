package main

// 教师数据范围(方案稿组织形态的权限落地):
//   - 学科教师只看到「学科匹配 ∧ 班级匹配」或本人创建的考试;
//   - 成绩/报告/导出按任教班级过滤(剔除不可见学生,索引保持原位);
//   - 阅卷不受班级过滤(跨班阅卷是工作职责,切片匿名只含考号),?grading=1 走全量;
//   - 年级主任/管理员全看;学生仅本人(见 student.go)。
// 未配置学科与班级的教师只能看自己创建的考试 —— 缺配置不放大权限。

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

// examClasses 返回考试名册中出现过的班级(去重,稳定排序)。
func examClasses(ex *Exam) []string {
	seen := map[string]bool{}
	var out []string
	for _, st := range ex.Roster {
		if st.Class != "" && !seen[st.Class] {
			seen[st.Class] = true
			out = append(out, st.Class)
		}
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// teacherSees 判定教师能否看到该考试(列表/打开)。
func teacherSees(u *auth.User, ex *Exam) bool {
	if ex.CreatedBy == u.Username {
		return true
	}
	if len(u.Subjects) == 0 && len(u.Class) == 0 {
		return false // 未配置任教信息的教师:仅本人创建的考试
	}
	subjectOK := ex.Subject == "" || len(u.Subjects) == 0 || containsStr(u.Subjects, ex.Subject)
	classes := examClasses(ex)
	classOK := len(classes) == 0 || containsStr(classes, u.Class)
	return subjectOK && classOK
}

// canOpenExam 统一入口:管理员/年级组长全看;教师按上述规则;其余角色不可。
func canOpenExam(u *auth.User, ex *Exam) bool {
	switch u.Role {
	case auth.RoleAdmin, auth.RoleLeader:
		return true
	case auth.RoleTeacher:
		return teacherSees(u, ex)
	default:
		return false
	}
}

// scopeExamForTeacher 返回按任教班级过滤后的考试视图:
// Results 中不可见学生置 nil(索引原位保留,评分键 s{n} 不变),
// Grades/Assignments 剔除不可见学生。非教师或未配置班级时原样返回。
func scopeExamForTeacher(ex *Exam, u *auth.User) *Exam {
	if u == nil || u.Role != auth.RoleTeacher || u.Class == "" {
		return ex
	}
	byReg := map[string]string{}
	for _, st := range ex.Roster {
		byReg[st.Reg] = st.Class
	}
	cp := *ex
	cp.Results = make([]*omr.Result, len(ex.Results))
	cp.Grades = map[string]score.Grade{}
	cp.Assignments = map[string]string{}
	for k, v := range ex.Grades {
		cp.Grades[k] = v
	}
	for k, v := range ex.Assignments {
		cp.Assignments[k] = v
	}
	for i := range ex.Results {
		reg := ex.AssignedReg(i + 1)
		cls, known := byReg[reg]
		// 名册未覆盖的学生(校级卷)对任课教师可见;名册覆盖但班级不符的过滤
		if known && cls != u.Class {
			cp.Results[i] = nil
			// 同步剔除该生的评分与指派
			for q := 1; q <= len(ex.Layout.Subjectives); q++ {
				delete(cp.Grades, score.GradeKey(i+1, q))
			}
			delete(cp.Assignments, fmt.Sprintf("s%d", i+1))
			continue
		}
		cp.Results[i] = ex.Results[i]
	}
	return &cp
}

// openExamFor 取考试并做可见性与数据范围裁剪:
//   - 不可见(不存在)→ (nil,false),对外一律 404,不泄露考试存在性;
//   - grading=true 时教师获得全量视图(跨班阅卷职责),否则按任教班级过滤;
//   - 管理员/年级组长恒为全量。
func (s *Store) openExamFor(r *http.Request, u *auth.User, grading bool) (*Exam, bool) {
	ex, ok := s.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex) {
		return nil, false
	}
	if grading || u.Role != auth.RoleTeacher {
		return ex, true
	}
	return scopeExamForTeacher(ex, u), true
}

var _ = strings.TrimSpace
