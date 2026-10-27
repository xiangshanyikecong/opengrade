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

// teacherSees 判定教师能否看到该考试(列表/打开)。常规产品的三口径:
//   - 学科组长(SubjectLead 命中考试学科):本学科跨班全看,支撑同科多师协作;
//   - 班主任(HeadClasses 与考试班级有交集):本班全学科可见,不受任教学科限制;
//   - 任课教师:学科匹配 ∧ 班级匹配(原有规则)。
func teacherSees(u *auth.User, ex *Exam) bool {
	if ex.CreatedBy == u.Username {
		return true
	}
	if ex.Subject != "" && containsStr(u.SubjectLead, ex.Subject) {
		return true // 学科组长:本学科跨班
	}
	if overlaps(u.HeadClasses, examClasses(ex)) {
		return true // 班主任:本班全科
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

// canOpenExam 统一入口:管理员全看;年级组长按组织架构年级收敛
// (未配置管辖年级、或学校未维护年级归属时保持全看,兼容小校);
// 教师按 teacherSees;其余角色不可。
func canOpenExam(u *auth.User, ex *Exam, sch *School) bool {
	switch u.Role {
	case auth.RoleAdmin:
		return true
	case auth.RoleLeader:
		if ex.CreatedBy == u.Username || len(u.Grades) == 0 {
			return true
		}
		if sch == nil || len(sch.Snapshot()) == 0 {
			return true // 组织架构未配置:组长保持全看(老行为)
		}
		classes := examClasses(ex)
		if len(classes) == 0 {
			return true // 校级卷(无名册班级)不设防
		}
		for _, g := range sch.GradesOfClasses(classes) {
			if containsStr(u.Grades, g) {
				return true
			}
		}
		return false
	case auth.RoleTeacher:
		return teacherSees(u, ex)
	default:
		return false
	}
}

// scopeExamForTeacher 返回按任教班级过滤后的考试视图
// (学生属于任一任教班级或班主任班级即可见)。
func scopeExamForTeacher(ex *Exam, u *auth.User) *Exam {
	if u == nil || u.Role != auth.RoleTeacher {
		return ex
	}
	classes := append(append([]string(nil), u.AllClasses()...), u.HeadClasses...)
	if len(classes) == 0 {
		return ex
	}
	return scopeExamToClasses(ex, classes)
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
			// 同步剔除该生的评分(整题键 + 拆分小问键)与指派
			for q := 1; q <= len(ex.Layout.Subjectives); q++ {
				delete(cp.Grades, score.GradeKey(i+1, q))
				for p := 1; p <= len(ex.Layout.Subjectives[q-1].Parts); p++ {
					delete(cp.Grades, score.GradePartKey(i+1, q, p))
				}
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
//   - 年级组长按组织架构收敛到管辖年级的班级(未配置年级=全量);
//   - grading=true 时教师获得全量视图(跨班阅卷职责),否则按任教班级过滤;
//   - 按班分配模式(TaskMode=class)下,被分到班级(含教学班)的教师,
//     阅卷流只看到所分班级学生的卷 —— 被分到多个班时合并可见(全部所分班),
//     否则第二个班的任务在阅卷端永远无解;
//   - 管理员恒为全量。
func (s *Store) openExamFor(r *http.Request, u *auth.User, grading bool) (*Exam, bool) {
	ex, ok := s.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, s.School) {
		return nil, false
	}
	if u.Role == auth.RoleTeacher {
		if grading {
			if cs := assignedClassesFor(ex, u); len(cs) > 0 {
				return scopeExamToClasses(ex, cs), true
			}
			return ex, true
		}
		return scopeExamForTeacher(ex, u), true
	}
	if u.Role == auth.RoleLeader && !grading {
		if classes := s.School.ClassesOfGrades(u.Grades); len(classes) > 0 {
			return scopeExamToClasses(ex, classes), true
		}
	}
	return ex, true
}

// assignedClassesFor 返回按班分配模式下教师被分到的全部班级(走班可多班;
// 阅卷切片过滤合并全部所分班 —— 只取第一个会让第二个班的任务永远完不成)。
func assignedClassesFor(ex *Exam, u *auth.User) []string {
	if u == nil || ex.TaskMode != "class" {
		return nil
	}
	var out []string
	for _, c := range u.AllClasses() {
		if ex.ClassTasks[c] == u.Username {
			out = append(out, c)
		}
	}
	return out
}
