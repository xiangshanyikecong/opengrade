package main

// 版式 v3 小题切片 + 账号批量导入 的端到端回归。
// 小题切片:一道大题拆多问逐问评分,半阅不进统计,大题总分自动求和;
// 账号导入:CSV 批量 upsert(创建/非空字段更新)、角色别名、失败行报告。

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

func TestSubQuestionGrading(t *testing.T) {
	dir := t.TempDir()
	fx := buildFixture(t)

	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// 版式:DefaultLayout 基础上把第 3 道主观题(解答 10 分)拆为 6+4 两问。
	// 小问拆分不改变几何(高度/位置不变),fixture 扫描件可直接复用。
	layout := fx.layout
	layout.Subjectives[2].Parts = []card.SubjectivePart{{Score: 6}, {Score: 4}}
	rosterJSON := `[` +
		`{"reg":"00000001","name":"李雷","class":"三年二班"},` +
		`{"reg":"00000002","name":"韩梅梅","class":"三年二班"}]`
	var files []multipartFile
	for i := 0; i < 2; i++ {
		files = append(files, multipartFile{field: "images", name: fmt.Sprintf("scan_%02d.jpg", i+1), ctype: "image/jpeg", data: fx.scans[i]})
	}
	var body bytes.Buffer
	writeExamForm(&body, layout, fx.key, rosterJSON, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考: %d\n%s", resp.StatusCode, data)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析建考响应: %v\n%s", err, data)
	}
	examID, _ := m["id"].(string)

	// 整题评分对拆分题应被拒绝
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 3, "score": 9, "graded": true,
	}, http.StatusBadRequest)
	// 未拆分题仍按整题评分,不受影响
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 5, "graded": true,
	}, http.StatusOK)
	// 小问序号越界
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 3, "part": 3, "score": 1, "graded": true,
	}, http.StatusBadRequest)

	// 半阅:学生 1 只给小问 (1)=6 → 大题不计入统计
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 3, "part": 1, "score": 6, "graded": true,
	}, http.StatusOK)
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if p := d["progress"].([]any)[2].(map[string]any); p["graded"].(float64) != 0 {
		t.Fatalf("半阅不应计入进度: %v", p)
	}
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	if rep["students"].([]any)[0].(map[string]any)["subjective"].(float64) != 5 {
		t.Fatalf("半阅学生主观分应只有第 1 题 5 分: %v", rep["students"].([]any)[0])
	}

	// 阅完第 (2) 问 → 大题 6+4=10,进度 +1,审计含小问标注
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 3, "part": 2, "score": 4, "graded": true,
	}, http.StatusOK)
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if p := d["progress"].([]any)[2].(map[string]any); p["graded"].(float64) != 1 {
		t.Fatalf("阅完两问后进度应 =1: %v", p)
	}
	rep = admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	s1 := rep["students"].([]any)[0].(map[string]any)
	if s1["subjective"].(float64) != 15 || s1["flagged"] != false {
		t.Fatalf("学生1 主观 = %v flagged=%v,期望 15/false", s1["subjective"], s1["flagged"])
	}

	// 切片列表应携带逐问状态
	sl := admin.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=3&grading=1", http.StatusOK)
	parts := sl[0]["parts"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["score"].(float64) != 6 || parts[1].(map[string]any)["graded"] != true {
		t.Fatalf("切片列表小问状态异常: %v", parts)
	}

	// 学情分析:小问逐问统计(DefaultLayout:30 客观 + 3 主观,拆分题为第 33 项)
	an := admin.json("GET", "/api/v1/exams/"+examID+"/analytics", nil, http.StatusOK)
	q3 := an["questions"].([]any)[32].(map[string]any)
	q3parts := q3["parts"].([]any)
	if len(q3parts) != 2 ||
		q3parts[0].(map[string]any)["mean"].(float64) != 6 ||
		q3parts[1].(map[string]any)["graded"].(float64) != 1 {
		t.Fatalf("小问统计异常: %v", q3parts)
	}
	if q3["mean"].(float64) != 10 { // 只有学生 1 阅完
		t.Fatalf("大题均分 = %v,期望 10", q3["mean"])
	}

	// 审计留痕含小问标签
	au := admin.json("GET", "/api/v1/exams/"+examID+"/audit", nil, http.StatusOK)
	found := false
	for _, e := range au["entries"].([]any) {
		if strings.Contains(e.(map[string]any)["detail"].(string), "小问(1)") {
			found = true
		}
	}
	if !found {
		t.Fatal("审计应含小问评分明细")
	}
}

func TestUserImport(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	teacher := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123")

	// 教师无权导入
	teacher.json("POST", "/api/v1/users/import", map[string]any{"csv": "x01,某人,学生"}, http.StatusForbidden)

	const csvData = `用户名,姓名,角色,密码,考号,班级,学科,管辖年级,绑定子女
s20250011,李雷,学生,,00000011,三年二班
t03,王老师,教师,,三年三班,数学|物理
p01,家长甲,家长,,,,,,00000011
x01,某人,校长
`
	r := admin.json("POST", "/api/v1/users/import", map[string]any{"csv": csvData, "defaultPassword": "og12345"}, http.StatusOK)
	if r["created"].(float64) != 3 || r["updated"].(float64) != 0 || len(r["failed"].([]any)) != 1 {
		t.Fatalf("导入结果异常: %s", r)
	}
	if got := r["failed"].([]any)[0].(map[string]any)["error"].(string); !strings.Contains(got, "第 5 行") {
		t.Fatalf("失败行应标注行号: %v", got)
	}

	// 英文表头(username 首列)同样应被跳过
	r = admin.json("POST", "/api/v1/users/import", map[string]any{
		"csv": "username,displayName,role,password,class,reg\ns20250012,英语表头,student,,三年二班,00000012", "defaultPassword": "og12345"}, http.StatusOK)
	if r["created"].(float64) != 1 || len(r["failed"].([]any)) != 0 {
		t.Fatalf("英文表头导入异常: %s", r)
	}
	admin.json("DELETE", "/api/v1/users/s20250012", nil, http.StatusOK)

	// 导入账号可用默认密码登录,字段落位
	stu := newClient(t, ts.URL)
	stu.json("POST", "/api/v1/auth/login", map[string]string{"username": "s20250011", "password": "og12345"}, http.StatusOK)
	me := stu.json("GET", "/api/v1/auth/me", nil, http.StatusOK)
	if me["reg"] != "00000011" || me["class"] != "三年二班" {
		t.Fatalf("导入学生字段异常: %v", me)
	}
	t03 := newClient(t, ts.URL)
	t03.json("POST", "/api/v1/auth/login", map[string]string{"username": "t03", "password": "og12345"}, http.StatusOK)
	me = t03.json("GET", "/api/v1/auth/me", nil, http.StatusOK)
	if me["role"] != "teacher" {
		t.Fatalf("角色别名解析失败: %v", me)
	}

	// 重复导入:更新非空字段(李雷换班),密码未给则不动
	r = admin.json("POST", "/api/v1/users/import", map[string]any{
		"csv":             "s20250011,李雷,学生,,00000011,三年三班\n",
		"defaultPassword": "",
	}, http.StatusOK)
	if r["updated"].(float64) != 1 || r["created"].(float64) != 0 {
		t.Fatalf("重复导入应计更新: %v", r)
	}
	me = stu.json("GET", "/api/v1/auth/me", nil, http.StatusOK)
	if me["class"] != "三年三班" {
		t.Fatalf("重复导入未更新班级: %v", me)
	}
	stu2 := newClient(t, ts.URL)
	stu2.json("POST", "/api/v1/auth/login", map[string]string{"username": "s20250011", "password": "og12345"}, http.StatusOK)
}
