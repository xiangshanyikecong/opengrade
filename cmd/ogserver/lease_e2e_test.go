package main

// 阅卷分片租约 e2e(lease.go):两位老师协作阅同一道题的撞片治理。
//   - 抢领冲突:他人持有未过期 → 409 + lockedBy;同账号重复 claim = 续期;
//   - 软闸红线:租约绝不拦写路径 —— 他人持有中评分照常写入(乐观锁兜底正确性);
//   - 已评回填:claim 已评完的片返回 graded(不建租约),阅卷端据此跳过;
//   - 释放语义:本人可释放,他人释放无效;断线不释放由 TTL 过期自愈;
//   - 列表回填:slices 列表 locked/lockedBy 只对非持有人置位。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opengrade/opengrade/card"
)

func TestSliceLeaseE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	// 协作阅卷的第二名教师:年级组长(未配管辖年级 = 全量可见,与 admin 同场阅卷)
	admin.json("POST", "/api/v1/users", map[string]any{
		"username": "lead2", "displayName": "组长二", "role": "leader", "password": "lead123",
	}, http.StatusCreated)
	peer := newClient(t, ts.URL)
	peer.login("lead2", "lead123")

	// 建考:5 卷 × 2 道主观题(带真实扫描件,切片可正常出图)
	layout := card.Layout{Version: 2, Title: "Lease E2E", RegDigits: 8, Columns: 2,
		Questions:   impRepQuestions(2),
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 30}, {Kind: card.KindSubjective, Score: 10, HeightMM: 30}}}
	key := []string{"A", "B"}
	var rosterBuf strings.Builder
	rosterBuf.WriteString(`[`)
	var files []multipartFile
	for n := 0; n < 5; n++ {
		if n > 0 {
			rosterBuf.WriteString(",")
		}
		fmt.Fprintf(&rosterBuf, `{"reg":"070000%02d","name":"租约%02d","class":"三年二班"}`, n+1, n+1)
		files = append(files, multipartFile{field: "images", name: fmt.Sprintf("ls_%02d.jpg", n+1),
			ctype: "image/jpeg", data: mpScanPage(t, mustBook(t, layout), fmt.Sprintf("070000%02d", n+1), key, int64(900+n))})
	}
	rosterBuf.WriteString(`]`)
	var body bytes.Buffer
	writeExamForm(&body, layout, key, rosterBuf.String(), files...)
	resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("建考失败 %d: %s", resp.StatusCode, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	examID := m["id"].(string)

	claim := func(c *e2eClient, student, question, wantCode int) map[string]any {
		t.Helper()
		return c.json("POST", "/api/v1/exams/"+examID+"/slices/claim",
			map[string]any{"student": student, "question": question}, wantCode)
	}
	release := func(c *e2eClient, student, question int) {
		t.Helper()
		c.json("POST", "/api/v1/exams/"+examID+"/slices/release",
			map[string]any{"student": student, "question": question}, http.StatusOK)
	}
	// sliceLocked 从切片列表读某卷的锁定状态(locked 他人持有才置位)
	sliceLocked := func(c *e2eClient, student int) (bool, string) {
		t.Helper()
		arr := c.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1&grading=1", http.StatusOK)
		for _, it := range arr {
			if it["student"].(float64) == float64(student) {
				locked, _ := it["locked"].(bool)
				by, _ := it["lockedBy"].(string)
				return locked, by
			}
		}
		t.Fatalf("切片列表缺学生 %d", student)
		return false, ""
	}

	// —— 领取/续期/抢领冲突 ——
	if got := claim(admin, 1, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("首领状态 = %v,期望 ok", got)
	}
	if got := claim(admin, 1, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("同账号重复 claim(心跳续期)状态 = %v,期望 ok", got)
	}
	conflict := claim(peer, 1, 1, http.StatusConflict)
	if conflict["lockedBy"] != "admin" {
		t.Fatalf("抢领冲突回执 lockedBy = %v,期望 admin", conflict["lockedBy"])
	}

	// —— 列表回填:他人持有才置 locked,持有人自己不置 ——
	if locked, by := sliceLocked(peer, 1); !locked || by != "admin" {
		t.Fatalf("他人视角 locked=%v lockedBy=%q,期望 true/admin", locked, by)
	}
	if locked, _ := sliceLocked(admin, 1); locked {
		t.Fatal("持有人自己视角不应置 locked")
	}

	// —— 软闸红线:评分写路径不看租约(正确性由乐观锁兜底) ——
	if got := claim(peer, 2, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("peer 领 s2 状态 = %v", got)
	}
	admin.json("POST", "/api/v1/grades", map[string]any{
		"examId": examID, "student": 2, "question": 1, "score": 7.5, "graded": true,
	}, http.StatusOK) // admin 无租约写入 s2:必须放行(硬闸会堵死复核/改分)

	// —— 已评回填:claim 已评完的片返回 graded,不建租约 ——
	graded := claim(admin, 2, 1, http.StatusOK)
	if graded["status"] != "graded" || graded["by"] != "admin" {
		t.Fatalf("已评片 claim = %v,期望 graded/by=admin", graded)
	}
	if sc, ok := graded["score"].(float64); !ok || sc != 7.5 {
		t.Fatalf("已评片回执 score = %v,期望 7.5", graded["score"])
	}

	// —— 释放语义(用未评的 s4):本人可释放,他人释放无效,幂等 ——
	if got := claim(peer, 4, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("peer 领 s4 状态 = %v", got)
	}
	release(admin, 4, 1)                    // admin 不是持有人:无效但不报错
	claim(admin, 4, 1, http.StatusConflict) // 他人释放不生效:仍是 409(json 内部断言状态码)
	release(peer, 4, 1)                     // 持有人释放
	if got := claim(admin, 4, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("他人释放后应可领取,状态 = %v", got)
	}
	release(admin, 1, 1)
	release(admin, 1, 1) // 幂等:重复释放仍 200

	// —— TTL 过期自愈:断线不释放,超时后他人可领 ——
	oldTTL := sliceLeaseTTL
	sliceLeaseTTL = 80 * time.Millisecond
	defer func() { sliceLeaseTTL = oldTTL }()
	if got := claim(admin, 3, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("过期测试首领状态 = %v", got)
	}
	time.Sleep(160 * time.Millisecond) // >2×TTL
	if got := claim(peer, 3, 1, http.StatusOK)["status"]; got != "ok" {
		t.Fatalf("租约过期后他人应可领取,状态 = %v", got)
	}

	// —— 参数校验:越界学生/题号 400,未知考试 404 ——
	claim(admin, 999, 1, http.StatusBadRequest)
	claim(admin, 1, 99, http.StatusBadRequest)
	admin.json("POST", "/api/v1/exams/no-such/slices/claim",
		map[string]any{"student": 1, "question": 1}, http.StatusNotFound)

	// —— 收尾:flush + 重启,租约是内存会话态,重启即清空且不影响已评数据 ——
	srv.Exams.FlushAll()
	srv.Exams.Stop()
	srv2, err := newServer(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Exams.Stop()
	ts2 := httptest.NewServer(srv2.Handler)
	defer ts2.Close()
	a2 := newClient(t, ts2.URL)
	a2.login("admin", "admin123")
	rep := a2.json("GET", "/api/v1/exams/"+examID+"/report", nil, http.StatusOK)
	students := rep["students"].([]any)
	if len(students) != 5 {
		t.Fatalf("重启后学生数 = %d,期望 5", len(students))
	}
	arr := a2.jsonArr("GET", "/api/v1/exams/"+examID+"/slices?q=1&grading=1", http.StatusOK)
	for _, it := range arr {
		if it["locked"] == true || it["lockedBy"] != nil {
			t.Fatalf("重启后不应残留租约:%v", it)
		}
	}
}
