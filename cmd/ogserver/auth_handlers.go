package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opengrade/opengrade/auth"
)

const sessionCookie = "og_session"

// setSessionCookie 下发/清除会话 Cookie。
func setSessionCookie(w http.ResponseWriter, token string, expire bool) {
	c := &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if expire {
		c.MaxAge = -1
	} else {
		c.MaxAge = 7 * 24 * 3600
	}
	http.SetCookie(w, c)
}

// handleLogin POST /api/v1/auth/login {username, password} → {role, displayName, redirect}
func handleLogin(w http.ResponseWriter, r *http.Request, authStore *auth.Store) {
	var req struct{ Username, Password string }
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	token, u, err := authStore.Login(strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		httpError(w, http.StatusUnauthorized, err)
		return
	}
	setSessionCookie(w, token, false)
	redirect := "/"
	switch u.Role {
	case auth.RoleStudent, auth.RoleParent:
		redirect = "/student.html"
	case auth.RoleAdmin:
		redirect = "/teacher.html"
	default:
		redirect = "/teacher.html"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "displayName": u.DisplayName, "role": u.Role, "redirect": redirect,
	})
}

// handleLogout POST /api/v1/auth/logout
func handleLogout(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		authStore.Logout(c.Value)
	}
	setSessionCookie(w, "", true)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMe GET /api/v1/auth/me → 当前用户(不含散列)
func handleMe(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "displayName": u.DisplayName, "role": u.Role,
		"reg": u.Reg, "class": u.Class, "children": u.Children,
	})
}

// handlePasswordChange POST /api/v1/auth/password {oldPassword, newPassword}(本人修改)
func handlePasswordChange(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var req struct{ OldPassword, NewPassword string }
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	full, ok := authStore.Users[u.Username]
	if !ok || !full.CheckPassword(req.OldPassword) {
		httpError(w, http.StatusForbidden, fmt.Errorf("原密码不正确"))
		return
	}
	if err := authStore.SetPassword(u.Username, req.NewPassword); err != nil {
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

// handleUserUpdate POST /api/v1/users/{username} {displayName, role, reg, class, grades}
func handleUserUpdate(w http.ResponseWriter, r *http.Request, authStore *auth.Store, u *auth.User) {
	var patch auth.User
	if err := decodeJSON(r, &patch); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	patch.Username = r.PathValue("username")
	if err := authStore.UpdateUser(patch.Username, &patch); err != nil {
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
