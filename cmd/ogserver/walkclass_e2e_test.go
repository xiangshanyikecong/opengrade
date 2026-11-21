package main

// 走班排考(新高考双归属)端到端:
// 学生「行政班|教学班」多班归属 → 教师按教学班可见 → 班级统计双口径(同一学生计入两班)
// → 按教学班分配阅卷只见该班卷 → 未配置该学科的教师不可见。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/synth"
)

// walkRoster 走班名册(CSV 形式,第 3 列多班用 | 分隔):
// 3 名学生:2 人属于「高二1班|物理走班A」,1 人只有行政班。
const walkRoster = `考号,姓名,班级
01000001,王一,高二1班|物理走班A
01000002,王二,高二1班
01000003,王三,高二1班|物理走班A
`

const walkImport = `用户名,姓名,角色,密码,考号,班级,学科
twang,王老师,教师,tw123,,物理走班A,物理
tadmin1,备用数学教师,教师,tm123,,三年二班,数学
s0101,王一,学生,spass1,01000001,高二1班|物理走班A,
s0102,王二,学生,spass2,01000002,高二1班,
`

func buildWalkScans(t *testing.T) (card.Layout, []string, [][]byte) {
	t.Helper()
	layout := card.Layout{
		Version: 2, Title: "WALK E2E", RegDigits: 8, RegMode: card.RegFill, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 42}},
	}
	sheet, err := layout.Build()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(66))
	key := randomAnswers(rng, sheet)
	var scans [][]byte
	for i := 0; i < 3; i++ {
		reg := fmt.Sprintf("%08d", 1000001+i)
		fill := card.Filling{Answers: randomAnswers(rng, sheet)}
		for _, ch := range reg {
			fill.Reg = append(fill.Reg, int(ch-'0'))
		}
		distorted := synth.Distort(card.RenderPNG(sheet, &fill).ToGray(), rand.New(rand.NewSource(int64(3000+i))))
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		scans = append(scans, jbuf.Bytes())
	}
	return layout, key, scans
}

func TestWalkClassE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher1 := newClient(t, ts.URL)
	teacher1.login("teacher1", "teach123")

	// ---------- 1. 账号导入:教师任教教学班,学生双归属 ----------
	t.Run("A_多班账号导入", func(t *testing.T) {
		res := admin.json("POST", "/api/v1/users/import", map[string]string{"csv": walkImport}, http.StatusOK)
		if res["created"].(float64) != 4 {
			t.Fatalf("导入创建数 = %v,期望 4(%v)", res["created"], res)
		}
		found := false
		for _, u := range admin.jsonArr("GET", "/api/v1/users", http.StatusOK) {
			if u["username"] == "twang" {
				cls, _ := u["classes"].([]any)
				if len(cls) != 1 || cls[0] != "物理走班A" {
					t.Fatalf("twang classes = %v", u["classes"])
				}
				found = true
			}
			if u["username"] == "s0101" {
				cls, _ := u["classes"].([]any)
				if len(cls) != 2 || cls[0] != "高二1班" || cls[1] != "物理走班A" {
					t.Fatalf("s0101 classes = %v(Classes[0] 应为行政班)", u["classes"])
				}
			}
		}
		if !found {
			t.Fatal("未找到 twang")
		}
	})

	// ---------- 2. 建考(走班 CSV 名册)----------
	var examID string
	layout, key, scans := buildWalkScans(t)
	{
		var body bytes.Buffer
		files := make([]multipartFile, len(scans))
		for i, s := range scans {
			files[i] = multipartFile{field: "images", name: fmt.Sprintf("w_%02d.jpg", i+1), ctype: "image/jpeg", data: s}
		}
		writeExamForm(&body, layout, key, walkRoster, files...)
		body.Truncate(body.Len() - len(fmt.Sprintf("--%s--\r\n", multipartBoundary)))
		fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"subject\"\r\n\r\n物理\r\n", multipartBoundary)
		fmt.Fprintf(&body, "--%s--\r\n", multipartBoundary)
		resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		examID, _ = m["id"].(string)
	}

	twang := newClient(t, ts.URL)
	twang.login("twang", "tw123")

	// ---------- 3. 教学班教师可见性 ----------
	t.Run("B_教学班教师可见", func(t *testing.T) {
		seen := false
		for _, e := range twang.jsonArr("GET", "/api/v1/exams", http.StatusOK) {
			if e["id"] == examID {
				seen = true
			}
		}
		if !seen {
			t.Fatal("任教教学班「物理走班A」的教师未看到物理考试")
		}
		// 学科不符的 teacher1(数学)不可见
		if got := teacher1.code("GET", "/api/v1/exams/"+examID, nil, ""); got != http.StatusNotFound {
			t.Fatalf("数学教师访问物理走班考试应 404,得到 %d", got)
		}
	})

	// ---------- 4. 班级统计双口径 ----------
	t.Run("C_统计双口径", func(t *testing.T) {
		// 管理员全量视图:同一学生计入行政班与教学班两个口径
		rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		counts := map[string]float64{}
		for _, c := range rep["byClass"].([]any) {
			m, _ := c.(map[string]any)
			counts[m["class"].(string)] = m["count"].(float64)
		}
		if counts["高二1班"] != 3 {
			t.Fatalf("行政班高二1班统计人数 = %v,期望 3(%v)", counts["高二1班"], counts)
		}
		if counts["物理走班A"] != 2 {
			t.Fatalf("教学班物理走班A统计人数 = %v,期望 2(%v)", counts["物理走班A"], counts)
		}
		// 教学班教师视图:只见本教学班学生(单班学生被过滤)
		repT := twang.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		if got := len(repT["students"].([]any)); got != 2 {
			t.Fatalf("教学班教师视图学生数 = %d,期望 2", got)
		}
	})

	// ---------- 5. 按教学班分配阅卷:被分教师只见该班卷 ----------
	t.Run("D_按教学班分配阅卷", func(t *testing.T) {
		admin.json("POST", "/api/v1/exams/"+examID+"/tasks", map[string]string{"class": "物理走班A", "teacher": "twang"}, http.StatusOK)
		items := twang.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1&grading=1", http.StatusOK)
		if len(items) != 2 {
			t.Fatalf("按教学班分配后阅卷流卷数 = %d,期望 2(只有物理走班A 的学生)", len(items))
		}
		for _, it := range items {
			reg, _ := it["reg"].(string)
			if reg == "01000002" {
				t.Fatalf("非物理走班A的学生 01000002 不应出现在阅卷流")
			}
		}
		// 未被分配的建考管理员仍全量
		adminItems := admin.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1&grading=1", http.StatusOK)
		if len(adminItems) != 3 {
			t.Fatalf("管理员阅卷流卷数 = %d,期望 3", len(adminItems))
		}
	})

	// ---------- 6. 重启持久化:多班归属不丢 ----------
	t.Run("E_重启持久化", func(t *testing.T) {
		srv2, err := newServer(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		ts2 := httptest.NewServer(srv2.Handler)
		defer ts2.Close()
		a2 := newClient(t, ts2.URL)
		a2.login("admin", "admin123")
		for _, u := range a2.jsonArr("GET", "/api/v1/users", http.StatusOK) {
			if u["username"] == "s0101" {
				cls, _ := u["classes"].([]any)
				if len(cls) != 2 {
					t.Fatalf("重启后 s0101 classes = %v", u["classes"])
				}
			}
		}
		rep := a2.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		found := false
		for _, c := range rep["byClass"].([]any) {
			if c.(map[string]any)["class"] == "物理走班A" {
				found = true
			}
		}
		if !found {
			t.Fatal("重启后教学班统计口径丢失")
		}
	})

	_ = strings.TrimSpace
}
