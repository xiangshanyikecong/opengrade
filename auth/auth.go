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
	Username           string   `json:"username"`
	DisplayName        string   `json:"displayName"`
	Role               Role     `json:"role"`
	Salt               string   `json:"salt"` // hex
	Hash               string   `json:"hash"` // hex, PBKDF2-HMAC-SHA256
	Reg                string   `json:"reg,omitempty"`
	Class              string   `json:"class,omitempty"`              // 学生所属班级(教师为任教班级);走班场景为行政班/主班
	Classes            []string `json:"classes,omitempty"`            // 全部班级(走班:学生双归属/教师多班任教);Classes[0]=Class
	Subjects           []string `json:"subjects,omitempty"`           // 教师任教学科(如 数学/语文)
	HeadClasses        []string `json:"headClasses,omitempty"`        // 班主任所带班级(本班全学科可见;常规流程"班主任看本班全科")
	SubjectLead        []string `json:"subjectLead,omitempty"`        // 学科组长牵头学科(本学科跨班可见;常规流程"学科组协作")
	Grades             []string `json:"grades,omitempty"`             // 年级组长管辖年级(配合组织架构 data/school.json 收敛可见考试)
	Children           []string `json:"children,omitempty"`           // 家长绑定的子女考号(家长角色)
	MustChangePassword bool     `json:"mustChangePassword,omitempty"` // 首登强制改密(默认管理员账号/批量导入初始密码置位)
	Iter               int      `json:"iter,omitempty"`               // PBKDF2 迭代次数;0=历史散列(60000)
}

// AllClasses 返回该账号的全部班级;只有 class 的老数据归一为单班。
func (u *User) AllClasses() []string {
	if u == nil {
		return nil
	}
	if len(u.Classes) > 0 {
		return u.Classes
	}
	if u.Class != "" {
		return []string{u.Class}
	}
	return nil
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

const (
	pbkdf2IterLegacy = 60000  // 历史强度:存量散列按此校验,登录成功后自动升级
	pbkdf2Iter       = 210000 // 新散列口径(约 3.5 倍成本):暴力猜解同步变难,登录约 200ms 可接受
)

// SetPassword 生成新盐值并写入散列(记录迭代次数,与旧散列兼容)。
func (u *User) SetPassword(password string) error {
	if len(password) < 5 {
		return fmt.Errorf("密码长度至少 5 位")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	u.Salt = hex.EncodeToString(salt)
	u.Iter = pbkdf2Iter
	u.Hash = hex.EncodeToString(pbkdf2Key([]byte(password), salt, pbkdf2Iter, 32))
	return nil
}

// CheckPassword 校验口令(按散列记录的迭代次数;旧散列无记录按历史 60000 次)。
func (u *User) CheckPassword(password string) bool {
	salt, err := hex.DecodeString(u.Salt)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(u.Hash)
	if err != nil {
		return false
	}
	iter := u.Iter
	if iter <= 0 {
		iter = pbkdf2IterLegacy
	}
	got := pbkdf2Key([]byte(password), salt, iter, len(want))
	return hmac.Equal(want, got)
}

type session struct {
	Username string    `json:"username"`
	Expires  time.Time `json:"expires"`
}

// Store 是账号与会话存储(org.json 账号 + sessions.json 会话)。
// 会话此前只活在内存里(重启全员掉线),现随变更落盘、启动恢复。
type Store struct {
	mu        sync.Mutex
	path      string
	sessPath  string
	Users     map[string]*User   `json:"users"`
	Sessions  map[string]session `json:"-"`
}

// UserPatch 是 UpdateUser 的增量语义:指针/非 nil 字段才生效,nil = 不变更
// (此前整 struct 覆盖,部分提交会把未带的字段静默清空)。
type UserPatch struct {
	DisplayName *string  `json:"displayName,omitempty"`
	Reg         *string  `json:"reg,omitempty"`
	Class       *string  `json:"class,omitempty"`
	Role        Role     `json:"role,omitempty"` // 空串 = 不变更
	Classes     []string `json:"classes,omitempty"`
	Subjects    []string `json:"subjects,omitempty"`
	HeadClasses []string `json:"headClasses,omitempty"`
	SubjectLead []string `json:"subjectLead,omitempty"`
	Grades      []string `json:"grades,omitempty"`
	Children    []string `json:"children,omitempty"`
}

type orgFile struct {
	Users map[string]*User `json:"users"`
}

// Load 加载账号库;文件不存在时返回空库。
func Load(dir string) (*Store, error) {
	s := &Store{
		path:     filepath.Join(dir, "org.json"),
		sessPath: filepath.Join(dir, "sessions.json"),
		Users:    map[string]*User{},
		Sessions: map[string]session{},
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.loadSessions()
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
	return s.loadSessions()
}

// loadSessions 恢复已落盘的会话;文件缺失/损坏按空会话处理(全员重新登录,不阻塞启动)。
func (s *Store) loadSessions() (*Store, error) {
	if b, err := os.ReadFile(s.sessPath); err == nil {
		var sess map[string]session
		if json.Unmarshal(b, &sess) == nil && sess != nil {
			now := time.Now()
			for tok, se := range sess {
				if now.Before(se.Expires) {
					s.Sessions[tok] = se
				}
			}
		}
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
	return writeFileAtomic(s.path, b, 0o600) // 含口令散列,收紧权限
}

// saveSessionsLocked 会话落盘(调用方持锁);失败只影响重启后的会话延续,不影响当前请求。
func (s *Store) saveSessionsLocked() error {
	b, err := json.MarshalIndent(s.Sessions, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.sessPath, b, 0o600)
}

// writeFileAtomic 原子落盘:先写临时文件再 rename 顶替。
// 直接 os.WriteFile 覆盖在断电/崩溃时会留下半截 org.json → 服务拒绝启动。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows 上目标被并发持有(备份拷贝等)时 rename 可能共享冲突:退回直接覆盖
		return os.WriteFile(path, data, perm)
	}
	return nil
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
	admin := &User{Username: "admin", DisplayName: "系统管理员", Role: RoleAdmin, MustChangePassword: true}
	if err := admin.SetPassword("admin123"); err != nil {
		return false, err
	}
	s.Users["admin"] = admin
	return true, s.saveLocked()
}

// Login 校验口令并创建会话(有效期 7 天)。
// 旧强度散列(无 Iter 记录)登录成功后顺手升级到新口径 —— 存量账号无感迁移。
func (s *Store) Login(username, password string) (token string, u *User, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok || !u.CheckPassword(password) {
		return "", nil, fmt.Errorf("用户名或密码错误")
	}
	if u.Iter != pbkdf2Iter {
		if err := u.SetPassword(password); err == nil {
			_ = s.saveLocked()
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = hex.EncodeToString(raw)
	s.Sessions[token] = session{Username: username, Expires: time.Now().Add(7 * 24 * time.Hour)}
	return token, u, s.saveSessionsLocked()
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
	if _, ok := s.Sessions[token]; !ok {
		return
	}
	delete(s.Sessions, token)
	_ = s.saveSessionsLocked()
}

// Lookup 返回账号副本(锁内读):处理器读账号信息一律走这里,
// 不再直接索引 Users map —— 与管理员开号/导入的并发写撞上是进程级致命错误。
func (s *Store) Lookup(username string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return User{}, false
	}
	cp := *u
	return cp, true
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
	// 走班归一:Classes[0] 恒为 Class(行政班/主班)
	if len(u.Classes) == 0 && u.Class != "" {
		u.Classes = []string{u.Class}
	}
	if u.Class == "" && len(u.Classes) > 0 {
		u.Class = u.Classes[0]
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
		u.MustChangePassword = true // 批量开号的初始密码:首登须改(已存在账号的重导不反复置位)
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
	if len(u.Classes) > 0 {
		existing.Classes = u.Classes
		if existing.Class == "" {
			existing.Class = u.Classes[0]
		}
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

// UpdateUser 按增量补丁更新账号(不改口令)。
// UserPatch 指针语义:nil = 不变更 —— 此前整 struct 覆盖,管理端只提交部分字段时
// DisplayName/Reg/Class 等会被静默清空;要清空请显式传空串。
func (s *Store) UpdateUser(username string, patch *UserPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return fmt.Errorf("用户 %s 不存在", username)
	}
	if patch.DisplayName != nil {
		u.DisplayName = *patch.DisplayName
	}
	if patch.Reg != nil {
		u.Reg = *patch.Reg
	}
	if patch.Class != nil {
		u.Class = *patch.Class
	}
	if patch.Classes != nil {
		u.Classes = patch.Classes
		if len(u.Classes) > 0 && u.Class == "" {
			u.Class = u.Classes[0]
		}
	}
	if patch.Subjects != nil {
		u.Subjects = patch.Subjects
	}
	if patch.HeadClasses != nil {
		u.HeadClasses = patch.HeadClasses
	}
	if patch.SubjectLead != nil {
		u.SubjectLead = patch.SubjectLead
	}
	if patch.Grades != nil {
		u.Grades = patch.Grades
	}
	if patch.Children != nil {
		u.Children = patch.Children
	}
	if patch.Role != "" && patch.Role != u.Role {
		if u.Role == RoleAdmin && !s.hasOtherAdminLocked(username) {
			// 通用不变量:降级的是"最后一个管理员"就拒绝 —— 不只保护内置 admin 账号
			// (旧逻辑先降 admin 再降第二个管理员,系统会落到零管理员)
			return fmt.Errorf("系统必须保留至少一名管理员")
		}
		u.Role = patch.Role
	}
	return s.saveLocked()
}

// hasOtherAdminLocked 报告除 exclude 外是否还有其他管理员(调用方持锁)。
func (s *Store) hasOtherAdminLocked(exclude string) bool {
	for name, other := range s.Users {
		if name != exclude && other.Role == RoleAdmin {
			return true
		}
	}
	return false
}

// SetPassword 重置口令(管理员重置):重置出的是临时口令,置强制改密标记,
// 下次使用前须本人改掉(guard 中间件拦截)。
// 同时吊销该账号全部既有会话 —— 口令已泄露/被冒用时的止血出口,不能等 7 天自然过期。
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
	u.MustChangePassword = true
	s.revokeSessionsLocked(username, "")
	return s.saveLocked()
}

// SetPasswordSelf 本人修改口令(需已通过原密码校验):成功后清除强制改密标记,
// 并吊销除当前会话外的其它会话(改密通常意味着口令疑似泄露,异地旧会话一并下线)。
func (s *Store) SetPasswordSelf(username, password, keepToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Users[username]
	if !ok {
		return fmt.Errorf("用户 %s 不存在", username)
	}
	if err := u.SetPassword(password); err != nil {
		return err
	}
	u.MustChangePassword = false
	s.revokeSessionsLocked(username, keepToken)
	return s.saveLocked()
}

// revokeSessionsLocked 吊销某账号全部会话(keepToken 保留,调用方持锁)。
func (s *Store) revokeSessionsLocked(username, keepToken string) {
	changed := false
	for tok, sess := range s.Sessions {
		if sess.Username == username && tok != keepToken {
			delete(s.Sessions, tok)
			changed = true
		}
	}
	if changed {
		_ = s.saveSessionsLocked()
	}
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
	Username           string   `json:"username"`
	DisplayName        string   `json:"displayName"`
	Role               Role     `json:"role"`
	Reg                string   `json:"reg,omitempty"`
	Class              string   `json:"class,omitempty"`
	Classes            []string `json:"classes,omitempty"`
	Subjects           []string `json:"subjects,omitempty"`
	HeadClasses        []string `json:"headClasses,omitempty"`
	SubjectLead        []string `json:"subjectLead,omitempty"`
	Grades             []string `json:"grades,omitempty"`
	Children           []string `json:"children,omitempty"`
	MustChangePassword bool     `json:"mustChangePassword,omitempty"`
}

// List 返回全部账号视图(按角色与用户名排序)。
func (s *Store) List() []*PublicUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*PublicUser, 0, len(s.Users))
	for _, u := range s.Users {
		out = append(out, &PublicUser{
			Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
			Reg: u.Reg, Class: u.Class, Classes: u.AllClasses(), Subjects: u.Subjects,
			HeadClasses: u.HeadClasses, SubjectLead: u.SubjectLead, Grades: u.Grades, Children: u.Children,
			MustChangePassword: u.MustChangePassword,
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
