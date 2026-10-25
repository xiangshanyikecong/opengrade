package main

// 纵向学情:跨考试按考号对齐的纯计算层。
// 每场考试依旧独立存储,这里把可见考试按时间排序、逐场统分后按考号拼接——
// 个人历次成绩/名次曲线、班级均分走势、名次进退步("这次比上次退步了 15 名")。
// 权限沿用既有可见性规则:管理员/组长全看,教师看任教范围内的考试,
// 学生/家长只看本人(或绑定子女)且已发布的成绩。

import (
	"net/http"
	"sort"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/score"
)

// TrendExam 是趋势时间轴上的一场考试(一个刻度)。
type TrendExam struct {
	ID         string  `json:"id"`
	Date       string  `json:"date"`
	Subject    string  `json:"subject,omitempty"`
	Students   int     `json:"students"`  // 实考人数(缺考不计)
	Total      float64 `json:"total"`     // 卷面满分(算得分率用)
	Mean       float64 `json:"mean"`      // 全体均分
	ClassMean  float64 `json:"classMean"` // 指定班级均分(未指定班级时=全体)
	ClassCount int     `json:"classCount,omitempty"`
}

// TrendPoint 是一名学生在一场考试中的对齐数据点。
type TrendPoint struct {
	ExamID    string  `json:"examId"`
	Date      string  `json:"date"`
	Subject   string  `json:"subject,omitempty"`
	Absent    bool    `json:"absent"`
	Score     float64 `json:"score"`
	Total     float64 `json:"total"`
	Rate      float64 `json:"rate"` // 得分率:跨卷满分不同也可纵向比较
	Rank      int     `json:"rank"` // 全卷排名
	ClassRank int     `json:"classRank,omitempty"`
	Mean      float64 `json:"mean,omitempty"`      // 全体均分(学生端画对照线用)
	ClassMean float64 `json:"classMean,omitempty"` // 班级均分(学生端画对照线用)
}

// TrendStudent 是按考号跨考对齐的一名学生;points 与 exams 同为时间升序。
type TrendStudent struct {
	Reg    string       `json:"reg"`
	Name   string       `json:"name,omitempty"`
	Class  string       `json:"class,omitempty"`
	Points []TrendPoint `json:"points"`
}

// mergeReport 统分一场考试(应用人工指派后的全量视图)。
func mergeReport(ex *Exam) *score.Report {
	return score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
}

// lineByReg 在报告中按考号定位成绩行(缺考行也在其中)。
func lineByReg(rep *score.Report, reg string) (score.StudentLine, bool) {
	for _, l := range rep.Students {
		if l.RegNumber == reg {
			return l, true
		}
	}
	return score.StudentLine{}, false
}

// classOfReg 从考试名册取考号对应班级;名册未覆盖返回空串。
// classOfReg 返回该考号在本场考试名册中的行政班。
func classOfReg(ex *Exam, reg string) string {
	for _, st := range ex.Roster {
		if st.Reg == reg {
			return st.Class
		}
	}
	return ""
}

// inClassOfReg 报告该考号在本场名册中是否属于指定班级(走班:教学班也算)。
func inClassOfReg(ex *Exam, reg, class string) bool {
	if class == "" {
		return true
	}
	for _, st := range ex.Roster {
		if st.Reg == reg {
			for _, c := range st.AllClasses() {
				if c == class {
					return true
				}
			}
			return false
		}
	}
	return false
}

// nameOfReg 从考试名册取考号对应姓名。
func nameOfReg(ex *Exam, reg string) string {
	for _, st := range ex.Roster {
		if st.Reg == reg {
			return st.Name
		}
	}
	return ""
}

// rankWithin 计算同一班级内的名次(并列同名次,1 起)。传入的行须已过滤缺考。
func rankWithin(lines []score.StudentLine, reg string) int {
	same := make([]float64, 0, len(lines))
	for _, l := range lines {
		if !l.Absent {
			same = append(same, l.Score)
		}
	}
	me, ok := lineByReg(&score.Report{Students: lines}, reg)
	if !ok {
		return 0
	}
	rank := 1
	for _, s := range same {
		if s > me.Score {
			rank++
		}
	}
	return rank
}

// classMeanOf 取某班级在一场考试中的均分与人数(缺考不计;班级为空时取全体)。
func classMeanOf(ex *Exam, rep *score.Report, class string) (mean float64, n int) {
	var sum float64
	for _, l := range rep.Students {
		if l.Absent {
			continue
		}
		if !inClassOfReg(ex, l.RegNumber, class) {
			continue
		}
		sum += l.Score
		n++
	}
	if n > 0 {
		mean = sum / float64(n)
	}
	return mean, n
}

// visibleExamsAsc 返回当前账号可见(可选再按学科过滤)的考试,时间升序;
// 同刻度按 ID 稳定排序,保证趋势序列可复现。仅供教职员使用(学生端见 handleStudentTrends)。
func visibleExamsAsc(store *Store, u *auth.User, subject string) []*Exam {
	out := make([]*Exam, 0)
	for _, ex := range store.List() { // List 为时间降序
		if !canOpenExam(u, ex, store.School) {
			continue
		}
		if subject != "" && ex.Subject != subject {
			continue
		}
		out = append(out, ex)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // 反转为升序
		out[i], out[j] = out[j], out[i]
	}
	// 时间轴口径与年级总表一致:考试日期优先(未填用创建时间)。
	// 只按创建时间排,补录建考(创建晚于考试日期)会让两个纵向视图的科目顺序互相矛盾。
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := examDateOf(out[i]), examDateOf(out[j])
		if di != dj {
			return di < dj
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// handleStaffTrends 教师端纵向趋势:GET /api/v1/trends?class=&subject=
// 返回可见考试序列(含班级均分走势)与逐生跨考对齐数据;班级/学科可选过滤。
func handleStaffTrends(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	class, subject := r.URL.Query().Get("class"), r.URL.Query().Get("subject")
	exams := visibleExamsAsc(store, u, subject)

	// 班级选择器候选:可见考试名册中出现过的班级
	classSet := map[string]bool{}
	for _, ex := range exams {
		for _, c := range examClasses(ex) {
			classSet[c] = true
		}
	}
	classes := make([]string, 0, len(classSet))
	for c := range classSet {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	// 关心哪些考号:指定班级 → 名册中该班的考号(走班含教学班归属);未指定 → 全部
	regClass := map[string]string{} // reg → 行政班(以最近一场考试的名册为准,学生换班跟随最新)
	regName := map[string]string{}
	for _, ex := range exams {
		for _, st := range ex.Roster {
			if st.Reg == "" {
				continue
			}
			if st.Class != "" {
				regClass[st.Reg] = st.Class // exams 升序遍历,后写覆盖=最近考试
			}
			if st.Name != "" {
				regName[st.Reg] = st.Name
			}
		}
	}
	interested := map[string]bool{}
	for _, ex := range exams {
		for _, st := range ex.Roster {
			if st.Reg == "" {
				continue
			}
			if class == "" {
				interested[st.Reg] = true
				continue
			}
			for _, c := range st.AllClasses() {
				if c == class {
					interested[st.Reg] = true
					break
				}
			}
		}
		if class != "" {
			continue
		}
		for i := range ex.Results { // 名册未覆盖的卷(校级卷)也纳入
			if reg := ex.AssignedReg(i + 1); reg != "" {
				interested[reg] = true
			}
		}
	}

	tExams := []TrendExam{} // 空序列输出 [] 而非 null
	students := map[string]*TrendStudent{}
	for _, ex := range exams {
		rep := mergeReport(ex)
		mean, n := classMeanOf(ex, rep, class)
		if class == "" { // 未指定班级:班级序列=全体序列
			mean, n = rep.Mean, rep.Count
		} else if n == 0 {
			continue // 该班不在本场考试中,不进入该班的时间轴
		}
		tExams = append(tExams, TrendExam{
			ID: ex.ID, Date: examDateOf(ex), Subject: ex.Subject,
			Students: rep.Count, Total: rep.Total, Mean: rep.Mean, ClassMean: mean, ClassCount: n,
		})
		for _, l := range rep.Students {
			reg := l.RegNumber
			if !interested[reg] {
				continue
			}
			ts := students[reg]
			if ts == nil {
				ts = &TrendStudent{Reg: reg, Name: regName[reg], Class: regClass[reg]}
				students[reg] = ts
			}
			pt := TrendPoint{ExamID: ex.ID, Date: examDateOf(ex),
				Subject: ex.Subject, Absent: l.Absent, Score: l.Score, Total: l.Total, Rank: l.Rank}
			if l.Total > 0 {
				pt.Rate = l.Score / l.Total
			}
			if class != "" && !l.Absent {
				sameClass := make([]score.StudentLine, 0, rep.Count)
				for _, ol := range rep.Students {
					if inClassOfReg(ex, ol.RegNumber, class) { // 走班双口径:排名池与均分口径一致(教学班也算)
						sameClass = append(sameClass, ol)
					}
				}
				pt.ClassRank = rankWithin(sameClass, reg)
			}
			ts.Points = append(ts.Points, pt)
		}
	}
	regs := make([]string, 0, len(students))
	for reg := range students {
		regs = append(regs, reg)
	}
	sort.Slice(regs, func(i, j int) bool {
		if students[regs[i]].Class != students[regs[j]].Class {
			return students[regs[i]].Class < students[regs[j]].Class
		}
		return regs[i] < regs[j]
	})
	out := make([]TrendStudent, 0, len(regs))
	for _, reg := range regs {
		out = append(out, *students[reg])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"classes":  classes,
		"class":    class,
		"subject":  subject,
		"exams":    tExams,
		"students": out,
	})
}

// handleStudentTrends 学生/家长端历次成绩:GET /api/v1/student/trends?child=
// 仅统计已发布考试,按考号跨考对齐,供个人成绩曲线与名次进退步展示。
func handleStudentTrends(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	reg, ok := pickTarget(w, r, u)
	if !ok {
		return
	}
	// 时间轴口径与教师端 visibleExamsAsc 一致:考试日期优先(未填用创建时间)。
	// 只按创建时间排,补录建考(创建晚于考试日期)会让个人曲线与教师端/年级总表互相矛盾。
	exams := make([]*Exam, 0)
	for _, ex := range store.List() {
		if !ex.Published {
			continue // 成绩发布控制:未发布不可查
		}
		exams = append(exams, ex)
	}
	sort.SliceStable(exams, func(i, j int) bool {
		di, dj := examDateOf(exams[i]), examDateOf(exams[j])
		if di != dj {
			return di < dj
		}
		if !exams[i].CreatedAt.Equal(exams[j].CreatedAt) {
			return exams[i].CreatedAt.Before(exams[j].CreatedAt)
		}
		return exams[i].ID < exams[j].ID
	})
	var points []TrendPoint
	for _, ex := range exams {
		idx := findIndex(ex, reg)
		if idx == 0 {
			continue
		}
		rep := mergeReport(ex)
		line, found := lineByReg(rep, reg)
		if !found {
			continue
		}
		pt := TrendPoint{ExamID: ex.ID, Date: examDateOf(ex),
			Subject: ex.Subject, Absent: line.Absent, Score: line.Score, Total: line.Total,
			Rank: line.Rank, Mean: rep.Mean}
		if line.Total > 0 {
			pt.Rate = line.Score / line.Total
		}
		// 班级口径:家长账号自身无班级,取该场名册中子女的班级(否则班级均分恒空、班级排名错位)
		cls := u.Class
		if cls == "" {
			cls = classOfReg(ex, reg)
		}
		if cls != "" {
			pt.ClassMean, _ = classMeanOf(ex, rep, cls) // 缺考也展示该场班级均分作参照
			if !line.Absent {                           // 缺考不参与班级排名(同教师端口径),不给垫底名次
				sameClass := make([]score.StudentLine, 0, rep.Count)
				for _, ol := range rep.Students {
					if inClassOfReg(ex, ol.RegNumber, cls) {
						sameClass = append(sameClass, ol)
					}
				}
				pt.ClassRank = rankWithin(sameClass, reg)
			}
		}
		points = append(points, pt)
	}
	if points == nil {
		points = []TrendPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reg":    reg,
		"class":  u.Class,
		"points": points,
	})
}
