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
	"strings"
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

	// 考试元数据(教务口径:考试日期/学期/类型/同场分组)。
	// ExamDate 为 "2006-01-02";趋势轴、列表排序、年级总表都以它优先(CreatedAt 兜底)。
	ExamDate  string `json:"examDate,omitempty"`
	Term      string `json:"term,omitempty"`      // 学期,如 2025-2026上
	ExamType  string `json:"examType,omitempty"`  // 类型:月考/期中/期末/单元/周测/其他
	ExamGroup string `json:"examGroup,omitempty"` // 同场分组:同一次考试的多科共用一个标签(如 "2025期中"),聚合出年级总分工表
	Session   string `json:"session,omitempty"`   // 场次(全校场次表 school.json.sessions 之一,如 第1节):同日同场次跨考试的监考冲突检测依据

	Anonymous bool              `json:"anonymous,omitempty"` // 匿名阅卷:阅卷工作台不显示考号
	TaskDue   map[string]string `json:"taskDue,omitempty"`   // 任务截止时间:"q{题号}" 或 "{班级}" → "2006-01-02"

	ManualFull float64      `json:"manualFull,omitempty"` // >0 = 手工成绩考试(免版式建考):单科直录分数,满分即此值
	Appeals    []Appeal     `json:"appeals,omitempty"`    // 学生/家长成绩复核申请(发布后)
	Rooms      *RoomPlan    `json:"rooms,omitempty"`      // 考场编排方案(生成后落盘,可微调)
	Audit      []AuditEntry `json:"audit,omitempty"`      // 关键操作留痕(改答案/改分/发布/指派)

	// AB 卷(版式 v3):A 卷即 Layout;B 卷客观题序可打乱,主观题区两版强制一致(建考校验)。
	// 作答入库时按 OrderB 归一为规范题号,统分/导出/趋势与单卷考试完全同口径。
	ShortID       string            `json:"shortId,omitempty"`       // 考试短号(4 位数字):复合码(短号+卷种位+考号)前缀,扫描时核对归属
	LayoutB       *card.Layout      `json:"layoutB,omitempty"`       // B 卷版式(Variant=B);无则非 AB 考试
	KeyB          []string          `json:"keyB,omitempty"`          // B 卷客观题参考答案(已归一为规范题号序)
	OrderB        []int             `json:"orderB,omitempty"`        // B 卷题序对照:卡面第 i 题对应规范题号 OrderB[i-1]
	AssignVariant map[string]string `json:"assignVariant,omitempty"` // 人工指派卷种:"s{卷号}" → "A"/"B"(卷种判不出时裁决)

	// 发布后语义:Dirty = 发布之后又发生过改分/补传/改答案等数据变更。
	// 学生端看到的是实时重算,发布方需要「重新发布」收口(清 Dirty、刷新发布时间)。
	Dirty bool `json:"dirty,omitempty"`
	// Acked 是残卷告警(缺页/页序不可信)的人工接受标记:"s{卷号}" → true。
	// 已接受的卷不再计入「异常待处理」阶段(重扫整份或作废可真正解决)。
	Acked map[string]bool `json:"acked,omitempty"`
}

// Appeal 是一条成绩复核申请:学生/家长在成绩发布后发起,教师裁决(回复/改分)。
type Appeal struct {
	Time       time.Time  `json:"time"`
	By         string     `json:"by"` // 发起人考号
	Reason     string     `json:"reason"`
	Status     string     `json:"status"`          // open | resolved
	Reply      string     `json:"reply,omitempty"` // 教师回复
	ResolvedBy string     `json:"resolvedBy,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
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
	Idx   int    `json:"idx"` // 座位号(场内 1-based)
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
	Layout     card.Layout
	Key        []string
	Results    []*omr.Result
	Slices     [][]omr.Slice
	Pages      []*image.Gray   // 矫正后的整卷图(答卷回溯;单页版式每生一张)
	PageGroups [][]*image.Gray // 多页版式:每生按页序的矫正图组(第 1 页兼容旧文件名)
	Threshold  string
	CreatedBy  string
	Subject    string
	Roster     []roster.Student

	ExamDate   string  // 考试日期 YYYY-MM-DD
	Term       string  // 学期
	ExamType   string  // 类型
	ExamGroup  string  // 同场分组
	Session    string  // 场次(全校场次表之一)
	ManualFull float64 // 手工成绩考试满分(>0 = 手工成绩考试)

	ShortID string       // 考试短号(AB 卷复合码)
	LayoutB *card.Layout // B 卷版式
	KeyB    []string     // B 卷答案(规范序)
	OrderB  []int        // B 卷题序对照
}

// AssignedReg 返回某学生的最终考号:人工指派优先,否则用 OMR 判读值。
func (ex *Exam) AssignedReg(student int) string {
	if ex.Assignments != nil {
		if reg, ok := ex.Assignments[fmt.Sprintf("s%d", student)]; ok {
			return reg
		}
	}
	if student >= 1 && student <= len(ex.Results) {
		if res := ex.Results[student-1]; res != nil {
			return res.RegNumber
		}
	}
	return ""
}

// Store 是考试会话的文件存储(目录 + JSON,零外部依赖;SQLite 迁移在路线图 M1 末)。
type Store struct {
	mu       sync.Mutex
	dir      string
	exams    map[string]*Exam
	shortSeq int     // 考试短号发号器(1000 起,重启后按存量去重续发)
	School   *School // 组织架构(年级↔班级),newServer 装配;nil=未配置(权限判定按全看兜底)
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
	shortID := ne.ShortID // 建考入参已预发(复合码核对要在入库生效)则复用,否则现场生成
	if shortID == "" {
		shortID = s.genShortIDLocked()
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
		ExamDate:     ne.ExamDate,
		Term:         ne.Term,
		ExamType:     ne.ExamType,
		ExamGroup:    ne.ExamGroup,
		Session:      ne.Session,
		ManualFull:   ne.ManualFull,
		ShortID:      shortID,
		LayoutB:      ne.LayoutB,
		KeyB:         ne.KeyB,
		OrderB:       ne.OrderB,
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
	if ne.PageGroups != nil { // 多页版式:按真实页号命名(第 1 页兼容旧文件名)
		for si, group := range ne.PageGroups {
			var pn []int
			if si < len(ne.Results) && ne.Results[si] != nil {
				pn = ne.Results[si].PageNos
			}
			if err := writePageGroups(filepath.Join(dir, "pages"), si+1, group, pn); err != nil {
				return nil, err
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

// ConflictError 报告乐观锁冲突:请求携带的版本号与服务器当前不一致。
// 处理器将其映射为 409,响应携带服务器当前评分,前端提示刷新。
type ConflictError struct {
	Current score.Grade
	Msg     string
}

func (e *ConflictError) Error() string { return e.Msg }

// maxScoreOf 返回评分目标的满分上限:小问取小问满分,整题取大题满分(未拆分缺省 10)。
func (ex *Exam) maxScoreOf(question, part int) float64 {
	spec := ex.Layout.Subjectives[question-1]
	if part > 0 && part <= len(spec.Parts) {
		return spec.Parts[part-1].Score
	}
	return score.SubjectiveMax(spec)
}

// markDirtyLocked 记录「发布后数据已变更」(调用方持锁):补传/改分/改答案/作废/指派都会触发。
// 学生端是实时口径,发布方据此收到「重新发布」信号。
func markDirtyLocked(ex *Exam) {
	if ex.Published {
		ex.Dirty = true
	}
}

// SetGrade 写入一条主观题整题评分并落盘。by 为操作者账号(审计留痕)。
// 拆分了小问的题目(版式 v3)必须走 SetGradePart 逐问评分。
// 乐观锁:目标已有「已评」记录时,请求必须携带与服务器一致的 ver(ver<=0 视为未携带,拒绝)——
// 防止批注/标记等旁路路径以旧分静默覆盖他人刚改的分。首次评分无需 ver。
// 分数越界(负分或超过满分)直接拒绝 —— 评分颗粒度修复的服务端兜底。
// 返回写入后的新版本号(前端回填本地 ver,后续批注/改分才能带上)。
func (s *Store) SetGrade(id string, student, question int, g score.Grade, by string, ver int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return 0, fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return 0, fmt.Errorf("主观题序号 %d 越界(共 %d 道)", question, len(ex.Layout.Subjectives))
	}
	if parts := len(ex.Layout.Subjectives[question-1].Parts); parts > 0 {
		return 0, fmt.Errorf("第 %d 题已拆分为 %d 个小问,请逐小问评分(part 参数)", question, parts)
	}
	max := ex.maxScoreOf(question, 0)
	if g.Score < -0.001 || g.Score > max+0.001 {
		return 0, fmt.Errorf("分数 %.1f 超出范围(0 ~ 满分 %g)", g.Score, max)
	}
	key := score.GradeKey(student, question)
	verb := "评分"
	if old, exists := ex.Grades[key]; exists && old.Graded {
		if ver <= 0 || old.Ver != ver {
			return 0, &ConflictError{Current: old, Msg: fmt.Sprintf("该卷刚被他人改过(当前 %g 分),请刷新后重试", old.Score)}
		}
		if old.Score != g.Score {
			verb = fmt.Sprintf("改分(%.1f → %.1f)", old.Score, g.Score)
		} else if !g.Graded {
			verb = "撤销评分"
		}
		g.Ver = old.Ver + 1
	} else {
		g.Ver = 1
	}
	g.By = by
	ex.Grades[key] = g
	markDirtyLocked(ex)
	ex.log(by, "setGrade", fmt.Sprintf("第 %d 卷 主观题 %d:%s %.1f 分", student, question, verb, g.Score))
	return g.Ver, s.save(ex)
}

// SetGradePart 写入一条小问评分(版式 v3 拆分题):大题总分由统分层自动求和。
// 乐观锁口径与 SetGrade 一致(已评过的小问必须带匹配 ver);分数越界拒绝。
func (s *Store) SetGradePart(id string, student, question, part int, g score.Grade, by string, ver int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return 0, fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return 0, fmt.Errorf("主观题序号 %d 越界(共 %d 道)", question, len(ex.Layout.Subjectives))
	}
	parts := len(ex.Layout.Subjectives[question-1].Parts)
	if parts == 0 {
		return 0, fmt.Errorf("第 %d 题未拆分小问,应整题评分", question)
	}
	if part < 1 || part > parts {
		return 0, fmt.Errorf("小问序号 %d 越界(共 %d 问)", part, parts)
	}
	max := ex.maxScoreOf(question, part)
	if g.Score < -0.001 || g.Score > max+0.001 {
		return 0, fmt.Errorf("分数 %.1f 超出范围(0 ~ 小问满分 %g)", g.Score, max)
	}
	key := score.GradePartKey(student, question, part)
	verb := "评分"
	if old, exists := ex.Grades[key]; exists && old.Graded {
		if ver <= 0 || old.Ver != ver {
			return 0, &ConflictError{Current: old, Msg: fmt.Sprintf("该小问刚被他人改过(当前 %g 分),请刷新后重试", old.Score)}
		}
		if old.Score != g.Score {
			verb = fmt.Sprintf("改分(%.1f → %.1f)", old.Score, g.Score)
		} else if !g.Graded {
			verb = "撤销评分"
		}
		g.Ver = old.Ver + 1
	} else {
		g.Ver = 1
	}
	g.By = by
	ex.Grades[key] = g
	markDirtyLocked(ex)
	label := ex.Layout.Subjectives[question-1].Parts[part-1].Label
	ex.log(by, "setGrade", fmt.Sprintf("第 %d 卷 主观题 %d 小问%s:%s %.1f 分", student, question, label, verb, g.Score))
	return g.Ver, s.save(ex)
}

// SetObjectiveKey 修正客观题参考答案并落盘(答案键录错时的补救,自动触发全班重统)。
// variant 为 "B" 时修正 B 卷答案(建考时留空的“后补”出口;规范题号序)。
func (s *Store) SetObjectiveKey(id string, key []string, by string, variant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	target := &ex.ObjectiveKey
	label := "客观题参考答案"
	if variant == "B" {
		if ex.LayoutB == nil {
			return fmt.Errorf("本场考试未配置 B 卷版式,无 B 卷答案可修正")
		}
		target = &ex.KeyB
		label = "B 卷客观题参考答案"
	}
	if len(key) != len(*target) {
		return fmt.Errorf("参考答案 %d 道,与本卷客观题 %d 道不一致", len(key), len(*target))
	}
	changed := 0
	for i := range key {
		if key[i] != (*target)[i] {
			changed++
		}
	}
	if changed == 0 {
		return fmt.Errorf("参考答案无变化")
	}
	*target = key
	markDirtyLocked(ex)
	ex.log(by, "setKey", fmt.Sprintf("修正%s(%d 道变更),全班重统", label, changed))
	return s.save(ex)
}

// SetPublished 设置成绩发布开关(published=false 时学生/家长端不可查)。
// 发布(含重新发布)会清掉 Dirty 标记并刷新发布时间。
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
		wasDirty := ex.Dirty
		ex.PublishedAt = &now
		ex.Dirty = false
		verb := "发布成绩"
		if wasDirty {
			verb = "重新发布成绩(同步发布后变更)"
		}
		ex.log(by, "publish", verb)
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
// 查重:目标考号已被其它未作废卷占用时拒绝(防止造出同考号双卷导致统计双计);
// 名册已知时,考号必须在名册内(手滑输错考号在这里拦下)。
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
	if reg != "" {
		if len(ex.Roster) > 0 {
			inRoster := false
			for _, st := range ex.Roster {
				if st.Reg == reg {
					inRoster = true
					break
				}
			}
			if !inRoster {
				return fmt.Errorf("考号 %s 不在名册中,请核对(名册共 %d 人)", reg, len(ex.Roster))
			}
		}
		for i := range ex.Results {
			if i+1 == student || ex.Results[i] == nil || ex.Results[i].Voided {
				continue // 作废卷对外语义是"不存在"
			}
			if ex.AssignedReg(i+1) == reg {
				return fmt.Errorf("考号 %s 已被第 %d 卷使用;如需调整请先作废/改派那张卷", reg, i+1)
			}
		}
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
	markDirtyLocked(ex)
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

// pageFileName 整卷回溯图文件名:第 1 页兼容旧名 sNN.jpg;第 pageNo(>1)页用真实页号
// sNN_p{页号}.jpg(页号未知时退回到达位置 pos+1)。按真实页号命名后,缺页残卷的
// 第 2 张图不再冒充第 2 页,卷种重读/跨批合并都能对上版式。
func pageFileName(student, pos, pageNo int) string {
	if pos == 0 {
		return fmt.Sprintf("s%02d.jpg", student)
	}
	p := pageNo
	if p <= 1 {
		p = pos + 1 // 页号未知/单页:按到达位置(旧语义)
	}
	return fmt.Sprintf("s%02d_p%02d.jpg", student, p)
}

// writePageGroups 落盘一组的整卷回溯图;pageNos 与 group 按到达序对齐(可为 nil)。
func writePageGroups(pageDir string, student int, group []*image.Gray, pageNos []int) error {
	for k, pg := range group {
		if pg == nil {
			continue
		}
		var pn int
		if k < len(pageNos) {
			pn = pageNos[k]
		}
		if err := saveJPEGFile(filepath.Join(pageDir, pageFileName(student, k, pn)), pg); err != nil {
			return err
		}
	}
	return nil
}

// AppendScans 补传扫描件(批量扫描会话/断点续传的核心):把新建考之后又扫完的一批卷
// 续接到既有会话——结果按现有卷序追加,切片与整卷回溯图落盘,学生号接着排。
// oldBase 是调用方判读时看到的存量卷数;若期间有并发写入,这里在锁内按实际 base
// 平移切片/页图的卷号,消除"锁外读 base"竞态。写入失败回滚:不留半截数据。
// 返回追加后的总卷数。
func (s *Store) AppendScans(id, by string, oldBase int, results []*omr.Result, slices [][]omr.Slice, pageGroups [][]*image.Gray, pageNos [][]int) (int, error) {
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
	if delta := base - oldBase; delta != 0 {
		for _, sls := range slices {
			for i := range sls {
				sls[i].StudentIndex += delta
			}
		}
	}
	var written []string
	cleanup := func() {
		for _, p := range written {
			_ = os.Remove(p)
		}
	}
	for _, sls := range slices {
		for _, sl := range sls {
			p := filepath.Join(sliceDir, fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
			if err := savePNGFile(p, sl.Image); err != nil {
				cleanup()
				return 0, err
			}
			written = append(written, p)
		}
	}
	if len(pageGroups) > 0 {
		pageDir := filepath.Join(dir, "pages")
		if err := os.MkdirAll(pageDir, 0o755); err != nil {
			cleanup()
			return 0, err
		}
		for i, group := range pageGroups {
			var pn []int
			if i < len(pageNos) {
				pn = pageNos[i]
			}
			for k, pg := range group {
				if pg == nil {
					continue
				}
				var p1 int
				if k < len(pn) {
					p1 = pn[k]
				}
				p := filepath.Join(pageDir, pageFileName(base+i+1, k, p1))
				if err := saveJPEGFile(p, pg); err != nil {
					cleanup()
					return 0, err
				}
				written = append(written, p)
			}
		}
	}
	ex.Results = append(ex.Results, results...)
	markDirtyLocked(ex)
	ex.log(by, "appendScans", fmt.Sprintf("补传扫描件 %d 卷,共 %d 卷", len(results), len(ex.Results)))
	if err := s.save(ex); err != nil {
		ex.Results = ex.Results[:base] // 回滚内存,避免与磁盘分叉
		cleanup()
		return 0, err
	}
	return len(ex.Results), nil
}

// ReplaceResult 跨批残卷合并:用合并后的新卷原地替换第 student 卷(卷号不变,
// 已评分数天然保留迁移),重写其切片与整卷图。res.PageNos 提供真实页号。
func (s *Store) ReplaceResult(id string, student int, res *omr.Result, slices []omr.Slice, pages []*image.Gray, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	dir := filepath.Join(s.dir, id)
	sliceDir := filepath.Join(dir, "slices")
	// 先清旧文件(切片 + 整卷图),再写新文件;任何一步失败把内存留在原状。
	old := ex.Results[student-1]
	pat := fmt.Sprintf("s%02d_", student)
	if entries, err := os.ReadDir(sliceDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), pat) {
				_ = os.Remove(filepath.Join(sliceDir, e.Name()))
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "pages")); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), pat) {
				_ = os.Remove(filepath.Join(dir, "pages", e.Name()))
			}
		}
	}
	for _, sl := range slices {
		p := filepath.Join(sliceDir, fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
		if err := savePNGFile(p, sl.Image); err != nil {
			return err
		}
	}
	if len(pages) > 0 {
		pageDir := filepath.Join(dir, "pages")
		if err := os.MkdirAll(pageDir, 0o755); err != nil {
			return err
		}
		if err := writePageGroups(pageDir, student, pages, res.PageNos); err != nil {
			return err
		}
	}
	ex.Results[student-1] = res
	// 残卷告警已人工接受的标记随替换清理(新卷若仍缺页会重新挂告警)
	if ex.Acked != nil {
		delete(ex.Acked, fmt.Sprintf("s%d", student))
	}
	markDirtyLocked(ex)
	detail := fmt.Sprintf("第 %d 卷(考号 %s)跨批合并残卷:%d 页 → %d 页", student, res.RegNumber, len(old.PageNos), len(res.PageNos))
	ex.log(by, "replaceResult", detail)
	return s.save(ex)
}

// SetSheetAck 残卷告警人工接受/撤销:接受后该卷不再阻塞「异常待处理」阶段
// (操作员已线下核对该生各页;真正修复=整份重扫自动合并,或作废)。
func (s *Store) SetSheetAck(id string, student int, ack bool, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	if ex.Acked == nil {
		ex.Acked = map[string]bool{}
	}
	k := fmt.Sprintf("s%d", student)
	if ack == ex.Acked[k] {
		return fmt.Errorf("接受状态无变化")
	}
	if ack {
		ex.Acked[k] = true
	} else {
		delete(ex.Acked, k)
	}
	verb := "接受残卷(已人工核对页序)"
	if !ack {
		verb = "撤销接受残卷"
	}
	ex.log(by, "ackSheet", fmt.Sprintf("第 %d 卷(考号 %s)%s", student, ex.AssignedReg(student), verb))
	return s.save(ex)
}

// ClearFlagMarks 清除某卷某题的 ⚑ 待复核标记(整题键 + 全部小问键),分数保持不变。
// 版本号自增:打开中的阅卷页再提交会 409,强制其拉取最新状态。
func (s *Store) ClearFlagMarks(id string, student, question int, by string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	cleared := 0
	bump := func(key string) {
		g, exists := ex.Grades[key]
		if !exists || g.Mark != "flag" {
			return
		}
		g.Mark = ""
		g.Ver++
		ex.Grades[key] = g
		cleared++
	}
	bump(score.GradeKey(student, question))
	for pi := 1; pi <= len(ex.Layout.Subjectives[question-1].Parts); pi++ {
		bump(score.GradePartKey(student, question, pi))
	}
	if cleared == 0 {
		return 0, fmt.Errorf("该卷该题没有 ⚑ 标记")
	}
	ex.log(by, "clearFlag", fmt.Sprintf("清除第 %d 卷主观题 %d 的 ⚑ 待复核标记(%d 处)", student, question, cleared))
	return cleared, s.save(ex)
}

// NextShortID 预发一个考试短号(建考时复合码核对要在入库前生效;Create 会复用它)。
func (s *Store) NextShortID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.genShortIDLocked()
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
	// 查重:作废卷对外语义是"不存在"(先作废坏卷再补录是最自然的顺序,不能被死卷挡住)
	for i := range ex.Results {
		res := ex.Results[i]
		if res == nil || res.Voided {
			continue
		}
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
	markDirtyLocked(ex)
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
	markDirtyLocked(ex)
	ex.log(by, "setAbsent", fmt.Sprintf("第 %d 卷(考号 %s)%s", student, ex.AssignedReg(student), verb))
	return s.save(ex)
}

// SetVoid 作废/恢复某张卷(卷生命周期:重扫替换后弃用旧卷、重复卷裁决保留其一)。
// 作废卷不进报告/统计/阅卷队列,卷位与评分键保留 —— 恢复即原样回归。
func (s *Store) SetVoid(id string, student int, voided bool, by string) error {
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
	if res.Voided == voided {
		return fmt.Errorf("第 %d 卷作废状态无变化", student)
	}
	res.Voided = voided
	verb := "作废"
	if !voided {
		verb = "恢复"
	}
	markDirtyLocked(ex)
	ex.log(by, "setVoid", fmt.Sprintf("第 %d 卷(考号 %s)%s", student, ex.AssignedReg(student), verb))
	return s.save(ex)
}

// genShortIDLocked 生成全库唯一的 4 位数字考试短号(AB 卷复合码前缀;调用方须持锁)。
func (s *Store) genShortIDLocked() string {
	if s.shortSeq == 0 {
		s.shortSeq = 1000
	}
	for s.shortSeq < 10000 {
		cand := fmt.Sprintf("%04d", s.shortSeq)
		s.shortSeq++
		taken := false
		for _, ex := range s.exams {
			if ex.ShortID == cand {
				taken = true
				break
			}
		}
		if !taken {
			return cand
		}
	}
	return "" // 9000 场已满:极端情况,由建考层报错(复合码前缀生成失败)
}

// AssignVariant 人工指派卷种(AB 卷):按所指派卷种从已存矫正图重读客观题作答并归一为规范序,
// 清除暂扣标记;主观题区两版一致,切片与评分不受影响。
func (s *Store) AssignVariant(id string, student int, variant, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if ex.LayoutB == nil {
		return fmt.Errorf("本场考试未配置 B 卷版式,不是 AB 卷考试")
	}
	if student < 1 || student > len(ex.Results) {
		return fmt.Errorf("学生序号 %d 越界(共 %d 卷)", student, len(ex.Results))
	}
	res := ex.Results[student-1]
	if res == nil {
		return fmt.Errorf("学生序号 %d 不可用", student)
	}
	base := &ex.Layout
	if variant == "B" {
		base = ex.LayoutB
	}
	book, err := base.BuildBook()
	if err != nil {
		return fmt.Errorf("卷版式编译失败: %w", err)
	}
	pages := s.loadPageGroups(id, student) // 已存矫正图(按真实页号命名;旧数据按位置)
	if len(pages) > 0 {
		opt := omr.Options{Threshold: ex.Threshold}
		var scans []pageScan
		for k, rect := range pages {
			if rect == nil {
				continue
			}
			// 页号对齐:残卷缺页时第 k 张图不一定是第 k 页,按入库时记录的真实页号选版式
			pageNo := k + 1
			if k < len(res.PageNos) && res.PageNos[k] >= 1 && res.PageNos[k] <= len(book) {
				pageNo = res.PageNos[k]
			}
			if pageNo > len(book) {
				continue
			}
			r, _, err := omr.ReadRectified(book[pageNo-1], rect, opt)
			if err != nil {
				return fmt.Errorf("按 %s 卷重读第 %d 页失败: %w", variant, pageNo, err)
			}
			scans = append(scans, pageScan{res: r, dbg: &omr.Debug{Rectified: rect}, sheet: book[pageNo-1], subOffset: subjectiveOffset(book, pageNo-1)})
		}
		if len(scans) == 0 {
			return fmt.Errorf("第 %d 卷没有可重读的矫正图", student)
		}
		merged := mergePageScans(scans)
		answers := merged.Answers
		if variant == "B" {
			answers = reorderAnswers(answers, ex.OrderB)
		}
		res.Answers = answers
		res.Electives = merged.Electives
	}
	res.Variant = variant
	res.PendingVariant = false
	// 清掉"卷种无法识别"暂扣告警(其余告警如重答/空白卡保留)
	kept := res.Warnings[:0]
	for _, w := range res.Warnings {
		if !strings.Contains(w, "卷种无法识别") {
			kept = append(kept, w)
		}
	}
	res.Warnings = kept
	if ex.AssignVariant == nil {
		ex.AssignVariant = map[string]string{}
	}
	ex.AssignVariant[fmt.Sprintf("s%d", student)] = variant
	markDirtyLocked(ex)
	ex.log(by, "assignVariant", fmt.Sprintf("第 %d 卷(考号 %s)指派为 %s 卷并重读客观题", student, ex.AssignedReg(student), variant))
	return s.save(ex)
}

// SetMeta 更新考试元数据(考试日期/学期/类型/同场分组/匿名阅卷)。
// 指针字段 nil = 不变更;空串 = 清空该字段。
func (s *Store) SetMeta(id string, examDate, term, examType, examGroup, session *string, anonymous *bool, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if examDate != nil {
		if *examDate != "" {
			if _, err := time.Parse("2006-01-02", *examDate); err != nil {
				return fmt.Errorf("考试日期须为 YYYY-MM-DD:%q", *examDate)
			}
		}
		ex.ExamDate = *examDate
	}
	if term != nil {
		ex.Term = *term
	}
	if examType != nil {
		ex.ExamType = *examType
	}
	if examGroup != nil {
		ex.ExamGroup = *examGroup
	}
	if session != nil {
		ex.Session = *session
	}
	if anonymous != nil && ex.Anonymous != *anonymous {
		ex.Anonymous = *anonymous
		ex.log(by, "setMeta", fmt.Sprintf("匿名阅卷:%v", *anonymous))
	}
	ex.log(by, "setMeta", fmt.Sprintf("更新考试元数据:日期=%s 学期=%s 类型=%s 分组=%s 场次=%s", ex.ExamDate, ex.Term, ex.ExamType, ex.ExamGroup, ex.Session))
	return s.save(ex)
}

// SetTaskDue 设置任务截止时间:key 为 "q{题号}"(按题)或 "{班级}"(按班);due 空串清除。
func (s *Store) SetTaskDue(id, key, due, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if key == "" {
		return fmt.Errorf("任务 key 不能为空")
	}
	if due != "" {
		if _, err := time.Parse("2006-01-02", due); err != nil {
			return fmt.Errorf("截止日期须为 YYYY-MM-DD:%q", due)
		}
	}
	if ex.TaskDue == nil {
		ex.TaskDue = map[string]string{}
	}
	if due == "" {
		delete(ex.TaskDue, key)
	} else {
		ex.TaskDue[key] = due
	}
	ex.log(by, "setTaskDue", fmt.Sprintf("任务 %s 截止时间 → %s", key, nonEmptyStr(due, "(清除)")))
	return s.save(ex)
}

// AddAppeal 学生/家长发起成绩复核申请(仅已发布考试)。返回申请序号(1-based)。
func (s *Store) AddAppeal(id, reg, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return 0, fmt.Errorf("考试 %s 不存在", id)
	}
	if !ex.Published {
		return 0, fmt.Errorf("成绩尚未发布,无需复核申请")
	}
	if reg == "" {
		return 0, fmt.Errorf("账号未绑定考号")
	}
	if idx := findIndex(ex, reg); idx == 0 {
		return 0, fmt.Errorf("本次考试未找到与考号 %s 关联的答卷", reg)
	}
	if strings.TrimSpace(reason) == "" {
		return 0, fmt.Errorf("请填写复核理由")
	}
	ex.Appeals = append(ex.Appeals, Appeal{Time: time.Now(), By: reg, Reason: strings.TrimSpace(reason), Status: "open"})
	ex.log("student:"+reg, "appeal", fmt.Sprintf("发起成绩复核申请:%s", strings.TrimSpace(reason)))
	return len(ex.Appeals), s.save(ex)
}

// ResolveAppeal 教师裁决复核申请:回复处理结论(改分请走阅卷工作台,留痕更完整)。
func (s *Store) ResolveAppeal(id string, n int, reply, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.exams[id]
	if !ok {
		return fmt.Errorf("考试 %s 不存在", id)
	}
	if n < 1 || n > len(ex.Appeals) {
		return fmt.Errorf("申请序号不合法")
	}
	a := &ex.Appeals[n-1]
	if a.Status == "resolved" {
		return fmt.Errorf("该申请已处理")
	}
	now := time.Now()
	a.Status, a.Reply, a.ResolvedBy, a.ResolvedAt = "resolved", reply, by, &now
	ex.log(by, "resolveAppeal", fmt.Sprintf("裁决考号 %s 的复核申请(第 %d 条):%s", a.By, n, nonEmptyStr(reply, "(无回复)")))
	return s.save(ex)
}

// DupGroups 找出同考号的多份卷(重扫/重复上传后的人工裁决入口):
// 返回每个考号对应的卷号列表(1-based),仅含 ≥2 份的组;作废卷不参与。
func (ex *Exam) DupGroups() [][]int {
	byReg := map[string][]int{}
	var regs []string
	for i := range ex.Results {
		res := ex.Results[i]
		if res == nil || res.Voided {
			continue
		}
		reg := ex.AssignedReg(i + 1)
		if reg == "" {
			continue
		}
		if _, seen := byReg[reg]; !seen {
			regs = append(regs, reg)
		}
		byReg[reg] = append(byReg[reg], i+1)
	}
	var out [][]int
	for _, reg := range regs {
		if len(byReg[reg]) > 1 {
			out = append(out, byReg[reg])
		}
	}
	return out
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
