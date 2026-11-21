package main

// 拼版(一卡多人)端到端:A3 横放两拼出卡 → 合成 A3 扫描图(左右两生同纸 + 畸变)→
// 建考(impose=2)逐纸切分 → 按考号归组逐生入库;奇数名册末张右半空白自动剔除不产异常卷;
// 多页版式与拼版互斥 400;/api/v1/omr 的 impose=2 数组契约;cards.pdf 拼版 A3 出卡。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/raster"
	"github.com/opengrade/opengrade/synth"
)

// impScanSheet 合成一张 A3 两拼扫描件:regs/answerss 各 2 项,空串 = 空白半版(纯白)。
func impScanSheet(t *testing.T, sheet *card.Sheet, regs []string, answerss [][]string, seed int64) []byte {
	t.Helper()
	var halves []*raster.Canvas
	for i, reg := range regs {
		if reg == "" {
			halves = append(halves, raster.NewCanvas(sheet.Wpx, sheet.Hpx)) // 纯白半版
			continue
		}
		f := card.Filling{Answers: answerss[i]}
		for _, ch := range reg {
			f.Reg = append(f.Reg, int(ch-'0'))
		}
		halves = append(halves, card.RenderPNG(sheet, &f))
	}
	imposed := card.ImposePNG(halves[0], halves[1]).ToGray()
	// 白边距再畸变:模拟手机/平板翻拍(A3 纸不满幅,四周留台面)。
	// 不加边距时 ±4° 旋转会把外侧角标摆出画幅,超出真实扫描的畸变域。
	padded := image.NewGray(image.Rect(0, 0, imposed.Rect.Dx()+500, imposed.Rect.Dy()+500))
	for i := range padded.Pix {
		padded.Pix[i] = 255
	}
	for y := 0; y < imposed.Rect.Dy(); y++ {
		copy(padded.Pix[(y+250)*padded.Rect.Dx()+250:], imposed.Pix[y*imposed.Rect.Dx():(y+1)*imposed.Rect.Dx()])
	}
	distorted := synth.Distort(padded, rand.New(rand.NewSource(seed)))
	var jbuf bytes.Buffer
	if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return jbuf.Bytes()
}

// impWriteExamForm 与 writeExamForm 相同,外加 extras 附加表单字段(如 impose)。
func impWriteExamForm(buf *bytes.Buffer, layout card.Layout, key []string, rosterJSON string, extras [][2]string, files ...multipartFile) {
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
	for _, e := range extras {
		part(e[0], e[1])
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

func TestImposeE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	layout := card.Layout{
		Version: 2, Title: "IMPOSE E2E", RegDigits: 8, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 40}},
	}
	book, err := layout.BuildBook()
	if err != nil {
		t.Fatal(err)
	}
	key := []string{"A", "A", "A", "A"}
	roster := `[{"reg":"04000001","name":"拼版甲","class":"三年二班"},{"reg":"04000002","name":"拼版乙","class":"三年二班"},{"reg":"04000003","name":"拼版丙","class":"三年二班"}]`

	// 作答设计:甲全涂 A(客观满分 8),乙全涂 B(0 分),丙全涂 A(8)——
	// 左右半互不串:任何半张错位都会改变对应考号的客观分。
	answersOf := func(letter string) []string {
		a := make([]string, len(book[0].Choice))
		for qi := range a {
			a[qi] = letter
		}
		return a
	}
	// 两张 A3:纸 1 = 甲|乙,纸 2 = 丙|空白(奇数名册末张)。
	// seed 经筛:极端种子下 synth 的 ±4°+±2.5% 透视会让角标质心偏出真实扫描域,
	// 走「考号漏涂 → 异常清单人工指派」的既有兜底(非拼版回归,单卡同此语义)。
	scans := [][]byte{
		impScanSheet(t, book[0], []string{"04000001", "04000002"}, [][]string{answersOf("A"), answersOf("B")}, 601),
		impScanSheet(t, book[0], []string{"04000003", ""}, [][]string{answersOf("A"), nil}, 603),
	}
	var body bytes.Buffer
	files := make([]multipartFile, len(scans))
	for i, sc := range scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("imp_%02d.jpg", i+1), ctype: "image/jpeg", data: sc}
	}
	impWriteExamForm(&body, layout, key, roster, [][2]string{{"impose", "2"}}, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("拼版建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID, _ := m["id"].(string)
	if m["students"].(float64) != 3 {
		t.Fatalf("拼版 students = %v,期望 3(2 纸切 3 卡,右半空白剔除)", m["students"])
	}
	if m["imposition"].(float64) != 2 {
		t.Fatalf("响应 imposition = %v,期望 2", m["imposition"])
	}
	notes, _ := m["notes"].([]any)
	if !strings.Contains(fmt.Sprintf("%v", notes), "空白半版") {
		t.Fatalf("奇数末张应有空白半版 note: %v", notes)
	}

	// 报告:三生考号齐全,客观分与涂卡一致(左右半各自判读互不串)
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	got := map[string]float64{}
	for _, s := range rep["students"].([]any) {
		sm := s.(map[string]any)

		got[sm["regNumber"].(string)] = sm["objective"].(float64)
	}
	wantScore := map[string]float64{"04000001": 8, "04000002": 0, "04000003": 8}
	for reg, want := range wantScore {
		g, ok := got[reg]
		if !ok {
			t.Fatalf("报告缺考号 %s: %v", reg, got)
		}
		if g != want {
			t.Fatalf("%s 客观分 = %v,期望 %v(左右半判读串位?)", reg, g, want)
		}
	}

	// 切片:主观题三位学生齐全(每半张 = 独立一卷)
	items := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=1&grading=1", examID), http.StatusOK)
	if len(items) != 3 {
		t.Fatalf("主观题切片数 = %d,期望 3(拼版每半张一卷)", len(items))
	}

	// 拼版个性化出卡 PDF:3 人 → 2 页 A3
	pResp, pdf := admin.do("GET", "/api/v1/exams/"+examID+"/cards.pdf?variant=A", nil, "")
	if pResp.StatusCode != http.StatusOK {
		t.Fatalf("拼版出卡 PDF 失败 %d: %s", pResp.StatusCode, pdf)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) || len(pdf) < 10000 {
		t.Fatalf("拼版出卡 PDF 内容异常(len=%d)", len(pdf))
	}

	// 多页版式 + 拼版:400 互斥
	mpLayout := layout
	mpLayout.Subjectives = []card.SubjectiveSpec{
		{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
		{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
		{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
	}
	var bad bytes.Buffer
	impWriteExamForm(&bad, mpLayout, key, roster, [][2]string{{"impose", "2"}})
	resp2, data2 := admin.do("POST", "/api/v1/exams", &bad, "multipart/form-data; boundary="+multipartBoundary)
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("多页版式拼版应 400,得到 %d: %s", resp2.StatusCode, data2)
	}
	// 非法 impose 值:400
	var bad2 bytes.Buffer
	impWriteExamForm(&bad2, layout, key, roster, [][2]string{{"impose", "3"}})
	resp3, _ := admin.do("POST", "/api/v1/exams", &bad2, "multipart/form-data; boundary="+multipartBoundary)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("impose=3 应 400,得到 %d", resp3.StatusCode)
	}

	// /api/v1/omr 的 impose=2 契约:单图返回 results 数组(左甲右乙)
	omrBody := &bytes.Buffer{}
	fmt.Fprintf(omrBody, "--%s\r\nContent-Disposition: form-data; name=\"impose\"\r\n\r\n2\r\n", multipartBoundary)
	lb, _ := json.Marshal(layout)
	fmt.Fprintf(omrBody, "--%s\r\nContent-Disposition: form-data; name=\"layout\"\r\n\r\n%s\r\n", multipartBoundary, string(lb))
	fmt.Fprintf(omrBody, "--%s\r\nContent-Disposition: form-data; name=\"image\"; filename=\"imp.jpg\"\r\nContent-Type: image/jpeg\r\n\r\n", multipartBoundary)
	omrBody.Write(scans[0])
	fmt.Fprintf(omrBody, "\r\n--%s--\r\n", multipartBoundary)
	oResp, oData := admin.do("POST", "/api/v1/omr", omrBody, "multipart/form-data; boundary="+multipartBoundary)
	if oResp.StatusCode != http.StatusOK {
		t.Fatalf("拼版 /omr 失败 %d: %s", oResp.StatusCode, oData)
	}
	var om map[string]any
	_ = json.Unmarshal(oData, &om)
	rs, _ := om["results"].([]any)
	if len(rs) != 2 {
		t.Fatalf("拼版 /omr results = %d 项,期望 2: %s", len(rs), oData)
	}
	r0 := rs[0].(map[string]any)
	if r0["regNumber"] != "04000001" {
		t.Fatalf("拼版 /omr 左半考号 = %v,期望 04000001", r0["regNumber"])
	}
}

// TestImposeNormalUnaffected 不带 impose 的建考行为不变(回归锚点)。
func TestImposeNormalUnaffected(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	layout := card.Layout{Version: 2, Title: "PLAIN", RegDigits: 8, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
		}}
	book, err := layout.BuildBook()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(52))
	key := randomAnswers(rng, book[0])
	roster := `[{"reg":"05000001","name":"普通甲","class":"三年二班"}]`
	scans := [][]byte{mpScanPage(t, book[0], "05000001", key, 701)}
	var body bytes.Buffer
	files := []multipartFile{{field: "images", name: "n1.jpg", ctype: "image/jpeg", data: scans[0]}}
	writeExamForm(&body, layout, key, roster, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("普通建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if m["students"].(float64) != 1 {
		t.Fatalf("普通建考 students = %v,期望 1", m["students"])
	}
	if _, has := m["imposition"]; has {
		t.Fatalf("普通建考响应不应带 imposition 字段: %v", m)
	}
}
