package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

// Exam 是一场考试的服务端会话:版式、参考答案、各卷 OMR 结果、
// 主观题切片索引与人工评分。全部数据落在本机 data/ 目录,可整目录打包迁移。
type Exam struct {
	ID           string                 `json:"id"`
	CreatedAt    time.Time              `json:"createdAt"`
	Layout       card.Layout            `json:"layout"`       // 规范化 v2 版式
	ObjectiveKey []string               `json:"objectiveKey"` // 客观题参考答案
	Results      []*omr.Result          `json:"results"`      // 按上传顺序排列;下标+1 = 学生号
	Grades       map[string]score.Grade `json:"grades"`       // key: score.GradeKey(s, q)
	Threshold    string                 `json:"threshold"`    // 读卡二值化方式
	Roster       []roster.Student       `json:"roster,omitempty"`
	Assignments  map[string]string      `json:"assignments,omitempty"` // "s{学生号}" → 人工确认的考号
	CreatedBy    string                 `json:"createdBy,omitempty"`   // 建考账号
	Subject      string                 `json:"subject,omitempty"`     // 学科(教师按学科过滤考试)
	Tasks        map[string]string      `json:"tasks,omitempty"`       // 阅卷任务分配:"q{主观题号}" → 阅卷教师账号
	TaskMode     string                 `json:"taskMode,omitempty"`    // 分配模式:"" | "question" | "class"
	ClassTasks   map[string]string      `json:"classTasks,omitempty"`  // 按班分配:"{班级}" → 教师账号(班主任改本班)
	Published    bool                   `json:"published,omitempty"`   // 成绩发布开关(教师确认后学生/家长可查)
	PublishedAt  *time.Time             `json:"publishedAt,omitempty"` // 发布时间
	Rooms        *RoomPlan              `json:"rooms,omitempty"`       // 考场编排方案(生成后落盘,可微调)
	Audit        []AuditEntry           `json:"audit,omitempty"`       // 关键操作留痕(改答案/改分/发布/指派)
}

// RoomPlan 是考场编排方案:按名册生成、落盘持久化,支持座位对调与监考指派。
type RoomPlan struct {
	Size  int    `json:"size"`  // 每场目标人数(生成参数,留档)
	Group string `json:"group"` // 编排方式:class=同班聚齐(按主班)|reg=纯考号序
	Rooms []Room `json:"rooms"`
}

// Room 是一个考场:名称、监考与座位表。
type Room struct {
	Name    string `json:"name"`              // 考场名(R01、R02…)
	Proctor string `json:"proctor,omitempty"` // 监考教师账号
	Seats   []Seat `json:"seats"`
}

// Seat 是一个座位;Reg 为空表示空座(可通过对调把学生移过来)。
type Seat struct {
	Idx   int    `json:"idx"`             // 座位号(场内 1-based)
	Reg   string `json:"reg,omitempty"`
	Name  string `json:"name,omitempty"`
	Class string `json:"class,omitempty"` // 主班(打印座位表用)
}

// AuditEntry 是一条操作审计记录:谁、何时、对考试做了什么,用于合规追溯。
type AuditEntry struct {
	Time   time.Time `json:"time"`
	By     string    `json:"by"`
	Action string    `json:"action"`           // setKey | setGrade | assign | publish | unpublish
	Detail string    `json:"detail,omitempty"` // 人类可读的变更说明
}

// NewExam 是 Create 的入参包。
type NewExam struct {
	Layout    card.Layout
	Key       []string
	Results   []*omr.Result
	Slices    [][]omr.Slice
	Pages      []*image.Gray    // 矫正后的整卷图(答卷回溯;单页版式每生一张)
	PageGroups [][]*image.Gray  // 多页版式:每生按页序的矫正图组(第 1 页兼容旧文件名)
	Threshold string
	CreatedBy string
	Subject   string
	Roster    []roster.Student
}

// AssignedReg 返回某学生的最终考号:人工指派优先,否则用 OMR 判读值。
func (ex *Exam) AssignedReg(student int) string {
	if ex.Assignments != nil {
		if reg, ok := ex.Assignments[fmt.Sprintf("s%d", student)]; ok {
			return reg
		}
	}
	if student >= 1 && student <= len(ex.Results) {
		return ex.Results[student-1].RegNumber
	}
	return ""
}

// Store 是考试会话的文件存储(目录 + JSON,零外部依赖;SQLite 迁移在路线图 M1 末)。
type Store struct {
	mu    sync.Mutex
	dir   string
	exams map[string]*Exam
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, exams: map[string]*Exam{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "exam.json")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var ex Exam
		if err := json.Unmarshal(b, &ex); err != nil {
			continue // 损坏的会话不阻塞启动
		}
		s.exams[ex.ID] = &ex
	}
	return s, nil
}

// Create 落盘一场新考试(切片 + 整卷回溯图)。
func (s *Store) Create(ne NewExam) (*Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := newID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(filepath.Join(dir, "slices"), 0o755); err != nil {
		return nil, err
	}
	ex := &Exam{
		ID:           id,
		CreatedAt:    time.Now(),
		Layout:       ne.Layout,
		ObjectiveKey: ne.Key,
		Results:      ne.Results,
		Grades:       map[string]score.Grade{},
		Threshold:    ne.Threshold,
		Roster:       ne.Roster,
		Assignments:  map[string]string{},
		CreatedBy:    ne.CreatedBy,
		Subject:      ne.Subject,
	}
	for si := range ne.Slices {
		for _, sl := range ne.Slices[si] {
			p := filepath.Join(dir, "slices", fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
			if err := savePNGFile(p, sl.Image); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		return nil, err
	}
	if ne.PageGroups != nil { // 多页版式:第 1 页用旧文件名,第 2+ 页加 _p 页号
		for si, group := range ne.PageGroups {
			for k, pg := range group {
				if pg == nil {
					continue
				}
				name := fmt.Sprintf("s%02d.jpg", si+1)
				if k > 0 {
					name = fmt.Sprintf("s%02d_p%02d.jpg", si+1, k+1)
				}
				if err := saveJPEGFile(filepath.Join(dir, "pages", name), pg); err != nil {
					return nil, err
				}
			}
		}
	} else if len(ne.Pages) > 0 {
		for si, pg := range ne.Pages {
			if pg == nil {
				continue
			}
			if err := saveJPEGFile(filepath.Join(dir, "pages", fmt.Sprintf("s%02d.jpg", si+1)), pg); err != nil {
				return nil, err
			}
		}
	}
	if err := s.save(ex); err != nil {
		return nil, err
	}
	s.exams[id] = ex
	return ex, nil
}

func (s *Store) Get(id string) (*Exam, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	return ex, ok
}

func (s *Store) List() []*Exam {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Exam, 0, len(s.exams))
	for _, ex := range s.exams {
		out = append(out, ex)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// SetGrade 写入一条主观题整题评分并落盘。by 为操作者账号(审计留痕)。
// 拆分了小问的题目(版式 v3)必须走 SetGradePart 逐问评分。
func (s *Store) SetGrade(id string, student, question int, g score.Grade, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return fmt.Errorf("主观题序号 %d 越界(共 %d 道)", question, len(ex.Layout.Subjectives))
	}
	if parts := len(ex.Layout.Subjectives[question-1].Parts); parts > 0 {
		return fmt.Errorf("第 %d 题已拆分为 %d 个小问,请逐小问评分(part 参数)", question, parts)
	}
	key := score.GradeKey(student, question)
	verb := "评分"
	if old, exists := ex.Grades[key]; exists && old.Graded {
		if old.Score != g.Score {
			verb = fmt.Sprintf("改分(%.1f → %.1f)", old.Score, g.Score)
		} else if !g.Graded {
			verb = "撤销评分"
		}
	}
	ex.Grades[key] = g
	ex.log(by, "setGrade", fmt.Sprintf("第 %d 卷 主观题 %d:%s %.1f 分", student, question, verb, g.Score))
	return s.save(ex)
}

// SetGradePart 写入一条小问评分(版式 v3 拆分题):大题总分由统分层自动求和。
func (s *Store) SetGradePart(id string, student, question, part int, g score.Grade, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return fmt.Errorf("主观题序号 %d 越界(共 %d 道)", question, len(ex.Layout.Subjectives))
	}
	parts := len(ex.Layout.Subjectives[question-1].Parts)
	if parts == 0 {
		return fmt.Errorf("第 %d 题未拆分小问,应整题评分", question)
	}
	if part < 1 || part > parts {
		return fmt.Errorf("小问序号 %d 越界(共 %d 问)", part, parts)
	}
	key := score.GradePartKey(student, question, part)
	verb := "评分"
	if old, exists := ex.Grades[key]; exists && old.Graded {
		if old.Score != g.Score {
			verb = fmt.Sprintf("改分(%.1f → %.1f)", old.Score, g.Score)
		} else if !g.Graded {
			verb = "撤销评分"
		}
	}
	ex.Grades[key] = g
	label := ex.Layout.Subjectives[question-1].Parts[part-1].Label
	ex.log(by, "setGrade", fmt.Sprintf("第 %d 卷 主观题 %d 小问%s:%s %.1f 分", student, question, label, verb, g.Score))
	return s.save(ex)
}

// SetObjectiveKey 修正客观题参考答案并落盘(答案键录错时的补救,自动触发全班重统)。
func (s *Store) SetObjectiveKey(id string, key []string, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if len(key) != len(ex.ObjectiveKey) {
		return fmt.Errorf("参考答案 %d 道,与本卷客观题 %d 道不一致", len(key), len(ex.ObjectiveKey))
	}
	changed := 0
	for i := range key {
		if key[i] != ex.ObjectiveKey[i] {
			changed++
		}
	}
	if changed == 0 {
		return fmt.Errorf("参考答案无变化")
	}
	ex.ObjectiveKey = key
	ex.log(by, "setKey", fmt.Sprintf("修正客观题参考答案(%d 道变更),全班重统", changed))
	return s.save(ex)
}

// SetPublished 设置成绩发布开关(published=false 时学生/家长端不可查)。
func (s *Store) SetPublished(id string, pub bool, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	ex.Published = pub
	now := time.Now()
	if pub {
		ex.PublishedAt = &now
		ex.log(by, "publish", "发布成绩")
	} else {
		ex.PublishedAt = nil
		ex.log(by, "unpublish", "撤回成绩发布")
	}
	return s.save(ex)
}

// log 追加一条审计记录(调用方持锁)。审计只保留最近 500 条。
func (ex *Exam) log(by, action, detail string) {
	if by == "" {
		by = "system"
	}
	ex.Audit = append(ex.Audit, AuditEntry{Time: time.Now(), By: by, Action: action, Detail: detail})
	if len(ex.Audit) > 500 {
		ex.Audit = ex.Audit[len(ex.Audit)-500:]
	}
}

// SetAssignment 人工确认某学生的考号(异常卷自愈)并落盘。by 为操作者账号。
func (s *Store) SetAssignment(id string, student int, reg string, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if ex.Assignments == nil {
		ex.Assignments = map[string]string{}
	}
	if reg == "" {
		delete(ex.Assignments, fmt.Sprintf("s%d", student))
		ex.log(by, "assign", fmt.Sprintf("撤销第 %d 卷考号指派", student))
	} else {
		ex.Assignments[fmt.Sprintf("s%d", student)] = reg
		ex.log(by, "assign", fmt.Sprintf("第 %d 卷考号指派为 %s", student, reg))
	}
	return s.save(ex)
}

// Delete 删除考试及其全部数据。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.exams[id]; !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	delete(s.exams, id)
	return os.RemoveAll(filepath.Join(s.dir, id))
}

// SetTask 分配阅卷任务:主观题 question(1-based)→ 教师账号(teacher 为空表示撤销)。
func (s *Store) SetTask(id string, question int, teacher string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return fmt.Errorf("主观题序号 %d 越界", question)
	}
	if ex.Tasks == nil {
		ex.Tasks = map[string]string{}
	}
	k := fmt.Sprintf("q%d", question)
	if teacher == "" {
		delete(ex.Tasks, k)
	} else {
		ex.Tasks[k] = teacher
	}
	ex.TaskMode = "question"
	return s.save(ex)
}

// SetClassTask 按班分配主观题阅卷:班级 → 教师账号(班主任改本班)。
// teacher 为空表示撤销该班分配。与按题分配共用同一场考试,由 TaskMode 标明主模式。
func (s *Store) SetClassTask(id, class, teacher, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if class == "" {
		return fmt.Errorf("班级不能为空")
	}
	if ex.ClassTasks == nil {
		ex.ClassTasks = map[string]string{}
	}
	verb := "分配"
	if teacher == "" {
		delete(ex.ClassTasks, class)
		verb = "撤销"
	} else {
		ex.ClassTasks[class] = teacher
	}
	ex.TaskMode = "class"
	ex.log(by, "setClassTask", fmt.Sprintf("%s班级 %s 的主观题阅卷任务 → %s", verb, class, teacher))
	return s.save(ex)
}

// SetRooms 保存考场编排方案(生成或整体重排)。
func (s *Store) SetRooms(id string, plan *RoomPlan, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	ex.Rooms = plan
	ex.log(by, "setRooms", fmt.Sprintf("考场编排:%d 个考场,每场 %d 人,%s", len(plan.Rooms), plan.Size, plan.Group))
	return s.save(ex)
}

// MutateRooms 在锁内微调考场方案(座位对调/监考指派),fn 返回操作描述用于审计。
func (s *Store) MutateRooms(id string, by string, fn func(*RoomPlan) (string, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if ex.Rooms == nil {
		return fmt.Errorf("尚未生成考场编排")
	}
	detail, err := fn(ex.Rooms)
	if err != nil {
		return err
	}
	ex.log(by, "mutateRooms", detail)
	return s.save(ex)
}

// PageDir 返回整卷回溯图所在目录。
func (s *Store) PageDir(id string) string {
	return filepath.Join(s.dir, id, "pages")
}

// AppendScans 补传扫描件(批量扫描会话/断点续传的核心):把新建考之后又扫完的一批卷
// 续接到既有会话——结果按现有卷序追加,切片与整卷回溯图落盘,学生号接着排。
// 调用方已逐卷完成 OMR 判读;本方法只做追加与持久化。返回追加后的总卷数。
func (s *Store) AppendScans(id, by string, results []*omr.Result, slices [][]omr.Slice, pageGroups [][]*image.Gray) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	dir := filepath.Join(s.dir, id)
	sliceDir := filepath.Join(dir, "slices")
	if err := os.MkdirAll(sliceDir, 0o755); err != nil {
		return 0, err
	}
	base := len(ex.Results)
	for _, sls := range slices {
		for _, sl := range sls {
			p := filepath.Join(sliceDir, fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
			if err := savePNGFile(p, sl.Image); err != nil {
				return 0, err
			}
		}
	}
	if len(pageGroups) > 0 {
		pageDir := filepath.Join(dir, "pages")
		if err := os.MkdirAll(pageDir, 0o755); err != nil {
			return 0, err
		}
		for i, group := range pageGroups {
			for k, pg := range group {
				if pg == nil {
					continue
				}
				name := fmt.Sprintf("s%02d.jpg", base+i+1)
				if k > 0 {
					name = fmt.Sprintf("s%02d_p%02d.jpg", base+i+1, k+1)
				}
				if err := saveJPEGFile(filepath.Join(pageDir, name), pg); err != nil {
					return 0, err
				}
			}
		}
	}
	ex.Results = append(ex.Results, results...)
	ex.log(by, "appendScans", fmt.Sprintf("补传扫描件 %d 卷,共 %d 卷", len(results), len(ex.Results)))
	if err := s.save(ex); err != nil {
		return 0, err
	}
	return len(ex.Results), nil
}

// AppendManual 手工补录一名学生(漏扫/答题卡作废的补救语义):
// reg 为考号,absent 标记缺考(不计入统计),answers 为手工录入的客观题作答(可缺省)。
// 返回该生的卷号(1-based,主观题评分键沿用 s{卷号}-q{题号})。
func (s *Store) AppendManual(id, reg string, absent bool, answers []string, by string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	if reg == "" {
		return 0, fmt.Errorf("考号不能为空")
	}
	for i := range ex.Results {
		if ex.AssignedReg(i+1) == reg {
			return 0, fmt.Errorf("考号 %s 已有答卷(第 %d 卷),无需补录", reg, i+1)
		}
	}
	if len(answers) > len(ex.ObjectiveKey) {
		return 0, fmt.Errorf("补录作答 %d 道,超出本卷客观题 %d 道", len(answers), len(ex.ObjectiveKey))
	}
	ans := make([]string, len(ex.ObjectiveKey))
	copy(ans, answers)
	ex.Results = append(ex.Results, &omr.Result{RegNumber: reg, RegValid: true, Answers: ans, Absent: absent, Manual: true})
	student := len(ex.Results)
	action, detail := "补录答卷", fmt.Sprintf("手工补录考号 %s(第 %d 卷,客观题 %d 道手输)", reg, student, len(answers))
	if absent {
		action, detail = "补录缺考", fmt.Sprintf("手工登记缺考:考号 %s(第 %d 卷)", reg, student)
	}
	ex.log(by, action, detail)
	return student, s.save(ex)
}

// SetAbsent 标记/取消某卷缺考(答题卡作废、判定缺考的补救语义)。
// 缺考卷保留卷位与考号对齐,但不计入统计、排名与选项分布。
func (s *Store) SetAbsent(id string, student int, absent bool, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	res := ex.Results[student-1]
	if res == nil {
		return fmt.Errorf("学生序号 %d 不可用", student)
	}
	if res.Absent == absent {
		return fmt.Errorf("第 %d 卷缺考标记无变化", student)
	}
	res.Absent = absent
	verb := "标记缺考"
	if !absent {
		verb = "取消缺考"
	}
	ex.log(by, "setAbsent", fmt.Sprintf("第 %d 卷(考号 %s)%s", student, ex.AssignedReg(student), verb))
	return s.save(ex)
}

// Dir 返回考试数据目录(全量打包导出用)。
func (s *Store) Dir(id string) string {
	return filepath.Join(s.dir, id)
}

// SliceDir 返回切片图所在目录。
func (s *Store) SliceDir(id string) string {
	return filepath.Join(s.dir, id, "slices")
}

func (s *Store) save(ex *Exam) error {
	dir := filepath.Join(s.dir, ex.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "exam.json"), b, 0o644)
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// saveJPEGFile 保存整卷回溯图(JPEG,体积约为 PNG 的 1/5)。
func saveJPEGFile(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 88}); err != nil {
		return err
	}
	return f.Close()
}

func savePNGFile(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return f.Close()
}
