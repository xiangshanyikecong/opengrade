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
}

// NewExam 是 Create 的入参包。
type NewExam struct {
	Layout    card.Layout
	Key       []string
	Results   []*omr.Result
	Slices    [][]omr.Slice
	Pages     []*image.Gray // 矫正后的整卷图(答卷回溯)
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
	if len(ne.Pages) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
			return nil, err
		}
		for si, pg := range ne.Pages {
			if pg == nil {
				continue
			}
			p := filepath.Join(dir, "pages", fmt.Sprintf("s%02d.jpg", si+1))
			if err := saveJPEGFile(p, pg); err != nil {
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

// SetGrade 写入一条主观题评分并落盘。
func (s *Store) SetGrade(id string, student, question int, g score.Grade) error {
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
	ex.Grades[score.GradeKey(student, question)] = g
	return s.save(ex)
}

// SetAssignment 人工确认某学生的考号(异常卷自愈)并落盘。
func (s *Store) SetAssignment(id string, student int, reg string) error {
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
	} else {
		ex.Assignments[fmt.Sprintf("s%d", student)] = reg
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
	return s.save(ex)
}

// PageDir 返回整卷回溯图所在目录。
func (s *Store) PageDir(id string) string {
	return filepath.Join(s.dir, id, "pages")
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
