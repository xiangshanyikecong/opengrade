package main

// 组织架构(常规产品"组织树"的最小落地):年级 → 班级归属,存 data/school.json。
//   - 年级组长的可见考试按"考试名册班级 → 所属年级"收敛(未配置年级=全看,兼容小校);
//   - 建考"从班级选名册"用学生账号班级做来源,组织树只做归属与分组展示;
//   - 班级宇宙 = 组织树已归档班级 ∪ 学生账号出现过的班级(动态,不强制先维护组织树)。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opengrade/opengrade/auth"
)

// GradeDef 是一个年级及其下辖班级。
type GradeDef struct {
	Name    string   `json:"name"`
	Classes []string `json:"classes"`
}

// SessionDef 是全校场次表的一节(监考时段与冲突检测的时间颗粒):
// Name 如「第1节」,Start/End 为 HH:MM。考试挂场次(Exam.Session)后,
// 同日同场次的不同考试若指派了同一监考教师即构成冲突。
type SessionDef struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// AppealBan 是全校复核黑名单的一条记录:被拉黑考号对所有考试的复核申请一律拒绝。
// 与考试级拉黑(Exam.AppealBlocked)两级并存:本场拉黑粒度细、误伤面小,全局名单管屡教不改者。
type AppealBan struct {
	Reg    string    `json:"reg"`
	Reason string    `json:"reason,omitempty"`
	By     string    `json:"by"`
	Time   time.Time `json:"time"`
}

// School 是组织架构存储(零依赖单文件,模式同 bank.json)。
type School struct {
	mu         sync.Mutex
	path       string
	Grades     []GradeDef   `json:"grades"`
	Sessions   []SessionDef `json:"sessions,omitempty"` // 场次表(可为空 = 不启用时段/冲突检测)
	AppealBans []AppealBan  `json:"appealBans,omitempty"`
}

// LoadSchool 从数据目录加载组织架构;文件不存在视为空(未配置)。
func LoadSchool(dataDir string) (*School, error) {
	s := &School{path: filepath.Join(dataDir, "school.json")}
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, s); err != nil {
			return nil, fmt.Errorf("school.json 解析失败: %w", err)
		}
	}
	return s, nil
}

func (s *School) save() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw, 0o644) // 原子落盘:防断电留下半截组织架构文件
}

// Snapshot 返回年级配置的深拷贝(接口响应用,避免外部改内部切片)。
func (s *School) Snapshot() []GradeDef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GradeDef, len(s.Grades))
	for i, g := range s.Grades {
		out[i] = GradeDef{Name: g.Name, Classes: append([]string(nil), g.Classes...)}
	}
	return out
}

// SessionsSnapshot 返回场次表深拷贝。
func (s *School) SessionsSnapshot() []SessionDef {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionDef, len(s.Sessions))
	copy(out, s.Sessions)
	return out
}

// HasSession 报告场次名是否存在于场次表(考试挂场次时校验用)。
func (s *School) HasSession(name string) bool {
	if s == nil || name == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.Sessions {
		if x.Name == name {
			return true
		}
	}
	return false
}

// AppealBanned 报告考号是否在全校复核黑名单。
func (s *School) AppealBanned(reg string) bool {
	if s == nil || reg == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.AppealBans {
		if b.Reg == reg {
			return true
		}
	}
	return false
}

// AppealBansSnapshot 返回全校复核黑名单深拷贝(接口响应用)。
func (s *School) AppealBansSnapshot() []AppealBan {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AppealBan, len(s.AppealBans))
	copy(out, s.AppealBans)
	return out
}

// SetAppealBan 加入/移出全校复核黑名单(加入可带原因;移出幂等)。
func (s *School) SetAppealBan(reg, reason, by string, banned bool) error {
	if s == nil {
		return fmt.Errorf("组织架构存储未初始化")
	}
	reg = strings.TrimSpace(reg)
	if reg == "" {
		return fmt.Errorf("考号不能为空")
	}
	reason = strings.TrimSpace(reason)
	s.mu.Lock()
	defer s.mu.Unlock()
	if banned {
		if n := len([]rune(reason)); n > 200 {
			return fmt.Errorf("拉黑原因请控制在 200 字以内(当前 %d 字)", n)
		}
		for i := range s.AppealBans {
			if s.AppealBans[i].Reg == reg { // 已在名单:只更新原因与操作人
				s.AppealBans[i].Reason, s.AppealBans[i].By, s.AppealBans[i].Time = reason, by, time.Now()
				return s.save()
			}
		}
		s.AppealBans = append(s.AppealBans, AppealBan{Reg: reg, Reason: reason, By: by, Time: time.Now()})
		return s.save()
	}
	kept := s.AppealBans[:0]
	for _, b := range s.AppealBans {
		if b.Reg != reg {
			kept = append(kept, b)
		}
	}
	s.AppealBans = kept
	return s.save()
}

// validateSessions 校验场次表:名称非空且不重复、HH:MM 可解析且开始早于结束。
func validateSessions(sessions []SessionDef) error {
	seen := map[string]bool{}
	for _, x := range sessions {
		if x.Name == "" {
			return fmt.Errorf("场次名不能为空")
		}
		if seen[x.Name] {
			return fmt.Errorf("场次 %s 重复", x.Name)
		}
		seen[x.Name] = true
		st, err1 := time.Parse("15:04", x.Start)
		en, err2 := time.Parse("15:04", x.End)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("场次 %s 的起止时间须为 HH:MM(如 08:00)", x.Name)
		}
		if !st.Before(en) {
			return fmt.Errorf("场次 %s 的开始时间须早于结束时间", x.Name)
		}
	}
	return nil
}

// SetSessions 整体替换场次表(仅管理员,与年级配置共用 school.json)。
func (s *School) SetSessions(sessions []SessionDef) error {
	if sessions == nil {
		sessions = []SessionDef{}
	}
	if err := validateSessions(sessions); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Sessions = sessions
	return s.save()
}

// SetGrades 整体替换年级配置:校验年级不重名、班级不重复归属。
func (s *School) SetGrades(grades []GradeDef) error {
	seenGrade, seenClass := map[string]bool{}, map[string]string{}
	for _, g := range grades {
		if g.Name == "" {
			return fmt.Errorf("年级名不能为空")
		}
		if seenGrade[g.Name] {
			return fmt.Errorf("年级 %s 重复", g.Name)
		}
		seenGrade[g.Name] = true
		for _, c := range g.Classes {
			if c == "" {
				continue
			}
			if prev, dup := seenClass[c]; dup {
				return fmt.Errorf("班级 %s 同时归属 %s 与 %s", c, prev, g.Name)
			}
			seenClass[c] = g.Name
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Grades = grades
	return s.save()
}

// ClassGrade 返回班级所属年级;未配置返回 ""。
func (s *School) ClassGrade(class string) string {
	if s == nil || class == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.Grades {
		for _, c := range g.Classes {
			if c == class {
				return g.Name
			}
		}
	}
	return ""
}

// GradesOfClasses 返回班级集合涉及的年级(去重排序);全未配置返回空。
func (s *School) GradesOfClasses(classes []string) []string {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, c := range classes {
		if g := s.ClassGrade(c); g != "" {
			seen[g] = true
		}
	}
	out := make([]string, 0, len(seen))
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// ClassesOfGrades 返回管辖年级集合下的全部班级。
func (s *School) ClassesOfGrades(grades []string) []string {
	if s == nil || len(grades) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, g := range grades {
		want[g] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, g := range s.Grades {
		if want[g.Name] {
			out = append(out, g.Classes...)
		}
	}
	return out
}

// handleSchoolGet GET /api/v1/school → {grades:[...], sessions:[...]}(教职员可读,建考/场次下拉用)。
func handleSchoolGet(w http.ResponseWriter, school *School, _ *auth.User) {
	grades := school.Snapshot()
	if grades == nil {
		grades = []GradeDef{} // 空列表返回 [] 而非 null
	}
	sessions := school.SessionsSnapshot()
	if sessions == nil {
		sessions = []SessionDef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"grades": grades, "sessions": sessions})
}

// handleSchoolSessionsGet GET /api/v1/school/sessions → 场次表(教职员可读)。
func handleSchoolSessionsGet(w http.ResponseWriter, school *School, _ *auth.User) {
	sessions := school.SessionsSnapshot()
	if sessions == nil {
		sessions = []SessionDef{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// handleSchoolSessionsSet PUT /api/v1/school/sessions {sessions:[...]}(仅管理员;整体替换)。
func handleSchoolSessionsSet(w http.ResponseWriter, r *http.Request, school *School, _ *auth.User) {
	var req struct {
		Sessions []SessionDef `json:"sessions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := school.SetSessions(req.Sessions); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAppealBansGet GET /api/v1/school/appeal-bans → 全校复核黑名单(教职员可读)。
func handleAppealBansGet(w http.ResponseWriter, school *School, _ *auth.User) {
	bans := school.AppealBansSnapshot()
	if bans == nil {
		bans = []AppealBan{} // 空列表返回 [] 而非 null
	}
	writeJSON(w, http.StatusOK, bans)
}

// handleAppealBansSet PUT /api/v1/school/appeal-bans {"reg":"00000001","blocked":true,"reason":"..."}(仅管理员)。
func handleAppealBansSet(w http.ResponseWriter, r *http.Request, school *School, u *auth.User) {
	var req struct {
		Reg     string `json:"reg"`
		Blocked *bool  `json:"blocked"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Blocked == nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 reg/blocked 字段"))
		return
	}
	if err := school.SetAppealBan(req.Reg, req.Reason, u.Username, *req.Blocked); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSchoolSet PUT /api/v1/school {grades:[...]}(仅管理员;整体替换)。
func handleSchoolSet(w http.ResponseWriter, r *http.Request, school *School, _ *auth.User) {
	var req struct {
		Grades []GradeDef `json:"grades"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Grades == nil {
		req.Grades = []GradeDef{}
	}
	if err := school.SetGrades(req.Grades); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// studentRow 是班级名册源的一行(建考"从班级选名册"用)。
type studentRow struct {
	Reg     string   `json:"reg"`
	Name    string   `json:"name"`
	Classes []string `json:"classes"`
	Grade   string   `json:"grade,omitempty"`
}

// handleStudentsList GET /api/v1/students(教职员):全部学生账号按班级汇总,
// 附班级年级归属;这是"选班级自动带出名册"的数据源,免逐场手打名单。
func handleStudentsList(w http.ResponseWriter, school *School, authStore *auth.Store, _ *auth.User) {
	out := []studentRow{} // 空态给 [],前端可安全 map
	for _, u := range authStore.List() {
		if u.Role != auth.RoleStudent {
			continue
		}
		classes := u.Classes // List() 已归一(u.AllClasses() 的结果)
		if classes == nil {
			classes = []string{}
		}
		grade := ""
		for _, c := range classes {
			if g := school.ClassGrade(c); g != "" {
				grade = g
				break
			}
		}
		out = append(out, studentRow{Reg: u.Reg, Name: u.DisplayName, Classes: classes, Grade: grade})
	}
	writeJSON(w, http.StatusOK, out)
}
