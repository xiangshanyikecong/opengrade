package main

// 校本题库 E2E 回归:录入校验(题型/选项字母)、学科/题型/关键词过滤、
// 创建者改删 vs 他人 403、管理员兜底、学生只读 403、重启(data 目录重开)持久化。

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBankQuestionsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	teacher := newClient(t, ts.URL) // 三年二班·数学
	other := newClient(t, ts.URL)   // 三年一班·语文
	student := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123")
	student.login("s0001", "student123")

	var qid string
	t.Run("录入与校验", func(t *testing.T) {
		q := teacher.json("POST", "/api/v1/bank", map[string]any{
			"subject": "数学", "kind": "choice", "options": 4, "score": 2,
			"stem": "已知函数 f(x)=x²,求导", "answer": "a", "knowledge": "导数、函数", "difficulty": "容易",
		}, http.StatusCreated)
		qid = q["id"].(string)
		if q["answer"] != "A" { // 选项字母统一大写
			t.Fatalf("答案应规范化为大写: %v", q["answer"])
		}
		// 非法题型
		teacher.json("POST", "/api/v1/bank", map[string]any{"kind": "essay"}, http.StatusBadRequest)
		// 非法选项字母
		teacher.json("POST", "/api/v1/bank", map[string]any{"kind": "choice", "options": 4, "answer": "E"}, http.StatusBadRequest)
		// 判断题答案必须 T/F
		teacher.json("POST", "/api/v1/bank", map[string]any{"kind": "judge", "answer": "A"}, http.StatusBadRequest)
		// 学生不可写
		student.json("POST", "/api/v1/bank", map[string]any{"kind": "choice", "options": 4, "answer": "A"}, http.StatusForbidden)
	})

	t.Run("列表与过滤", func(t *testing.T) {
		teacher.json("POST", "/api/v1/bank", map[string]any{
			"subject": "数学", "kind": "subjective", "score": 10, "stem": "证明题", "answer": "要点", "knowledge": "立体几何",
		}, http.StatusCreated)
		teacher.json("POST", "/api/v1/bank", map[string]any{
			"subject": "语文", "kind": "fillblank", "score": 5, "stem": "默写", "knowledge": "名句",
		}, http.StatusCreated)
		all := teacher.jsonArr("GET", "/api/v1/bank", http.StatusOK)
		if len(all) != 3 {
			t.Fatalf("题库总数 = %d,期望 3", len(all))
		}
		if got := len(teacher.jsonArr("GET", "/api/v1/bank?kind=choice", http.StatusOK)); got != 1 {
			t.Fatalf("按题型过滤 = %d,期望 1", got)
		}
		if got := len(teacher.jsonArr("GET", "/api/v1/bank?subject=语文", http.StatusOK)); got != 1 {
			t.Fatalf("按学科过滤 = %d,期望 1", got)
		}
		if got := len(teacher.jsonArr("GET", "/api/v1/bank?q=%E5%AF%BC%E6%95%B0", http.StatusOK)); got != 1 { // "导数"
			t.Fatalf("关键词过滤 = %d,期望 1", got)
		}
	})

	t.Run("改删权限", func(t *testing.T) {
		// 创建者可改
		teacher.json("POST", "/api/v1/bank/"+qid, map[string]any{
			"subject": "数学", "kind": "choice", "options": 4, "score": 3, "stem": "改", "answer": "b", "knowledge": "导数",
		}, http.StatusOK)
		// 他人(另一学科教师)不可改/删
		other.login("teacher2", "teach123")
		other.json("POST", "/api/v1/bank/"+qid, map[string]any{"kind": "choice", "options": 4, "answer": "A"}, http.StatusForbidden)
		other.json("DELETE", "/api/v1/bank/"+qid, nil, http.StatusForbidden)
		// 管理员可删
		other2 := teacher.json("GET", "/api/v1/bank/"+qid, nil, http.StatusOK)
		if other2["score"].(float64) != 3 {
			t.Fatalf("更新未生效: %v", other2)
		}
		admin.json("DELETE", "/api/v1/bank/"+qid, nil, http.StatusOK)
		if got := len(teacher.jsonArr("GET", "/api/v1/bank", http.StatusOK)); got != 2 {
			t.Fatalf("删除后题库总数 = %d,期望 2", got)
		}
	})

	t.Run("持久化", func(t *testing.T) {
		srv2, err := newServer(dir, false) // 同一数据目录重开:题库应恢复
		if err != nil {
			t.Fatal(err)
		}
		ts2 := httptest.NewServer(srv2.Handler)
		defer ts2.Close()
		again := newClient(t, ts2.URL)
		again.login("teacher1", "teach123")
		if got := len(again.jsonArr("GET", "/api/v1/bank", http.StatusOK)); got != 2 {
			t.Fatalf("重启后题库总数 = %d,期望 2", got)
		}
	})
}
