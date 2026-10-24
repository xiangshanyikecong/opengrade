package main

// 现实对齐批(P0/P1/P2)端到端回归:
//   - 考试元数据(日期/学期/类型/同场分组)与匿名阅卷开关;
//   - 卷生命周期:重扫产生重复卷 → 异常告警 → 作废其一 → 报告只留一份;
//   - 评分乐观锁:ver 过期 409、ver 正确 200;分数越界 400;
//   - 手工成绩科目:免版式建考 + 批量直录(含小数)→ 统分报告;
//   - 年级总分工表:同场分组两科聚合 → 总分/排名;
//   - 成绩复核申请:学生发起(须已发布)→ 教师裁决;
//   - 全班成绩条 PDF 与阅卷工作量统计。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRealityAlignmentE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	teacher := newClient(t, ts.URL)
	teacher.login("teacher1", "teach123")
	student := newClient(t, ts.URL)
	student.login("s0001", "student123")

	// —— 建考(带名册与扫描件,考号 00000001~00000005)——
	fx := buildFixture(t)
	rosterJSON := `[{"reg":"00000001","name":"李雷","class":"三年二班"},{"reg":"00000002","name":"韩梅梅","class":"三年二班"},
		{"reg":"00000003","name":"王小明","class":"三年二班"},{"reg":"00000004","name":"赵小红","class":"三年一班"},
		{"reg":"00000005","name":"孙小刚","class":"三年一班"}]`
	var body bytes.Buffer
	files := make([]multipartFile, len(fx.scans))
	for i, sc := range fx.scans {
		files[i] = multipartFile{field: "images", name: fmt.Sprintf("r_%02d.jpg", i+1), ctype: "image/jpeg", data: sc}
	}
	writeExamForm(&body, fx.layout, fx.key, rosterJSON, files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID := m["id"].(string)

	// —— 元数据 + 匿名开关 ——
	admin.json("POST", "/api/v1/exams/"+examID+"/meta", map[string]any{
		"examDate": "2026-10-20", "term": "2026-2027上", "examType": "月考", "examGroup": "R2026月考", "anonymous": true,
	}, http.StatusOK)
	admin.json("POST", "/api/v1/exams/"+examID+"/meta", map[string]any{"examDate": "bad-date"}, http.StatusBadRequest)
	d := admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if d["examDate"] != "2026-10-20" || d["term"] != "2026-2027上" || d["examType"] != "月考" || d["examGroup"] != "R2026月考" || d["anonymous"] != true {
		t.Fatalf("元数据未落盘: %v", d)
	}

	// —— 乐观锁:ver=0 兼容不校验;携带当前 ver 改分成功后,旧 ver 再改 → 409;越界分数 400 ——
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 99, "graded": true,
	}, http.StatusBadRequest)
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 3, "graded": true,
	}, http.StatusOK)
	sl := admin.jsonArr("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=1&grading=1", examID), http.StatusOK)
	var ver1 float64
	for _, it := range sl {
		if it["student"].(float64) == 1 {
			ver1 = it["ver"].(float64)
		}
	}
	if ver1 != 1 {
		t.Fatalf("首次评分 ver = %v,期望 1", ver1)
	}
	// 携带当前版本改分成功 → 版本推进;再用过期版本改分 → 409(双开互踩被拦截)
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 3.5, "graded": true, "ver": int(ver1),
	}, http.StatusOK)
	r1, data1 := admin.do("POST", "/api/v1/grades", jsonBody(map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 4, "graded": true, "ver": int(ver1),
	}), "application/json")
	if r1.StatusCode != http.StatusConflict {
		t.Fatalf("过期 ver 应 409,得到 %d: %s", r1.StatusCode, data1)
	}
	r2, _ := admin.do("POST", "/api/v1/grades", jsonBody(map[string]any{
		"examId": examID, "student": 1, "question": 1, "score": 4, "graded": true, "ver": int(ver1) + 1,
	}), "application/json")
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("正确 ver 应 200,得到 %d", r2.StatusCode)
	}

	// —— 卷生命周期:重扫同一考号 → 重复告警 → 作废其一 ——
	var dup bytes.Buffer
	writeExamForm(&dup, fx.layout, fx.key, rosterJSON, files[0]) // 考号 00000001 重扫
	resp, data = teacher.do("POST", "/api/v1/exams/"+examID+"/scans", &dup, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重扫补传失败 %d: %s", resp.StatusCode, data)
	}
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	dups, _ := d["duplicates"].([]any)
	if len(dups) != 1 {
		t.Fatalf("应检出 1 组重复卷:%v", d["duplicates"])
	}
	group := dups[0].([]any)
	if len(group) != 2 {
		t.Fatalf("重复组应含 2 卷:%v", group)
	}
	// 作废第 6 卷(重扫那份),报告回到 5 行且考号唯一
	voided := int(group[1].(float64))
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/results/%d/void", examID, voided), map[string]any{"voided": true}, http.StatusOK)
	rep := admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	regCount := map[string]int{}
	for _, s := range rep["students"].([]any) {
		regCount[s.(map[string]any)["regNumber"].(string)]++
	}
	if regCount["00000001"] != 1 {
		t.Fatalf("作废后考号 00000001 应只出现 1 次:%v", regCount)
	}
	if len(rep["students"].([]any)) != 5 {
		t.Fatalf("作废卷不应出现在报告:%d 行", len(rep["students"].([]any)))
	}
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if vr := d["voidedRows"].([]any); len(vr) != 1 {
		t.Fatalf("voidedRows = %v,期望 1 条", vr)
	}

	// —— 权限收窄:作废/恢复限建卷人/组长/管理员,普通任课教师 403 ——
	rVoid, _ := teacher.do("POST", fmt.Sprintf("/api/v1/exams/%s/results/%d/void", examID, voided),
		jsonBody(map[string]any{"voided": false}), "application/json")
	if rVoid.StatusCode != http.StatusForbidden {
		t.Fatalf("教师恢复卷应 403,得到 %d", rVoid.StatusCode)
	}

	// —— 重答检测不认作废卷:作废该考号唯一在册卷后重传,应无声入库 ——
	// (修复前:存量循环计入已作废卷 → 新卷被误报"疑似重答"且指向死卷,异常清单出现悬案)
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/results/1/void", examID), map[string]any{"voided": true}, http.StatusOK)
	var again bytes.Buffer
	writeExamForm(&again, fx.layout, fx.key, rosterJSON, files[0])
	resp, data = admin.do("POST", "/api/v1/exams/"+examID+"/scans", &again, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("作废后重传失败 %d: %s", resp.StatusCode, data)
	}
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if dups2, _ := d["duplicates"].([]any); len(dups2) != 0 {
		t.Fatalf("作废后重传不应再报重复组:%v", dups2)
	}
	// 只查「疑似重答」误报(fixture 的卷 3 本就带"第 5 位漏涂"既存告警,用于推荐指派场景)
	for _, w := range d["warnings"].([]any) {
		wm, _ := w.(map[string]any)
		if wm == nil || wm["resolved"].(bool) {
			continue
		}
		for _, tx := range wm["texts"].([]any) {
			if strings.Contains(fmt.Sprint(tx), "重答") {
				t.Fatalf("不应再报重答误报(指向作废卷):%v", wm)
			}
		}
	}
	rep = admin.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	if n := len(rep["students"].([]any)); n != 5 {
		t.Fatalf("重传后报告应仍 5 行:%d", n)
	}

	// —— 手工成绩科目(体育,满分 100)——
	var mb bytes.Buffer
	writeMultipartForm(&mb, map[string]string{
		"manual":    "1",
		"subject":   "体育",
		"full":      "100",
		"examDate":  "2026-10-21",
		"examGroup": "R2026月考",
		"roster":    `[{"reg":"00000001","name":"李雷","class":"三年二班"},{"reg":"00000002","name":"韩梅梅","class":"三年二班"}]`,
	})
	resp, data = admin.do("POST", "/api/v1/exams", &mb, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("手工建考失败 %d: %s", resp.StatusCode, data)
	}
	_ = json.Unmarshal(data, &m)
	manualID := m["id"].(string)
	if m["manualFull"].(float64) != 100 {
		t.Fatalf("manualFull = %v", m["manualFull"])
	}
	admin.json("POST", "/api/v1/exams/"+manualID+"/manual-scores", map[string]any{
		"scores": []map[string]any{
			{"reg": "00000001", "score": 92.5},
			{"reg": "00000002", "score": 88},
			{"reg": "99999999", "score": 50}, // 不存在的考号 → 报错但其余成功
		},
	}, http.StatusOK)
	repM := admin.json("GET", "/api/v1/exams/"+manualID+"/report", nil, http.StatusOK)
	students := repM["students"].([]any)
	if len(students) != 2 {
		t.Fatalf("手工考试报告应 2 行:%v", students)
	}
	if s0 := students[0].(map[string]any); s0["regNumber"] != "00000001" || s0["score"] != 92.5 {
		t.Fatalf("手工直录分未进报告:%v", s0)
	}
	// manual-scores 仅限手工科目:对扫描考试调用 → 400(防止误写第 1 题评分)
	admin.json("POST", "/api/v1/exams/"+examID+"/manual-scores", map[string]any{
		"scores": []map[string]any{{"reg": "00000001", "score": 50}},
	}, http.StatusBadRequest)

	// —— 年级总分工表:两科(数学=手工 100 分制 + 默认卷)同场分组聚合 ——
	agg := admin.json("GET", "/api/v1/aggregate?group=R2026月考", nil, http.StatusOK)
	cols := agg["exams"].([]any)
	if len(cols) != 2 {
		t.Fatalf("分组应聚合 2 场考试:%v", cols)
	}
	aggStudents := agg["students"].([]any)
	var aggLei map[string]any
	for _, s := range aggStudents {
		if s.(map[string]any)["reg"] == "00000001" {
			aggLei = s.(map[string]any)
		}
	}
	if aggLei == nil {
		t.Fatalf("总表缺考号 00000001:%v", aggStudents)
	}
	// 总分 = 默认卷得分 + 体育 92.5;名次按总分并列规则
	if aggLei["rank"].(float64) != 1 {
		t.Fatalf("李雷应年级第 1:%v", aggLei)
	}

	// —— 缺考排名口径 + 可见科目口径提示 ——
	// 孙小刚(卷5)默认卷标缺考,又不在体育名册 → 全科未参与:默认不进排名池(rank=0),
	// absentRank=include 恢复旧口径(0 分垫底有名次);totalExams 与可见 exams 数量分离供前端提示。
	admin.json("POST", "/api/v1/exams/"+examID+"/absent", map[string]any{"student": 5, "absent": true}, http.StatusOK)
	rankOfReg := func(a map[string]any, reg string) float64 {
		for _, s := range a["students"].([]any) {
			if sm, _ := s.(map[string]any); sm != nil && sm["reg"] == reg {
				return sm["rank"].(float64)
			}
		}
		return -1
	}
	aggEx := admin.json("GET", "/api/v1/aggregate?group=R2026月考", nil, http.StatusOK)
	if aggEx["totalExams"].(float64) != 2 {
		t.Fatalf("totalExams = %v,期望 2", aggEx["totalExams"])
	}
	if rk := rankOfReg(aggEx, "00000005"); rk != 0 {
		t.Fatalf("全科缺考默认不参与排名(rank 应 0):%v", rk)
	}
	aggInc := admin.json("GET", "/api/v1/aggregate?group=R2026月考&absentRank=include", nil, http.StatusOK)
	if rk := rankOfReg(aggInc, "00000005"); rk <= 0 {
		t.Fatalf("absentRank=include 应给全科缺考垫底名次:%v", rk)
	}
	// teacher1(三年二班·数学)看不到体育(手工科目):可见 1/2 科,totalExams 仍为 2 → 前端口径提示
	aggT := teacher.json("GET", "/api/v1/aggregate?group=R2026月考", nil, http.StatusOK)
	if aggT["totalExams"].(float64) != 2 || len(aggT["exams"].([]any)) != 1 {
		t.Fatalf("教师可见科目口径异常:totalExams=%v exams=%v", aggT["totalExams"], aggT["exams"])
	}

	// —— 任务截止时间 ——
	admin.json("POST", "/api/v1/exams/"+examID+"/task-due", map[string]any{"key": "q1", "due": "2026-12-31"}, http.StatusOK)
	admin.json("POST", "/api/v1/exams/"+examID+"/task-due", map[string]any{"key": "q1", "due": "12/31"}, http.StatusBadRequest)

	// —— 复核申请:未发布不可申请;发布后学生发起、教师裁决 ——
	student.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "第 3 题我答对了"}, http.StatusBadRequest) // 未发布
	admin.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true}, http.StatusOK)
	student.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": "第 3 题我答对了"}, http.StatusCreated)
	student.json("POST", "/api/v1/student/exams/"+examID+"/appeals", map[string]any{"reason": ""}, http.StatusBadRequest)
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	apls := d["appeals"].([]any)
	if len(apls) != 1 || apls[0].(map[string]any)["status"] != "open" || apls[0].(map[string]any)["name"] != "李雷" {
		t.Fatalf("复核申请列表异常:%v", apls)
	}
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/appeals/1/resolve", examID), map[string]any{"reply": "已核对,维持原判"}, http.StatusOK)
	admin.json("POST", fmt.Sprintf("/api/v1/exams/%s/appeals/1/resolve", examID), map[string]any{"reply": "再裁一次"}, http.StatusBadRequest) // 已裁决
	d = admin.json("GET", "/api/v1/exams/"+examID, nil, http.StatusOK)
	if got := d["appeals"].([]any)[0].(map[string]any)["status"]; got != "resolved" {
		t.Fatalf("裁决后状态 = %v", got)
	}

	// —— 全班成绩条 PDF(连排,家长签字线)——
	pResp, pdf := teacher.do("GET", "/api/v1/exams/"+examID+"/slips.pdf", nil, "")
	if pResp.StatusCode != http.StatusOK || !strings.HasPrefix(string(pdf), "%PDF") {
		t.Fatalf("成绩条 PDF 异常:%d %s", pResp.StatusCode, string(pdf[:minInt(60, len(pdf))]))
	}

	// —— 阅卷工作量统计(teacher1 评了卷 1 第 1 题 + 已记录 By)——
	wl := admin.jsonArr("GET", "/api/v1/workload", http.StatusOK)
	found := false
	for _, row := range wl {
		if row["username"] == "admin" && row["graded"].(float64) >= 2 {
			found = true // admin 评了 student1 q1 两次(3 分→4 分)
		}
	}
	if !found {
		t.Fatalf("工作量统计缺 admin:%v", wl)
	}

	// —— 强制改密闭环(服务端闸门,非仅前端引导):管理员重置 → 旧会话立即被锁
	//    (仅放行 认证端点,业务 API 403)→ 本人改密后自动解锁 ——
	admin.json("POST", "/api/v1/users/teacher1/password", map[string]any{"newPassword": "reset123"}, http.StatusOK)
	if c := teacher.code("GET", "/api/v1/my/tasks", nil, ""); c != http.StatusForbidden {
		t.Fatalf("重置后旧会话应被强制改密拦截(403),得到 %d", c)
	}
	teacher.json("POST", "/api/v1/auth/password", map[string]string{"oldPassword": "reset123", "newPassword": "teach123"}, http.StatusOK)
	teacher.json("GET", "/api/v1/my/tasks", nil, http.StatusOK)
}

// writeMultipartForm 构造纯字段 multipart 表单(手工建考用)。
func writeMultipartForm(buf *bytes.Buffer, fields map[string]string) {
	for k, v := range fields {
		fmt.Fprintf(buf, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", multipartBoundary, k, v)
	}
	fmt.Fprintf(buf, "--%s--\r\n", multipartBoundary)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
