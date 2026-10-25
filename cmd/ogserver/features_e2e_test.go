package main

// 新功能 E2E 回归:知识点体系(标签编辑/班级掌握度/学生个人掌握度)、
// 错题重组练习卡(教师端与学生端)、按班分配阅卷、21 级赋分细分档、
// 服务端 PDF 成绩单。既有主流程回归见 e2e_test.go。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewFeaturesE2E(t *testing.T) {
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
	t301 := newClient(t, ts.URL) // 三年一班·数学(G3 按班分配用)
	student := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123") // 三年二班·数学
	student.login("s0001", "student123")

	// ---------- 建考(带名册,与主回归一致) ----------
	var examID string
	t.Run("G0_建考", func(t *testing.T) {
		// 三年一班的数学教师(按班分配场景需要):admin 新建
		admin.json("POST", "/api/v1/users", map[string]any{
			"username": "t301", "displayName": "班一数学", "role": "teacher",
			"password": "teach123", "class": "三年一班", "subjects": []string{"数学"},
		}, http.StatusCreated)
		rosterJSON := `[` +
			`{"reg":"00000001","name":"李雷","class":"三年二班"},` +
			`{"reg":"00000002","name":"韩梅梅","class":"三年二班"},` +
			`{"reg":"00000003","name":"王小明","class":"三年二班"},` +
			`{"reg":"00000004","name":"赵小红","class":"三年一班"},` +
			`{"reg":"00000005","name":"孙小刚","class":"三年一班"}]`
		var files []multipartFile
		for i, scan := range fx.scans {
			files = append(files, multipartFile{field: "images", name: fmt.Sprintf("scan_%02d.jpg", i+1), ctype: "image/jpeg", data: scan})
		}
		var body bytes.Buffer
		writeExamForm(&body, fx.layout, fx.key, rosterJSON, files...)
		body.Truncate(body.Len() - len(fmt.Sprintf("--%s--\r\n", multipartBoundary)))
		fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"subject\"\r\n\r\n数学\r\n", multipartBoundary)
		fmt.Fprintf(&body, "--%s--\r\n", multipartBoundary)
		resp, data := teacher.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("建考: %d\n%s", resp.StatusCode, data)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		examID = m["id"].(string)
	})

	// ---------- G1. 知识点标签 ----------
	t.Run("G1_知识点写入与越界拒绝", func(t *testing.T) {
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		n := len(d["objective"].([]any)) + len(d["subjective"].([]any))
		tags := map[string]string{"1": "函数", "2": "函数、导数"}
		tags[fmt.Sprintf("%d", n)] = "综合应用"
		teacher.json("POST", "/api/v1/exams/"+examID+"/knowledge", map[string]any{"tags": tags}, http.StatusOK)

		k := teacher.json("GET", "/api/v1/exams/"+examID+"/knowledge", nil, http.StatusOK)
		got := k["tags"].(map[string]any)
		if got["1"] != "函数" {
			t.Fatalf("题1 标签 = %v", got["1"])
		}
		if k["editable"] != true {
			t.Fatal("建卷教师应可编辑")
		}
		// 越界题号必须拒绝
		bad := map[string]any{"tags": map[string]string{"999": "x"}}
		if got := teacher.code("POST", "/api/v1/exams/"+examID+"/knowledge", jsonBody(bad), "application/json"); got != http.StatusBadRequest {
			t.Fatalf("越界题号应 400,得到 %d", got)
		}
		// 学生不可写
		if got := student.code("POST", "/api/v1/exams/"+examID+"/knowledge", jsonBody(bad), "application/json"); got != http.StatusForbidden {
			t.Fatalf("学生写知识点应 403,得到 %d", got)
		}
	})

	t.Run("G2_班级掌握度进学情分析", func(t *testing.T) {
		a := teacher.json("GET", "/api/v1/exams/"+examID+"/analytics", nil, http.StatusOK)
		ks, ok := a["knowledge"].([]any)
		if !ok || len(ks) == 0 {
			t.Fatalf("打标后学情分析应含知识点,得到 %v", a["knowledge"])
		}
		first := ks[0].(map[string]any)
		if first["name"] == "" || first["rate"] == nil {
			t.Fatalf("知识点统计字段缺失: %v", first)
		}
	})

	// ---------- G3. 按班分配主观题 ----------
	t.Run("G3_按班分配与可见范围", func(t *testing.T) {
		t301.login("t301", "teach123")
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if d["classes"] == nil || len(d["classes"].([]any)) != 2 {
			t.Fatalf("名册班级 = %v", d["classes"])
		}
		// 三年一班数学教师(t301)被分到三年一班 → 只看到该班 2 卷;teacher1(三年二班)→ 3 卷
		// 任务分配为管理员/组长职责
		admin.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]any{"class": "三年一班", "teacher": "t301"}, http.StatusOK)
		admin.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]any{"class": "三年二班", "teacher": "teacher1"}, http.StatusOK)

		d2 := t301.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if d2["taskMode"] != "class" {
			t.Fatalf("taskMode = %v,期望 class", d2["taskMode"])
		}
		// 阅卷切片视图(grading=1)下按班过滤:名册已知班级的学生不可跨班可见;
		// 名册未覆盖的卷(学生 3 考号漏涂、无法归类)对任课教师可见。
		visible := func(arr []map[string]any) map[int]bool {
			m := map[int]bool{}
			for _, it := range arr {
				m[int(it["student"].(float64))] = true
			}
			return m
		}
		sl := visible(t301.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=1&grading=1", examID), http.StatusOK))
		if sl[1] || sl[2] {
			t.Fatalf("t301(三年一班)不应看到三年二班的卷 1、2: %v", sl)
		}
		if !sl[4] || !sl[5] {
			t.Fatalf("t301 应看到三年一班的卷 4、5: %v", sl)
		}
		slFull := visible(teacher.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=1&grading=1", examID), http.StatusOK))
		if slFull[4] || slFull[5] {
			t.Fatalf("teacher1(三年二班)不应看到三年一班的卷 4、5: %v", slFull)
		}
		if !slFull[1] || !slFull[2] {
			t.Fatalf("teacher1 应看到三年二班的卷 1、2: %v", slFull)
		}
	})

	// ---------- G4. 错题重组练习卡 ----------
	t.Run("G4_教师端练习卡", func(t *testing.T) {
		p := teacher.json("GET", "/api/v1/exams/"+examID+"/practice?student=1", nil, http.StatusOK)
		if p["objective"].(float64)+p["subjective"].(float64) == 0 {
			t.Fatal("随机作答卷不应全对,练习卡不应为空")
		}
		layout, ok := p["layout"].(map[string]any)
		if !ok || layout["questions"] == nil {
			t.Fatalf("练习卡版式缺失: %v", p)
		}
		qs := p["questions"].([]any)
		if len(qs) == 0 {
			t.Fatal("重练题号列表为空")
		}
	})

	// ---------- G5. 赋分 21 级细分档 ----------
	t.Run("G5_赋分细分档", func(t *testing.T) {
		r := admin.json("POST", "/api/v1/exams/"+examID+"/grade-convert", map[string]any{"fine": true}, http.StatusOK)
		scale := r["scale"].(map[string]any)
		bands := scale["bands"].([]any)
		if len(bands) < 15 {
			t.Fatalf("细分档数 = %d,期望 >= 15", len(bands))
		}
		conv := r["converted"].([]any)
		if len(conv) != 5 {
			t.Fatalf("换算人数 = %d,期望 5", len(conv))
		}
	})

	// ---------- G6. 学生端:个人掌握度 / 练习卡 / PDF ----------
	t.Run("G6_学生端新能力", func(t *testing.T) {
		// 发布门禁收口:裁决考号异常卷(学生 3 漏涂)+ 评完剩余切片
		// (admin 补评:本场已按班分配,teacher1 的阅卷视野被裁到本班,补不全其它班的切片)
		teacher.json("POST", "/api/v1/assign", map[string]any{"examId": examID, "student": 3, "reg": "00000003"}, http.StatusOK)
		gradeAllForPublish(t, admin, examID)
		teacher.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)

		d := student.json("GET", "/api/v1/student/exams/"+examID, nil, http.StatusOK)
		know, ok := d["knowledge"].([]any)
		if !ok || len(know) == 0 {
			t.Fatalf("学生端个人掌握度缺失: %v", d["knowledge"])
		}

		// 练习卡
		p := student.json("GET", "/api/v1/student/exams/"+examID+"/practice", nil, http.StatusOK)
		if p["layout"] == nil {
			t.Fatal("学生练习卡版式缺失")
		}
		// 练习卡必须能出图:学生直接拿服务端渲染的 SVG(前端渲染入口对 student 是 403)
		svgResp, svg := student.do("GET", "/api/v1/student/exams/"+examID+"/practice.svg", nil, "")
		if svgResp.StatusCode != http.StatusOK {
			t.Fatalf("practice.svg: %d\n%s", svgResp.StatusCode, svg)
		}
		if !bytes.Contains(svg, []byte("<svg")) {
			t.Fatalf("practice.svg 不是 SVG: %q", svg[:min(40, len(svg))])
		}
		// 回归:学生不得借道 staff-only 的 /api/v1/cards 渲染任意版式
		if got := student.code("POST", "/api/v1/cards", jsonBody(map[string]any{"layout": p["layout"]}), "application/json"); got != http.StatusForbidden {
			t.Fatalf("学生调 /api/v1/cards 应 403,得到 %d", got)
		}

		// PDF 成绩单:头字节 %PDF
		resp, data := student.do("GET", "/api/v1/student/exams/"+examID+"/report.pdf", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("report.pdf: %d\n%s", resp.StatusCode, data)
		}
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			t.Fatalf("report.pdf 不是 PDF: %q", data[:16])
		}
		if len(data) < 2000 {
			t.Fatalf("report.pdf 过小(%d B),渲染可能为空", len(data))
		}
	})

	// ---------- G7. 按班分配下评分仍按卷号键 ----------
	t.Run("G7_按班分配后评分与统计", func(t *testing.T) {
		// t301 只见三年一班的 2 卷(卷 4、5),给第 31 题(主观题 1)打分
		// 通过切片列表确认其可见学生号,然后逐一评分
		sl := t301.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=1&grading=1", examID), http.StatusOK)
		for _, item := range sl {
			studentNo := int(item["student"].(float64))
			ver, _ := item["ver"].(float64) // 乐观锁:发布门禁补评过的卷携带当前版本改分
			t301.json("POST", "/api/v1/grades", map[string]any{
				"examId": examID, "student": studentNo, "question": 1, "score": 5, "ver": int(ver),
			}, http.StatusOK)
		} // 统计不受影响:第 31 题已阅数应 ≥ 2(仅这 2 卷被改)
		a := admin.json("GET", "/api/v1/exams/"+examID+"/analytics", nil, http.StatusOK)
		qs := a["questions"].([]any)
		q31 := qs[30].(map[string]any)
		if int(q31["graded"].(float64)) < 2 {
			t.Fatalf("主观题 1 已阅 = %v,期望 >= 2", q31["graded"])
		}
	})
}

// jsonBody 把 map 序列化为请求体。
func jsonBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// 确保 strings 引用(测试内部分支可能用到)。
var _ = strings.TrimSpace
