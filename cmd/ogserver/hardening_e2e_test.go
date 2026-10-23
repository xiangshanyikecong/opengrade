package main

// 加固项端到端:重答检测(同考号重复上传)、空白卡告警、题库-考试使用史回记、
// 成绩册导出班级列。

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

// hardFixture 生成指定考号/作答的扫描卷(畸变种子固定)。
func hardScan(t *testing.T, layout card.Layout, reg string, answers []string, seed int64) []byte {
	t.Helper()
	sheet, err := layout.Build()
	if err != nil {
		t.Fatal(err)
	}
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

func hardLayout() card.Layout {
	return card.Layout{
		Version: 2, Title: "HARD E2E", RegDigits: 8, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 42}},
	}
}

func postExam(t *testing.T, c *e2eClient, layout card.Layout, key []string, roster string, scans [][]byte, bankID string) string {
	t.Helper()
	if bankID != "" {
		for i := range layout.Questions {
			layout.Questions[i].BankID = bankID
		}
	}
	var body bytes.Buffer
	files := make([]multipartFile, len(scans))
	for i, s := range scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("h_%02d.jpg", i+1), ctype: "image/jpeg", data: s}
	}
	writeExamForm(&body, layout, key, roster, files...)
	resp, data := c.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	id, _ := m["id"].(string)
	return id
}

func TestHardeningE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	layout := hardLayout()
	sheet, _ := layout.Build()
	rng := rand.New(rand.NewSource(99))
	key := randomAnswers(rng, sheet)

	// ---------- 1. 题库回记 + 重答检测 + 空白卡 ----------
	bankItem := admin.json("POST", "/api/v1/bank", map[string]any{
		"kind": "choice", "options": 4, "score": 2, "stem": "1+1=?", "answer": "B", "knowledge": "算术", "subject": "数学",
	}, http.StatusCreated)
	bankID, _ := bankItem["id"].(string)

	scans := [][]byte{
		hardScan(t, layout, "01000001", []string{"A", "B"}, 4001), // 正常卷
		hardScan(t, layout, "01000001", []string{"C", "A"}, 4002), // 同考号重答
		hardScan(t, layout, "01000002", []string{"", ""}, 4003),   // 空白卡
	}
	roster := `[{"reg":"01000001","name":"张一","classes":["三年二班","物理走班A"]},{"reg":"01000002","name":"张二","class":"三年二班"}]`
	examID := postExam(t, admin, layout, key, roster, scans, bankID)

	t.Run("A_重答与空白卡告警", func(t *testing.T) {
		d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
		warns, _ := d["warnings"].([]any)
		got := map[string][]string{}
		for _, w := range warns {
			m, _ := w.(map[string]any)
			stu := fmt.Sprintf("%v", m["student"])
			for _, tx := range m["texts"].([]any) {
				got[stu] = append(got[stu], fmt.Sprintf("%v", tx))
			}
		}
		joined := strings.Join(got["2"], ";")
		if !strings.Contains(joined, "疑似重答") {
			t.Fatalf("卷 2 缺重答告警:%v", got)
		}
		joined3 := strings.Join(got["3"], ";")
		if !strings.Contains(joined3, "疑似空白卡") {
			t.Fatalf("卷 3 缺空白卡告警:%v", got)
		}
	})

	t.Run("B_题库使用史回记", func(t *testing.T) {
		found := false
		for _, it := range admin.jsonArr("GET", "/api/v1/bank", http.StatusOK) {
			if it["id"] == bankID {
				used, _ := it["usedIn"].([]any)
				if len(used) != 1 || used[0] != examID {
					t.Fatalf("usedIn = %v,期望 [%s]", it["usedIn"], examID)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("题库中未找到该题")
		}
	})

	t.Run("C_成绩册班级列与姓名", func(t *testing.T) {
		resp, data := admin.do("GET", "/api/v1/exams/"+examID+"/export.csv", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("导出 CSV %d", resp.StatusCode)
		}
		csv := string(data)
		if !strings.Contains(csv, "姓名,班级") {
			t.Fatal("CSV 表头缺 姓名/班级 列")
		}
		if !strings.Contains(csv, "张一,三年二班|物理走班A") {
			t.Fatalf("CSV 缺走班班级列(行政班|教学班):%s", csv[:min(300, len(csv))])
		}
	})
}
