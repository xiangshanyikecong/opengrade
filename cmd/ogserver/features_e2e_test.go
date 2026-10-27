package main

// 新功能 E2E 回归:知识点体系(标签编辑/班级掌握度/学生个人掌握度)、
// 错题重组练习卡(教师端与学生端)、按班分配阅卷、21 级赋分细分档、
// 服务端 PDF 成绩单。既有主流程回归见 e2e_test.go。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
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
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("解析建考响应: %v\n%s", err, data)
		}
		examID, _ = m["id"].(string)
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

	t.Run("G8_评分方案", func(t *testing.T) {
		// 默认方案 = 传统精确匹配语义;设置分节规则后卷面满分与逐题满分随规则
		// Correct 同源变化,恢复 null 后回到原状 —— 判分在读取时现场重算。
		g0 := teacher.json("GET", "/api/v1/exams/"+examID+"/scoring", nil, http.StatusOK)
		if g0["editable"] != true {
			t.Fatal("建卷教师应可编辑评分方案")
		}
		rep0 := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		total0 := rep0["total"].(float64)

		scheme := map[string]any{
			"default": map[string]any{},
			"sections": []any{
				map[string]any{"from": 1, "to": 10, "rule": map[string]any{"correct": 2, "incorrect": -1, "multiPartial": "ratio"}},
			},
		}
		if got := student.code("POST", "/api/v1/exams/"+examID+"/scoring", jsonBody(map[string]any{"scoring": scheme}), "application/json"); got != http.StatusForbidden {
			t.Fatalf("学生写评分方案应 403,得到 %d", got)
		}
		teacher.json("POST", "/api/v1/exams/"+examID+"/scoring", map[string]any{"scoring": scheme}, http.StatusOK)

		got := teacher.json("GET", "/api/v1/exams/"+examID+"/scoring", nil, http.StatusOK)["scoring"].(map[string]any)
		if len(got["sections"].([]any)) != 1 {
			t.Fatalf("方案未落库: %v", got)
		}
		rep1 := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		var wantDelta float64
		for _, q := range rep0["perQuestion"].([]any) {
			qm := q.(map[string]any)
			if idx := int(qm["index"].(float64)); idx <= 10 {
				wantDelta += 2 - qm["max"].(float64) // 分节内满分统一覆盖为 2
			}
		}
		if d := rep1["total"].(float64) - total0; d != wantDelta {
			t.Fatalf("卷面满分变化 = %v, 期望 %v(分节 Correct 覆盖应与逐题满分同源)", d, wantDelta)
		}
		q1 := rep1["perQuestion"].([]any)[0].(map[string]any)
		if q1["max"].(float64) != 2 {
			t.Fatalf("分节内逐题满分 = %v, 期望 2", q1["max"])
		}
		if q1["scoreRate"] == nil || q1["difficulty"].(float64) != q1["scoreRate"].(float64) {
			t.Fatalf("难度系数应等于得分率: %v", q1)
		}
		// 非法方案拒绝
		bad := map[string]any{"default": map[string]any{"multiPartial": "bogus"}}
		if got := teacher.code("POST", "/api/v1/exams/"+examID+"/scoring", jsonBody(map[string]any{"scoring": bad}), "application/json"); got != http.StatusBadRequest {
			t.Fatalf("非法 multiPartial 应 400,得到 %d", got)
		}
		// 恢复传统语义
		teacher.json("POST", "/api/v1/exams/"+examID+"/scoring", map[string]any{"scoring": nil}, http.StatusOK)
		rep2 := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		if rep2["total"].(float64) != total0 {
			t.Fatalf("恢复后卷面满分应还原: %v != %v", rep2["total"], total0)
		}
	})

	t.Run("G9_答案卡与判读叠加图", func(t *testing.T) {
		// 教师答案卡(考号全 9)随批扫入:不进学生卷流,自动录入/核对参考答案;
		// 判读叠加图:异常卷必存、正常卷按 1/20 抽样,复核接口可取。
		sheet, err := fx.layout.Build()
		if err != nil {
			t.Fatal(err)
		}
		renderCard := func(reg string, answers []string) []byte {
			f := card.Filling{Answers: answers}
			for _, ch := range reg {
				f.Reg = append(f.Reg, int(ch-'0'))
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, card.RenderPNG(sheet, &f)); err != nil {
				t.Fatal(err)
			}
			return buf.Bytes()
		}
		keyReg := strings.Repeat("9", fx.layout.RegDigits)

		// 与现有参考答案完全一致的答案卡:提示"一致",答案不变
		files := []multipartFile{{field: "images", name: "key_match.png", ctype: "image/png", data: renderCard(keyReg, fx.key)}}
		var body bytes.Buffer
		writeScanForm(&body, files)
		resp, data := teacher.do("POST", "/api/v1/exams/"+examID+"/scans", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("上传答案卡: %d\n%s", resp.StatusCode, data)
		}
		if !strings.Contains(string(data), "与现有参考答案一致") {
			t.Fatalf("一致答案卡应提示一致: %s", data)
		}

		// 与现有答案冲突的答案卡(翻转第 1 题):不覆盖,提示手动核对
		conflict := append([]string(nil), fx.key...)
		if conflict[0] == "A" {
			conflict[0] = "B"
		} else {
			conflict[0] = "A"
		}
		files = []multipartFile{{field: "images", name: "key_conflict.png", ctype: "image/png", data: renderCard(keyReg, conflict)}}
		body.Reset()
		writeScanForm(&body, files)
		resp, data = teacher.do("POST", "/api/v1/exams/"+examID+"/scans", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("上传冲突答案卡: %d\n%s", resp.StatusCode, data)
		}
		if !strings.Contains(string(data), "不一致,未覆盖") {
			t.Fatalf("冲突答案卡应提示未覆盖: %s", data)
		}
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if got := d["key"].([]any); got[0] != fx.key[0] {
			t.Fatalf("冲突答案卡不得覆盖现有答案: 第 1 题 = %v", got[0])
		}

		// 叠加图:正常未抽样卷 404;抽样卷(每批第 1 张)与异常卷(空白卡)200
		if got := teacher.code("GET", "/api/v1/exams/"+examID+"/overlay/2", nil, "application/json"); got != http.StatusNotFound {
			t.Fatalf("正常卷默认无叠加图应 404,得到 %d", got)
		}
		blank := renderCard("00000006", make([]string, len(fx.key))) // 全空作答 → 空白卡告警 → 异常卷必存
		files = []multipartFile{{field: "images", name: "blank.png", ctype: "image/png", data: blank}}
		body.Reset()
		writeScanForm(&body, files)
		resp, data = teacher.do("POST", "/api/v1/exams/"+examID+"/scans", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("上传空白卡: %d\n%s", resp.StatusCode, data)
		}
		if got := teacher.code("GET", "/api/v1/exams/"+examID+"/overlay/2", nil, "application/json"); got != http.StatusNotFound {
			t.Fatalf("卷 2 仍未抽样应 404,得到 %d", got)
		}
		// 空白卡卷号 = 既有学生数(答案卡不占学生号);空白卡必为异常卷 → 叠加图必存
		nStudents := int(teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)["students"].(float64))
		last := fmt.Sprintf("/api/v1/exams/%s/overlay/%d", examID, nStudents)
		if got := teacher.code("GET", last, nil, "application/json"); got != http.StatusOK {
			t.Fatalf("异常卷(空白卡)叠加图应 200,得到 %d", got)
		}
	})
}

// writeScanForm 构造补传扫描件的 multipart 表单(仅 images 字段)。
func writeScanForm(body *bytes.Buffer, files []multipartFile) {
	writeExamForm(body, card.Layout{}, nil, "[]", files...)
	body.Truncate(body.Len() - len(fmt.Sprintf("--%s--\r\n", multipartBoundary)))
	fmt.Fprintf(body, "--%s--\r\n", multipartBoundary)
}

// jsonBody 把 map 序列化为请求体。
func jsonBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// 确保 strings 引用(测试内部分支可能用到)。
var _ = strings.TrimSpace
