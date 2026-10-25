package main

// 选做题/题号区端到端回归(版式 v3):卡面题号区涂选 → OMR 判读 →
// "判分只取所选"统分 → 细目表选做人数 → 切片列表涂选明细 → 学生端未选不计分。
// 漏涂回退(组内恰一题已批阅则取该题)与缺考/手工补录口径在 score 包单元测试覆盖。

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

func TestElectiveQuestionsE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	student := newClient(t, ts.URL)
	student.login("s0001", "student123") // 考号 00000001(卷 1)

	// 版式:30 客观 + 主观题 1(填空 5 分)+ 主观题 2、3(各 10 分解答,选做组 A,全局题号 32/33)
	layout := card.DefaultLayout()
	layout.Subjectives[1] = card.SubjectiveSpec{Kind: card.KindSubjective, Score: 10, HeightMM: 30, Elective: "A"}
	layout.Subjectives[2] = card.SubjectiveSpec{Kind: card.KindSubjective, Score: 10, HeightMM: 30, Elective: "A"}
	sheet, err := layout.Build()
	if err != nil {
		t.Fatal(err)
	}
	key := make([]string, len(layout.Questions))
	for i, q := range layout.Questions {
		if q.Kind == card.KindJudge {
			key[i] = "T" // 判断题契约用 T/F
		} else {
			key[i] = "A"
		}
	}
	// 三张模拟扫描卷:客观全对;卷 1 涂主观题 2、卷 2 涂主观题 3、卷 3 题号区漏涂
	type stu struct {
		reg    string
		elect  int // 涂点下标;-1 = 漏涂
		eMarks map[int]int
	}
	students := []stu{
		{reg: "00000001", elect: 0, eMarks: map[int]int{2: 0}},
		{reg: "00000002", elect: 1, eMarks: map[int]int{2: 1}},
		{reg: "00000003", elect: -1},
	}
	var files []multipartFile
	for i, s := range students {
		fill := card.Filling{Answers: key, Electives: s.eMarks}
		for _, ch := range s.reg {
			fill.Reg = append(fill.Reg, int(ch-'0'))
		}
		canvas := card.RenderPNG(sheet, &fill)
		distorted := synth.Distort(canvas.ToGray(), rand.New(rand.NewSource(int64(7000+i))))
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, distorted, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		files = append(files, multipartFile{field: "images", name: fmt.Sprintf("scan_%02d.jpg", i+1), ctype: "image/jpeg", data: jbuf.Bytes()})
	}
	rosterJSON := `[` +
		`{"reg":"00000001","name":"李雷","class":"三年二班"},` +
		`{"reg":"00000002","name":"韩梅梅","class":"三年二班"},` +
		`{"reg":"00000003","name":"王小明","class":"三年二班"}]`
	var body bytes.Buffer
	writeExamForm(&body, layout, key, rosterJSON, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考: %d\n%s", resp.StatusCode, data)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	examID := m["id"].(string)

	// 考试详情:进度带选做标签
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if p := d["progress"].([]any)[1].(map[string]any); p["elective"] != "A" {
		t.Fatalf("进度应带选做组标签: %v", p)
	}

	// 批阅:主观题 1 = 5/3/-;主观题 2 = 8/4/-;主观题 3 = 7/9/6(卷 1 的 7 分不计入)
	type grade struct {
		student, question int
		score             float64
	}
	for _, g := range []grade{
		{1, 1, 5}, {2, 1, 3},
		{1, 2, 8}, {2, 2, 4},
		{1, 3, 7}, {2, 3, 9}, {3, 3, 6},
	} {
		admin.json("POST", "/api/v1/grades", map[string]any{
			"examId": examID, "student": g.student, "question": g.question, "score": g.score, "graded": true,
		}, http.StatusOK)
	}

	// 报告:卷面满分 = 60 客观 + 5 填空 + 10 选做(一组一题)= 75;
	// 卷 1 = 60+5+8 = 73;卷 2 = 60+3+9 = 72;卷 3(漏涂回退,组内仅题 3 已批)= 60+6 = 66
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	if rep["total"].(float64) != 75 {
		t.Fatalf("卷面满分 = %v,期望 75(选做组只计一题)", rep["total"])
	}
	type want struct {
		score float64
		rank  int
		subj  float64
	}
	for i, w := range []want{{73, 1, 13}, {72, 2, 12}, {66, 3, 6}} {
		s := rep["students"].([]any)[i].(map[string]any)
		if s["score"].(float64) != w.score || int(s["rank"].(float64)) != w.rank || s["subjective"].(float64) != w.subj {
			t.Fatalf("卷 %d = %v,期望 总分 %v 名次 %d 主观 %v", i+1, s, w.score, w.rank, w.subj)
		}
	}
	// 逐题:选做题统计分母 = 实际选做人数
	q32 := rep["perQuestion"].([]any)[31].(map[string]any)
	q33 := rep["perQuestion"].([]any)[32].(map[string]any)
	if q32["elective"] != "A" || q32["chosen"].(float64) != 1 || q32["mean"].(float64) != 8 {
		t.Fatalf("题 32 统计 = %v,期望 选做 1 人均分 8(卷 2 未选此题,4 分不计)", q32)
	}
	if q33["chosen"].(float64) != 2 || q33["mean"].(float64) != 7.5 {
		t.Fatalf("题 33 统计 = %v,期望 选做 2 人(卷 2 涂选 + 卷 3 漏涂回退)均分 7.5", q33)
	}

	// 切片列表:携带该生题号区涂选与判分计入题
	sl := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=2&grading=1", examID), http.StatusOK)
	marks := func(i int) ([]any, float64) {
		e := sl[i]["elective"].(map[string]any)
		return e["marks"].([]any), e["chosen"].(float64)
	}
	if mk, ch := marks(0); len(mk) != 1 || int(mk[0].(float64)) != 0 || ch != 2 {
		t.Fatalf("卷 1 涂选明细异常: %v %v", mk, ch)
	}
	if mk, ch := marks(1); len(mk) != 1 || int(mk[0].(float64)) != 1 || ch != 3 {
		t.Fatalf("卷 2 涂选明细异常: %v %v", mk, ch)
	}
	if _, ch := marks(2); ch != 3 {
		t.Fatalf("卷 3 漏涂回退应计入题 3: chosen=%v", ch)
	}

	// 发布后学生端:未选题标注 notCounted,不进错题本
	// 发布门禁:补齐卷 3 漏评的填空题(0 分,不改变其总分与选做统计 —— 漏涂回退语义只依赖题 3 的批阅)
	admin.json("POST", "/api/v1/grades", map[string]any{"examId": examID, "student": 3, "question": 1, "score": 0, "graded": true}, http.StatusOK)
	admin.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)
	sd := student.json("GET", "/api/v1/student/exams/"+examID, nil, http.StatusOK)
	wrongs := sd["wrongQuestions"].([]any)
	if len(wrongs) != 1 || int(wrongs[0].(float64)) != 32 {
		t.Fatalf("卷 1 错题本 = %v,期望仅 [32](未选的 33 不计)", wrongs)
	}
	q33v := sd["questions"].([]any)[32].(map[string]any)
	if q33v["notCounted"] != true || q33v["elective"] != "32/33" {
		t.Fatalf("学生端题 33 应标注未选不计分: %v", q33v)
	}
	if sd["score"].(float64) != 73 {
		t.Fatalf("学生端总分 = %v,期望 73", sd["score"])
	}
}
