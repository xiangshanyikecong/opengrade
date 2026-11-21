package main

// Bearer 令牌通道 E2E(鸿蒙等原生客户端):native 登录 → Authorization: Bearer 全角色访问
// → 权限矩阵与 Cookie 一致 → logout/改密吊销 → web 路径(无 native 字段)响应零变化。
// 鉴权中间件双通道语义:Cookie 优先,Bearer 回退,同一份 sessions.json 会话存储。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// bearerClient 无 Cookie 罐:模拟原生客户端,显式携带 Authorization: Bearer 头。
type bearerClient struct {
	t     *testing.T
	base  string
	token string
}

func newBearerClient(t *testing.T, base string) *bearerClient {
	return &bearerClient{t: t, base: base}
}

func (b *bearerClient) do(method, path string, body io.Reader, contentType string) (*http.Response, []byte) {
	b.t.Helper()
	req, err := http.NewRequest(method, b.base+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func (b *bearerClient) code(method, path string, body io.Reader, contentType string) int {
	b.t.Helper()
	resp, data := b.do(method, path, body, contentType)
	if resp.StatusCode >= 500 {
		b.t.Fatalf("%s %s: 服务端内部错误 %d: %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

func (b *bearerClient) json(method, path string, body any, wantCode int) map[string]any {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		p, _ := json.Marshal(body)
		rd = bytes.NewReader(p)
	}
	resp, data := b.do(method, path, rd, "application/json")
	if resp.StatusCode != wantCode {
		b.t.Fatalf("%s %s: got %d want %d\n%s", method, path, resp.StatusCode, wantCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// nativeLogin 以 native:true 登录并记住返回的 token。
func (b *bearerClient) nativeLogin(user, pass string) {
	b.t.Helper()
	resp, data := b.do("POST", "/api/v1/auth/login",
		jsonBody(map[string]any{"username": user, "password": pass, "native": true}), "application/json")
	if resp.StatusCode != http.StatusOK {
		b.t.Fatalf("native login %s: got %d\n%s", user, resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	tok, _ := m["token"].(string)
	if tok == "" {
		b.t.Fatalf("native 登录响应缺少 token 字段: %s", data)
	}
	b.token = tok
	if m["mustChangePassword"] == true {
		b.json("POST", "/api/v1/auth/password",
			map[string]string{"oldPassword": pass, "newPassword": pass}, http.StatusOK)
	}
}

func TestBearerTokenE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// web 路径回归:不带 native 字段,响应不暴露 token。
	webc := newClient(t, ts.URL)
	resp, data := webc.do("POST", "/api/v1/auth/login", jsonBody(map[string]string{"username": "s0001", "password": "student123"}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("web 登录失败: %d\n%s", resp.StatusCode, data)
	}
	var wm map[string]any
	_ = json.Unmarshal(data, &wm)
	if _, has := wm["token"]; has {
		t.Fatalf("web 登录响应不应包含 token 字段(避免暴露给页面脚本): %s", data)
	}

	// native 登录:三种角色各拿一份 token。
	stu := newBearerClient(t, ts.URL)
	stu.nativeLogin("s0001", "student123")
	tea := newBearerClient(t, ts.URL)
	tea.nativeLogin("teacher1", "teach123")
	adm := newBearerClient(t, ts.URL)
	adm.nativeLogin("admin", "admin123")

	// 学生:本人数据可达,越权 403/404 与 Cookie 路径同口径。
	stu.json("GET", "/api/v1/student/exams", nil, http.StatusOK)
	stu.json("GET", "/api/v1/student/trends", nil, http.StatusOK)
	stu.json("GET", "/api/v1/exams", nil, http.StatusForbidden) // 学生不可见教职员端点
	// 教师:教职员端点可达,管理端点 403。
	tea.json("GET", "/api/v1/exams", nil, http.StatusOK)
	tea.json("GET", "/api/v1/my/tasks", nil, http.StatusOK)
	tea.json("GET", "/api/v1/users", nil, http.StatusForbidden)
	// 管理员:全部可达。
	adm.json("GET", "/api/v1/users", nil, http.StatusOK)
	adm.json("GET", "/api/v1/workload", nil, http.StatusOK)

	// 强制改密闸门对 Bearer 同样生效:重置出带标记的账号,Bearer 全端点 403、仅认证端点放行。
	adm.json("POST", "/api/v1/users/s0001/password", map[string]string{"newPassword": "reset123"}, http.StatusOK)
	stu2 := newBearerClient(t, ts.URL)
	stu2.nativeLogin("s0001", "reset123") // nativeLogin 内部已改密解锁(keepToken 保留 Bearer 会话)
	stu2.json("GET", "/api/v1/auth/me", nil, http.StatusOK)

	// 改密保留当前 Bearer 会话:改密后原 token 继续有效(与 web 的 keepToken 语义一致)。
	stu3 := newBearerClient(t, ts.URL)
	resp, data = stu3.do("POST", "/api/v1/auth/login", jsonBody(map[string]any{"username": "s0002", "password": "student123", "native": true}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("s0002 native 登录失败: %d\n%s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	stu3.token, _ = m["token"].(string)
	stu3.json("POST", "/api/v1/auth/password", map[string]string{"oldPassword": "student123", "newPassword": "student123"}, http.StatusOK)
	stu3.json("GET", "/api/v1/auth/me", nil, http.StatusOK)

	// logout 吊销 Bearer 会话:再访问 401。
	stu3.json("POST", "/api/v1/auth/logout", nil, http.StatusOK)
	if got := stu3.code("GET", "/api/v1/auth/me", nil, ""); got != http.StatusUnauthorized {
		t.Fatalf("logout 后 Bearer 应 401, got %d", got)
	}

	// 畸形 Bearer 头按未登录处理。
	bad := newBearerClient(t, ts.URL)
	bad.token = "not-a-token"
	if got := bad.code("GET", "/api/v1/auth/me", nil, ""); got != http.StatusUnauthorized {
		t.Fatalf("无效 Bearer 应 401, got %d", got)
	}

	// Cookie 与 Bearer 同源互操作:Cookie 登录的会话令牌,以 Bearer 方式携带也应有效。
	cookiec := newClient(t, ts.URL)
	resp, data = cookiec.do("POST", "/api/v1/auth/login", jsonBody(map[string]string{"username": "teacher2", "password": "teach123"}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Cookie 登录失败: %d\n%s", resp.StatusCode, data)
	}
	// 从登录响应 Set-Cookie 提取令牌,再以纯 Bearer 客户端复用。
	var tokenFromCookie string
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			tokenFromCookie = ck.Value
		}
	}
	if tokenFromCookie == "" {
		t.Fatalf("Cookie 登录响应未下发 %s", sessionCookie)
	}
	x := newBearerClient(t, ts.URL)
	x.token = tokenFromCookie
	x.json("GET", "/api/v1/auth/me", nil, http.StatusOK)
}
