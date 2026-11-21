package main

// AB 卷全链路 e2e:
//   - 建考校验(主观题区必须一致 / 题序对照必须是排列 / 客观题集合一致);
//   - 卷种点 + 二维码复合内容(考试短号+卷种位+考号):A/B 混扫自动分版判读,
//     B 卷作答按题序对照归一为规范题号 —— 两版卷按同一份规范答案键都得满分;
//   - 卷种判不出(无卷种点的旧形态卡)→ 暂扣客观题(不进报告)→ 人工指派 A 卷后恢复;
//   - 复合码考试短号不符 → 告警「疑似其它考试」但不影响判分;
//   - 个性化出卡 cards.pdf(A/B 卷按名册,卷头复合码)。

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

// abLayout 构造 AB 测试版式:6 道单选(各 1 分,4 选项)+ 1 道解答(10 分),fill+qr 考号通道。
// order 非 nil 时按其重排客观题(B 卡面第 i 题 = 原第 order[i] 题,0-based)。
func abLayout(variant string, order []int) card.Layout {
	l := card.Layout{Version: 2, Title: "AB-e2e 卷", RegDigits: 8, RegMode: card.RegFillQR, Columns: 2}
	for i := 0; i < 6; i++ {
		l.Questions = append(l.Questions, card.QuestionSpec{Kind: card.KindChoice, Options: 4, Score: 1})
	}
	l.Subjectives = []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 40}}
	if order != nil {
		qs := make([]card.QuestionSpec, len(order))
		for i, src := range order {
			qs[i] = l.Questions[src]
		}
		l.Questions = qs
	}
	l.Variant = variant
	return l
}

// abScan 合成一张扫描件(sheet 对应卷种的卡;answers 按该卡面题序;codePrefix 为复合码前缀,空=纯考号码)。
// seed 用已验证可判读的畸变种子(避开 synth 少数极端畸变档,与产品无关,纯测试夹具稳定性)。
func abScan(t *testing.T, l card.Layout, reg, codePrefix string, answers []string, seed int64) []byte {
	t.Helper()
	sheet, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	fill := card.Filling{Answers: answers, CodePrefix: codePrefix}
	for _, ch := range reg {
		fill.Reg = append(fill.Reg, int(ch-'0'))
	}
	distorted := synth.Distort(card.RenderPNG(sheet, &fill).ToGray(), rand.New(rand.NewSource(seed)))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, distorted, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestABVariantsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// B 卡面序:A 卷题 2,3,4,5,6,1(即 orderB = 2,3,4,5,6,1)
	order := []int{1, 2, 3, 4, 5, 0}
	orderB := []int{2, 3, 4, 5, 6, 1}
	keyA := []string{"A", "B", "A", "B", "A", "B"} // 规范序答案键
	keyB := make([]string, 6)                      // B 卡面序答案(建考时由服务端归一为规范序)
	for i := range keyB {
		keyB[i] = keyA[orderB[i]-1]
	}
	rosterJSON := `[{"reg":"00000001","name":"甲","class":"三年二班"},{"reg":"00000002","name":"乙","class":"三年二班"},
		{"reg":"00000003","name":"丙","class":"三年二班"},{"reg":"00000004","name":"丁","class":"三年二班"}]`

	mkForm := func(la, lb card.Layout, key, key2, order2 string) *bytes.Buffer {
		var b bytes.Buffer
		fields := map[string]string{
			"layout": mustJSON(la), "key": key, "roster": rosterJSON,
			"subject": "数学", "threshold": "otsu",
		}
		if lb.Version != 0 {
			fields["layout2"] = mustJSON(lb)
			fields["key2"] = key2
			fields["order2"] = order2
		}
		writeMultipartForm(&b, fields)
		return &b
	}

	// —— 建考校验:三个非法 B 卷必须 400 ——
	badSubj := abLayout("", order)
	badSubj.Subjectives[0].Score = 12 // 主观题区与 A 不一致
	resp, data := admin.do("POST", "/api/v1/exams", mkForm(abLayout("", nil), badSubj,
		`["A","B","A","B","A","B"]`, `["B","A","B","A","B","A"]`, "2,3,4,5,6,1"),
		"multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "主观题区") {
		t.Fatalf("主观题不一致应 400:%d %s", resp.StatusCode, data)
	}
	resp, data = admin.do("POST", "/api/v1/exams", mkForm(abLayout("", nil), abLayout("", order),
		`["A","B","A","B","A","B"]`, `["B","A","B","A","B","A"]`, "1,1,2,3,4,4"),
		"multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "排列") {
		t.Fatalf("order2 非排列应 400:%d %s", resp.StatusCode, data)
	}
	badOpts := abLayout("", order)
	badOpts.Questions[0].Options = 5 // 客观题集合与 A 不一致
	resp, data = admin.do("POST", "/api/v1/exams", mkForm(abLayout("", nil), badOpts,
		`["A","B","A","B","A","B"]`, `["B","A","B","A","B","A"]`, "2,3,4,5,6,1"),
		"multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "客观题集合") {
		t.Fatalf("客观题集合不一致应 400:%d %s", resp.StatusCode, data)
	}

	// —— 合法建考(先建档拿考试短号,再合成带复合码的扫描件)——
	resp, data = admin.do("POST", "/api/v1/exams", mkForm(abLayout("", nil), abLayout("", order),
		`["A","B","A","B","A","B"]`, `["B","A","B","A","B","A"]`, "2,3,4,5,6,1"),
		"multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("AB 建考失败 %d: %s", resp.StatusCode, data)
	}
	var created map[string]any
	_ = json.Unmarshal(data, &created)
	examID := created["id"].(string)
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["hasB"] != true || len(fmt.Sprint(d["shortId"])) != 4 {
		t.Fatalf("AB 详情异常:hasB=%v shortId=%v", d["hasB"], d["shortId"])
	}
	shortID := fmt.Sprint(d["shortId"])

	// —— 合成扫描:A 卷(学生1)、B 卷(学生2,卡面序作答)、无卷种点旧形态卡(学生3)、短号不符 A 卡(学生4)——
	ansCanonical := keyA
	ansBCardFace := make([]string, 6)
	for i := range ansBCardFace {
		ansBCardFace[i] = ansCanonical[orderB[i]-1] // 与 keyB 相同:卡面序的满分作答
	}
	layoutA, layoutB := abLayout("A", nil), abLayout("B", order)
	layoutPlain := abLayout("", nil) // 无卷种点、纯考号码(模拟旧形态/漏印卷种)
	var body bytes.Buffer
	writeScanMultipart(&body, map[string][]byte{
		"s1.jpg": abScan(t, layoutA, "00000001", shortID+"1", ansCanonical, 11),
		"s2.jpg": abScan(t, layoutB, "00000002", shortID+"2", ansBCardFace, 13),
		"s3.jpg": abScan(t, layoutPlain, "00000003", "", ansCanonical, 16),
		"s4.jpg": abScan(t, layoutA, "00000004", "99991", ansCanonical, 19),
	})
	resp, data = admin.do("POST", "/api/v1/exams/"+examID+"/scans", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AB 补传失败 %d: %s", resp.StatusCode, data)
	}

	// —— 报告:A/B 两版按同一份规范答案键都得满分(证明 B 卷归一);卷种待指派卷不进报告 ——
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	objByReg := map[string]float64{}
	for _, s := range rep["students"].([]any) {
		sm := s.(map[string]any)
		objByReg[sm["regNumber"].(string)] = sm["objective"].(float64)
	}
	if objByReg["00000001"] != 6 || objByReg["00000002"] != 6 {
		t.Fatalf("A/B 卷规范归一后客观题应各得 6 分:%v", objByReg)
	}
	if objByReg["00000004"] != 6 {
		t.Fatalf("短号不符只告警不影响判分:%v", objByReg)
	}
	if _, ok := objByReg["00000003"]; ok {
		t.Fatalf("卷种待指派卷不应进报告:%v", objByReg)
	}

	// —— 异常清单:卷种暂扣 + 短号不符告警 ——
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	warnText := ""
	for _, w := range d["warnings"].([]any) {
		wm := w.(map[string]any)
		warnText += strings.Join(textsOf(wm), ";") + "\n"
	}
	if !strings.Contains(warnText, "卷种无法识别") {
		t.Fatalf("缺卷种暂扣告警:%s", warnText)
	}
	if !strings.Contains(warnText, "疑似其它考试") {
		t.Fatalf("短缺考试短号核对告警:%s", warnText)
	}

	// —— 人工指派卷种(学生3 = A 卷):重读客观题,恢复进报告且满分 ——
	admin.json("POST", "/api/v1/exams/"+examID+"/assign-variant", map[string]any{"student": 3, "variant": "A"}, http.StatusOK)
	rep = admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	for _, s := range rep["students"].([]any) {
		sm := s.(map[string]any)
		if sm["regNumber"] == "00000003" && sm["objective"].(float64) != 6 {
			t.Fatalf("指派 A 卷后客观题应 6 分:%v", sm)
		}
	}

	// —— 个性化出卡(A/B 卷按名册,卷头复合码)——
	for _, v := range []string{"A", "B"} {
		pResp, pdf := admin.do("GET", "/api/v1/exams/"+examID+"/cards.pdf?variant="+v, nil, "")
		if pResp.StatusCode != http.StatusOK || !strings.HasPrefix(string(pdf), "%PDF") {
			t.Fatalf("cards.pdf(%s) 异常:%d", v, pResp.StatusCode)
		}
	}
}

func textsOf(wm map[string]any) []string {
	out := []string{}
	for _, t := range wm["texts"].([]any) {
		out = append(out, fmt.Sprint(t))
	}
	return out
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// writeScanMultipart 构造 images[] 文件 multipart(扫描上传用)。
func writeScanMultipart(buf *bytes.Buffer, files map[string][]byte) {
	for name, data := range files {
		fmt.Fprintf(buf, "--%s\r\nContent-Disposition: form-data; name=\"images\"; filename=\"%s\"\r\nContent-Type: image/jpeg\r\n\r\n", multipartBoundary, name)
		buf.Write(data)
		fmt.Fprint(buf, "\r\n")
	}
	fmt.Fprintf(buf, "--%s--\r\n", multipartBoundary)
}
