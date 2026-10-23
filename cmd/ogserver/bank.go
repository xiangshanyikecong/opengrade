package main

// 校本题库 bank/(CE 轨道,对齐智学网"题库+知识点"隐形发动机的最小可行版):
// 只存"学校自己录的题"——题干文本、参考答案、知识点标签、难度先验,数据落在本机
// data/bank.json,与"学校自录、数据本地"的定位一致。不内置任何版权题库。
//
// 建考联动:设计器可从题库选题插入版式(自动带出题型/选项/分值/知识点),
// 客观题参考答案随题带出,替代逐题手填。后续个性化推题、变式训练、增值评价
// 都以这份"题 → 知识点/答案"数据为共同底座。

import (
	"crypto/rand"
	"encoding/hex"
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
	"github.com/opengrade/opengrade/card"
)

// BankQuestion 是校本题库的一道题(学校自录)。
type BankQuestion struct {
	ID         string    `json:"id"`
	Subject    string    `json:"subject,omitempty"`    // 学科(过滤用)
	Kind       string    `json:"kind"`                 // choice | judge | multiple | fillblank | subjective
	Options    int       `json:"options,omitempty"`    // 客观题选项数 2~6;判断题恒为 2
	Score      float64   `json:"score,omitempty"`      // 建议分值(插入版式时带出)
	Stem       string    `json:"stem,omitempty"`       // 题干文本(可空:主观题可用截图/教辅编号代替)
	Answer     string    `json:"answer,omitempty"`     // 参考答案:客观题为选项字母(如 "A"/"AC"),主观题为答案要点
	Knowledge  string    `json:"knowledge,omitempty"`  // 知识点标签(多个用 , 、 分隔)
	Difficulty string    `json:"difficulty,omitempty"` // 难度先验:容易 | 中等 | 较难 | 难(可空,统计难度仍以实考得分率准)
	UsedIn     []string  `json:"usedIn,omitempty"`      // 使用史:用过该题的考试 ID(建考自动回记,去重)
	CreatedBy  string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// BankStore 是题库的文件存储(data/bank.json,单文件整读整写)。
type BankStore struct {
	mu    sync.Mutex
	path  string
	items []BankQuestion
}

// LoadBank 从数据目录加载题库;文件不存在时返回空库。
func LoadBank(dataDir string) (*BankStore, error) {
	s := &BankStore{path: filepath.Join(dataDir, "bank.json")}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.items); err != nil {
		return nil, fmt.Errorf("题库文件损坏: %w", err)
	}
	return s, nil
}

func (s *BankStore) save() error {
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// List 返回题库快照(可按学科/题型过滤,题干/答案/知识点模糊搜索),按创建时间倒序。
func (s *BankStore) List(subject, kind, q string) []BankQuestion {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []BankQuestion{}
	needle := strings.ToLower(strings.TrimSpace(q))
	for _, it := range s.items {
		if subject != "" && it.Subject != subject {
			continue
		}
		if kind != "" && it.Kind != kind {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(it.Stem+it.Answer+it.Knowledge), needle) {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Get 返回一道题。
func (s *BankStore) Get(id string) (BankQuestion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return BankQuestion{}, false
}

// Create 新增一道题(校验题型契约,与版式模型一致)。
func (s *BankStore) Create(q BankQuestion, by string) (BankQuestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateBankQuestion(&q); err != nil {
		return BankQuestion{}, err
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return BankQuestion{}, err
	}
	q.ID = hex.EncodeToString(b[:])
	q.CreatedBy = by
	q.CreatedAt = time.Now()
	s.items = append(s.items, q)
	if err := s.save(); err != nil {
		return BankQuestion{}, err
	}
	return q, nil
}

// Update 更新一道题(仅创建者/管理员/年级组长可改,校验在处理器)。
func (s *BankStore) Update(id string, patch BankQuestion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		if err := validateBankQuestion(&patch); err != nil {
			return err
		}
		patch.ID, patch.CreatedBy, patch.CreatedAt = s.items[i].ID, s.items[i].CreatedBy, s.items[i].CreatedAt
		s.items[i] = patch
		return s.save()
	}
	return fmt.Errorf("题目 %s 不存在", id)
}

// Delete 删除一道题(仅创建者/管理员/年级组长可删,校验在处理器)。
// MarkUsed 回记题目被某场考试使用(去重);题不存在时静默忽略。
func (s *BankStore) MarkUsed(id, examID string) error {
	if id == "" || examID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		for _, e := range s.items[i].UsedIn {
			if e == examID {
				return nil
			}
		}
		s.items[i].UsedIn = append(s.items[i].UsedIn, examID)
		return s.save()
	}
	return nil
}

func (s *BankStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("题目 %s 不存在", id)
}

// validateBankQuestion 校验题型契约(客观题答案必须是合法选项字母组合)。
func validateBankQuestion(q *BankQuestion) error {
	switch q.Kind {
	case card.KindChoice, card.KindMultiple:
		if q.Options < 2 || q.Options > 6 {
			return fmt.Errorf("选项数必须在 2~6 之间,当前 %d", q.Options)
		}
		letters := card.OptionLetters(q.Kind, q.Options)
		for _, ch := range strings.ToUpper(q.Answer) {
			ok := false
			for _, l := range letters {
				if string(ch) == l {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("参考答案含非法选项 %q(合法:%s)", string(ch), strings.Join(letters, ""))
			}
		}
		q.Answer = strings.ToUpper(q.Answer)
	case card.KindJudge:
		q.Options = 2
		a := strings.ToUpper(strings.TrimSpace(q.Answer))
		if a != "" && a != "T" && a != "F" {
			return fmt.Errorf("判断题参考答案必须是 T 或 F,当前 %q", q.Answer)
		}
		q.Answer = a
	case card.KindFillBlank, card.KindSubjective:
		q.Options = 0
	default:
		return fmt.Errorf("题型 %q 不合法(choice/judge/multiple/fillblank/subjective)", q.Kind)
	}
	if q.Score < 0 {
		return fmt.Errorf("分值不能为负")
	}
	return nil
}

// —— HTTP 处理器 ——

// handleBankList 题库列表:GET /api/v1/bank?subject=&kind=&q=
func handleBankList(w http.ResponseWriter, r *http.Request, bank *BankStore, _ *auth.User) {
	writeJSON(w, http.StatusOK, bank.List(r.URL.Query().Get("subject"), r.URL.Query().Get("kind"), r.URL.Query().Get("q")))
}

// handleBankGet 单题详情:GET /api/v1/bank/{id}
func handleBankGet(w http.ResponseWriter, r *http.Request, bank *BankStore, _ *auth.User) {
	q, ok := bank.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("题目不存在"))
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// handleBankCreate 新增题目:POST /api/v1/bank(全体教职员)。
func handleBankCreate(w http.ResponseWriter, r *http.Request, bank *BankStore, u *auth.User) {
	var q BankQuestion
	if err := decodeJSON(r, &q); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	created, err := bank.Create(q, u.Username)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// canManageBank 判断当前账号能否改/删某道题:创建者、管理员或年级组长。
func canManageBank(u *auth.User, q BankQuestion) bool {
	return u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || q.CreatedBy == u.Username
}

// handleBankUpdate 更新题目:POST /api/v1/bank/{id}。
func handleBankUpdate(w http.ResponseWriter, r *http.Request, bank *BankStore, u *auth.User) {
	q, ok := bank.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("题目不存在"))
		return
	}
	if !canManageBank(u, q) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅题目创建者、管理员或年级组长可修改"))
		return
	}
	var patch BankQuestion
	if err := decodeJSON(r, &patch); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := bank.Update(q.ID, patch); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleBankDelete 删除题目:DELETE /api/v1/bank/{id}。
func handleBankDelete(w http.ResponseWriter, r *http.Request, bank *BankStore, u *auth.User) {
	q, ok := bank.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("题目不存在"))
		return
	}
	if !canManageBank(u, q) {
		httpError(w, http.StatusForbidden, fmt.Errorf("仅题目创建者、管理员或年级组长可删除"))
		return
	}
	if err := bank.Delete(q.ID); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
