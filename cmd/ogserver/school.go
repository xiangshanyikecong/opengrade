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
	"sync"

	"github.com/opengrade/opengrade/auth"
)

// GradeDef 是一个年级及其下辖班级。
type GradeDef struct {
	Name    string   `json:"name"`
	Classes []string `json:"classes"`
}

// School 是组织架构存储(零依赖单文件,模式同 bank.json)。
type School struct {
	mu     sync.Mutex
	path   string
	Grades []GradeDef `json:"grades"`
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
	return os.WriteFile(s.path, raw, 0o644)
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

// handleSchoolGet GET /api/v1/school → {grades:[{name,classes[]}]}(教职员可读,建考选名册/筛选用)。
func handleSchoolGet(w http.ResponseWriter, school *School, _ *auth.User) {
	grades := school.Snapshot()
	if grades == nil {
		grades = []GradeDef{} // 空列表返回 [] 而非 null
	}
	writeJSON(w, http.StatusOK, map[string]any{"grades": grades})
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
