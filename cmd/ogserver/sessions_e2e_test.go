package main

// 场次表与监考冲突检测 e2e:
//   - 场次表 CRUD(PUT/GET /api/v1/school/sessions)+ 校验(重名/时间格式/起止顺序);
//   - 考试挂场次(建考表单与 /meta),未知场次 400;
//   - 监考冲突:同日同场次的两场考试指派同一监考 → conflicts 命中;换场次 → 冲突消失。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionsAndProctorConflictsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// —— 场次表:写入 + 校验 ——
	sessions := []map[string]string{
		{"name": "第1节", "start": "08:00", "end": "09:30"},
		{"name": "第2节", "start": "10:00", "end": "11:30"},
	}
	admin.json("PUT", "/api/v1/school/sessions", map[string]any{"sessions": sessions}, http.StatusOK)
	dup := append(append([]map[string]string{}, sessions...), map[string]string{"name": "第1节", "start": "13:00", "end": "14:00"})
	admin.json("PUT", "/api/v1/school/sessions", map[string]any{"sessions": dup}, http.StatusBadRequest) // 重名
	admin.json("PUT", "/api/v1/school/sessions", map[string]any{"sessions": []map[string]string{{"name": "坏时间", "start": "9点", "end": "10:00"}}}, http.StatusBadRequest)
	admin.json("PUT", "/api/v1/school/sessions", map[string]any{"sessions": []map[string]string{{"name": "倒序", "start": "10:00", "end": "09:00"}}}, http.StatusBadRequest)
	sch := admin.json("GET", "/api/v1/school", nil, http.StatusOK)
	if got := sch["sessions"].([]any); len(got) != 2 {
		t.Fatalf("场次表应 2 节:%v", got)
	}

	// —— 两场同日同场次考试(手工科目,免扫描) ——
	mkExam := func(subject string) string {
		var b bytes.Buffer
		writeMultipartForm(&b, map[string]string{
			"manual":    "1",
			"subject":   subject,
			"full":      "100",
			"examDate":  "2026-10-25",
			"examGroup": "S2026期中",
			"session":   "第1节",
			"roster":    `[{"reg":"00000001","name":"李雷","class":"三年二班"},{"reg":"00000002","name":"韩梅梅","class":"三年二班"}]`,
		})
		resp, data := admin.do("POST", "/api/v1/exams", &b, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("建考(%s)失败 %d: %s", subject, resp.StatusCode, data)
		}
		var created map[string]any
		_ = json.Unmarshal(data, &created)
		return created["id"].(string)
	}
	exMath := mkExam("数学")
	exChinese := mkExam("语文")
	// 未知场次校验(meta 路径):防手滑造出永远匹配不上冲突的幽灵时段
	admin.json("POST", "/api/v1/exams/"+exMath+"/meta", map[string]any{"session": "幽灵场次"}, http.StatusBadRequest)

	// —— 生成考场并给两场指派同一监考(teacher1)——
	for _, id := range []string{exMath, exChinese} {
		admin.json("POST", "/api/v1/exams/"+id+"/rooms", map[string]any{"size": 30, "group": "class"}, http.StatusOK)
		admin.json("PATCH", "/api/v1/exams/"+id+"/rooms", map[string]any{"proctor": map[string]any{"room": "R01", "teacher": "teacher1"}}, http.StatusOK)
	}
	rooms := admin.json("GET", "/api/v1/exams/"+exMath+"/rooms", nil, http.StatusOK)
	conflicts, _ := rooms["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("同日同场次同一监考应报 1 处冲突:%v", rooms["conflicts"])
	}
	c := conflicts[0].(map[string]any)
	if c["teacher"] != "teacher1" || c["session"] != "第1节" || c["examId"] != exChinese {
		t.Fatalf("冲突明细异常:%v", c)
	}

	// —— 换场次 → 冲突消失 ——
	admin.json("POST", "/api/v1/exams/"+exChinese+"/meta", map[string]any{"session": "第2节"}, http.StatusOK)
	rooms = admin.json("GET", "/api/v1/exams/"+exMath+"/rooms", nil, http.StatusOK)
	if conflicts, _ = rooms["conflicts"].([]any); len(conflicts) != 0 {
		t.Fatalf("错开场次后不应再有冲突:%v", conflicts)
	}
}
