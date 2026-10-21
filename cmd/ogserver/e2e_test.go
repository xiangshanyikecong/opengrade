package main

// 全系统 E2E 回归测试:在进程内用 httptest 起完整服务(真实账号库/考试存储/全部路由),
// 覆盖:鉴权与四级角色权限矩阵、出卡/读卡 API、建考全流程(切片+整卷落盘核数)、
// 任务分配、阅卷、异常卷推荐指派、报告与班级聚合、四种导出产物的深度校验、
// 学生端全流与隐私隔离、用户管理、重启持久化与会话失效、删除。
// 运行:go test ./cmd/ogserver/

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/synth"
)

// pngEncodeForTest 包装标准库 PNG 编码。
func pngEncodeForTest(w io.Writer, img image.Image) error { return png.Encode(w, img) }

// randomAnswers 按题型生成随机作答(与 ogctl 同逻辑:judge 用 T/F)。
func randomAnswers(rng *rand.Rand, sheet *card.Sheet) []string {
	ans := make([]string, len(sheet.Choice))
	for qi, opts := range sheet.Choice {
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(opts))
		ans[qi] = letters[rng.Intn(len(letters))]
	}
	return ans
}

// ---- 测试客户端 ----

type e2eClient struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newClient(t *testing.T, base string) *e2eClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &e2eClient{t: t, base: base, hc: &http.Client{Jar: jar}}
}

func (c *e2eClient) do(method, path string, body io.Reader, contentType string) (*http.Response, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

// code 只取状态码;5xx 直接判失败(复核测试不允许服务端内部错误)。
func (c *e2eClient) code(method, path string, body io.Reader, contentType string) int {
	resp, data := c.do(method, path, body, contentType)
	if resp.StatusCode >= 500 {
		c.t.Fatalf("%s %s: 服务端内部错误 %d: %s", method, path, resp.StatusCode, data)
	}
	return resp.StatusCode
}

func (c *e2eClient) json(method, path string, body any, wantCode int) map[string]any {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	resp, data := c.do(method, path, rd, "application/json")
	if resp.StatusCode != wantCode {
		c.t.Fatalf("%s %s: got %d want %d\n%s", method, path, resp.StatusCode, wantCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func (c *e2eClient) jsonArr(method, path string, wantCode int) []map[string]any {
	c.t.Helper()
	resp, data := c.do(method, path, nil, "")
	if resp.StatusCode != wantCode {
		c.t.Fatalf("%s %s: got %d want %d\n%s", method, path, resp.StatusCode, wantCode, data)
	}
	var arr []map[string]any
	_ = json.Unmarshal(data, &arr)
	return arr
}

func (c *e2eClient) login(user, pass string) {
	c.t.Helper()
	c.json("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, http.StatusOK)
}

// ---- 测试数据 ----

type fixture struct {
	layout   card.Layout
	key      []string
	fillings []card.Filling
	regNums  []string
	scans    [][]byte
	wantObj  []float64
}

func buildFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{layout: card.DefaultLayout()}
	sheet, err := f.layout.Build()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(2024))
	f.key = randomAnswers(rng, sheet)
	for i := 0; i < 5; i++ {
		reg := fmt.Sprintf("%08d", i+1)
		f.regNums = append(f.regNums, reg)
		fill := card.Filling{Answers: randomAnswers(rng, sheet)}
		for _, ch := range reg {
			fill.Reg = append(fill.Reg, int(ch-'0'))
		}
		if i == 2 && len(fill.Reg) > 2 {
			fill.Reg[len(fill.Reg)/2] = -1 // 学生 3 模拟考号漏涂
		}
		f.fillings = append(f.fillings, fill)
		obj := 0.0
		for qi := range f.layout.Questions {
			if fill.Answers[qi] == f.key[qi] {
				obj += f.layout.Questions[qi].Score
			}
		}
		f.wantObj = append(f.wantObj, obj)
		canvas := card.RenderPNG(sheet, &fill)
		distorted := synth.Distort(canvas.ToGray(), rand.New(rand.NewSource(int64(1000+i))))
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		f.scans = append(f.scans, jbuf.Bytes())
	}
	return f
}

func TestFullSystemReview(t *testing.T) {
	dir := t.TempDir()
	fx := buildFixture(t)

	srv, err := newServer(dir, true) // 播种示例组织
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	teacher := newClient(t, ts.URL)
	leader := newClient(t, ts.URL)
	student := newClient(t, ts.URL)
	anon := newClient(t, ts.URL)

	// ---------- A. 鉴权 ----------
	t.Run("A0_未登录全部拒绝", func(t *testing.T) {
		for _, p := range []string{"/api/v1/exams", "/api/v1/users", "/api/v1/student/exams", "/api/v1/auth/me"} {
			if got := anon.code("GET", p, nil, ""); got != http.StatusUnauthorized {
				t.Fatalf("GET %s 未登录应 401,得到 %d", p, got)
			}
		}
	})
	t.Run("A1_登录失败401", func(t *testing.T) {
		admin.json("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, http.StatusUnauthorized)
	})
	t.Run("A2_四角色登录播种核验", func(t *testing.T) {
		admin.login("admin", "admin123")
		teacher.login("teacher1", "teach123")
		leader.login("leader1", "leader123")
		student.login("s0001", "student123")
		me := admin.json("GET", "/api/v1/auth/me", nil, http.StatusOK)
		if me["role"] != "admin" {
			t.Fatalf("me.role = %v", me["role"])
		}
		users := admin.jsonArr("GET", "/api/v1/users", http.StatusOK)
		if len(users) != 9 { // admin + leader1 + 2 教师 + 5 学生
			t.Fatalf("播种账号数 = %d,期望 9", len(users))
		}
		for _, u := range users {
			if u["hash"] != nil || u["salt"] != nil {
				t.Fatal("用户列表不应外泄口令散列")
			}
		}
	})

	// ---------- B. 权限矩阵 ----------
	t.Run("B_权限矩阵", func(t *testing.T) {
		cases := []struct {
			name         string
			c            *e2eClient
			method, path string
			want         int
		}{
			{"学生访问考试列表", student, "GET", "/api/v1/exams", http.StatusForbidden},
			{"学生访问用户管理", student, "GET", "/api/v1/users", http.StatusForbidden},
			{"学生删除考试", student, "DELETE", "/api/v1/exams/zzz", http.StatusForbidden},
			{"学生建考", student, "POST", "/api/v1/exams", http.StatusForbidden},
			{"教师访问用户管理", teacher, "GET", "/api/v1/users", http.StatusForbidden},
			{"教师删除考试", teacher, "DELETE", "/api/v1/exams/zzz", http.StatusForbidden},
			{"教师分配任务", teacher, "POST", "/api/v1/exams/zzz/tasks", http.StatusForbidden},
			{"教师查看学生端", teacher, "GET", "/api/v1/student/exams", http.StatusForbidden},
			{"组长删除不存在的考试(过守卫后404)", leader, "DELETE", "/api/v1/exams/zzz", http.StatusNotFound},
			{"组长访问用户管理", leader, "GET", "/api/v1/users", http.StatusForbidden},
			{"教师查看考试列表", teacher, "GET", "/api/v1/exams", http.StatusOK},
			{"管理员访问学生端应拒绝", admin, "GET", "/api/v1/student/exams", http.StatusForbidden},
		}
		for _, tc := range cases {
			var body io.Reader
			if tc.method == "POST" {
				body = strings.NewReader("{}")
			}
			if got := tc.c.code(tc.method, tc.path, body, "application/json"); got != tc.want {
				t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
			}
		}
	})

	// ---------- C. 出卡 / 读卡 API ----------
	t.Run("C1_出卡SVG元素计数与中文", func(t *testing.T) {
		_, svg := admin.do("POST", "/api/v1/cards", strings.NewReader(
			`{"layout":{"version":2,"title":"复核测试","regDigits":8,"columns":2,`+
				`"questions":[{"kind":"choice","options":4,"score":2},{"kind":"judge","score":2},{"kind":"multiple","options":4,"score":2}],`+
				`"subjectives":[{"kind":"fillblank","score":5,"heightMM":16}]}}`), "application/json")
		// 1×(4+2+4)=10 涂卡圆 + 考号 80 = 90
		if got := bytes.Count(svg, []byte("<circle")); got != 90 {
			t.Fatalf("SVG 涂卡圆 = %d,期望 90", got)
		}
		if !strings.Contains(string(svg), "复核测试") {
			t.Fatal("SVG 应含中文卷头")
		}
	})
	t.Run("C2_出卡PNG魔数", func(t *testing.T) {
		_, data := admin.do("POST", "/api/v1/cards?format=png", strings.NewReader(`{"layout":{"version":1,"choiceCount":5,"options":4,"regDigits":4}}`), "application/json")
		if len(data) < 8 || string(data[1:4]) != "PNG" {
			t.Fatalf("PNG 魔数错误: %x", data[:8])
		}
	})
	t.Run("C3_非法版式400", func(t *testing.T) {
		admin.json("POST", "/api/v1/cards", map[string]any{"layout": map[string]any{"version": 2, "questions": []any{}}}, http.StatusBadRequest)
	})
	t.Run("C4_单卷读卡API", func(t *testing.T) {
		sheet, _ := fx.layout.Build()
		canvas := card.RenderPNG(sheet, &fx.fillings[0])
		var pbuf bytes.Buffer
		if err := pngEncodeForTest(&pbuf, canvas.ToGray()); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		writeExamForm(&body, fx.layout, fx.key, "", multipartFile{field: "image", name: "card.png", ctype: "image/png", data: pbuf.Bytes()})
		resp, data := admin.do("POST", "/api/v1/omr", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != 200 {
			t.Fatalf("omr: %d\n%s", resp.StatusCode, data)
		}
		var res omr.Result
		if err := json.Unmarshal(data, &res); err != nil {
			t.Fatal(err)
		}
		if res.RegNumber != "00000001" || len(res.Answers) != len(fx.key) {
			t.Fatalf("读卡结果异常: reg=%q answers=%d", res.RegNumber, len(res.Answers))
		}
	})

	// ---------- D. 建考全流程 ----------
	var examID string
	t.Run("D1_教师建考与磁盘核数", func(t *testing.T) {
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
		// 学科字段追加在 writeExamForm 之后(独立 part)
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
		if m["students"].(float64) != 5 || m["roster"].(float64) != 5 {
			t.Fatalf("建考返回异常: %s", data)
		}
		slices, _ := filepath.Glob(filepath.Join(dir, examID, "slices", "*.png"))
		if len(slices) != 15 { // 5 卷 × 3 道主观题
			t.Fatalf("切片数 = %d,期望 15", len(slices))
		}
		pages, _ := filepath.Glob(filepath.Join(dir, examID, "pages", "*.jpg"))
		if len(pages) != 5 {
			t.Fatalf("整卷回溯图数 = %d,期望 5", len(pages))
		}
	})
	t.Run("D2_考试详情", func(t *testing.T) {
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if d["createdBy"] != "teacher1" {
			t.Fatalf("createdBy = %v", d["createdBy"])
		}
		if d["subject"] != "数学" {
			t.Fatalf("subject = %v", d["subject"])
		}
		if len(d["progress"].([]any)) != 3 {
			t.Fatalf("主观题进度数应为 3")
		}
		if len(d["staff"].([]any)) == 0 {
			t.Fatal("详情应包含可分配教师列表")
		}
	})
	t.Run("D3_学生3考号无效与推荐", func(t *testing.T) {
		sug := teacher.json("GET", "/api/v1/exams/"+examID+"/suggest/3", nil, http.StatusOK)
		if sug["regValid"] != false {
			t.Fatalf("学生3应考号无效: %v", sug["regValid"])
		}
		marks := sug["regMarks"].([]any)
		if len(marks[4].([]any)) != 0 {
			t.Fatalf("第 5 位应漏涂: %v", marks)
		}
		sugList := sug["suggestions"].([]any)
		if len(sugList) == 0 || sugList[0].(map[string]any)["reg"] != "00000003" {
			t.Fatalf("应推荐 00000003: %v", sugList)
		}
	})

	// ---------- E. 任务分配 ----------
	t.Run("E_任务分配", func(t *testing.T) {
		leader.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]any{"question": 1, "teacher": "teacher1"}, http.StatusOK)
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		if d["tasks"].(map[string]any)["q1"] != "teacher1" {
			t.Fatalf("任务未生效: %v", d["tasks"])
		}
		// 越界题号 → 400(用组长:任务分配允许角色)
		leader.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]any{"question": 9, "teacher": "teacher1"}, http.StatusBadRequest)
	})

	// ---------- F. 阅卷 ----------
	t.Run("F_教师阅卷与进度", func(t *testing.T) {
		scores := []float64{5, 3, 4, 2, 5}
		marks := []string{"check", "cross", "check", "flag", "check"}
		for i, sc := range scores {
			teacher.json("POST", "/api/v1/grades", map[string]any{
				"examId": examID, "student": i + 1, "question": 1,
				"score": sc, "mark": marks[i], "graded": true,
				"ink": []map[string]any{{"color": "red", "pts": []float64{0.1, 0.1, 0.5, 0.5}}},
			}, http.StatusOK)
		}
		d := teacher.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		p0 := d["progress"].([]any)[0].(map[string]any)
		if p0["graded"].(float64) != 5 {
			t.Fatalf("第 31 题已阅 = %v", p0["graded"])
		}
		sl := teacher.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1&grading=1", http.StatusOK)
		if len(sl) != 5 {
			t.Fatalf("阅卷切片列表 = %d,期望 5(grading=1 跨班)", len(sl))
		}
		if sl[0]["ink"] == nil {
			t.Fatal("切片列表应含批注笔迹")
		}
		if sl[3]["mark"] != "flag" {
			t.Fatalf("学生4 ⚑ 标记丢失: %v", sl[3]["mark"])
		}
	})

	// ---------- G. 异常卷指派与报告 ----------
	t.Run("G_异常卷指派_报告与班级聚合", func(t *testing.T) {
		leader.json("POST", "/api/v1/assign", map[string]any{"examId": examID, "student": 3, "reg": "00000003"}, http.StatusOK)
		rep := leader.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		students := rep["students"].([]any)
		if students[2].(map[string]any)["regNumber"] != "00000003" {
			t.Fatalf("指派未生效: %v", students[2])
		}
		if rep["total"].(float64) != 80 {
			t.Fatalf("卷面满分 = %v", rep["total"])
		}
		if students[3].(map[string]any)["flagged"] != true {
			t.Fatal("学生4 的 ⚑ 应进入报告")
		}
		byClass := rep["byClass"].([]any)
		if len(byClass) != 2 {
			t.Fatalf("班级聚合应 2 个班: %v", byClass)
		}
		// 客观分与 fixture 期望一致
		for i, line := range students {
			got := line.(map[string]any)["objective"].(float64)
			if got != fx.wantObj[i] {
				t.Fatalf("学生 %d 客观分 = %v,期望 %v", i+1, got, fx.wantObj[i])
			}
		}
	})

	// ---------- G2. 发布控制与学情分析(发布后学生端才可查) ----------
	t.Run("G2_发布控制_学情分析与审计", func(t *testing.T) {
		// 发布前:学生不可查
		if got := student.code("GET", "/api/v1/student/exams/"+examID, nil, ""); got != http.StatusNotFound {
			t.Fatalf("未发布成绩应 404,得到 %d", got)
		}
		// 他科教师看不到本场考试(404,不泄露存在性),更无法改答案
		teacher2c := newClient(t, ts.URL)
		teacher2c.login("teacher2", "teach123")
		if got := teacher2c.code("POST", "/api/v1/exams/"+examID+"/key", strings.NewReader(`{"key":[]}`), "application/json"); got != http.StatusNotFound {
			t.Fatalf("他科教师改答案应 404,得到 %d", got)
		}
		// 建卷教师修正答案(第 1 题改为不存在的选项)→ 全班客观分重统
		newKey := append([]string{}, fx.key...)
		newKey[0] = "Z"
		teacher.json("POST", "/api/v1/exams/"+examID+"/key", map[string]any{"key": newKey}, http.StatusOK)
		rep1 := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		for i, line := range rep1["students"].([]any) {
			obj := line.(map[string]any)["objective"].(float64)
			if fx.fillings[i].Answers[0] == fx.key[0] && obj >= fx.wantObj[i] {
				t.Fatalf("答案修正后第 %d 卷客观分未重统:%v(原 %v)", i+1, obj, fx.wantObj[i])
			}
		}
		// 恢复原答案
		teacher.json("POST", "/api/v1/exams/"+examID+"/key", map[string]any{"key": fx.key}, http.StatusOK)
		rep2 := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		for i, line := range rep2["students"].([]any) {
			if got := line.(map[string]any)["objective"].(float64); got != fx.wantObj[i] {
				t.Fatalf("答案恢复后第 %d 卷客观分 = %v,期望 %v", i+1, got, fx.wantObj[i])
			}
		}
		// 学情分析:统计摘要与逐题维度(用组长看全量)
		an := leader.json("GET", "/api/v1/exams/"+examID+"/analytics", nil, http.StatusOK)
		st := an["stats"].(map[string]any)
		if st["count"].(float64) != 5 || st["total"].(float64) != 80 {
			t.Fatalf("统计摘要异常: %v", st)
		}
		if len(st["segments"].([]any)) != 10 {
			t.Fatal("分数段应为 10 段")
		}
		qs := an["questions"].([]any)
		if len(qs) != 33 {
			t.Fatalf("逐题分析数 = %d,期望 33", len(qs))
		}
		q0 := qs[0].(map[string]any)
		if q0["options"] == nil || q0["difficulty"] == nil {
			t.Fatal("客观题应含选项分布与难度")
		}
		// 发布 → 学生可查;教师(建卷人)可发布
		teacher.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)
		if got := student.code("GET", "/api/v1/student/exams/"+examID, nil, ""); got != http.StatusOK {
			t.Fatalf("发布后学生查分应 200,得到 %d", got)
		}
		d := student.json("GET", "/api/v1/student/exams/"+examID, nil, http.StatusOK)
		if d["rank"] == nil || d["classRank"] == nil {
			t.Fatal("学生报告应含排名")
		}
		// 组长审计日志:应含 setKey/publish/setGrade/assign 记录
		au := leader.json("GET", "/api/v1/exams/"+examID+"/audit", nil, http.StatusOK)
		acts := map[string]bool{}
		for _, e := range au["entries"].([]any) {
			acts[e.(map[string]any)["action"].(string)] = true
		}
		for _, want := range []string{"setKey", "publish", "setGrade", "assign"} {
			if !acts[want] {
				t.Fatalf("审计缺少 %s 记录: %v", want, acts)
			}
		}
		// 学生不可看审计
		if got := student.code("GET", "/api/v1/exams/"+examID+"/audit", nil, ""); got != http.StatusForbidden {
			t.Fatalf("学生看审计应 403,得到 %d", got)
		}
	})

	// ---------- G3. 等级赋分 ----------
	t.Run("G3_等级赋分", func(t *testing.T) {
		cv := leader.json("POST", "/api/v1/exams/"+examID+"/grade-convert", map[string]any{}, http.StatusOK)
		rows := cv["converted"].([]any)
		if len(rows) != 5 {
			t.Fatalf("赋分行数 = %d,期望 5", len(rows))
		}
		first := rows[0].(map[string]any)
		if first["grade"] == nil || first["converted"] == nil {
			t.Fatalf("赋分结果不完整: %v", first)
		}
		// 最高原始分应拿最高赋分(按转换后降序)
		prev := 1e9
		for _, r := range rows {
			c := r.(map[string]any)["converted"].(float64)
			if c > prev {
				t.Fatal("赋分应单调不增")
			}
			prev = c
		}
	})

	// ---------- H. 导出产物深度校验 ----------
	t.Run("H1_成绩册xlsx", func(t *testing.T) {
		resp, data := leader.do("GET", "/api/v1/exams/"+examID+"/export.xlsx", nil, "")
		if resp.StatusCode != 200 {
			t.Fatalf("xlsx: %d", resp.StatusCode)
		}
		parts := mustZipParts(t, data)
		for _, want := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/styles.xml"} {
			if _, ok := parts[want]; !ok {
				t.Fatalf("xlsx 缺少 %s", want)
			}
		}
		if !strings.Contains(parts["xl/worksheets/sheet1.xml"], "00000003") {
			t.Fatal("成绩单应含指派后的考号")
		}
	})
	t.Run("H2_成绩册csv", func(t *testing.T) {
		_, data := teacher.do("GET", "/api/v1/exams/"+examID+"/export.csv", nil, "")
		if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
			t.Fatal("csv 缺少 BOM")
		}
		if !strings.Contains(string(data), "00000003") {
			t.Fatal("csv 应含指派后的考号")
		}
	})
	t.Run("H3_全量数据包zip", func(t *testing.T) {
		_, data := leader.do("GET", "/api/v1/exams/"+examID+"/export.zip", nil, "")
		parts := mustZipParts(t, data)
		for _, want := range []string{
			"opengrade/README.txt", "opengrade/layout.json", "opengrade/key.json",
			"opengrade/report.json", "opengrade/exam.json",
		} {
			if _, ok := parts[want]; !ok {
				t.Fatalf("数据包缺少 %s", want)
			}
		}
		sliceCount := 0
		for name := range parts {
			if strings.HasPrefix(name, "opengrade/slices/") {
				sliceCount++
			}
		}
		if sliceCount != 15 {
			t.Fatalf("数据包切片数 = %d,期望 15", sliceCount)
		}
		var rep map[string]any
		if err := json.Unmarshal([]byte(parts["opengrade/report.json"]), &rep); err != nil {
			t.Fatalf("数据包内 report.json 不合法: %v", err)
		}
	})
	t.Run("H4_讲评课件pptx", func(t *testing.T) {
		_, data := leader.do("GET", "/api/v1/exams/"+examID+"/review.pptx", nil, "")
		parts := mustZipParts(t, data)
		for _, want := range []string{
			"ppt/presentation.xml", "ppt/slideMasters/slideMaster1.xml",
			"ppt/slideLayouts/slideLayout1.xml", "ppt/theme/theme1.xml",
			"ppt/slides/slide1.xml", "[Content_Types].xml", "_rels/.rels",
		} {
			if _, ok := parts[want]; !ok {
				t.Fatalf("pptx 缺少 %s", want)
			}
		}
		slideCount := 0
		for name, content := range parts {
			if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
				slideCount++
				if !strings.Contains(content, "<p:sld ") && !strings.Contains(content, "<p:sld>") {
					t.Fatalf("%s 不是幻灯片 XML", name)
				}
			}
		}
		// 概览 1 页 + 低分率题(两道未阅主观题必中,客观随机可能更多,截取前 12)
		if slideCount < 3 {
			t.Fatalf("幻灯片数 = %d,至少应含概览+低分率题", slideCount)
		}
	})

	// ---------- I. 学生端 ----------
	t.Run("I1_学生考试列表", func(t *testing.T) {
		list := student.jsonArr("GET", "/api/v1/student/exams", http.StatusOK)
		found := false
		for _, e := range list {
			if e["id"] == examID && e["found"] == true {
				found = true
			}
		}
		if !found {
			t.Fatal("学生列表未找到本场考试")
		}
	})
	t.Run("I2_学生成绩报告与错题本", func(t *testing.T) {
		d := student.json("GET", "/api/v1/student/exams/"+examID, nil, http.StatusOK)
		if d["objective"].(float64) != fx.wantObj[0] {
			t.Fatalf("客观分 = %v,期望 %v", d["objective"], fx.wantObj[0])
		}
		if d["subjective"].(float64) != 5 { // 第 31 题给了满分
			t.Fatalf("主观分 = %v", d["subjective"])
		}
		if d["total"].(float64) != 80 {
			t.Fatalf("满分 = %v", d["total"])
		}
		if len(d["questions"].([]any)) != 33 {
			t.Fatalf("逐题数应为 33")
		}
		expectWrong := 0
		for qi := range fx.layout.Questions {
			if fx.fillings[0].Answers[qi] != fx.key[qi] {
				expectWrong++
			}
		}
		expectWrong += 2 // 两道未阅主观题(0 分 < 满分)计入错题本
		if got := len(d["wrongQuestions"].([]any)); got != expectWrong {
			t.Fatalf("错题数 = %d,期望 %d", got, expectWrong)
		}
		if len(d["slices"].([]any)) != 3 {
			t.Fatal("学生应看到 3 张自己的切片")
		}
		if d["pageURL"] == nil || d["pageURL"] == "" {
			t.Fatal("学生应有整卷回溯")
		}
		if len(d["byClass"].([]any)) != 2 {
			t.Fatal("班级参照缺失")
		}
	})
	t.Run("I3_学生答卷回溯", func(t *testing.T) {
		resp, data := student.do("GET", "/api/v1/student/exams/"+examID+"/slice/1", nil, "")
		if resp.StatusCode != 200 || len(data) < 8 || string(data[1:4]) != "PNG" {
			t.Fatalf("学生切片异常: %d", resp.StatusCode)
		}
		resp, data = student.do("GET", "/api/v1/student/exams/"+examID+"/page", nil, "")
		if resp.StatusCode != 200 || len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
			t.Fatalf("学生整卷回溯应为 JPEG: %d", resp.StatusCode)
		}
	})
	t.Run("I4_学生隐私隔离", func(t *testing.T) {
		if got := student.code("GET", "/api/v1/exams/"+examID+"/slices?q=1", nil, ""); got != http.StatusForbidden {
			t.Fatalf("学生访问阅卷切片列表应 403,得到 %d", got)
		}
		if got := student.code("GET", "/api/v1/exams/"+examID+"/page/2", nil, ""); got != http.StatusForbidden {
			t.Fatalf("学生访问他人整卷入口应 403,得到 %d", got)
		}
		d := student.json("GET", "/api/v1/student/exams/"+examID, nil, http.StatusOK)
		if d["reg"] != "00000001" {
			t.Fatalf("学生报告考号应为本人: %v", d["reg"])
		}
	})

	t.Run("I5_家长端只读绑定子女", func(t *testing.T) {
		// 管理员创建家长账号,绑定 s0001(考号 00000001)
		admin.json("POST", "/api/v1/users", map[string]any{
			"username": "p0001", "displayName": "李雷家长", "role": "parent",
			"password": "parent123", "children": []string{"00000001"},
		}, http.StatusCreated)
		parent := newClient(t, ts.URL)
		parent.login("p0001", "parent123")
		// 家长账号不能进教师端
		if got := parent.code("GET", "/api/v1/exams", nil, ""); got != http.StatusForbidden {
			t.Fatalf("家长访问教师考试列表应 403,得到 %d", got)
		}
		// 不加 child 参数 → 400
		if got := parent.code("GET", "/api/v1/student/exams/"+examID, nil, ""); got != http.StatusBadRequest {
			t.Fatalf("家长未指定 child 应 400,得到 %d", got)
		}
		// 指定非绑定子女 → 403
		if got := parent.code("GET", "/api/v1/student/exams/"+examID+"?child=00000004", nil, ""); got != http.StatusForbidden {
			t.Fatalf("家长看非绑定子女应 403,得到 %d", got)
		}
		// 指定绑定子女 → 200,且数据为该子女
		d := parent.json("GET", "/api/v1/student/exams/"+examID+"?child=00000001", nil, http.StatusOK)
		if d["reg"] != "00000001" {
			t.Fatalf("家长视图考号 = %v,期望 00000001", d["reg"])
		}
		if d["objective"].(float64) != fx.wantObj[0] {
			t.Fatalf("家长视图客观分 = %v,期望 %v", d["objective"], fx.wantObj[0])
		}
		// 家长也不能查看阅卷切片列表
		if got := parent.code("GET", "/api/v1/exams/"+examID+"/slices?q=1", nil, ""); got != http.StatusForbidden {
			t.Fatalf("家长访问阅卷切片应 403,得到 %d", got)
		}
	})

	// ---------- M. 教师数据范围与学科过滤 ----------
	t.Run("M_教师数据范围与学科过滤", func(t *testing.T) {
		// teacher1(数学,三年二班):报告与导出只含本班学生
		rep := teacher.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		if rep["count"].(float64) != 3 {
			t.Fatalf("教师报告 count = %v,期望 3(仅三年二班)", rep["count"])
		}
		for _, line := range rep["students"].([]any) {
			reg := line.(map[string]any)["regNumber"].(string)
			if reg == "00000004" || reg == "00000005" {
				t.Fatalf("教师报告不应包含他班学生: %s", reg)
			}
		}
		sl := teacher.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1", http.StatusOK)
		if len(sl) != 3 {
			t.Fatalf("教师默认切片视图 = %d,期望 3(本班)", len(sl))
		}
		_, xlsx := teacher.do("GET", "/api/v1/exams/"+examID+"/export.xlsx", nil, "")
		parts := mustZipParts(t, xlsx)
		if strings.Contains(parts["xl/worksheets/sheet1.xml"], "00000004") {
			t.Fatal("教师成绩册不应含他班学生")
		}
		// 整卷回溯:本班 200,他班 404(整卷含手写姓名)
		if got := teacher.code("GET", "/api/v1/exams/"+examID+"/page/1", nil, ""); got != http.StatusOK {
			t.Fatalf("本班整卷应 200,得到 %d", got)
		}
		if got := teacher.code("GET", "/api/v1/exams/"+examID+"/page/4", nil, ""); got != http.StatusNotFound {
			t.Fatalf("他班整卷应 404,得到 %d", got)
		}
		// teacher2(语文,三年一班):数学考试不可见(404,不泄露存在性)
		teacher2 := newClient(t, ts.URL)
		teacher2.login("teacher2", "teach123")
		if got := teacher2.code("GET", "/api/v1/exams/"+examID, nil, ""); got != http.StatusNotFound {
			t.Fatalf("语文教师看数学考试应 404,得到 %d", got)
		}
		if got := teacher2.code("GET", "/api/v1/exams/"+examID+"/report", nil, ""); got != http.StatusNotFound {
			t.Fatalf("语文教师看数学报告应 404,得到 %d", got)
		}
		// 管理员建语文考试(无名册 → 校级卷):teacher2(语文)可见,teacher1(数学)不可见
		var body bytes.Buffer
		writeExamForm(&body, fx.layout, fx.key, "", multipartFile{field: "images", name: "s.jpg", ctype: "image/jpeg", data: fx.scans[0]})
		body.Truncate(body.Len() - len(fmt.Sprintf("--%s--\r\n", multipartBoundary)))
		fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"subject\"\r\n\r\n语文\r\n", multipartBoundary)
		fmt.Fprintf(&body, "--%s--\r\n", multipartBoundary)
		resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("建语文考试: %d\n%s", resp.StatusCode, data)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		cnExam := m["id"].(string)
		defer leader.json("DELETE", "/api/v1/exams/"+cnExam, nil, http.StatusOK)
		if got := teacher2.code("GET", "/api/v1/exams/"+cnExam, nil, ""); got != http.StatusOK {
			t.Fatalf("语文教师应可见语文校级卷,得到 %d", got)
		}
		if got := teacher.code("GET", "/api/v1/exams/"+cnExam, nil, ""); got != http.StatusNotFound {
			t.Fatalf("数学教师不应见语文校级卷,得到 %d", got)
		}
	})

	// ---------- J. 用户管理 ----------
	t.Run("J_用户管理", func(t *testing.T) {
		admin.json("POST", "/api/v1/users", map[string]any{
			"username": "t9", "displayName": "复核教师", "role": "teacher", "password": "pass1234", "class": "三年三班",
		}, http.StatusCreated)
		admin.json("POST", "/api/v1/users", map[string]any{
			"username": "t9", "displayName": "重复", "role": "teacher", "password": "pass1234",
		}, http.StatusBadRequest)
		admin.json("POST", "/api/v1/users/t9/password", map[string]string{"newPassword": "newpass9"}, http.StatusOK)
		t9 := newClient(t, ts.URL)
		t9.login("t9", "newpass9") // 新密码可登录
		admin.json("POST", "/api/v1/users", map[string]any{
			"username": "t10", "displayName": "x", "role": "teacher", "password": "123",
		}, http.StatusBadRequest) // 弱口令拒绝
		admin.json("DELETE", "/api/v1/users/t9", nil, http.StatusOK)
		admin.json("DELETE", "/api/v1/users/admin", nil, http.StatusBadRequest) // 不能删自己
	})

	// ---------- K. 重启持久化与会话失效 ----------
	t.Run("K_重启持久化与会话失效", func(t *testing.T) {
		srv2, err := newServer(dir, false) // 模拟重启:同数据目录,全新进程状态
		if err != nil {
			t.Fatal(err)
		}
		ts2 := httptest.NewServer(srv2.Handler)
		defer ts2.Close()
		// 显式把 ts1 的旧会话令牌发到新实例(端口不同但 Cookie 同域共享)
		u1, _ := url.Parse(ts.URL)
		oldToken := ""
		for _, ck := range teacher.hc.Jar.Cookies(u1) {
			if ck.Name == sessionCookie {
				oldToken = ck.Value
			}
		}
		if oldToken == "" {
			t.Fatal("未取到旧会话令牌")
		}
		req, _ := http.NewRequest("GET", ts2.URL+"/api/v1/auth/me", nil)
		req.Header.Set("Cookie", sessionCookie+"="+oldToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("重启后旧会话应 401,得到 %d(%s)", resp.StatusCode, data)
		}
		admin2 := newClient(t, ts2.URL)
		admin2.login("admin", "admin123")
		list := admin2.jsonArr("GET", "/api/v1/exams", http.StatusOK)
		if len(list) != 1 || list[0]["id"] != examID {
			t.Fatalf("重启后考试应仍在: %v", list)
		}
		rep := admin2.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		students := rep["students"].([]any)
		if students[0].(map[string]any)["subjective"].(float64) != 5 {
			t.Fatal("重启后评分数据丢失")
		}
		t1 := newClient(t, ts2.URL)
		t1.login("teacher1", "teach123") // 口令散列持久化校验
	})

	// ---------- L. 删除 ----------
	t.Run("L_删除考试", func(t *testing.T) {
		leader.json("DELETE", "/api/v1/exams/"+examID, nil, http.StatusOK)
		leader.json("GET", "/api/v1/exams/"+examID, nil, http.StatusNotFound)
		if _, err := os.Stat(filepath.Join(dir, examID)); !os.IsNotExist(err) {
			t.Fatal("删除后数据目录应清空")
		}
	})
}

// ---- multipart 构造 ----

const multipartBoundary = "og-e2e-boundary-73501"

type multipartFile struct {
	field, name, ctype string
	data               []byte
}

// writeExamForm 构造建考/读卡 multipart 表单。
func writeExamForm(buf *bytes.Buffer, layout card.Layout, key []string, rosterJSON string, files ...multipartFile) {
	part := func(name, content string) {
		fmt.Fprintf(buf, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", multipartBoundary, name, content)
	}
	lb, _ := json.Marshal(layout)
	part("layout", string(lb))
	kb, _ := json.Marshal(key)
	part("key", string(kb))
	if rosterJSON != "" {
		part("roster", rosterJSON)
	}
	for _, f := range files {
		fmt.Fprintf(buf, "--%s\r\n", multipartBoundary)
		fmt.Fprintf(buf, "Content-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n", f.field, f.name)
		fmt.Fprintf(buf, "Content-Type: %s\r\n\r\n", f.ctype)
		buf.Write(f.data)
		fmt.Fprintf(buf, "\r\n")
	}
	fmt.Fprintf(buf, "--%s--\r\n", multipartBoundary)
}

// mustZipParts 解包 zip 并返回 部件名 → 内容。
func mustZipParts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("不是合法 zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}
