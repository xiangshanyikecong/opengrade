package main

// 复查报告回归测试(B 组/A 组修复的行为锚点):
//   - B-1 跨批合并三个边界:混合批次切片重编号、完整卷+残页不合并;
//   - B-2 软告警精确匹配:三类含「已按」的硬告警必须阻断;
//   - B-3 旧数据 Ver 迁移:无 ver 的已评记录改分不再永久 409;
//   - B-4 卷种待定的卷不算「已裁决」;
//   - A-1 读写并发冒烟(配 go test -race 验证快照隔离)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

func xbLayout() card.Layout {
	return card.Layout{
		Version: 2, Title: "XB MIX", RegDigits: 8, RegMode: card.RegFillBarcode, Columns: 2,
		Questions: []card.QuestionSpec{
			{Kind: card.KindChoice, Options: 4, Score: 2}, {Kind: card.KindChoice, Options: 4, Score: 2},
		},
		Subjectives: []card.SubjectiveSpec{
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
			{Kind: card.KindSubjective, Score: 10, HeightMM: 42}, {Kind: card.KindSubjective, Score: 10, HeightMM: 42},
		},
	}
}

// TestCrossBatchMergeMixedBatchE2E 混合批次:B-1 修复的核心回归。
// 批次里被合并的学生夹在中间时,其后学生的切片必须按新位置重编
// (旧实现烙着剔除前卷号 → 该生阅卷端全 Missing、后续批次覆盖切片文件);
// 存量完整卷后再扫到该生单页,不得并入(旧实现截出页号重复的假完整卷)。
func TestCrossBatchMergeMixedBatchE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	layout := xbLayout()
	book, err := layout.BuildBook()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(654))
	key := randomAnswers(rng, book[0])
	roster := `[{"reg":"03000001","name":"混合甲","class":"三年二班"},{"reg":"03000002","name":"混合乙","class":"三年二班"},{"reg":"03000003","name":"混合丙","class":"三年二班"}]`

	// 第一批:甲只扫到第 1 页(残卷),乙两页齐全
	var b1 bytes.Buffer
	writeExamForm(&b1, layout, key, roster,
		multipartFile{field: "images", name: "a1.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[0], "03000001", key, 601)},
		multipartFile{field: "images", name: "b1.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[0], "03000002", key, 602)},
		multipartFile{field: "images", name: "b2.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[1], "03000002", nil, 603)},
	)
	resp, data := admin.do("POST", "/api/v1/exams", &b1, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID := m["id"].(string)

	// 第二批:甲的第 2 页(合并目标)在前,丙的两页在后 —— 被合并学生夹在中间
	var b2 bytes.Buffer
	writeExamForm(&b2, layout, key, "",
		multipartFile{field: "images", name: "a2.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[1], "03000001", nil, 701)},
		multipartFile{field: "images", name: "c1.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[0], "03000003", key, 702)},
		multipartFile{field: "images", name: "c2.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[1], "03000003", nil, 703)},
	)
	resp2, data2 := admin.do("POST", "/api/v1/exams/"+examID+"/scans", &b2, "multipart/form-data; boundary="+multipartBoundary)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("补传失败 %d: %s", resp2.StatusCode, data2)
	}
	var m2 map[string]any
	_ = json.Unmarshal(data2, &m2)
	if m2["added"].(float64) != 1 {
		t.Fatalf("应只追加丙 1 卷(甲并入存量),added = %v", m2["added"])
	}
	if merged, _ := m2["merged"].([]any); len(merged) != 1 || merged[0].(float64) != 1 {
		t.Fatalf("应报告合并进第 1 卷: %v", m2["merged"])
	}

	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["students"].(float64) != 3 {
		t.Fatalf("合并+追加后 students = %v,期望 3", d["students"])
	}
	// 丙(第 3 卷)的全部主观切片必须齐且不 Missing —— 中间剔除后切片卷号曾整体错位
	for q := 1; q <= 6; q++ {
		items := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=%d&grading=1", examID, q), http.StatusOK)
		if len(items) != 3 {
			t.Fatalf("第 %d 题切片数 = %d,期望 3: %v", q, len(items), items)
		}
		for _, it := range items {
			if it["missing"] == true {
				t.Fatalf("第 %d 题存在 Missing 切片(切片卷号错位): %v", q, it)
			}
		}
		// 切片图像可实际取回(文件名与卷号对齐)
		s3, _ := admin.do("GET", fmt.Sprintf("/api/v1/exams/%s/slice/3/%d", examID, q), nil, "")
		if s3.StatusCode != http.StatusOK {
			t.Fatalf("第 3 卷第 %d 题切片图像 HTTP %d(文件名未随重编落盘)", q, s3.StatusCode)
		}
	}
	// 丙的客观题按页 1 正确判分(B-1b 语义:重编/合并不得引起整页错位)
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	objC := -1.0
	for _, s := range rep["students"].([]any) {
		sm := s.(map[string]any)
		if sm["regNumber"] == "03000003" {
			objC = sm["objective"].(float64)
		}
	}
	if objC != 4 { // 2 题 × 2 分全对
		t.Fatalf("丙的客观题得分 = %v,期望 4(页 1 判读错位或答案没带上)", objC)
	}

	// 存量完整卷(甲)后再扫到单页:不得并入(旧实现可截出页号重复的假完整卷)
	var b3 bytes.Buffer
	writeExamForm(&b3, layout, key, "",
		multipartFile{field: "images", name: "a1r.jpg", ctype: "image/jpeg", data: mpScanPage(t, book[0], "03000001", key, 801)},
	)
	resp3, data3 := admin.do("POST", "/api/v1/exams/"+examID+"/scans", &b3, "multipart/form-data; boundary="+multipartBoundary)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("重扫单页失败 %d: %s", resp3.StatusCode, data3)
	}
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["students"].(float64) != 4 {
		t.Fatalf("完整卷重扫单页应走追加裁决(students=4),不得并入: %v", d["students"])
	}
	if dups, _ := d["duplicates"].([]any); len(dups) != 1 {
		t.Fatalf("重扫单页应产生重复卷裁决入口: %v", d["duplicates"])
	}
	// 甲(第 1 卷)页号不得出现重复:合并语义保持 [1,2]
	ex, _ := srv.Exams.Get(examID)
	if got := fmt.Sprint(ex.Results[0].PageNos); got != "[1 2]" {
		t.Fatalf("第 1 卷 PageNos = %v,期望 [1 2](不得出现重复页号的假完整卷)", got)
	}
}

// TestBlockingWarningClassification B-2:含「已按」的硬告警必须阻断,
// 只有精确登记过的自愈/确定口径文案才是信息类。
func TestBlockingWarningClassification(t *testing.T) {
	blocking := []string{
		"页点不可信(缺号/重复/旧卡),已按到达顺序合并——请人工核对该生各页顺序,确认无误可在异常清单「接受残卷」",
		"卷面为 B 卡但本场未配置 B 卷版式,已按 A 版式判读,客观题作答可疑——请核对纸质卷或作废",
		"第 1 页考号 03000001 与第 2 页 03000002 不一致,已按后者",
		"本批未能判读出考号,已按「每生连续 P 页」位置配对,请抽查核对归属",
		"考号 03000001 的卷仅识别到 1/2 页(缺第 2 页)",
		"疑似重答",
		"卷种无法识别(卷种点未判出且码无卷种位),客观题暂不计分——请在异常清单指派 A/B 卷",
	}
	for _, w := range blocking {
		if !blockingWarning(w) {
			t.Errorf("应判阻断(含「已按」但需人工裁决): %s", w)
		}
	}
	soft := []string{
		"考号填涂异常,已按条码修复",
		"考号填涂异常,已按二维码修复",
		"考号填涂 3 与条码 8 不一致,已按条码",
		"条码解码失败,已按填涂考号",
		"第 3 题 B 选项涂写偏浅,建议人工复核",
		"第 3 题多涂(AB),按错判处理",
		"第 17 题选做题号区未唯一涂选(2 处),判分按批阅结果计入",
		"卷种点(A 卷)与码内卷种位(B 卷)不一致,已按卷种点判读,请人工复核",
	}
	for _, w := range soft {
		if blockingWarning(w) {
			t.Errorf("应判信息类: %s", w)
		}
	}
}

// TestVerMigration B-3:乐观锁引入前的已评记录(无 ver)升级后可改分。
func TestVerMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := &Exam{
		ID:        "legacy1",
		CreatedAt: time.Now(),
		Layout:    xbLayout(),
		Results:   []*omr.Result{{RegNumber: "03000001", RegValid: true, Answers: []string{"A", "B"}}},
		Grades:    map[string]score.Grade{"s1-q5": {Score: 7, Graded: true, By: "admin"}},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\"ver\"")) {
		t.Fatal("测试前提:legacy 序列化不应带 ver 字段")
	}
	if err := os.MkdirAll(filepath.Join(dir, "legacy1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "legacy1", "exam.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 迁移补 Ver=1:刷新后的 grader 带 ver=1 改分应成功(未迁移则 0≠1 永久 409)
	if _, err := s.SetGrade("legacy1", 1, 5, score.Grade{Score: 8, Graded: true}, "admin", 1); err != nil {
		t.Fatalf("旧数据迁移后改分应成功: %v", err)
	}
	// 全新评分键无需 ver
	if _, err := s.SetGrade("legacy1", 1, 6, score.Grade{Score: 6, Graded: true}, "admin", 0); err != nil {
		t.Fatalf("首次评分无需 ver: %v", err)
	}
}

// TestSheetResolvedPendingVariant B-4:卷种待定(客观题被暂扣)的卷不算已裁决,
// 即便残留历史人工指派记录 —— 否则该生静默从报告消失且无待办。
func TestSheetResolvedPendingVariant(t *testing.T) {
	ex := &Exam{
		Results:       []*omr.Result{{RegNumber: "03000001", PendingVariant: true}},
		AssignVariant: map[string]string{"s1": "B"},
	}
	if sheetResolved(ex, 1) {
		t.Fatal("PendingVariant 时不得视为已裁决(合并重判后必须重新进入异常清单)")
	}
	ex.Results[0].PendingVariant = false
	if !sheetResolved(ex, 1) {
		t.Fatal("卷种已人工指派且不再待定:应视为已裁决")
	}
}

// TestConcurrentReadWriteSmoke A-1:读路径快照隔离冒烟。
// go test -race 下运行可捕获任何逃过快照的共享态访问。
// 写路径固定迭代次数(每次 SetGrade 都全量落盘,时间驱动循环在慢盘/杀软扫描下会超时)。
func TestConcurrentReadWriteSmoke(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	layout := xbLayout()
	book, _ := layout.BuildBook()
	res := &omr.Result{RegNumber: "03000001", RegValid: true, Answers: []string{"A", "B"}}
	ex, err := s.Create(NewExam{Layout: layout, Key: []string{"A", "B"}, Results: []*omr.Result{res}, Roster: []roster.Student{{Reg: "03000001", Name: "甲", Class: "三年二班"}}, Threshold: "otsu"})
	if err != nil {
		t.Fatal(err)
	}
	_ = book
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ver := 0
			for i := 0; i < 150; i++ {
				if v, err := s.SetGrade(ex.ID, 1, 1, score.Grade{Score: 5, Graded: true}, "writer", ver); err == nil {
					ver = v
				}
			}
		}()
	}
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				snap, ok := s.Get(ex.ID)
				if !ok {
					return
				}
				_ = examStage(snap)
				_ = flaggedQueue(snap)
				_ = scopeExamToClasses(snap, []string{"三年二班"})
			}
		}()
	}
	wg.Wait()
}
