package main

// 考号码制通道(条码/二维码)端到端:
// 建考(regMode=barcode / qr / fill+barcode + 合成畸变扫描件)→ 全链路考号判读 →
// 双通道漏涂修复 → 统分报告考号对齐 → 学生端(考号契约)可见本人考试。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/synth"
)

// codeE2EFixture 构造一场码制通道考试的版式、参考答案、名册与 5 张畸变扫描卷
// (考号 00000001..5)。双通道模式下第 3 卷考号第 4 位漏涂,应由码修复。
func codeE2EFixture(t *testing.T, regMode string) (card.Layout, []string, string, [][]byte) {
	t.Helper()
	layout := card.Layout{
		Version: 2, Title: "CODE E2E", RegDigits: 8, RegMode: regMode, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindChoice, Options: 4, Score: 2},
			{Kind: card.KindJudge, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 42}},
	}
	sheet, err := layout.Build()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(88))
	key := randomAnswers(rng, sheet)

	var rosterBuf bytes.Buffer
	rosterBuf.WriteString("[")
	var scans [][]byte
	for i := 0; i < 5; i++ {
		reg := fmt.Sprintf("%08d", i+1)
		if i > 0 {
			rosterBuf.WriteString(",")
		}
		fmt.Fprintf(&rosterBuf, `{"reg":"%s","name":"学生%d","class":"三年二班"}`, reg, i+1)
		fill := card.Filling{Answers: randomAnswers(rng, sheet)}
		for _, ch := range reg {
			fill.Reg = append(fill.Reg, int(ch-'0'))
		}
		if regMode == card.RegFillBarcode && i == 2 { // 第 3 卷漏涂一位,条码修复
			fill.RegUnfilled = []bool{false, false, false, true, false, false, false, false}
		}
		distorted := synth.Distort(card.RenderPNG(sheet, &fill).ToGray(), rand.New(rand.NewSource(int64(2000+i))))
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		scans = append(scans, jbuf.Bytes())
	}
	rosterBuf.WriteString("]")
	return layout, key, rosterBuf.String(), scans
}

func createCodeExam(t *testing.T, base, regMode string) string {
	t.Helper()
	layout, key, roster, scans := codeE2EFixture(t, regMode)
	var body bytes.Buffer
	files := make([]multipartFile, len(scans))
	for i, s := range scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("card_%02d.jpg", i+1), ctype: "image/jpeg", data: s}
	}
	writeExamForm(&body, layout, key, roster, files...)
	body.Truncate(body.Len() - len(fmt.Sprintf("--%s--\r\n", multipartBoundary)))
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"subject\"\r\n\r\n数学\r\n", multipartBoundary)
	fmt.Fprintf(&body, "--%s--\r\n", multipartBoundary)

	c := newClient(t, base)
	c.login("teacher1", "teach123")
	resp, data := c.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("建考响应缺 id: %s", data)
	}
	return id
}

func TestCodeChannelsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	teacher := newClient(t, ts.URL)
	teacher.login("teacher1", "teach123")

	for _, mode := range []string{card.RegBarcode, card.RegQR, card.RegFillBarcode} {
		t.Run("通道_"+mode, func(t *testing.T) {
			id := createCodeExam(t, ts.URL, mode)
			// 统分报告:5 名学生考号全部按码对齐(双通道含漏涂修复的第 3 卷)
			rep := teacher.json("GET", "/api/v1/exams/"+id+"/report", nil, http.StatusOK)
			students, _ := rep["students"].([]any)
			if len(students) != 5 {
				t.Fatalf("报告学生数 = %d,期望 5", len(students))
			}
			regs := map[string]bool{}
			for _, s := range students {
				m, _ := s.(map[string]any)
				if r, _ := m["regNumber"].(string); r != "" {
					regs[r] = true
				}
			}
			for i := 1; i <= 5; i++ {
				want := fmt.Sprintf("%08d", i)
				if !regs[want] {
					t.Fatalf("考号 %s 缺失(已识别:%v)", want, regs)
				}
			}
			// 发布后学生端考号契约:s0001(考号 00000001)能在本人列表看到这场考试
			teacher.json("POST", "/api/v1/exams/"+id+"/publish", map[string]any{"published": true}, http.StatusOK)
			stu := newClient(t, ts.URL)
			stu.login("s0001", "student123")
			found := false
			for _, e := range stu.jsonArr("GET", "/api/v1/student/exams", http.StatusOK) {
				if e["id"] == id {
					found = true
				}
			}
			if !found {
				t.Fatal("学生 s0001 未在学生端看到码制通道考试(考号契约断裂)")
			}
		})
	}
}
