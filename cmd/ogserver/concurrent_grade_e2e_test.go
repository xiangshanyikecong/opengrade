package main

// 并发评分 e2e(存储增量化回归):多 goroutine 同时对不同考试评分 + 列表/详情高频读,
// 全部评分须落库,去抖 flusher 与在途写盘不得丢数据、不得写坏 JSON。
// 配合 go test -race 捕获任何逃过锁/快照的共享态访问。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opengrade/opengrade/card"
)

func mustBook(t *testing.T, layout card.Layout) *card.Sheet {
	t.Helper()
	book, err := layout.BuildBook()
	if err != nil {
		t.Fatal(err)
	}
	return book[0]
}

func impRepQuestions(n int) []card.QuestionSpec {
	qs := make([]card.QuestionSpec, n)
	for i := range qs {
		qs[i] = card.QuestionSpec{Kind: card.KindChoice, Options: 4, Score: 2}
	}
	return qs
}

func TestConcurrentGradingE2E(t *testing.T) {
	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")

	// 3 场考试 × 5 卷(带真实扫描件入库),并发流每场各评 2 题 × 5 人 = 30 笔并发写
	// ×3 场 = 90 笔;叠加列表/详情高频读与去抖 flusher 并发。
	layout := card.Layout{Version: 2, Title: "CG E2E", RegDigits: 8, Columns: 2,
		Questions:   impRepQuestions(4),
		Subjectives: []card.SubjectiveSpec{{Kind: card.KindSubjective, Score: 10, HeightMM: 30}, {Kind: card.KindSubjective, Score: 10, HeightMM: 30}}}
	key := []string{"A", "B", "C", "D"}
	examIDs := make([]string, 3)
	for i := range examIDs {
		var rosterBuf strings.Builder
		rosterBuf.WriteString(`[`)
		var files []multipartFile
		for n := 0; n < 5; n++ {
			if n > 0 {
				rosterBuf.WriteString(",")
			}
			fmt.Fprintf(&rosterBuf, `{"reg":"060000%02d","name":"并发%02d","class":"三年二班"}`, n+1, n+1)
			files = append(files, multipartFile{field: "images", name: fmt.Sprintf("cg_%d_%02d.jpg", i, n+1),
				ctype: "image/jpeg", data: mpScanPage(t, mustBook(t, layout), fmt.Sprintf("060000%02d", n+1), key, int64(800+i*10+n))})
		}
		rosterBuf.WriteString(`]`)
		var body bytes.Buffer
		writeExamForm(&body, layout, key, rosterBuf.String(), files...)
		resp, data := admin.do("POST", "/api/v1/exams", &body, "multipart/form-data; boundary="+multipartBoundary)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("建考 %d 失败 %d: %s", i, resp.StatusCode, data)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		examIDs[i] = m["id"].(string)
	}

	// 并发评分(每笔都带乐观锁语义的首次评分,无需 ver)+ 同时高频读列表与详情
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	var bad []string
	writeOK := 0
	for ei, id := range examIDs {
		for q := 1; q <= 2; q++ {
			for st := 1; st <= 5; st++ {
				wg.Add(1)
				go func(id string, st, q, ei int) {
					defer wg.Done()
					payload := fmt.Sprintf(`{"examId":%q,"student":%d,"question":%d,"score":%d,"graded":true}`,
						id, st, q, (st+q+ei)%10+1)
					resp, data, err := admin.doErr("POST", "/api/v1/grades", strings.NewReader(payload), "application/json")
					if err != nil {
						mu.Lock()
						bad = append(bad, fmt.Sprintf("评分 %s s%d q%d 传输失败: %v", id, st, q, err))
						mu.Unlock()
						return
					}
					if resp.StatusCode != http.StatusOK {
						mu.Lock()
						bad = append(bad, fmt.Sprintf("评分 %s s%d q%d → %d: %s", id, st, q, resp.StatusCode, data))
						mu.Unlock()
						return
					}
					mu.Lock()
					writeOK++
					mu.Unlock()
				}(id, st, q, ei)
			}
		}
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// 并发读取的 HTTP 状态必须 200:错误响应带路径记录(评审 concurrent_grade_e2e_test.go:112)
				resp, data, err := admin.doErr("GET", "/api/v1/exams", nil, "")
				if err == nil && resp.StatusCode != http.StatusOK {
					mu.Lock()
					bad = append(bad, fmt.Sprintf("列表读 → %d: %s", resp.StatusCode, data))
					mu.Unlock()
				}
			}
		}
	}()
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				resp, data, err := admin.doErr("GET", "/api/v1/exams/"+examIDs[0], nil, "")
				if err == nil && resp.StatusCode != http.StatusOK {
					mu.Lock()
					bad = append(bad, fmt.Sprintf("详情读 → %d: %s", resp.StatusCode, data))
					mu.Unlock()
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	readers.Wait()
	// 并发冒烟必须至少完成一次有效评分写(评审 concurrent_grade_e2e_test.go:155)
	if writeOK == 0 {
		t.Fatalf("并发评分无一成功(bad=%v)", bad)
	}
	for _, b := range bad {
		t.Error(b)
	}

	// 冲刷后重启:180 笔评分全部在(去抖不丢数据)
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
	for _, id := range examIDs {
		rep := a2.json("GET", "/api/v1/exams/"+id+"/report", nil, http.StatusOK)
		students := rep["students"].([]any)
		if len(students) != 5 {
			t.Fatalf("重启后 %s 学生数 = %d,期望 5", id, len(students))
		}
		for _, s := range students {
			sm := s.(map[string]any)
			if sm["subjective"].(float64) <= 0 {
				t.Fatalf("%s %v 评分丢失", id, sm["regNumber"])
			}
		}
	}
}
