package main

// 常规流程全链路 E2E(对照智学网式阶段流):
//   建考=建档(不带扫描件/答案)→ 考后批量补扫 → 答案后补重统 → 异常卷裁决闭环 →
//   我的任务聚合 → 待复核队列 → 全部评完进入待发布 → 发布;
//   权限:班主任本班全科、学科组长跨班、年级组长按组织架构年级收敛。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
)

// postScans 构造补传 multipart(仅 images)。
func postScans(c *e2eClient, examID string, scans [][]byte, begin, end int) (int, map[string]any) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\n", multipartBoundary)
	for i := begin; i < end; i++ {
		f := scans[i]
		fmt.Fprintf(&buf, "Content-Disposition: form-data; name=\"images\"; filename=\"scan_%02d.jpg\"\r\nContent-Type: image/jpeg\r\n\r\n", i+1)
		buf.Write(f)
		fmt.Fprintf(&buf, "\r\n--%s\r\n", multipartBoundary)
	}
	resp, data := c.do("POST", "/api/v1/exams/"+examID+"/scans", &buf, "multipart/form-data; boundary="+multipartBoundary)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return resp.StatusCode, m
}

// createDraft 建档:版式+名册,不带扫描件与答案(常规流程"建考=建档")。
func createDraft(t *testing.T, c *e2eClient, fx *fixture, rosterJSON, subject string) string {
	t.Helper()
	var buf bytes.Buffer
	writeExamForm(&buf, fx.layout, nil, rosterJSON) // key=nil:不带答案
	var body bytes.Buffer
	body.Write(buf.Bytes())
	// writeExamForm 以收尾边界结束;补 subject 字段需要重拼,直接用其内部 part 逻辑太绕 —— 手工完整拼一次
	body.Reset()
	part := func(name, content string) {
		fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", multipartBoundary, name, content)
	}
	lb, _ := json.Marshal(fx.layout)
	part("layout", string(lb))
	if rosterJSON != "" {
		part("roster", rosterJSON)
	}
	if subject != "" {
		part("subject", subject)
	}
	fmt.Fprintf(&body, "--%s--\r\n", multipartBoundary)
	resp, data := c.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建档: %d\n%s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m["id"].(string)
}

func TestConventionalFlowE2E(t *testing.T) {
	dir := t.TempDir()
	fx := buildFixture(t)
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	teacher := newClient(t, ts.URL) // teacher1:三年二班·数学
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123")

	rosterJSON := `[` +
		`{"reg":"00000001","name":"李雷","class":"三年二班"},` +
		`{"reg":"00000002","name":"韩梅梅","class":"三年二班"},` +
		`{"reg":"00000003","name":"王小明","class":"三年二班"},` +
		`{"reg":"00000004","name":"赵小红","class":"三年一班"},` +
		`{"reg":"00000005","name":"孙小刚","class":"三年一班"}]`

	// ---------- 1. 建档:不带扫描件/答案 ----------
	var examID string
	t.Run("建档_无扫描件无答案", func(t *testing.T) {
		examID = createDraft(t, teacher, fx, rosterJSON, "数学")
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		st := d["stage"].(map[string]any)
		if st["stage"] != "awaiting_scan" {
			t.Fatalf("建档后阶段 = %v,期望 awaiting_scan", st["stage"])
		}
		if d["students"].(float64) != 0 {
			t.Fatalf("建档后卷数 = %v,期望 0", d["students"])
		}
		if got := len(d["key"].([]any)); got != len(fx.layout.Questions) {
			t.Fatalf("空答案占位数 = %d,期望 %d", got, len(fx.layout.Questions))
		}
		// 列表也带阶段
		list := teacher.jsonArr("GET", "/api/v1/exams", http.StatusOK)
		var found map[string]any
		for _, e := range list {
			if e["id"] == examID {
				found = e
			}
		}
		if found == nil || found["stageLabel"] != "待扫描" {
			t.Fatalf("列表阶段 = %v", found)
		}
	})

	// ---------- 2. 考后分批补扫 ----------
	t.Run("补扫_扫描中_异常待裁决", func(t *testing.T) {
		code, m := postScans(teacher, examID, fx.scans, 0, 3) // 先扫 3 份
		if code != http.StatusOK || m["added"].(float64) != 3 {
			t.Fatalf("第一批补扫: %d %v", code, m)
		}
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		st := d["stage"].(map[string]any)
		if st["stage"] != "scanning" { // 名册 5 > 已扫 3
			t.Fatalf("补扫 3/5 后阶段 = %v(%v),期望 scanning", st["stage"], st["label"])
		}
		// 缺扫名单:详情应能推导(scannedReg 缺考号;学生3漏涂读不出,只计入可读的 1、2 号)
		sr := d["scannedReg"].(map[string]any)
		if len(sr) != 2 {
			t.Fatalf("scannedReg = %v,期望 2 个可读考号", sr)
		}
		code, m = postScans(teacher, examID, fx.scans, 3, 5) // 再扫 2 份(含学生3漏涂考号)
		if code != http.StatusOK || m["added"].(float64) != 2 {
			t.Fatalf("第二批补扫: %d %v", code, m)
		}
		d = teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		st = d["stage"].(map[string]any)
		if st["stage"] != "anomalies" { // 学生3考号漏涂 → 未裁决异常
			t.Fatalf("扫齐后阶段 = %v(%v),期望 anomalies", st["stage"], st["label"])
		}
		if st["warnings"].(float64) < 1 {
			t.Fatalf("未裁决异常数 = %v,期望 ≥1", st["warnings"])
		}
	})

	// ---------- 3. 答案后补:修正参考答案全班重统 ----------
	t.Run("答案后补_重统", func(t *testing.T) {
		teacher.json("POST", "/api/v1/exams/"+examID+"/key", map[string]any{"key": fx.key}, http.StatusOK)
		rep := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		if rep["students"] == nil {
			t.Fatalf("报告缺 students: %v", rep)
		}
	})

	// ---------- 4. 异常裁决闭环:推荐考号 → 采纳 → 阶段推进 ----------
	t.Run("异常裁决_采纳推荐考号", func(t *testing.T) {
		sug := teacher.json("GET", "/api/v1/exams/"+examID+"/suggest/3", nil, http.StatusOK)
		recs, _ := sug["suggestions"].([]any)
		if len(recs) == 0 {
			t.Fatalf("学生3应有名册推荐: %v", sug)
		}
		rec := recs[0].(map[string]any)
		teacher.json("POST", "/api/v1/assign", map[string]any{"examId": examID, "student": 3, "reg": rec["reg"]}, http.StatusOK)
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		for _, w := range d["warnings"].([]any) {
			wm := w.(map[string]any)
			if wm["student"].(float64) == 3 && wm["resolved"] != true {
				t.Fatalf("学生3告警应已裁决: %v", wm)
			}
		}
		st := d["stage"].(map[string]any)
		if st["stage"] != "grading" { // 异常清零 → 进入阅卷
			t.Fatalf("裁决后阶段 = %v(%v),期望 grading", st["stage"], st["label"])
		}
	})

	// ---------- 5. 我的任务:分配 → 聚合 → 评一份进度+1 ----------
	t.Run("我的任务_按题", func(t *testing.T) {
		before := teacher.jsonArr("GET", "/api/v1/my/tasks", http.StatusOK)
		if len(before) == 0 {
			t.Fatal("建卷人兜底任务应存在(未评完)")
		}
		admin.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]any{"question": 1, "teacher": "teacher1"}, http.StatusOK)
		items := teacher.jsonArr("GET", "/api/v1/my/tasks", http.StatusOK)
		var q1 map[string]any
		for _, it := range items {
			if it["kind"] == "question" && it["exam"] == examID && it["question"].(float64) == 1 {
				q1 = it
			}
		}
		if q1 == nil {
			t.Fatalf("按题任务缺失: %v", items)
		}
		if q1["total"].(float64) != 5 || q1["graded"].(float64) != 0 {
			t.Fatalf("任务进度 = %v", q1)
		}
		// 评一份(带 ⚑ 标记,同时造待复核样本)
		teacher.json("POST", "/api/v1/grades", map[string]any{
			"examId": examID, "student": 1, "question": 1, "score": 9, "mark": "flag", "graded": true,
		}, http.StatusOK)
		items = teacher.jsonArr("GET", "/api/v1/my/tasks", http.StatusOK)
		for _, it := range items {
			if it["kind"] == "question" && it["exam"] == examID && it["question"].(float64) == 1 {
				if it["graded"].(float64) != 1 {
					t.Fatalf("评一份后任务进度 = %v,期望 graded=1", it)
				}
			}
		}
		// 待复核队列出口
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		fl := d["flagged"].([]any)
		if len(fl) != 1 {
			t.Fatalf("待复核队列 = %v,期望 1 条", fl)
		}
		if fl[0].(map[string]any)["student"].(float64) != 1 {
			t.Fatalf("待复核队列内容 = %v", fl[0])
		}
	})

	// ---------- 6. 评完 → 待发布 → 发布 ----------
	t.Run("评完_待发布_发布", func(t *testing.T) {
		nSub := len(fx.layout.Subjectives)
		for q := 1; q <= nSub; q++ {
			for s := 1; s <= 5; s++ {
				if q == 1 && s == 1 {
					continue // 已评
				}
				teacher.json("POST", "/api/v1/grades", map[string]any{
					"examId": examID, "student": s, "question": q, "score": 8, "graded": true,
				}, http.StatusOK)
			}
		}
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		st := d["stage"].(map[string]any)
		if st["stage"] != "ready" {
			t.Fatalf("评完后阶段 = %v(%v),期望 ready(未评 %v)", st["stage"], st["label"], st["ungraded"])
		}
		teacher.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)
		d = teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if d["stage"].(map[string]any)["stage"] != "published" {
			t.Fatalf("发布后阶段 = %v", d["stage"])
		}
	})

	// ---------- 7. 缺扫收口:手工补录(名册外场景单测在此不做,补录已有套件) ----------
}

func TestPermissionGroupsE2E(t *testing.T) {
	dir := t.TempDir()
	fx := buildFixture(t)
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	teacher := newClient(t, ts.URL)
	head := newClient(t, ts.URL)    // 班主任(三年二班,任体育 → 与数学考试学科不符)
	lead := newClient(t, ts.URL)    // 学科组长(数学,班级与考试无关)
	leader1 := newClient(t, ts.URL) // 年级组长·三年级
	leader2 := newClient(t, ts.URL) // 年级组长·二年级
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123")
	leader1.login("leader1", "leader123")

	mk := func(username, display, role, password, class string, subjects, headCls, leadSub []string) map[string]any {
		return map[string]any{
			"username": username, "displayName": display, "role": role, "password": password,
			"class": class, "classes": []string{class}, "subjects": subjects,
			"headClasses": headCls, "subjectLead": leadSub,
		}
	}
	admin.json("POST", "/api/v1/users", mk("head1", "陈班主任", "teacher", "teach123", "三年三班", []string{"体育"}, []string{"三年二班"}, nil), http.StatusCreated)
	admin.json("POST", "/api/v1/users", mk("lead1", "数学组长", "teacher", "teach123", "二年一班", []string{"数学"}, nil, []string{"数学"}), http.StatusCreated)
	admin.json("POST", "/api/v1/users", map[string]any{
		"username": "leader2", "displayName": "二年级主任", "role": "leader", "password": "leader123", "grades": []string{"二年级"},
	}, http.StatusCreated)
	head.login("head1", "teach123")
	lead.login("lead1", "teach123")
	leader2.login("leader2", "leader123")

	// 组织架构:三年级含两班;二年级含二年一班
	admin.json("PUT", "/api/v1/school", map[string]any{"grades": []map[string]any{
		{"name": "三年级", "classes": []string{"三年一班", "三年二班"}},
		{"name": "二年级", "classes": []string{"二年一班"}},
	}}, http.StatusOK)

	rosterJSON := `[{"reg":"00000001","name":"李雷","class":"三年二班"},{"reg":"00000002","name":"韩梅梅","class":"三年二班"}]`
	examID := createDraft(t, teacher, fx, rosterJSON, "数学")

	t.Run("班主任_本班全科可见", func(t *testing.T) {
		list := head.jsonArr("GET", "/api/v1/exams", http.StatusOK)
		found := false
		for _, e := range list {
			if e["id"] == examID {
				found = true
			}
		}
		if !found {
			t.Fatal("班主任(三年二班)应看到数学考试,尽管任教是体育")
		}
		head.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	})
	t.Run("学科组长_本学科跨班可见", func(t *testing.T) {
		list := lead.jsonArr("GET", "/api/v1/exams", http.StatusOK)
		found := false
		for _, e := range list {
			if e["id"] == examID {
				found = true
			}
		}
		if !found {
			t.Fatal("数学组长应跨班看到数学考试,尽管班级无交集")
		}
	})
	t.Run("年级组长_按年级收敛", func(t *testing.T) {
		// leader1 管三年级:可见
		leader1.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		// leader2 管二年级:不可见(404 不泄露存在性),列表也没有
		if got := leader2.code("GET", "/api/v1/exams/"+examID, nil, ""); got != http.StatusNotFound {
			t.Fatalf("二年级组长看三年级考试 = %d,期望 404", got)
		}
		list := leader2.jsonArr("GET", "/api/v1/exams", http.StatusOK)
		for _, e := range list {
			if e["id"] == examID {
				t.Fatal("二年级组长列表不应出现三年级考试")
			}
		}
	})
	t.Run("组织校验_班级不可双归属", func(t *testing.T) {
		if got := admin.code("PUT", "/api/v1/school", strings.NewReader(`{"grades":[{"name":"a","classes":["x"]},{"name":"b","classes":["x"]}]}`), "application/json"); got != http.StatusBadRequest {
			t.Fatalf("班级双归属应 400,得到 %d", got)
		}
	})
	t.Run("名册源与组织读取", func(t *testing.T) {
		students := admin.jsonArr("GET", "/api/v1/students", http.StatusOK)
		if len(students) == 0 {
			t.Fatal("学生名册源为空")
		}
		sch := admin.json("GET", "/api/v1/school", nil, http.StatusOK)
		if g := sch["grades"].([]any); len(g) != 2 {
			t.Fatalf("组织年级 = %v", g)
		}
		// 空组织(未配置)返回 [] 而非 null
		dir2 := t.TempDir()
		srv2, err := newServer(dir2, false)
		if err != nil {
			t.Fatal(err)
		}
		ts2 := httptest.NewServer(srv2.Handler)
		defer ts2.Close()
		c2 := newClient(t, ts2.URL)
		c2.login("admin", "admin123")
		sch2 := c2.json("GET", "/api/v1/school", nil, http.StatusOK)
		if _, ok := sch2["grades"].([]any); !ok {
			t.Fatalf("空组织应返回 [],得到 %T", sch2["grades"])
		}
	})
	t.Run("学生不可读组织与任务", func(t *testing.T) {
		s := newClient(t, ts.URL)
		s.login("s0001", "student123")
		if got := s.code("GET", "/api/v1/my/tasks", nil, ""); got != http.StatusForbidden {
			t.Fatalf("学生读任务 = %d,期望 403", got)
		}
	})
}

// card 包引用占位(保持导入与 fixtures 一致)。
var _ = card.DefaultLayout
