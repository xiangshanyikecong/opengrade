// ogseed 面向演示/压测的大规模造数工具:在已部署的 ogserver 上创建一届
// 大名册(默认 3000 人)的多场考试,并把各场推进到不同阶段
// (待扫描 / 扫描中 / 异常待处理 / 阅卷中 / 待发布 / 已发布)。
//
// 全程走正式 HTTP API:真实出卡(card.RenderPNG)→ 模拟扫描损伤(synth.Distort)
// → 批量上传判读(POST /exams/{id}/scans)→ 手工缺考登记 → 主观题评分 → 发布,
// 与生产写路径完全一致,产出的数据(矫正图/切片/成绩/阶段)与真实考试无异。
//
// 断点续跑:状态文件记录每场考试 ID 与已上传张数;中断后重跑自动接续,
// 评分阶段以「未评切片」为增量天然幂等。
//
// 用法示例:
//
//	go run ./cmd/ogseed -server http://192.168.188.7:8787 -user admin -pass admin123
//	go run ./cmd/ogseed -only scanning,grading -students 3000
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/synth"
)

func main() {
	log.SetFlags(log.Ltime)
	var (
		server   = flag.String("server", "http://192.168.188.7:8787", "ogserver 地址")
		user     = flag.String("user", "admin", "管理员账号")
		pass     = flag.String("pass", "admin123", "管理员密码")
		students = flag.Int("students", 3000, "每场考试名册人数")
		seed     = flag.Int64("seed", 20261031, "随机种子(复现)")
		batch    = flag.Int("batch", 12, "每次上传的扫描件张数(服务端逐张解码,过大易耗尽内存)")
		conc     = flag.Int("conc", 2, "并发上传请求数")
		genconc  = flag.Int("genconc", runtime.NumCPU(), "本地出图并发数")
		only     = flag.String("only", "", "只跑指定考试(逗号分隔 key:awaiting,scanning,anomaly,grading,ready,published)")
		stateF   = flag.String("state", "ogseed-state.json", "断点续跑状态文件")
		phase    = flag.String("phase", "seed", "gen=本地出图到 -out;seed=向服务器建考/上传/评分(可走 -scans 复用 gen 产物)")
		out      = flag.String("out", "scans", "gen 阶段输出目录(<out>/<key>/NNNNNN.jpg)")
		scansDir = flag.String("scans", "", "seed 阶段扫描件来源目录(<dir>/<key>/NNNNNN.jpg);空 = 边生成边上传(不推荐:长连接易被宿主安全软件中断)")
		distort  = flag.Bool("distort", false, "出图时叠加模拟扫描损伤(透视/旋转/光照/噪声)")
		suffix   = flag.String("subject-suffix", "", "科目名后缀(如 \"②\"),用于补建同名演示考试")
	)
	flag.Parse()

	layout := card.DefaultLayout()
	sheet, err := layout.Build()
	if err != nil {
		log.Fatalf("版式编译失败: %v", err)
	}
	key := randomKey(rand.New(rand.NewSource(*seed)), sheet)
	roster := buildRoster(*students)
	specs := examSpecs(*students)
	gen := &genCtx{sheet: sheet, key: key, seed: *seed, distort: *distort}

	pick := func() []*examSpec {
		var out []*examSpec
		for i := range specs {
			s := &specs[i]
			if *only != "" && !strings.Contains(","+*only+",", ","+s.key+",") {
				continue
			}
			out = append(out, s)
		}
		return out
	}

	if *phase == "gen" {
		for _, s := range pick() {
			if s.scans == 0 {
				continue
			}
			generateScans(gen, s, filepath.Join(*out, s.key), *genconc)
		}
		return
	}

	c := newClient(*server)
	c.login(*user, *pass)
	log.Printf("已登录 %s(%s)", *server, *user)
	ensureStaff(c)
	st := loadState(*stateF)

	for _, s := range pick() {
		if st.Done[s.key] {
			log.Printf("[%s] 已完成,跳过", s.key)
			continue
		}
		id := st.ExamID(s.key, func() string {
			created := c.createExam(layout, key, roster, s.subject+*suffix)
			log.Printf("[%s] 建考成功 id=%s(%d 人名册)", s.key, created, len(roster))
			return created
		})
		if s.scans > 0 {
			dir := filepath.Join(*scansDir, s.key)
			if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%06d.jpg", s.scans))); err != nil {
				log.Fatalf("[%s] 缺少 gen 产物 %s(%d 张的末张不存在): 先用 -phase gen 生成", s.key, dir, s.scans)
			}
			uploadScansFromDir(c, id, s, st, dir, *batch, *conc)
		}
		for _, reg := range s.absentRegs {
			c.absent(id, reg)
		}
		if s.assignTasks {
			c.assignTasks(id, []string{"t-teacher1", "t-teacher2"})
		}
		if s.gradeAll || s.gradeUpTo > 0 {
			gradeExam(c, id, s, layout, *seed)
		}
		if s.publish {
			c.publish(id)
		}
		st.MarkDone(s.key)
	}

	verify(c, specs, *only)
}

// ---- 客户端 ----

type client struct {
	base string
	hc   *http.Client
}

func newClient(base string) *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatal(err)
	}
	return &client{base: strings.TrimRight(base, "/"), hc: &http.Client{Jar: jar, Timeout: 15 * time.Minute}}
}

func (c *client) do(method, path, ctype string, body io.Reader) (int, []byte) {
	code, data, err := c.try(method, path, ctype, body)
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	return code, data
}

// try 与 do 相同,但传输层错误以 error 返回(供上传重试),不直接终止进程。
func (c *client) try(method, path, ctype string, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

func (c *client) json(method, path string, payload any) (int, []byte) {
	if payload == nil {
		return c.do(method, path, "", nil)
	}
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		log.Fatal(err)
	}
	return c.do(method, path, "application/json", buf)
}

func (c *client) login(user, pass string) {
	code, data := c.json("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass})
	if code != http.StatusOK {
		log.Fatalf("登录 %s 失败(%d): %s", user, code, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if m["mustChangePassword"] == true {
		// 初始密码强制改密:改成同密码解锁,账号保持可复现的口令
		code, data = c.json("POST", "/api/v1/auth/password", map[string]string{"oldPassword": pass, "newPassword": pass})
		if code != http.StatusOK {
			log.Fatalf("解锁改密 %s 失败(%d): %s", user, code, data)
		}
	}
}

// ensureStaff 建演示协作账号(存在即跳过),并各自首登解锁。
func ensureStaff(c *client) {
	staff := []struct{ user, display, role, pass string }{
		{"t-leader1", "演示组长(高一年级)", "leader", "leader123"},
		{"t-teacher1", "演示教师甲", "teacher", "teach123"},
		{"t-teacher2", "演示教师乙", "teacher", "teach123"},
	}
	for _, s := range staff {
		code, data := c.json("POST", "/api/v1/users", map[string]any{
			"username": s.user, "displayName": s.display, "role": s.role, "password": s.pass,
		})
		switch {
		case code == http.StatusCreated:
			log.Printf("创建账号 %s(%s)", s.user, s.role)
		case code == http.StatusBadRequest && strings.Contains(string(data), "存在"):
			// 已存在,复用
		default:
			log.Fatalf("创建账号 %s 失败(%d): %s", s.user, code, data)
		}
		// 用独立 client 做首登解锁,不顶掉主 client 的管理员会话
		sc := newClient(c.base)
		sc.login(s.user, s.pass)
	}
}

func (c *client) createExam(layout card.Layout, key []string, roster []map[string]string, subject string) string {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	writeField(mw, "layout", mustJSON(layout))
	writeField(mw, "key", mustJSON(key))
	writeField(mw, "roster", mustJSON(roster))
	writeField(mw, "subject", subject)
	writeField(mw, "examDate", time.Now().Format("2006-01-02"))
	writeField(mw, "term", "2026-2027学年第一学期")
	writeField(mw, "examType", "期中考试")
	if err := mw.Close(); err != nil {
		log.Fatal(err)
	}
	code, data := c.do("POST", "/api/v1/exams", mw.FormDataContentType(), buf)
	if code != http.StatusOK && code != http.StatusCreated {
		log.Fatalf("建考失败(%d): %s", code, data)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	id, _ := m["id"].(string)
	if id == "" {
		log.Fatalf("建考响应缺 id: %s", data)
	}
	return id
}

func writeField(mw *multipart.Writer, name, val string) {
	fw, err := mw.CreateFormField(name)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := fw.Write([]byte(val)); err != nil {
		log.Fatal(err)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		log.Fatal(err)
	}
	return string(b)
}

// generateScans 把一场考试的 [0, scans) 张卷并行出图到 dir/NNNNNN.jpg(1-based)。
func generateScans(gen *genCtx, s *examSpec, dir string, genconc int) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}
	t0 := time.Now()
	var done atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, genconc)
	for i := 0; i < s.scans; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			img := gen.render(i, s.fillingAt(i, gen.key))
			p := filepath.Join(dir, fmt.Sprintf("%06d.jpg", i+1))
			if err := os.WriteFile(p, img, 0o644); err != nil {
				log.Fatalf("写 %s 失败: %v", p, err)
			}
			n := done.Add(1)
			if n%500 == 0 {
				rate := float64(n) / time.Since(t0).Seconds()
				log.Printf("[%s] 已出图 %d/%d(%.0f 张/分)", s.key, n, s.scans, rate*60)
			}
		}(i)
	}
	wg.Wait()
	log.Printf("[%s] 出图完成 %d 张 → %s(耗时 %s)", s.key, s.scans, dir, time.Since(t0).Truncate(time.Second))
}

// uploadScansFromDir 从 gen 阶段产物目录分批上传,断点续跑从 state.Uploaded 接续。
// 设计为在服务器本机运行(localhost 上传),绕开开发机到服务器之间会被
// 宿主安全软件周期性掐断的长连接路径。
func uploadScansFromDir(c *client, examID string, s *examSpec, st *seedState, dir string, batch, conc int) {
	start := st.Uploaded[s.key]
	if start >= s.scans {
		log.Printf("[%s] 扫描已传 %d/%d,跳过", s.key, start, s.scans)
		return
	}
	log.Printf("[%s] 开始上传扫描件 %d→%d(批 %d,并发 %d,来源 %s)", s.key, start, s.scans, batch, conc, dir)

	type work struct {
		first int
		imgs  [][]byte
	}
	batches := make(chan work, conc+2)
	var wg sync.WaitGroup
	var doneCount atomic.Int64
	t0 := time.Now()

	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range batches {
				if err := c.uploadBatch(examID, b.imgs); err != nil {
					log.Fatalf("[%s] 上传批 %d 失败: %v", s.key, b.first, err)
				}
				n := doneCount.Add(int64(len(b.imgs)))
				st.SetUploaded(s.key, start+int(n))
				if n%int64(batch*10) < int64(batch) {
					rate := float64(n) / time.Since(t0).Seconds()
					eta := time.Duration(float64(s.scans-start-int(n))/rate) * time.Second
					log.Printf("[%s] 已传 %d/%d(%.0f 张/分,预计剩 %s)", s.key, start+int(n), s.scans, rate*60, eta.Truncate(time.Second))
				}
			}
		}()
	}

	for first := start; first < s.scans; first += batch {
		last := min(first+batch, s.scans)
		imgs := make([][]byte, last-first)
		for i := first; i < last; i++ {
			p := filepath.Join(dir, fmt.Sprintf("%06d.jpg", i+1))
			data, err := os.ReadFile(p)
			if err != nil {
				log.Fatalf("读 %s 失败: %v", p, err)
			}
			imgs[i-first] = data
		}
		batches <- work{first: first, imgs: imgs}
	}
	close(batches)
	wg.Wait()
	rate := float64(s.scans-start) / time.Since(t0).Seconds()
	log.Printf("[%s] 扫描上传完成 %d 张(平均 %.0f 张/分)", s.key, s.scans-start, rate*60)
}

// uploadBatch 单批上传(multipart images=多张),带限次重试。
func (c *client) uploadBatch(examID string, imgs [][]byte) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		buf := &bytes.Buffer{}
		mw := multipart.NewWriter(buf)
		for i, img := range imgs {
			fw, err := mw.CreateFormFile("images", fmt.Sprintf("scan_%05d.jpg", i))
			if err != nil {
				return err
			}
			if _, err := fw.Write(img); err != nil {
				return err
			}
		}
		if err := mw.Close(); err != nil {
			return err
		}
		code, data, derr := c.try("POST", "/api/v1/exams/"+examID+"/scans", mw.FormDataContentType(), buf)
		switch {
		case derr != nil:
			lastErr = derr // 传输层错误(连接被掐断等):重试
			continue
		case code == http.StatusOK:
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			if failed, ok := m["failed"].([]any); ok && len(failed) > 0 {
				log.Printf("批内有 %d 张识别失败(计入服务端 failed,不影响其余): %v", len(failed), failed)
			}
			return nil
		case code >= 500:
			lastErr = fmt.Errorf("%d: %s", code, data)
			continue
		default:
			return fmt.Errorf("%d: %s", code, data)
		}
	}
	return lastErr
}

func (c *client) absent(examID, reg string) {
	code, data := c.json("POST", "/api/v1/exams/"+examID+"/manual", map[string]any{"reg": reg, "absent": true})
	if code != http.StatusOK && !(code == http.StatusBadRequest && strings.Contains(string(data), "已")) {
		log.Fatalf("缺考登记 %s 失败(%d): %s", reg, code, data)
	}
}

func (c *client) assignTasks(examID string, teachers []string) {
	for q := 1; q <= 3; q++ {
		code, data := c.json("POST", "/api/v1/exams/"+examID+"/tasks",
			map[string]any{"question": q, "teacher": teachers[(q-1)%len(teachers)]})
		if code != http.StatusOK && code != http.StatusBadRequest { // 已分配过会 400,容忍
			log.Fatalf("分配任务 q%d 失败(%d): %s", q, code, data)
		}
	}
}

func (c *client) publish(examID string) {
	code, data := c.json("POST", "/api/v1/exams/"+examID+"/publish", map[string]any{"published": true})
	if code != http.StatusOK {
		log.Fatalf("发布失败(%d): %s", code, data)
	}
	log.Printf("已发布考试 %s", examID)
}

// gradeExam 评分该场考试的主观切片:gradeUpTo>0 只评 student≤gradeUpTo(演示阅卷中),否则全评。
// 以服务端「未评」清单为增量,重跑幂等;分数按学生水平分层,统计/报告更真实。
func gradeExam(c *client, examID string, s *examSpec, layout card.Layout, seed int64) {
	t0 := time.Now()
	var total, graded int64
	for q := 1; q <= len(layout.Subjectives); q++ {
		full := layout.Subjectives[q-1].Score
		code, data := c.json("GET", fmt.Sprintf("/api/v1/exams/%s/slices?q=%d&grading=1", examID, q), nil)
		if code != http.StatusOK {
			log.Fatalf("取切片清单 q%d 失败(%d): %s", q, code, data)
		}
		var items []struct {
			Student int     `json:"student"`
			Graded  bool    `json:"graded"`
			Parts   []any   `json:"parts"`
			Ver     int     `json:"ver"`
		}
		if err := json.Unmarshal(data, &items); err != nil {
			log.Fatalf("切片清单解析失败: %v", err)
		}
		type job struct {
			student int
			score   float64
		}
		jobs := make(chan job, 256)
		var gwg sync.WaitGroup
		for w := 0; w < 4; w++ {
			gwg.Add(1)
			go func() {
				defer gwg.Done()
				for j := range jobs {
					payload := map[string]any{
						"examId": examID, "student": j.student, "question": q,
						"score": j.score, "graded": true, "ver": 0,
					}
					code, data := c.json("POST", "/api/v1/grades", payload)
					if code != http.StatusOK {
						log.Fatalf("评分 s%d-q%d 失败(%d): %s", j.student, q, code, data)
					}
					n := atomic.AddInt64(&graded, 1)
					if n%1000 == 0 {
						log.Printf("[%s] 评分进度 %d(共约 %d)", s.key, n, atomic.LoadInt64(&total))
					}
				}
			}()
		}
		for _, it := range items {
			if it.Graded {
				continue
			}
			if s.gradeUpTo > 0 && it.Student > s.gradeUpTo {
				continue
			}
			if len(it.Parts) > 0 {
				log.Fatalf("版式含拆分小问题,本工具未实现小问评分(q%d)", q)
			}
			atomic.AddInt64(&total, 1)
			rng := rand.New(rand.NewSource(seed + int64(it.Student)*131 + int64(q)*17))
			jobs <- job{student: it.Student, score: studentScore(rng, full, it.Student)}
		}
		close(jobs)
		gwg.Wait()
	}
	log.Printf("[%s] 评分完成 %d 份切片(耗时 %s)", s.key, graded, time.Since(t0).Truncate(time.Second))
}

// studentScore 按学生序号分层给分:前 20% 优、中间 60% 中、后 20% 弱,叠题间抖动。
func studentScore(rng *rand.Rand, full float64, student int) float64 {
	acc := 0.9 + rng.Float64()*0.1 // 优
	switch student % 5 {
	case 2, 3:
		acc = 0.6 + rng.Float64()*0.25 // 中
	case 4:
		acc = 0.25 + rng.Float64()*0.35 // 弱
	}
	return math.Round(full*acc*2) / 2 // 0.5 步进
}

func verify(c *client, specs []examSpec, only string) {
	fmt.Println("\n===== 验证:考试列表阶段 =====")
	code, data := c.json("GET", "/api/v1/exams", nil)
	if code != http.StatusOK {
		log.Fatalf("取考试列表失败(%d): %s", code, data)
	}
	var list []struct {
		ID         string `json:"id"`
		Subject    string `json:"subject"`
		Students   int    `json:"students"`
		Objective  int    `json:"objective"`
		Subjective int    `json:"subjective"`
		Published  bool   `json:"published"`
		Stage      string `json:"stage"`
		StageLabel string `json:"stageLabel"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		log.Fatalf("列表解析失败: %v", err)
	}
	fmt.Printf("%-30s %-22s %-8s %-6s %s\n", "科目", "阶段", "已扫", "客观/主观", "发布")
	for _, e := range list {
		pub := ""
		if e.Published {
			pub = "✔"
		}
		fmt.Printf("%-32s %-24s %-8d %d/%d      %s\n", e.Subject, e.StageLabel, e.Students, e.Objective, e.Subjective, pub)
	}
}

// ---- 造数规格 ----

type examSpec struct {
	key         string
	subject     string
	scans       int    // 上传扫描张数(按全局序号 0..scans-1 生成)
	special     func(i int, key []string) *card.Filling // 覆盖全局序号 i 的特型卷(重答/漏涂考号)
	absentRegs  []string
	gradeAll    bool
	gradeUpTo   int
	assignTasks bool
	publish     bool
}

// fillingAt 返回全局第 i 张卷的填涂:i 号卷默认考号 i+1 的按水平作答;special 覆盖。
func (s *examSpec) fillingAt(i int, key []string) *card.Filling {
	if s.special != nil {
		if f := s.special(i, key); f != nil {
			return f
		}
	}
	return studentFilling(i, key, 0, 0)
}

func examSpecs(n int) []examSpec {
	reg := func(x int) string { return fmt.Sprintf("%08d", x) }
	return []examSpec{
		{
			key:     "awaiting",
			subject: "语文(演示·待扫描)",
		},
		{
			key:     "scanning",
			subject: "数学(演示·扫描中)",
			scans:   n * 19 / 30, // 1900/3000:名册已知而未扫满 → 扫描中
		},
		{
			key:     "anomaly",
			subject: "英语(演示·异常待处理)",
			scans:   n,
			special: func(i int, key []string) *card.Filling {
				switch i {
				case n - 2: // 重答:与第 6 号卷同考号二次上传 → 疑似重答告警(小名册也成立)
					return studentFilling(5, key, 5*131+7, 3)
				case n - 1: // 考号漏涂 → 考号无法定位,进异常清单待人工指派
					f := studentFilling(i, key, 0, 0)
					f.Reg[len(f.Reg)/2] = -1
					return f
				}
				return nil
			},
		},
		{
			key:         "grading",
			subject:     "物理(演示·阅卷中)",
			scans:       n,
			assignTasks: true,
			gradeUpTo:   n * 6 / 10, // 评完 60% → 阅卷中
		},
		{
			key:      "ready",
			subject:  "化学(演示·待发布)",
			scans:    n - 5,
			absentRegs: []string{reg(n - 4), reg(n - 3), reg(n - 2), reg(n - 1), reg(n)},
			gradeAll: true,
		},
		{
			key:        "published",
			subject:    "生物(演示·已发布)",
			scans:      n - 3,
			absentRegs: []string{reg(n - 2), reg(n - 1), reg(n)},
			gradeAll:   true,
			publish:    true,
		},
	}
}

// studentFilling 第 si 个学生(si 从 0 计,考号 si+1)的作答:answerSeed 驱动作答,
// wrong 控制与标准答案的差异度(0=按水平,>0 强化为演示重答的近似卷)。
func studentFilling(si int, key []string, answerSeed int64, wrong int) *card.Filling {
	seed := answerSeed
	if seed == 0 {
		seed = int64(si)*131 + 17
	}
	rng := rand.New(rand.NewSource(seed))
	f := &card.Filling{Answers: make([]string, len(key))}
	for _, ch := range fmt.Sprintf("%08d", si+1) {
		f.Reg = append(f.Reg, int(ch-'0'))
	}
	acc := 0.9 + rng.Float64()*0.1
	switch si % 5 {
	case 2, 3:
		acc = 0.6 + rng.Float64()*0.25
	case 4:
		acc = 0.25 + rng.Float64()*0.35
	}
	if wrong > 0 {
		acc = 0.7
	}
	for qi := range key {
		if rng.Float64() < acc+float64(wrong)*0.05 {
			f.Answers[qi] = key[qi]
			continue
		}
		letters := []string{"A", "B", "C", "D"}
		if key[qi] == "T" || key[qi] == "F" {
			letters = []string{"T", "F"}
		}
		a := letters[rng.Intn(len(letters))]
		if a == key[qi] {
			a = letters[(rng.Intn(len(letters)-1)+1)%len(letters)]
		}
		f.Answers[qi] = a
	}
	return f
}

func randomKey(rng *rand.Rand, sheet *card.Sheet) []string {
	ans := make([]string, len(sheet.Choice))
	for qi, opts := range sheet.Choice {
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(opts))
		ans[qi] = letters[rng.Intn(len(letters))]
	}
	return ans
}

// buildRoster 一届学生:考号 1..n,班级 高一(1)班..(20)班,姓名混进制构造保证唯一。
func buildRoster(n int) []map[string]string {
	surnames := []string{
		"王", "李", "张", "刘", "陈", "杨", "黄", "赵", "吴", "周", "徐", "孙",
		"马", "朱", "胡", "郭", "何", "林", "罗", "高", "郑", "梁", "谢", "宋",
		"唐", "许", "韩", "冯", "邓", "曹", "彭", "肖",
	}
	g1 := []rune("梓涵宇轩浩然雨欣子墨思远静怡佳琪俊昊诗雪丽华明志强文博雅慧颖洁岚")
	g2 := []rune("泽琪晨曦嘉铭心怡瑞霖彤彤飞扬启铭书瑶婉清若溪柏川景行云舒望舒知行")
	out := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s%c%c", surnames[i%len(surnames)],
			g1[(i/len(surnames))%len(g1)], g2[(i/len(surnames)/len(g1))%len(g2)])
		out = append(out, map[string]string{
			"reg":   fmt.Sprintf("%08d", i+1),
			"name":  name,
			"class": fmt.Sprintf("高一(%d)班", i/150+1),
		})
	}
	return out
}

// ---- 出图 ----

type genCtx struct {
	sheet   *card.Sheet
	key     []string
	seed    int64
	distort bool // 模拟扫描损伤(更真实,但 2 核服务器上出图慢数倍,且极小概率产生计划外告警)
}

// renderLock 串行化出卡:font 包的字形缓存(maskCache/segCache)无并发保护,
// 并发 RenderPNG 会触发 concurrent map read and write。
var renderLock sync.Mutex

// render 出一张模拟扫描件:出卡 →(可选)透视/旋转/光照/噪声损伤 → JPEG(质量 85,与 e2e 同口径)。
func (g *genCtx) render(idx int, f *card.Filling) []byte {
	rng := rand.New(rand.NewSource(g.seed*1000003 + int64(idx)))
	renderLock.Lock()
	png := card.RenderPNG(g.sheet, f)
	renderLock.Unlock()
	img := png.ToGray()
	if g.distort {
		img = synth.Distort(img, rng)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		log.Fatalf("JPEG 编码失败: %v", err)
	}
	return buf.Bytes()
}

// ---- 断点续跑状态 ----

type seedState struct {
	path     string
	mu       sync.Mutex
	Exams    map[string]string `json:"exams"`
	Uploaded map[string]int    `json:"uploaded"`
	Done     map[string]bool   `json:"done"`
}

func loadState(path string) *seedState {
	st := &seedState{path: path, Exams: map[string]string{}, Uploaded: map[string]int{}, Done: map[string]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	return st
}

func (st *seedState) save() {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return
	}
	if err := os.WriteFile(st.path, b, 0o644); err != nil {
		log.Printf("状态文件写入失败(断点续跑不可用): %v", err)
	}
}

// ExamID 取考试 ID,缺则调 create 并记录。
func (st *seedState) ExamID(key string, create func() string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if id, ok := st.Exams[key]; ok {
		return id
	}
	id := create()
	st.Exams[key] = id
	st.save()
	return id
}

func (st *seedState) SetUploaded(key string, n int) {
	st.mu.Lock()
	st.Uploaded[key] = n
	st.mu.Unlock()
	if n%50 == 0 {
		st.save()
	}
}

func (st *seedState) MarkDone(key string) {
	st.mu.Lock()
	st.Done[key] = true
	st.mu.Unlock()
	st.save()
}
