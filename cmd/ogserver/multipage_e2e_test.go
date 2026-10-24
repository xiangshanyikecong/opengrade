package main

// 多页版式端到端:主观题超容量自动分页(每页重印身份区)→ 逐页判读考号+页点页号 →
// 按考号归组合并(考号/客观题/选做题号区/切片全局题号)→ 缺页告警 + 乱序上传归组自愈。

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

func mpScanPage(t *testing.T, sheet *card.Sheet, reg string, answers []string, seed int64) []byte {
	t.Helper()
	f := card.Filling{Answers: answers}
	for _, ch := range reg {
		f.Reg = append(f.Reg, int(ch-'0'))
	}
	distorted := synth.Distort(card.RenderPNG(sheet, &f).ToGray(), rand.New(rand.NewSource(seed)))
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return jbuf.Bytes()
}

func TestMultipageE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// 10 客观 + 6 道解答(42mm)→ 第 1 页约装 3 道,自动分 2 页
	layout := card.Layout{
		Version: 2, Title: "MP E2E", RegDigits: 8, RegMode: card.RegFillBarcode, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
		},
	}
	book, err := layout.BuildBook()
	if err != nil {
		t.Fatal(err)
	}
	if len(book) != 2 {
		t.Fatalf("预期自动分 2 页,得到 %d 页", len(book))
	}
	page1Subs, page2Subs := len(book[0].SubjectiveBoxes), len(book[1].SubjectiveBoxes)
	if page1Subs+page2Subs != 6 || page1Subs == 0 || page2Subs == 0 {
		t.Fatalf("两页主观题分布异常:%d + %d", page1Subs, page2Subs)
	}

	rng := rand.New(rand.NewSource(77))
	key := randomAnswers(rng, book[0])
	roster := `[{"reg":"02000001","name":"多页甲","class":"三年二班"},{"reg":"02000002","name":"多页乙","class":"三年二班"}]`

	// 每生 2 页连续:甲(页1+页2),乙(页1+页2)
	var scans [][]byte
	answers := func() []string { return randomAnswers(rng, book[0]) }
	for _, reg := range []string{"02000001", "02000002"} {
		a := answers()
		scans = append(scans, mpScanPage(t, book[0], reg, a, int64(len(scans)+500)))
		scans = append(scans, mpScanPage(t, book[1], reg, nil, int64(len(scans)+500))) // 第 2 页无客观题
		_ = a
	}

	var body bytes.Buffer
	files := make([]multipartFile, len(scans))
	for i, sc := range scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("mp_%02d.jpg", i+1), ctype: "image/jpeg", data: sc}
	}
	writeExamForm(&body, layout, key, roster, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID, _ := m["id"].(string)
	if m["pages"].(float64) != 2 {
		t.Fatalf("响应 pages = %v,期望 2", m["pages"])
	}
	if m["students"].(float64) != 2 {
		t.Fatalf("响应 students = %v,期望 2(4 张按 2 页一组)", m["students"])
	}

	// 报告:两生考号由页 1 条码对齐
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	regs := map[string]bool{}
	for _, s := range rep["students"].([]any) {
		regs[s.(map[string]any)["regNumber"].(string)] = true
	}
	if !regs["02000001"] || !regs["02000002"] {
		t.Fatalf("报告考号缺失:%v", regs)
	}

	// 切片:全局题号贯通两页(第 5、6 题在页 2,切片仍以全局题号命名)
	for q := 1; q <= 6; q++ {
		items := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=%d&grading=1", examID, q), http.StatusOK)
		if len(items) != 2 {
			t.Fatalf("第 %d 题切片数 = %d,期望 2(含第 2 页主观题)", q, len(items))
		}
	}

	// 张数不是页数整数倍:不再报错 —— 按考号归组,乙缺第 2 页 → 建考成功 + 页级缺页告警
	var bad bytes.Buffer
	writeExamForm(&bad, layout, key, roster, files[:3]...) // 甲(p1+p2) + 乙(p1)
	resp2, data2 := admin.do("POST", "/api/v1/exams", &bad, "multipart/form-data; boundary="+multipartBoundary)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("3 张(乙缺第 2 页)应 201 并带缺页告警,得到 %d: %s", resp2.StatusCode, data2)
	}
	var m2 map[string]any
	_ = json.Unmarshal(data2, &m2)
	if m2["students"].(float64) != 2 {
		t.Fatalf("缺页场景 students = %v,期望 2(甲完整,乙缺页仍入库)", m2["students"])
	}
	notes, _ := m2["notes"].([]any)
	joined := fmt.Sprintf("%v", notes)
	if !strings.Contains(joined, "02000002") || !strings.Contains(joined, "缺第 2 页") {
		t.Fatalf("缺页告警应指出考号与缺的页号,得到 %v", notes)
	}

	// 乱序上传(扫描仪卡纸重排):乙p1、甲p2、甲p1、乙p2 —— 按考号归组后结果必须与顺序上传一致
	var shuf bytes.Buffer
	shuffled := []multipartFile{files[2], files[1], files[0], files[3]} // 乙p1 甲p2 甲p1 乙p2
	writeExamForm(&shuf, layout, key, roster, shuffled...)
	resp3, data3 := admin.do("POST", "/api/v1/exams", &shuf, "multipart/form-data; boundary="+multipartBoundary)
	if resp3.StatusCode != http.StatusCreated {
		t.Fatalf("乱序 4 张应 201,得到 %d: %s", resp3.StatusCode, data3)
	}
	var m3 map[string]any
	_ = json.Unmarshal(data3, &m3)
	shufID, _ := m3["id"].(string)
	if m3["students"].(float64) != 2 {
		t.Fatalf("乱序场景 students = %v,期望 2", m3["students"])
	}
	rep3 := admin.json("GET", "/api/v1/exams/"+shufID+"/report", nil, http.StatusOK)
	regs3 := map[string]bool{}
	for _, s := range rep3["students"].([]any) {
		regs3[s.(map[string]any)["regNumber"].(string)] = true
	}
	if !regs3["02000001"] || !regs3["02000002"] {
		t.Fatalf("乱序归组后报告考号缺失:%v", regs3)
	}
	// 乱序归组:第 5、6 题(第 2 页)切片仍应两位学生齐全(页点判读把 p2 归回原卷)
	for q := 5; q <= 6; q++ {
		items := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=%d&grading=1", shufID, q), http.StatusOK)
		if len(items) != 2 {
			t.Fatalf("乱序归组后第 %d 题切片数 = %d,期望 2", q, len(items))
		}
	}
}
