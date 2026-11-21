package main

// 纵向趋势(跨考试按考号对齐)+ 补传扫描件 + 手工补录/缺考标记 的全流程回归。
// 与 e2e_test.go 共用客户端/夹具/multipart 构造工具。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/synth"
)

func TestTrendsAndSupplements(t *testing.T) {
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
	stu1 := newClient(t, ts.URL)
	stu5 := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher.login("teacher1", "teach123")
	stu1.login("s0001", "student123")
	stu5.login("s0005", "student123")

	rosterJSON := `[` +
		`{"reg":"00000001","name":"李雷","class":"三年二班"},` +
		`{"reg":"00000002","name":"韩梅梅","class":"三年二班"},` +
		`{"reg":"00000003","name":"王小明","class":"三年二班"},` +
		`{"reg":"00000004","name":"赵小红","class":"三年一班"},` +
		`{"reg":"00000005","name":"孙小刚","class":"三年一班"}]`

	// createExam 取 fixture 前 n 张扫描件建一场数学考试。
	createExam := func(n int) string {
		var files []multipartFile
		for i := 0; i < n; i++ {
			files = append(files, multipartFile{field: "images", name: fmt.Sprintf("scan_%02d.jpg", i+1), ctype: "image/jpeg", data: fx.scans[i]})
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
		return m["id"].(string)
	}

	examA := createExam(3)
	time.Sleep(80 * time.Millisecond) // 保证 CreatedAt 严格递增(趋势时间轴升序断言)
	examB := createExam(3)

	// 异常卷自愈:两场考试的第 3 卷考号漏涂,人工指派回名册考号
	// (班级统计与趋势对齐按最终考号,依赖这一步;同时覆盖 指派→趋势 联动)
	for _, id := range []string{examA, examB} {
		teacher.json("POST", "/api/v1/assign", map[string]any{"examId": id, "student": 3, "reg": "00000003"}, http.StatusOK)
	}

	// ---------- 补传扫描件 ----------
	t.Run("补传扫描件", func(t *testing.T) {
		// 无 images → 400
		var empty bytes.Buffer
		writeExamForm(&empty, fx.layout, fx.key, "")
		if got := teacher.code("POST", "/api/v1/exams/"+examA+"/scans", &empty, "multipart/form-data; boundary="+multipartBoundary); got != http.StatusBadRequest {
			t.Fatalf("空补传应 400,得到 %d", got)
		}
		// 学生越权 → 403
		if got := stu1.code("POST", "/api/v1/exams/"+examA+"/scans", &empty, "multipart/form-data; boundary="+multipartBoundary); got != http.StatusForbidden {
			t.Fatalf("学生补传应 403,得到 %d", got)
		}
		// 批次:1 张垃圾(不计入)+ 1 张有效(现造考号 00000006 的卷)
		scan6 := renderScanForTest(t, fx, "00000006")
		var body bytes.Buffer
		writeExamForm(&body, fx.layout, fx.key, "", []multipartFile{
			{field: "images", name: "bad.jpg", ctype: "image/jpeg", data: []byte("not-an-image")},
			{field: "images", name: "scan6.jpg", ctype: "image/jpeg", data: scan6},
		}...)
		resp, data := teacher.do("POST", "/api/v1/exams/"+examA+"/scans", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("补传: %d\n%s", resp.StatusCode, data)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("解析补传响应: %v\n%s", err, data)
		}
		if m["added"].(float64) != 1 || m["students"].(float64) != 4 {
			t.Fatalf("补传结果异常: %s", data)
		}
		if len(m["failed"].([]any)) != 1 {
			t.Fatalf("垃圾图应计入失败: %s", data)
		}
		// 补传卷的切片与整卷图应落盘(第 4 卷 × 3 道主观题)
		slices, _ := filepath.Glob(filepath.Join(dir, examA, "slices", "s04_*.png"))
		if len(slices) != 3 {
			t.Fatalf("补传卷切片数 = %d,期望 3", len(slices))
		}
		if _, err := os.Stat(filepath.Join(dir, examA, "pages", "s04.jpg")); err != nil {
			t.Fatalf("补传卷整卷图缺失: %v", err)
		}
	})

	// ---------- 手工补录(缺考) ----------
	t.Run("手工补录与重复防护", func(t *testing.T) {
		admin.json("POST", "/api/v1/exams/"+examA+"/manual", map[string]any{"reg": "00000005", "absent": true}, http.StatusOK)
		// 重复补录 → 400
		admin.json("POST", "/api/v1/exams/"+examA+"/manual", map[string]any{"reg": "00000005", "absent": true}, http.StatusBadRequest)
		// 与已扫描考号冲突 → 400
		admin.json("POST", "/api/v1/exams/"+examA+"/manual", map[string]any{"reg": "00000001"}, http.StatusBadRequest)
		// 空考号 → 400
		admin.json("POST", "/api/v1/exams/"+examA+"/manual", map[string]any{"reg": ""}, http.StatusBadRequest)
		// 学生越权 → 403
		stu1.json("POST", "/api/v1/exams/"+examA+"/manual", map[string]any{"reg": "00000009"}, http.StatusForbidden)
	})

	// ---------- 缺考标记与统计剔除 ----------
	t.Run("缺考标记与统计剔除", func(t *testing.T) {
		// 已扫卷(第 4 卷,考号 00000006)标记缺考 → 统计剔除;再取消恢复
		admin.json("POST", "/api/v1/exams/"+examA+"/absent", map[string]any{"student": 4, "absent": true}, http.StatusOK)
		rep := admin.json("GET", "/api/v1/exams/"+examA+"/report", nil, http.StatusOK)
		if rep["count"].(float64) != 3 || rep["absentCount"].(float64) != 2 {
			t.Fatalf("标记缺考后 count/absentCount = %v/%v,期望 3/2", rep["count"], rep["absentCount"])
		}
		admin.json("POST", "/api/v1/exams/"+examA+"/absent", map[string]any{"student": 4, "absent": false}, http.StatusOK)
		// 无变化 → 400
		admin.json("POST", "/api/v1/exams/"+examA+"/absent", map[string]any{"student": 4, "absent": false}, http.StatusBadRequest)
		rep = admin.json("GET", "/api/v1/exams/"+examA+"/report", nil, http.StatusOK)
		if rep["count"].(float64) != 4 || rep["absentCount"].(float64) != 1 {
			t.Fatalf("恢复后 count/absentCount = %v/%v,期望 4/1", rep["count"], rep["absentCount"])
		}
		// 详情暴露缺考名单与补录卷计数
		d := admin.json("GET", "/api/v1/exams/"+examA, nil, http.StatusOK)
		absentList := d["absent"].([]any)
		if len(absentList) != 1 {
			t.Fatalf("缺考名单 = %v", d["absent"])
		}
		ab := absentList[0].(map[string]any)
		if ab["reg"] != "00000005" || ab["student"].(float64) != 5 {
			t.Fatalf("缺考名单项异常: %v", ab)
		}
		if d["manual"].(float64) != 1 {
			t.Fatalf("补录卷计数 = %v", d["manual"])
		}
		// 缺考占位卷不进阅卷队列(无需给幽灵卷打分解锁阶段):列表 = 4 份实卷
		sl := teacher.jsonArr("GET", "/api/v1/exams/"+examA+"/slices?q=1&grading=1", http.StatusOK)
		if len(sl) != 4 {
			t.Fatalf("切片列表 = %d,期望 4(缺考卷不进阅卷队列)", len(sl))
		}
		for _, it := range sl {
			if it["student"].(float64) == 5 {
				t.Fatal("缺考占位卷(第 5 卷)不应出现在阅卷队列")
			}
		}
	})

	// ---------- 成绩册导出:缺考列与细目表新增列 ----------
	t.Run("成绩册导出缺考与细目列", func(t *testing.T) {
		_, data := admin.do("GET", "/api/v1/exams/"+examA+"/export.xlsx", nil, "")
		parts := mustZipParts(t, data)
		s1 := parts["xl/worksheets/sheet1.xml"]
		if !strings.Contains(s1, "缺考") || !strings.Contains(s1, "排名") {
			t.Fatal("成绩单应含 排名/缺考 列")
		}
		s2 := parts["xl/worksheets/sheet2.xml"]
		for _, want := range []string{"难度系数", "区分度", "选项分布"} {
			if !strings.Contains(s2, want) {
				t.Fatalf("细目表缺少列:%s", want)
			}
		}
		if !strings.Contains(s2, "A:") {
			t.Fatal("细目表应含选项人数分布文本")
		}
		_, csv := admin.do("GET", "/api/v1/exams/"+examA+"/export.csv", nil, "")
		if !strings.Contains(string(csv), "缺考") || !strings.Contains(string(csv), "排名") {
			t.Fatal("CSV 应含 排名/缺考 列")
		}
	})

	// ---------- 纵向趋势(教师/管理员端) ----------
	t.Run("纵向趋势_教师端", func(t *testing.T) {
		tr := admin.json("GET", "/api/v1/trends?subject=数学", nil, http.StatusOK)
		exams := tr["exams"].([]any)
		if len(exams) != 2 {
			t.Fatalf("时间轴应有两场考试: %d", len(exams))
		}
		if exams[0].(map[string]any)["id"] != examA {
			t.Fatal("时间轴应按时间升序,examA 在前")
		}
		byReg := map[string]map[string]any{}
		for _, s := range tr["students"].([]any) {
			m := s.(map[string]any)
			byReg[m["reg"].(string)] = m
		}
		if pts := byReg["00000001"]["points"].([]any); len(pts) != 2 {
			t.Fatalf("考号 1 应跨两场对齐出 2 个数据点: %d", len(pts))
		}
		if pts := byReg["00000006"]["points"].([]any); len(pts) != 1 {
			t.Fatalf("补录考号 6 只应在 examA 出点: %d", len(pts))
		}
		p5 := byReg["00000005"]["points"].([]any)
		if len(p5) != 1 || p5[0].(map[string]any)["absent"] != true {
			t.Fatalf("考号 5 应为 examA 缺考点: %v", p5)
		}
		// 班级过滤:只出三年二班学生,两场都含该班
		trc := admin.json("GET", "/api/v1/trends?class="+url.QueryEscape("三年二班"), nil, http.StatusOK)
		for _, s := range trc["students"].([]any) {
			if s.(map[string]any)["class"] != "三年二班" {
				t.Fatalf("班级过滤失效: %v", s)
			}
		}
		for _, e := range trc["exams"].([]any) {
			if e.(map[string]any)["classCount"].(float64) != 3 {
				t.Fatalf("本班人数应为 3: %v", e)
			}
		}
		// 学科过滤无匹配 → 空时间轴
		trx := admin.json("GET", "/api/v1/trends?subject=语文", nil, http.StatusOK)
		if len(trx["exams"].([]any)) != 0 {
			t.Fatal("语文学科应无考试")
		}
		// 学生访问教师端趋势 → 403
		if got := stu1.code("GET", "/api/v1/trends", nil, ""); got != http.StatusForbidden {
			t.Fatalf("学生访问 /trends 应 403,得到 %d", got)
		}
	})

	// ---------- 发布后学生端:列表/趋势/缺考视图 ----------
	t.Run("学生端趋势与缺考视图", func(t *testing.T) {
		gradeAllForPublish(t, admin, examA) // 发布门禁:主观题评完才能发布
		gradeAllForPublish(t, admin, examB)
		admin.json("POST", "/api/v1/exams/"+examA+"/publish", map[string]any{"published": true}, http.StatusOK)
		admin.json("POST", "/api/v1/exams/"+examB+"/publish", map[string]any{"published": true}, http.StatusOK)

		lr := stu1.json("GET", "/api/v1/student/exams", nil, http.StatusOK)
		list := lr["exams"].([]any)
		if len(list) != 2 {
			t.Fatalf("s0001 应见两场考试: %d", len(list))
		}
		tr := stu1.json("GET", "/api/v1/student/trends", nil, http.StatusOK)
		pts := tr["points"].([]any)
		if len(pts) != 2 {
			t.Fatalf("s0001 趋势点 = %d,期望 2", len(pts))
		}
		p0 := pts[0].(map[string]any)
		if p0["classRank"].(float64) < 1 {
			t.Fatalf("学生端趋势应含班级排名: %v", p0)
		}
		if p0["classMean"].(float64) <= 0 {
			t.Fatalf("学生端趋势应含班级均分对照: %v", p0)
		}

		// s0005:examA 缺考;examB 无卷(不进列表,计入 unmatched 汇总 —— 不再逐条刷"未找到关联答卷")
		lr5 := stu5.json("GET", "/api/v1/student/exams", nil, http.StatusOK)
		list5 := lr5["exams"].([]any)
		var examAItem map[string]any
		for _, it := range list5 {
			m := it.(map[string]any)
			if m["id"] == examA {
				examAItem = m
			}
		}
		if examAItem == nil || examAItem["found"] != true || examAItem["absent"] != true {
			t.Fatalf("s0005 应在 examA 显示缺考: %v", list5)
		}
		if lr5["unmatched"].(float64) < 1 {
			t.Fatalf("examB 无卷应计入 unmatched 汇总: %v", lr5["unmatched"])
		}
		det := stu5.json("GET", "/api/v1/student/exams/"+examA, nil, http.StatusOK)
		if det["absent"] != true {
			t.Fatalf("s0005 examA 详情应为缺考视图: %v", det["absent"])
		}
	})

	// ---------- 审计留痕 ----------
	t.Run("补救操作审计留痕", func(t *testing.T) {
		a := admin.json("GET", "/api/v1/exams/"+examA+"/audit", nil, http.StatusOK)
		actions := map[string]bool{}
		for _, e := range a["entries"].([]any) {
			actions[e.(map[string]any)["action"].(string)] = true
		}
		for _, want := range []string{"appendScans", "setAbsent", "补录缺考"} {
			if !actions[want] {
				t.Fatalf("审计缺少动作 %s: %v", want, actions)
			}
		}
	})
}

// renderScanForTest 按版式渲染一张指定考号的合成扫描件(容忍畸变),供补传用例使用。
func renderScanForTest(t *testing.T, fx *fixture, reg string) []byte {
	t.Helper()
	sheet, err := fx.layout.Build()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(4321))
	fill := card.Filling{Answers: randomAnswers(rng, sheet)}
	for _, ch := range reg {
		fill.Reg = append(fill.Reg, int(ch-'0'))
	}
	canvas := card.RenderPNG(sheet, &fill)
	distorted := synth.Distort(canvas.ToGray(), rand.New(rand.NewSource(77)))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
