package main

// 教师数据范围(方案稿组织形态的权限落地),走班(新高考)语义:
//   - 学生可双归属(行政班+教学班),教师可多班任教;判定一律按"班级集合有交集";
//   - 学科教师只看到「学科匹配 ∧ 班级匹配」或本人创建的考试;
//   - 成绩/报告/导出按任教班级过滤(剔除不可见学生,索引保持原位),
//     学生属于教师任一班级即保留(走班跨班可见);
//   - 阅卷不受班级过滤(跨班阅卷是工作职责,切片匿名只含考号),?grading=1 走全量;
//   - 年级主任/管理员全看;学生仅本人(见 student.go)。
// 未配置学科与班级的教师只能看自己创建的考试 —— 缺配置不放大权限。

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

// examClasses 返回考试名册中出现过的全部班级(行政班+教学班并集,去重排序)。
func examClasses(ex *Exam) []string {
	seen := map[string]bool{}
	for _, st := range ex.Roster {
		for _, c := range st.AllClasses() {
			seen[c] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
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

// overlaps 判定两个班级集合是否有交集(任一为空按"不匹配"处理,由调用方兜底)。
func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// teacherSees 判定教师能否看到该考试(列表/打开)。
func teacherSees(u *auth.User, ex *Exam) bool {
	if ex.CreatedBy == u.Username {
		return true
	}
	myClasses := u.AllClasses()
	if len(u.Subjects) == 0 && len(myClasses) == 0 {
		return false // 未配置任教信息的教师:仅本人创建的考试
	}
	subjectOK := ex.Subject == "" || len(u.Subjects) == 0 || containsStr(u.Subjects, ex.Subject)
	classes := examClasses(ex)
	classOK := len(classes) == 0 || overlaps(myClasses, classes)
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

// scopeExamForTeacher 返回按任教班级过滤后的考试视图(学生属于任一任教班级即可见)。
func scopeExamForTeacher(ex *Exam, u *auth.User) *Exam {
	if u == nil || u.Role != auth.RoleTeacher || len(u.AllClasses()) == 0 {
		return ex
	}
	return scopeExamToClasses(ex, u.AllClasses())
}

// scopeExamToClasses 返回按班级集合过滤后的考试视图:
// Results 中不可见学生置 nil(索引原位保留,评分键 s{n} 不变),
// Grades/Assignments 剔除不可见学生。走班语义:学生班级集合与给定集合有交集即保留。
func scopeExamToClasses(ex *Exam, classes []string) *Exam {
	if len(classes) == 0 {
		return ex
	}
	byReg := map[string][]string{}
	for _, st := range ex.Roster {
		byReg[st.Reg] = st.AllClasses()
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
		// 名册未覆盖的学生(校级卷)对任课教师可见;名册覆盖但班级无交集的过滤
		if known && !overlaps(cls, classes) {
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
//   - 按班分配模式(TaskMode=class)下,被分到某班(含教学班)的教师,
//     阅卷流只看到所分班级学生的卷(多班教师不再按全部任教班放行);
//   - 管理员/年级组长恒为全量。
func (s *Store) openExamFor(r *http.Request, u *auth.User, grading bool) (*Exam, bool) {
	ex, ok := s.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex) {
		return nil, false
	}
	if u.Role != auth.RoleTeacher {
		return ex, true
	}
	if grading {
		if c := assignedClassFor(ex, u); c != "" {
			return scopeExamToClasses(ex, []string{c}), true
		}
		return ex, true
	}
	return scopeExamForTeacher(ex, u), true
}

// classAssigned 判定教师是否处于"按班分配"任务模式且被分到了班级
// (走班:教师任一任教班级命中分配表即可)。
// 此时即便在阅卷流(grading=1),也只应看到所分班级学生的卷。
func classAssigned(ex *Exam, u *auth.User) bool {
	if u == nil || u.Role != auth.RoleTeacher {
		return false
	}
	if ex.TaskMode != "class" {
		return false
	}
	for _, c := range u.AllClasses() {
		if ex.ClassTasks[c] == u.Username {
			return true
		}
	}
	return false
}

// assignedClassFor 返回按班分配模式下教师被分的班级(用于切片过滤的口径提示)。
// 未被分班返回空串。
func assignedClassFor(ex *Exam, u *auth.User) string {
	if u == nil || ex.TaskMode != "class" {
		return ""
	}
	for _, c := range u.AllClasses() {
		if ex.ClassTasks[c] == u.Username {
			return c
		}
	}
	return ""
}
