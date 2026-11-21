package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opengrade/opengrade/auth"
)

const sessionCookie = "og_session"

// cookieSecure 由启动参数 -cookie-secure / OPENGRADE_COOKIE_SECURE 决定(缺省关):
// 部署常态是局域网 HTTP,强置 Secure 会让登录直接失效;HTTPS 部署时应开启。
var cookieSecure bool

// setSessionCookie 下发/清除会话 Cookie。
func setSessionCookie(w http.ResponseWriter, token string, expire bool) {
	c := &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: cookieSecure,
	}
	if expire {
		c.MaxAge = -1
	} else {
		c.MaxAge = 7 * 24 * 3600
	}
	http.SetCookie(w, c)
}

// bearerToken 从 Authorization: Bearer 头解析会话令牌(原生客户端通道,如鸿蒙 App)。
// 原生 HTTP 客户端没有浏览器 Cookie 罐,改用标准 Bearer 头携带同一份会话令牌;
// 令牌仍落在 sessions.json,生命周期/吊销语义与 Cookie 完全一致。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// sessionToken 解析本次请求的会话令牌:Cookie 优先(web 路径零变化),缺省回退 Bearer(原生客户端)。
func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return bearerToken(r)
}

// loginLimiter 登录失败限速(A-6):按用户名滚动窗口计数,连错达阈值即临时锁定。
// 内存态、单实例口径 —— 目的是把在线爆破拖到不可行,不做审计。
var loginLimiter = struct {
	mu     sync.Mutex
	fails  map[string][]time.Time
	locked map[string]time.Time
}{fails: map[string][]time.Time{}, locked: map[string]time.Time{}}

const (
	loginFailWindow = 15 * time.Minute
	loginFailMax    = 5
	loginLockSpan   = 15 * time.Minute
)

func loginLocked(username string) bool {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	until, ok := loginLimiter.locked[username]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(loginLimiter.locked, username)
		return false
	}
	return true
}

func loginFail(username string) {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	now := time.Now()
	fs := loginLimiter.fails[username]
	keep := fs[:0]
	for _, t := range fs {
		if now.Sub(t) < loginFailWindow {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	loginLimiter.fails[username] = keep
	if len(keep) >= loginFailMax {
		loginLimiter.locked[username] = now.Add(loginLockSpan)
		delete(loginLimiter.fails, username)
	}
}

func loginOK(username string) {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	delete(loginLimiter.fails, username)
	delete(loginLimiter.locked, username)
}

// handleLogin POST /api/v1/auth/login {username, password} → {role, displayName, redirect}
// 可选 native:true(鸿蒙等原生客户端):响应附 token 字段,客户端以 Bearer 头携带;
// web 端不传 native 字段,响应不含 token(Cookie 仍 HttpOnly,不暴露给页面脚本)。
func handleLogin(w http.ResponseWriter, r *http.Request, authStore *auth.Store) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Native   bool   `json:"native"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	username := strings.TrimSpace(req.Username)
	if loginLocked(username) {
		httpError(w, http.StatusTooManyRequests, fmt.Errorf("连续失败次数过多,账号已临时锁定(约 %d 分钟),请稍后再试", int(loginLockSpan.Minutes())))
		return
	}
	token, u, err := authStore.Login(username, req.Password)
	if err != nil {
		loginFail(username)
		httpError(w, http.StatusUnauthorized, err)
		return
	}
	loginOK(username)
	setSessionCookie(w, token, false)
	redirect := "/teacher.html"
	if u.Role == auth.RoleStudent || u.Role == auth.RoleParent {
		redirect = "/student.html"
	}
	resp := map[string]any{
		"username": u.Username, "displayName": u.DisplayName, "role": u.Role, "redirect": redirect,
		"mustChangePassword": u.MustChangePassword, // 首登强制改密(前端落地到改密页)
	}
	if req.Native {
		resp["token"] = token
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleLogout POST /api/v1/auth/logout
func handleLogout(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	if tok := sessionToken(r); tok != "" {
		authStore.Logout(tok)
	}
	setSessionCookie(w, "", true)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMe GET /api/v1/auth/me → 当前用户(不含散列)
func handleMe(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "displayName": u.DisplayName, "role": u.Role,
		"reg": u.Reg, "class": u.Class, "classes": u.AllClasses(), "children": u.Children,
		"mustChangePassword": u.MustChangePassword,
	})
}

// handlePasswordChange POST /api/v1/auth/password {oldPassword, newPassword}(本人修改)
func handlePasswordChange(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var req struct{ OldPassword, NewPassword string }
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	full, ok := authStore.Lookup(u.Username) // 锁内副本:不直接索引 Users map(与开号/导入并发写互斥)
	if !ok || !full.CheckPassword(req.OldPassword) {
		httpError(w, http.StatusForbidden, fmt.Errorf("原密码不正确"))
		return
	}
	keepToken := sessionToken(r) // 保留当前会话(Cookie 或 Bearer),其余(疑似泄露的)会话随改密吊销
	if err := authStore.SetPasswordSelf(u.Username, req.NewPassword, keepToken); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUserList GET /api/v1/users(管理员;返回不含口令散列的账号视图)
func handleUserList(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	writeJSON(w, http.StatusOK, authStore.List())
}

// handleUserCreate POST /api/v1/users {username, displayName, role, password, reg, class, grades}
func handleUserCreate(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var req struct {
		auth.User
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	nu := req.User
	if err := authStore.CreateUser(&nu, req.Password); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// handleUserUpdate POST /api/v1/users/{username} {displayName, role, reg, class, grades…}
// 增量语义:未携带的字段不变更(此前部分提交会把 DisplayName/Reg 等静默清空)。
func handleUserUpdate(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var patch auth.UserPatch
	if err := decodeJSON(r, &patch); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := authStore.UpdateUser(r.PathValue("username"), &patch); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUserDelete DELETE /api/v1/users/{username}(管理员;不可删自己与最后一名管理员)
func handleUserDelete(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	name := r.PathValue("username")
	if name == u.Username {
		httpError(w, http.StatusBadRequest, fmt.Errorf("不能删除当前登录账号"))
		return
	}
	if err := authStore.DeleteUser(name); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleUserResetPassword POST /api/v1/users/{username}/password {newPassword}(管理员重置)
func handleUserResetPassword(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var req struct {
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := authStore.SetPassword(r.PathValue("username"), req.NewPassword); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTaskSet 分配阅卷任务:POST /api/v1/exams/{id}/tasks {question, teacher}
// 也可按班分配:{class, teacher}(班主任改本班)。
func handleTaskSet(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		Question int    `json:"question"`
		Teacher  string `json:"teacher"`
		Class    string `json:"class"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Class != "" {
		if err := store.SetClassTask(r.PathValue("id"), req.Class, req.Teacher, u.Username); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := store.SetTask(r.PathValue("id"), req.Question, req.Teacher); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
