// Package auth 提供零依赖的账号与角色权限体系:
// 用户(管理员/年级组长/教师/学生)、PBKDF2-HMAC-SHA256 口令散列、
// 随机令牌会话(Cookie),数据持久化在 data/org.json。
// 权限模型对应方案稿的组织形态:
//
//	admin   学校管理员:用户管理、全部考试与报告
//	leader  年级组长:管辖年级的考试管理、任务分配、全年级报告
//	teacher 教师:建考、阅卷(含分配给自己的任务)、本班报告
//	student 学生:仅本人成绩、错题本与答卷回溯(基础查分永久免费)
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Role 是用户角色。
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleLeader  Role = "leader"
	RoleTeacher Role = "teacher"
	RoleStudent Role = "student"
	RoleParent  Role = "parent" // 家长:仅绑定子女,只读查分
)

// User 是一个账号。
type User struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Role        Role     `json:"role"`
	Salt        string   `json:"salt"` // hex
	Hash        string   `json:"hash"` // hex, PBKDF2-HMAC-SHA256
	Reg         string   `json:"reg,omitempty"`
	Class       string   `json:"class,omitempty"`    // 学生所属班级(教师为任教班级)
	Subjects    []string `json:"subjects,omitempty"` // 教师任教学科(如 数学/语文)
	Grades      []string `json:"grades,omitempty"`   // 年级组长管辖年级
	Children    []string `json:"children,omitempty"` // 家长绑定的子女考号(家长角色)
}

// IsStaff 报告是否为教职员(非学生)。
func (u *User) IsStaff() bool { return u != nil && u.Role != RoleStudent }

// CanManageUsers 报告是否可管理账号(仅管理员)。
func (u *User) CanManageUsers() bool { return u != nil && u.Role == RoleAdmin }

// CanDeleteExam 报告是否可删除考试(管理员/年级组长)。
func (u *User) CanDeleteExam() bool { return u != nil && (u.Role == RoleAdmin || u.Role == RoleLeader) }

// CanAssignTasks 报告是否可分配阅卷任务(管理员/年级组长)。
func (u *User) CanAssignTasks() bool { return u.CanDeleteExam() }

// pbkdf2Key 是 RFC 2898 PBKDF2 的标准库实现(HMAC-SHA256)。
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		dk = prf.Sum(dk)
		T := dk[len(dk)-hashLen:]
		copy(U, T)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(U)
			U = U[:0]
			U = prf.Sum(U)
			for x := range U {
				T[x] ^= U[x]
			}
		}
	}
	return dk[:keyLen]
}

const pbkdf2Iter = 60000 // 自建局域网服务的合理强度;登录约 60ms

// SetPassword 生成新盐值并写入散列。
func (u *User) SetPassword(password string) error {
	if len(password) < 5 {
		return fmt.Errorf("密码长度至少 5 位")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	u.Salt = hex.EncodeToString(salt)
	u.Hash = hex.EncodeToString(pbkdf2Key([]byte(password), salt, pbkdf2Iter, 32))
	return nil
}

// CheckPassword 校验口令。
func (u *User) CheckPassword(password string) bool {
	salt, err := hex.DecodeString(u.Salt)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(u.Hash)
	if err != nil {
		return false
	}
	got := pbkdf2Key([]byte(password), salt, pbkdf2Iter, len(want))
	return hmac.Equal(want, got)
}

type session struct {
	Username string    `json:"username"`
	Expires  time.Time `json:"expires"`
}

// Store 是账号与会话存储(单文件 org.json + 内存)。
type Store struct {
	mu       sync.Mutex
	path     string
	Users    map[string]*User   `json:"users"`
	Sessions map[string]session `json:"-"`
}

type orgFile struct {
	Users map[string]*User `json:"users"`
}

// Load 加载账号库;文件不存在时返回空库。
func Load(dir string) (*Store, error) {
	s := &Store{
		path:     filepath.Join(dir, "org.json"),
		Users:    map[string]*User{},
		Sessions: map[string]session{},
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f orgFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("org.json 解析失败: %w", err)
	}
	if f.Users != nil {
		s.Users = f.Users
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(orgFile{Users: s.Users}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600) // 含口令散列,收紧权限
}

// Bootstrap 确保至少存在一个管理员;首次初始化使用默认口令并返回提示。
func (s *Store) Bootstrap() (first bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.Users {
		if u.Role == RoleAdmin {
			return false, nil
		}
	}
	admin := &User{Username: "admin", DisplayName: "系统管理员", Role: RoleAdmin}
	if err := admin.SetPassword("admin123"); err != nil {
		return false, err
	}
	s.Users["admin"] = admin
	return true, s.saveLocked()
}

// Login 校验口令并创建会话(有效期 7 天)。
func (s *Store) Login(username, password string) (token string, u *User, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok || !u.CheckPassword(password) {
		return "", nil, fmt.Errorf("用户名或密码错误")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = hex.EncodeToString(raw)
	s.Sessions[token] = session{Username: username, Expires: time.Now().Add(7 * 24 * time.Hour)}
	return token, u, s.saveLocked()
}

// SessionUser 由令牌取当前用户(过期会话顺带清理)。
func (s *Store) SessionUser(token string) (*User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.Sessions[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.Expires) {
		delete(s.Sessions, token)
		_ = s.saveLocked()
		return nil, false
	}
	u, ok := s.Users[sess.Username]
	return u, ok
}

// Logout 注销会话。
func (s *Store) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Sessions, token)
	_ = s.saveLocked()
}

// CreateUser 创建账号(管理员)。
func (s *Store) CreateUser(u *User, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.Username == "" {
		return fmt.Errorf("用户名不能为空")
	}
	switch u.Role {
	case RoleAdmin, RoleLeader, RoleTeacher, RoleStudent, RoleParent:
	default:
		return fmt.Errorf("角色不合法: %q", u.Role)
	}
	if _, exists := s.Users[u.Username]; exists {
		return fmt.Errorf("用户名 %s 已存在", u.Username)
	}
	if err := u.SetPassword(password); err != nil {
		return err
	}
	s.Users[u.Username] = u
	return s.saveLocked()
}

// ImportUpsert 批量导入的写入语义(管理员):
//   - 账号不存在 → 创建,密码取行内密码(为空时由调用方传默认密码);
//   - 已存在 → 仅更新行内给出的非空字段(角色/口令仅在显式提供时变更),
//     避免省略列把既有信息抹掉。
//
// 返回是否为新创建。u.Username 为空、角色不合法或口令不足 5 位时返回错误。
func (s *Store) ImportUpsert(u *User, password string) (created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.Username == "" {
		return false, fmt.Errorf("用户名不能为空")
	}
	existing, ok := s.Users[u.Username]
	if !ok {
		switch u.Role {
		case RoleAdmin, RoleLeader, RoleTeacher, RoleStudent, RoleParent:
		default:
			return false, fmt.Errorf("角色不合法: %q", u.Role)
		}
		if password == "" {
			return false, fmt.Errorf("新账号必须提供密码(行内或默认初始密码)")
		}
		if err := u.SetPassword(password); err != nil {
			return false, err
		}
		s.Users[u.Username] = u
		return true, s.saveLocked()
	}
	if password != "" {
		if err := existing.SetPassword(password); err != nil {
			return false, err
		}
	}
	if u.DisplayName != "" {
		existing.DisplayName = u.DisplayName
	}
	if u.Reg != "" {
		existing.Reg = u.Reg
	}
	if u.Class != "" {
		existing.Class = u.Class
	}
	if len(u.Subjects) > 0 {
		existing.Subjects = u.Subjects
	}
	if len(u.Grades) > 0 {
		existing.Grades = u.Grades
	}
	if len(u.Children) > 0 {
		existing.Children = u.Children
	}
	if u.Role != "" && u.Role != existing.Role {
		if u.Role == RoleAdmin || existing.Role == RoleAdmin {
			// 角色升降涉及管理员,复用保留最后一名管理员的保护
			hasOther := false
			for name, other := range s.Users {
				if name != u.Username && other.Role == RoleAdmin {
					hasOther = true
					break
				}
			}
			if !hasOther {
				return false, fmt.Errorf("系统必须保留至少一名管理员")
			}
		}
		existing.Role = u.Role
	}
	return false, s.saveLocked()
}

// UpdateUser 更新显示名/角色/考号/班级/管辖年级(不改口令)。
func (s *Store) UpdateUser(username string, patch *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return fmt.Errorf("用户 %s 不存在", username)
	}
	u.DisplayName = patch.DisplayName
	u.Reg, u.Class = patch.Reg, patch.Class
	if patch.Subjects != nil {
		u.Subjects = patch.Subjects
	}
	if patch.Grades != nil {
		u.Grades = patch.Grades
	}
	if patch.Children != nil {
		u.Children = patch.Children
	}
	if patch.Role != "" {
		if patch.Role != u.Role && username == "admin" && u.Role == RoleAdmin {
			// 保留至少一名管理员
			hasOther := false
			for name, other := range s.Users {
				if name != username && other.Role == RoleAdmin {
					hasOther = true
					break
				}
			}
			if !hasOther {
				return fmt.Errorf("系统必须保留至少一名管理员")
			}
		}
		u.Role = patch.Role
	}
	return s.saveLocked()
}

// SetPassword 重置口令(管理员重置或本人修改)。
func (s *Store) SetPassword(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return fmt.Errorf("用户 %s 不存在", username)
	}
	if err := u.SetPassword(password); err != nil {
		return err
	}
	return s.saveLocked()
}

// DeleteUser 删除账号(不可删除最后一个管理员)。
func (s *Store) DeleteUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return fmt.Errorf("用户 %s 不存在", username)
	}
	if u.Role == RoleAdmin {
		hasOther := false
		for name, other := range s.Users {
			if name != username && other.Role == RoleAdmin {
				hasOther = true
				break
			}
		}
		if !hasOther {
			return fmt.Errorf("系统必须保留至少一名管理员")
		}
	}
	delete(s.Users, username)
	return s.saveLocked()
}

// PublicUser 是对外可见的账号视图(绝不包含口令散列字段)。
type PublicUser struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Role        Role     `json:"role"`
	Reg         string   `json:"reg,omitempty"`
	Class       string   `json:"class,omitempty"`
	Subjects    []string `json:"subjects,omitempty"`
	Grades      []string `json:"grades,omitempty"`
	Children    []string `json:"children,omitempty"`
}

// List 返回全部账号视图(按角色与用户名排序)。
func (s *Store) List() []*PublicUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*PublicUser, 0, len(s.Users))
	for _, u := range s.Users {
		out = append(out, &PublicUser{
			Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
			Reg: u.Reg, Class: u.Class, Subjects: u.Subjects, Grades: u.Grades, Children: u.Children,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i].Role) != rank(out[j].Role) {
			return rank(out[i].Role) < rank(out[j].Role)
		}
		return out[i].Username < out[j].Username
	})
	return out
}

func rank(r Role) int {
	switch r {
	case RoleAdmin:
		return 0
	case RoleLeader:
		return 1
	case RoleTeacher:
		return 2
	case RoleStudent:
		return 3
	default:
		return 4
	}
}
