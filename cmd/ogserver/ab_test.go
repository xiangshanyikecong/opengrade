package main

// AB 卷自动生成(deriveAB / abAuto=1)与答案键统一:
//   - deriveAB:题序对照是 1..N 排列且为错位排列(无固定点 → B 卷题序必与 A 不同),
//     B 卡面第 i 题规格 = 规范第 orderB[i] 题,其余字段与 A 一致;
//   - abAuto=1 全链路 e2e:建考自动出 B 卷 → A/B 混扫分版判读并归一题序 → 两卷按同一份
//     共用答案键判分 → 修正答案键后 KeyB 同步、两卷同步重统 → 个性化出卡;
//   - 手填路径的矛盾拦截:B 卷答案键与 A 卷不一致 400;B 版式卡面题序与题序对照矛盾 400
//     (规格可区分的题逐一比对;规格全同时校验自然放行,见 ab_e2e_test.go)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
)

func TestDeriveAB(t *testing.T) {
	la := abLayout("", nil) // 6 单选 + 1 解答
	lb, orderB := deriveAB(&la, rand.New(rand.NewSource(42)))
	if lb.Variant != "B" || la.Variant != "" {
		t.Fatalf("卷种标记错误:A=%q B=%q", la.Variant, lb.Variant)
	}
	if len(orderB) != 6 || lb.Title != la.Title || lb.RegDigits != la.RegDigits {
		t.Fatalf("B 版式基本字段应与 A 一致:%+v", lb)
	}
	seen := map[int]bool{}
	for i, v := range orderB {
		if v < 1 || v > 6 || seen[v] {
			t.Fatalf("orderB 应为 1..6 的排列:%v", orderB)
		}
		seen[v] = true
		if lb.Questions[i] != la.Questions[v-1] {
			t.Fatalf("B 卡面第 %d 题应为规范第 %d 题", i+1, v)
		}
	}
	for i, v := range orderB {
		if v == i+1 {
			t.Fatalf("错位排列不应有固定点(B 卷题序必与 A 不同):%v", orderB)
		}
	}
	if fmt.Sprint(lb.Subjectives) != fmt.Sprint(la.Subjectives) {
		t.Fatalf("主观题区必须与 A 完全一致")
	}
}

// abLayoutMixed 构造规格可区分的版式(选项数 4/5 交替),用于题序一致性校验测试。
func abLayoutMixed(order []int) card.Layout {
	l := card.Layout{Version: 2, Title: "AB-mixed 卷", RegDigits: 8, RegMode: card.RegFillQR, Columns: 2}
	for i := 0; i < 4; i++ {
		l.Questions = append(l.Questions, card.QuestionSpec{Kind: card.KindChoice, Options: 4 + i%2, Score: 1})
	}
	l.Subjectives = []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 40}}
	if order != nil {
		qs := make([]card.QuestionSpec, len(order))
		for i, src := range order {
			qs[i] = l.Questions[src]
		}
		l.Questions = qs
	}
	return l
}

func TestABAutoE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	keyA := []string{"A", "B", "A", "B", "A", "B"}
	rosterJSON := `[{"reg":"00000001","name":"甲","class":"三年二班"},{"reg":"00000002","name":"乙","class":"三年二班"},
		{"reg":"00000003","name":"丙","class":"三年二班"}]`
	fields := map[string]string{
		"layout": mustJSON(abLayout("", nil)), "key": mustJSON(keyA), "roster": rosterJSON,
		"subject": "数学", "threshold": "otsu", "abAuto": "1",
	}
	var body bytes.Buffer
	writeMultipartForm(&body, fields)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("abAuto 建考失败 %d: %s", resp.StatusCode, data)
	}
	var created map[string]any
	_ = json.Unmarshal(data, &created)
	examID := created["id"].(string)

	// 详情:hasB / 短号 / orderB 错位排列 / KeyB ≡ A 答案键
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["hasB"] != true || len(fmt.Sprint(d["shortId"])) != 4 {
		t.Fatalf("AB 详情异常:hasB=%v shortId=%v", d["hasB"], d["shortId"])
	}
	shortID := fmt.Sprint(d["shortId"])
	orderRaw, ok := d["orderB"].([]any)
	if !ok || len(orderRaw) != 6 {
		t.Fatalf("orderB 应为 6 项:%v", d["orderB"])
	}
	order0 := make([]int, 6) // 0-based:B 卡面第 i 题 = 规范第 order0[i]+1 题
	seen := map[int]bool{}
	for i, v := range orderRaw {
		n := int(v.(float64))
		if n < 1 || n > 6 || seen[n] {
			t.Fatalf("orderB 应为 1..6 的排列:%v", orderRaw)
		}
		seen[n] = true
		order0[i] = n - 1
		if n == i+1 {
			t.Fatalf("自动生成的 B 卷题序应与 A 不同(错位排列):%v", orderRaw)
		}
	}
	keyBOut := fmt.Sprint(d["keyB"])
	if keyBOut != fmt.Sprint(keyA) {
		t.Fatalf("KeyB 应与 A 答案键全等(规范序共用):%s vs %v", keyBOut, keyA)
	}

	// 合成扫描:A 卷(生1,规范序作答)、B 卷(生2,按 B 卡面序作答)、无卷种点旧卡(生3)
	layoutA, layoutB := abLayout("A", nil), abLayout("B", order0)
	ansBCardFace := make([]string, 6)
	for i := range ansBCardFace {
		ansBCardFace[i] = keyA[order0[i]]
	}
	var scans bytes.Buffer
	writeScanMultipart(&scans, map[string][]byte{
		"s1.jpg": abScan(t, layoutA, "00000001", shortID+"1", keyA, 21),
		"s2.jpg": abScan(t, layoutB, "00000002", shortID+"2", ansBCardFace, 23),
		"s3.jpg": abScan(t, abLayout("", nil), "00000003", "", keyA, 27),
	})
	resp, data = admin.do("POST", "/api/v1/exams/"+examID+"/scans", &scans, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("补传失败 %d: %s", resp.StatusCode, data)
	}

	// 报告:A/B 两版按共用答案键都得满分;卷种待指派卷不进报告
	objOf := func() map[string]float64 {
		rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
		m := map[string]float64{}
		for _, s := range rep["students"].([]any) {
			sm := s.(map[string]any)
			m[sm["regNumber"].(string)] = sm["objective"].(float64)
		}
		return m
	}
	obj := objOf()
	if obj["00000001"] != 6 || obj["00000002"] != 6 {
		t.Fatalf("A/B 卷按共用答案键应各得 6 分:%v", obj)
	}
	if _, ok := obj["00000003"]; ok {
		t.Fatalf("卷种待指派卷不应进报告:%v", obj)
	}

	// 修正答案键(第 1 题 A→B):KeyB 同步,两卷同步重统(生1 第 1 题答 A 失分)
	admin.json("POST", "/api/v1/exams/"+examID+"/key", map[string]any{
		"key": []string{"B", "B", "A", "B", "A", "B"}, "variant": "A"}, http.StatusOK)
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if fmt.Sprint(d["keyB"]) != fmt.Sprint([]string{"B", "B", "A", "B", "A", "B"}) {
		t.Fatalf("修正答案键后 KeyB 应同步:%v", d["keyB"])
	}
	if obj := objOf(); obj["00000001"] != 5 || obj["00000002"] != 5 {
		t.Fatalf("答案键修正后 A/B 卷应同步重统(各 5 分):%v", obj)
	}

	// 卷种指派 + 个性化出卡
	admin.json("POST", "/api/v1/exams/"+examID+"/assign-variant", map[string]any{"student": 3, "variant": "A"}, http.StatusOK)
	for _, v := range []string{"A", "B"} {
		pResp, pdf := admin.do("GET", "/api/v1/exams/"+examID+"/cards.pdf?variant="+v, nil, "")
		if pResp.StatusCode != http.StatusOK || !strings.HasPrefix(string(pdf), "%PDF") {
			t.Fatalf("cards.pdf(%s) 异常:%d", v, pResp.StatusCode)
		}
	}
}

// TestABKey2Mismatch 手填路径:B 卷答案键与 A 卷答案键在规范序下不一致 → 400
// (AB 卷只打乱题序,两卷共用同一份参考答案;独立异内容 B 键只会有误导性)。
func TestABKey2Mismatch(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// B 卡面序 = A 的 2,1,4,3(0-based 1,0,3,2);正确 B 卡面键应为 B,A,D,C,故意给 A,B,C,D
	fields := map[string]string{
		"layout": mustJSON(abLayoutMixed(nil)), "key": mustJSON([]string{"A", "B", "C", "D"}),
		"layout2": mustJSON(abLayoutMixed([]int{1, 0, 3, 2})), "key2": mustJSON([]string{"A", "B", "C", "D"}),
		"order2": "2,1,4,3",
	}
	var body bytes.Buffer
	writeMultipartForm(&body, fields)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "共用同一份参考答案") {
		t.Fatalf("B 键与 A 键不一致应 400:%d %s", resp.StatusCode, data)
	}
}

// TestABOrder2LayoutMismatch 手填路径:B 版式卡面题序与题序对照矛盾(规格可区分)→ 400。
func TestABOrder2LayoutMismatch(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// B 卡面实际是 A 的 2,1,4,3 题,却宣称题序对照为恒等 1,2,3,4 → 卡面第 1 题(5 选项)
	// 与对照所指 A 第 1 题(4 选项)规格不符
	fields := map[string]string{
		"layout": mustJSON(abLayoutMixed(nil)), "key": mustJSON([]string{"A", "B", "C", "D"}),
		"layout2": mustJSON(abLayoutMixed([]int{1, 0, 3, 2})), "key2": mustJSON([]string{"B", "A", "D", "C"}),
		"order2": "1,2,3,4",
	}
	var body bytes.Buffer
	writeMultipartForm(&body, fields)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "题序对照矛盾") {
		t.Fatalf("版式与题序对照矛盾应 400:%d %s", resp.StatusCode, data)
	}
}

// TestABKey2AllEmpty 高级手填路径:B 答案格全空(全空串数组,前端网格未填的真实形态)
// 必须视同「与 A 卷答案同步」而非显式异内容键 —— 2026-10-25 复查抓出的 400 死胡同。
func TestABKey2AllEmpty(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	order := []int{1, 0, 3, 2}
	fields := map[string]string{
		"layout": mustJSON(abLayoutMixed(nil)), "key": mustJSON([]string{"A", "B", "C", "D"}),
		"layout2": mustJSON(abLayoutMixed(order)), "key2": mustJSON([]string{"", "", "", ""}),
		"order2": "2,1,4,3",
	}
	var body bytes.Buffer
	writeMultipartForm(&body, fields)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("key2 全空应视同与 A 同步建考成功:%d %s", resp.StatusCode, data)
	}
	var created map[string]any
	_ = json.Unmarshal(data, &created)
	d := admin.json("GET", "/api/v1/exams/"+created["id"].(string), nil, http.StatusOK)
	if fmt.Sprint(d["keyB"]) != fmt.Sprint([]string{"A", "B", "C", "D"}) {
		t.Fatalf("key2 全空时 KeyB 应等于 A 答案键:%v", d["keyB"])
	}
}

// TestABAutoExclusive abAuto=1 与手填字段同传 → 400(静默忽略会掩盖调用方真实意图)。
func TestABAutoExclusive(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	for name, extra := range map[string]string{
		"layout2": mustJSON(abLayout("", []int{1, 0})), "key2": `["A","A","A","A","A","A"]`, "order2": "1,2,3,4,5,6",
	} {
		fields := map[string]string{
			"layout": mustJSON(abLayout("", nil)), "key": mustJSON([]string{"A", "B", "A", "B", "A", "B"}),
			"abAuto": "1", name: extra,
		}
		var body bytes.Buffer
		writeMultipartForm(&body, fields)
		resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), name) {
			t.Fatalf("abAuto=1 + %s 应 400:%d %s", name, resp.StatusCode, data)
		}
	}
}

// TestABAutoWithoutObjectives 无客观题/单客观题版式开 abAuto → 400(打乱题序无意义)。
func TestABAutoWithoutObjectives(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	l := abLayout("", nil)
	l.Questions = nil // 只有主观题
	fields := map[string]string{
		"layout": mustJSON(l), "key": "[]", "abAuto": "1",
	}
	var body bytes.Buffer
	writeMultipartForm(&body, fields)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "至少 2 道客观题") {
		t.Fatalf("无客观题开 abAuto 应 400:%d %s", resp.StatusCode, data)
	}
}
