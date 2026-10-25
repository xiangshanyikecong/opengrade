package main

// 复核改造端到端回归:
//   - 复核申请防刷:重复 open 申请拒绝、理由长度上限;
//   - 两级拉黑:本场拉黑(appeal/block)与全校黑名单(school/appeal-bans),解除后恢复;
//   - 学生个体复核视图 GET /exams/{id}/students/{reg}:逐题明细/主观题 ver/拉黑状态/权限门禁;
//   - 复核页改分走 /grades(ver 乐观锁)并置 Dirty;
//   - 家长多孩:同场考试每个子女一行,?child= 过滤。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppealReworkE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher := newClient(t, ts.URL)
	teacher.login("teacher1", "teach123")
	s1 := newClient(t, ts.URL)
	s1.login("s0001", "student123") // 李雷 00000001
	s2 := newClient(t, ts.URL)
	s2.login("s0002", "student123") // 韩梅梅 00000002

	// —— 建考(名册 5 人 + 扫描件;卷 3 考号漏涂需指派)→ 评完 → 发布 ——
	fx := buildFixture(t)
	rosterJSON := `[{"reg":"00000001","name":"李雷","class":"三年二班"},{"reg":"00000002","name":"韩梅梅","class":"三年二班"},
		{"reg":"00000003","name":"王小明","class":"三年二班"},{"reg":"00000004","name":"赵小红","class":"三年一班"},
		{"reg":"00000005","name":"孙小刚","class":"三年一班"}]`
	var body bytes.Buffer
	files := make([]multipartFile, len(fx.scans))
	for i, sc := range fx.scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("r_%02d.jpg", i+1), ctype: "image/jpeg", data: sc}
	}
	writeExamForm(&body, fx.layout, fx.key, rosterJSON, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID := m["id"].(string)
	admin.json("POST", "/api/v1/assign", map[string]any{"examId": examID, "student": 3, "reg": "00000003"}, http.StatusOK)
	gradeAllForPublish(t, admin, examID)
	admin.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)

	// —— 防刷:重复 open 申请与超长理由 ——
	s1.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "第 3 题我答对了"}, http.StatusCreated)
	s1.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "再来一条"}, http.StatusBadRequest) // 已有 open
	long := strings.Repeat("超", 201)
	resp, data = s1.do("POST", "/api/v1/student/exams/"+examID+"/appeals", jsonBody(map[string]any{"reason": long}), "application/json")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "200") {
		t.Fatalf("超长理由应 400 并提示字数:%d %s", resp.StatusCode, data)
	}

	// —— 本场拉黑:先裁决存量申请,拉黑后新申请被拒,解除后恢复 ——
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/appeals/1/resolve", examID), map[string]any{"reply": "已核对"}, http.StatusOK)
	admin.json("POST", "/api/v1/exams/"+examID+"/appeals/block", map[string]any{"reg": "00000001", "blocked": true, "reason": "恶意刷申请"}, http.StatusOK)
	// 权限:普通教师(非建卷人/组长/管理员)不可拉黑
	teacher.json("POST", "/api/v1/exams/"+examID+"/appeals/block", map[string]any{"reg": "00000002", "blocked": true}, http.StatusForbidden)
	resp, data = s1.do("POST", "/api/v1/student/exams/"+examID+"/appeals", jsonBody(map[string]any{"reason": "还想再申一次"}), "application/json")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "限制") {
		t.Fatalf("本场拉黑后申请应被拒并说明原因:%d %s", resp.StatusCode, data)
	}
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	blocked, _ := d["appealBlocked"].(map[string]any)
	if blocked["00000001"] != "恶意刷申请" {
		t.Fatalf("详情应下发本场拉黑:%v", blocked)
	}
	// 韩梅梅不受李雷被拉黑影响
	s2.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "第 2 题想核对"}, http.StatusCreated)
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/appeals/2/resolve", examID), map[string]any{"reply": "维持原判"}, http.StatusOK)
	admin.json("POST", "/api/v1/exams/"+examID+"/appeals/block", map[string]any{"reg": "00000001", "blocked": false}, http.StatusOK)
	s1.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "重新申请核对第 3 题"}, http.StatusCreated)

	// —— 全校黑名单(仅管理员):拉黑后所有考试拒申,详情标注,解除恢复 ——
	teacher.json("PUT", "/api/v1/school/appeal-bans", map[string]any{"reg": "00000001", "blocked": true}, http.StatusForbidden) // 教师无权
	admin.json("PUT", "/api/v1/school/appeal-bans", map[string]any{"reg": "00000001", "blocked": true, "reason": "屡教不改"}, http.StatusOK)
	bans := teacher.jsonArr("GET", "/api/v1/school/appeal-bans", http.StatusOK) // 教职员可读
	if len(bans) != 1 || bans[0]["reg"] != "00000001" {
		t.Fatalf("全校黑名单列表异常:%v", bans)
	}
	if got := s1.code("GET", "/api/v1/school/appeal-bans", nil, ""); got != http.StatusForbidden {
		t.Fatalf("学生读全校黑名单应 403,得到 %d", got)
	}
	resp, data = s1.do("POST", "/api/v1/student/exams/"+examID+"/appeals", jsonBody(map[string]any{"reason": "再试一次"}), "application/json")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "限制") {
		t.Fatalf("全局拉黑后申请应被拒:%d %s", resp.StatusCode, data)
	}
	// 先裁决第 3 条 open,再解除全局拉黑,恢复申请资格
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/appeals/3/resolve", examID), map[string]any{"reply": "ok"}, http.StatusOK)
	admin.json("PUT", "/api/v1/school/appeal-bans", map[string]any{"reg": "00000001", "blocked": false}, http.StatusOK)
	s1.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "解除后恢复申请"}, http.StatusCreated)

	// —— 学生个体复核视图 ——
	if got := s1.code("GET", "/api/v1/exams/"+examID+"/students/00000001", nil, ""); got != http.StatusForbidden {
		t.Fatalf("学生访问教师复核视图应 403,得到 %d", got)
	}
	d = teacher.json("GET", "/api/v1/exams/"+examID+"/students/00000001", nil, http.StatusOK)
	if d["student"].(float64) != 1 || d["name"] != "李雷" || d["canEdit"] != false {
		t.Fatalf("复核视图基本信息异常:%v", d)
	}
	qs := d["questions"].([]any)
	if len(qs) != len(fx.layout.Questions)+len(fx.layout.Subjectives) {
		t.Fatalf("逐题明细数量 = %d,期望 %d", len(qs), len(fx.layout.Questions)+len(fx.layout.Subjectives))
	}
	subs := d["subjectives"].([]any)
	if len(subs) == 0 || subs[0].(map[string]any)["sliceURL"] == "" {
		t.Fatalf("主观题状态应含切片 URL:%v", subs)
	}
	if d["pages"].(float64) < 1 || d["pageURL"] == "" {
		t.Fatalf("整卷回溯异常:pages=%v pageURL=%v", d["pages"], d["pageURL"])
	}
	if len(d["appeals"].([]any)) != 3 {
		t.Fatalf("该生复核申请应为 3 条(韩梅梅的不计):%v", d["appeals"])
	}
	if got := teacher.code("GET", "/api/v1/exams/"+examID+"/students/99999999", nil, ""); got != http.StatusNotFound {
		t.Fatalf("不存在的考号应 404,得到 %d", got)
	}

	// —— 复核页改分:带 ver 改主观题分 → 落库置 Dirty;视图回读新分 ——
	first := subs[0].(map[string]any)
	ver := first["ver"].(float64)
	max := first["max"].(float64)
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": max, "graded": true, "ver": int(ver),
	}, http.StatusOK)
	d = admin.json("GET", "/api/v1/exams/"+examID+"/students/00000001", nil, http.StatusOK)
	if got := d["subjectives"].([]any)[0].(map[string]any)["score"].(float64); got != max {
		t.Fatalf("改分后复核视图分数 = %v,期望 %v", got, max)
	}
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["stage"].(map[string]any)["dirty"] != true {
		t.Fatalf("发布后改分应置 Dirty:%v", d["stage"])
	}

	// —— 家长多孩:同场每个子女一行;?child= 过滤;非法 child 403 ——
	admin.json("POST", "/api/v1/users", map[string]any{
		"username": "p2", "displayName": "双孩家长", "role": "parent",
		"password": "parent123", "children": []string{"00000001", "00000002"},
	}, http.StatusCreated)
	parent := newClient(t, ts.URL)
	parent.login("p2", "parent123")
	pl := parent.json("GET", "/api/v1/student/exams", nil, http.StatusOK)
	rows := pl["exams"].([]any)
	kids := map[string]bool{}
	for _, row := range rows {
		if row.(map[string]any)["id"] == examID {
			kids[row.(map[string]any)["child"].(string)] = true
		}
	}
	if !kids["00000001"] || !kids["00000002"] {
		t.Fatalf("多孩家长应看到两个子女各一行:%v", rows)
	}
	if got := parent.code("GET", "/api/v1/student/exams?child=99999999", nil, ""); got != http.StatusForbidden {
		t.Fatalf("家长指定非绑定 child 应 403,得到 %d", got)
	}
	pc := parent.json("GET", "/api/v1/student/exams?child=00000002", nil, http.StatusOK)
	for _, row := range pc["exams"].([]any) {
		if row.(map[string]any)["id"] == examID && row.(map[string]any)["child"] != "00000002" {
			t.Fatalf("child 过滤后不应出现其它子女:%v", row)
		}
	}
}
