// ogserver 把开源内核以 HTTP API 形式暴露,并带完整账号权限体系:
//   - 原子能力:出卡 / 读卡 / 统分
//   - 考试工作流:建考 → 切片 → 任务分配 → 阅卷 → 指派 → 报告 → 导出
//   - 账号角色:admin(管理员)/ leader(年级组长)/ teacher(教师)/ student(学生)
//   - 学生端:免费查分、错题本、答卷回溯(只看本人)
//
// 数据全部落在本机 data/ 目录 —— "数据 100% 本地化"的承诺。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	_ "image/jpeg"
	"image/png"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
)

// Server 是组装完成的整机(供 main 与 E2E 回归测试共用)。
type Server struct {
	Handler http.Handler
	Auth    *auth.Store
	Exams   *Store
	Bank    *BankStore
}

// newServer 组装账号库、考试存储与全部路由(不含网络监听,便于 httptest 复测)。
func newServer(dataDir string, seed bool) (*Server, error) {
	authStore, err := auth.Load(dataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化账号库失败: %w", err)
	}
	if first, err := authStore.Bootstrap(); err != nil {
		return nil, fmt.Errorf("初始化管理员失败: %w", err)
	} else if first {
		log.Println("已创建默认管理员 admin/admin123 —— 请登录后立即修改密码!")
	}
	if seed {
		if err := seedDemo(authStore); err != nil {
			log.Printf("播种示例组织失败: %v", err)
		}
	}

	store, err := NewStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化数据目录失败: %w", err)
	}
	bank, err := LoadBank(dataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化题库失败: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "opengrade", "version": "0.3.0"})
	})

	// —— 鉴权(公开) ——
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		handleLogin(w, r, authStore)
	})
	// —— 鉴权(任意已登录) ——
	any := guard(authStore)
	withAuth := func(h func(http.ResponseWriter, *http.Request, *auth.Store, *auth.User)) authedHandler {
		return func(w http.ResponseWriter, r *http.Request, u *auth.User) { h(w, r, authStore, u) }
	}
	mux.HandleFunc("POST /api/v1/auth/logout", any(withAuth(handleLogout)))
	mux.HandleFunc("GET /api/v1/auth/me", any(withAuth(handleMe)))
	mux.HandleFunc("POST /api/v1/auth/password", any(withAuth(handlePasswordChange)))

	// —— 教职员能力 ——
	staff := guard(authStore, auth.RoleAdmin, auth.RoleLeader, auth.RoleTeacher)
	withStore := func(h func(http.ResponseWriter, *http.Request, *Store, *auth.User)) authedHandler {
		return func(w http.ResponseWriter, r *http.Request, u *auth.User) { h(w, r, store, u) }
	}
	mux.HandleFunc("POST /api/v1/cards", staff(handleCards))
	mux.HandleFunc("POST /api/v1/omr", staff(handleOMR))
	mux.HandleFunc("POST /api/v1/score", staff(handleScore))
	mux.HandleFunc("POST /api/v1/exams", staff(withStore(func(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
		handleCreateExam(w, r, store, bank, u) // 建考后向校本题库回记「题目被本场使用」
	})))
	mux.HandleFunc("GET /api/v1/exams", staff(withStore(handleListExams)))
	mux.HandleFunc("GET /api/v1/exams/{id}", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleExamDetail(w, r, store, u, authStore)
	}))
	mux.HandleFunc("GET /api/v1/exams/{id}/report", staff(withStore(handleExamReport)))
	mux.HandleFunc("GET /api/v1/exams/{id}/slices", staff(withStore(handleSlices)))
	mux.HandleFunc("GET /api/v1/exams/{id}/slice/{student}/{q}", staff(withStore(handleSliceImage)))
	mux.HandleFunc("GET /api/v1/exams/{id}/page/{student}", staff(withStore(handleExamPageStaff)))
	mux.HandleFunc("POST /api/v1/grades", staff(withStore(handleSetGrade)))
	mux.HandleFunc("POST /api/v1/assign", staff(withStore(handleAssign)))
	mux.HandleFunc("GET /api/v1/exams/{id}/suggest/{student}", staff(withStore(handleSuggest)))
	mux.HandleFunc("GET /api/v1/exams/{id}/roster", staff(withStore(handleRosterSearch)))
	mux.HandleFunc("GET /api/v1/exams/{id}/export.xlsx", staff(withStore(handleExportXlsx)))
	mux.HandleFunc("GET /api/v1/exams/{id}/export.csv", staff(withStore(handleExportCSV)))
	mux.HandleFunc("GET /api/v1/exams/{id}/export.zip", staff(withStore(handleExportFullZip)))
	mux.HandleFunc("GET /api/v1/exams/{id}/review.pptx", staff(withStore(handleReviewPPTX)))
	mux.HandleFunc("GET /api/v1/exams/{id}/analytics", staff(withStore(handleAnalytics)))
	mux.HandleFunc("GET /api/v1/exams/{id}/knowledge", staff(withStore(handleKnowledgeGet)))
	mux.HandleFunc("POST /api/v1/exams/{id}/knowledge", staff(withStore(handleKnowledgeSet)))
	mux.HandleFunc("GET /api/v1/exams/{id}/practice", staff(withStore(handleStaffPractice)))
	mux.HandleFunc("POST /api/v1/exams/{id}/grade-convert", staff(withStore(handleGradeConvert)))
	// 纵向学情(跨考试按考号对齐)与扫描/补录补救语义
	mux.HandleFunc("GET /api/v1/trends", staff(withStore(handleStaffTrends)))
	mux.HandleFunc("POST /api/v1/exams/{id}/scans", staff(withStore(handleAppendScans)))
	mux.HandleFunc("POST /api/v1/exams/{id}/manual", staff(withStore(handleManualEntry)))
	mux.HandleFunc("POST /api/v1/exams/{id}/absent", staff(withStore(handleSetAbsentFlag)))

	// —— 校本题库(学校自录题;建考选题带出题型/答案/知识点) ——
	withBank := func(h func(http.ResponseWriter, *http.Request, *BankStore, *auth.User)) authedHandler {
		return func(w http.ResponseWriter, r *http.Request, u *auth.User) { h(w, r, bank, u) }
	}
	mux.HandleFunc("GET /api/v1/bank", staff(withBank(handleBankList)))
	mux.HandleFunc("GET /api/v1/bank/{id}", staff(withBank(handleBankGet)))
	mux.HandleFunc("POST /api/v1/bank", staff(withBank(handleBankCreate)))
	mux.HandleFunc("POST /api/v1/bank/{id}", staff(withBank(handleBankUpdate)))
	mux.HandleFunc("DELETE /api/v1/bank/{id}", staff(withBank(handleBankDelete)))

	// —— 管理员/年级组长 ——
	manage := guard(authStore, auth.RoleAdmin, auth.RoleLeader)
	mux.HandleFunc("DELETE /api/v1/exams/{id}", manage(withStore(handleDeleteExam)))
	mux.HandleFunc("POST /api/v1/exams/{id}/tasks", manage(withStore(handleTaskSet)))
	mux.HandleFunc("GET /api/v1/exams/{id}/rooms", staff(withStore(handleRoomsGet)))
	mux.HandleFunc("POST /api/v1/exams/{id}/rooms", staff(withStore(handleRoomsGenerate)))
	mux.HandleFunc("PATCH /api/v1/exams/{id}/rooms", staff(withStore(handleRoomsPatch)))
	mux.HandleFunc("GET /api/v1/exams/{id}/audit", manage(withStore(handleAudit)))
	// 参考答案修正与成绩发布:管理员/组长/建卷教师(处理器内再校验建卷人)
	mux.HandleFunc("POST /api/v1/exams/{id}/key", staff(withStore(handleSetKey)))
	mux.HandleFunc("POST /api/v1/exams/{id}/publish", staff(withStore(handlePublish)))

	// —— 管理员 ——
	adminOnly := guard(authStore, auth.RoleAdmin)
	mux.HandleFunc("GET /api/v1/users", adminOnly(withAuth(handleUserList)))
	mux.HandleFunc("POST /api/v1/users", adminOnly(withAuth(handleUserCreate)))
	mux.HandleFunc("POST /api/v1/users/import", adminOnly(withAuth(handleUserImport)))
	mux.HandleFunc("POST /api/v1/users/{username}", adminOnly(withAuth(handleUserUpdate)))
	mux.HandleFunc("DELETE /api/v1/users/{username}", adminOnly(withAuth(handleUserDelete)))
	mux.HandleFunc("POST /api/v1/users/{username}/password", adminOnly(withAuth(handleUserResetPassword)))

	// —— 学生端(仅本人);家长端(仅绑定子女)复用同一组处理器 ——
	studentOnly := guard(authStore, auth.RoleStudent, auth.RoleParent)
	mux.HandleFunc("GET /api/v1/student/exams", studentOnly(withStore(handleStudentExams)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}", studentOnly(withStore(handleStudentExam)))
	mux.HandleFunc("GET /api/v1/student/trends", studentOnly(withStore(handleStudentTrends)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}/slice/{q}", studentOnly(withStore(handleStudentSlice)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}/page", studentOnly(withStore(handleStudentPage)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}/practice", studentOnly(withStore(handleStudentPractice)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}/practice.svg", studentOnly(withStore(handleStudentPracticeSVG)))
	mux.HandleFunc("GET /api/v1/student/exams/{id}/report.pdf", studentOnly(withStore(handleStudentReportPDF)))

	if _, err := fs.Stat(os.DirFS("web"), "index.html"); err == nil {
		mux.Handle("GET /", http.FileServerFS(os.DirFS("web")))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "OpenGrade API 已启动。请打开 /login.html 登录。\n")
		})
	}

	return &Server{Handler: mux, Auth: authStore, Exams: store, Bank: bank}, nil
}

func main() {
	addr := flag.String("addr", ":8787", "监听地址")
	dataDir := flag.String("data", "data", "数据目录")
	seed := flag.Bool("seed-demo", false, "播种示例组织(默认管理员/组长/教师/学生/班级),便于快速评估")
	flag.Parse()
	srv, err := newServer(*dataDir, *seed)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("OpenGrade API 监听 %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// authedHandler 是已通过鉴权(并可带角色约束)的处理器。
type authedHandler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// guard 返回鉴权中间件;不传角色 = 任意已登录用户。
func guard(authStore *auth.Store, roles ...auth.Role) func(authedHandler) http.HandlerFunc {
	return func(h authedHandler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookie)
			if err != nil {
				httpError(w, http.StatusUnauthorized, fmt.Errorf("未登录"))
				return
			}
			u, ok := authStore.SessionUser(c.Value)
			if !ok {
				httpError(w, http.StatusUnauthorized, fmt.Errorf("未登录或会话已过期"))
				return
			}
			if len(roles) > 0 {
				allowed := false
				for _, want := range roles {
					if u.Role == want {
						allowed = true
						break
					}
				}
				if !allowed {
					httpError(w, http.StatusForbidden, fmt.Errorf("当前角色 %s 无权执行此操作", u.Role))
					return
				}
			}
			h(w, r, u)
		}
	}
}

// seedDemo 播种示例组织,便于快速评估(已存在的账号跳过)。
func seedDemo(authStore *auth.Store) error {
	seed := []struct {
		username, display string
		role              auth.Role
		password          string
		reg, class        string
		subjects, grades  []string
	}{
		{"leader1", "李主任", auth.RoleLeader, "leader123", "", "", nil, []string{"三年级"}},
		{"teacher1", "张老师", auth.RoleTeacher, "teach123", "", "三年二班", []string{"数学"}, nil},
		{"teacher2", "王老师", auth.RoleTeacher, "teach123", "", "三年一班", []string{"语文"}, nil},
		{"s0001", "李雷", auth.RoleStudent, "student123", "00000001", "三年二班", nil, nil},
		{"s0002", "韩梅梅", auth.RoleStudent, "student123", "00000002", "三年二班", nil, nil},
		{"s0003", "王小明", auth.RoleStudent, "student123", "00000003", "三年二班", nil, nil},
		{"s0004", "赵小红", auth.RoleStudent, "student123", "00000004", "三年一班", nil, nil},
		{"s0005", "孙小刚", auth.RoleStudent, "student123", "00000005", "三年一班", nil, nil},
	}
	for _, s := range seed {
		if _, exists := authStore.Users[s.username]; exists {
			continue
		}
		u := &auth.User{Username: s.username, DisplayName: s.display, Role: s.role,
			Reg: s.reg, Class: s.class, Subjects: s.subjects, Grades: s.grades}
		if err := authStore.CreateUser(u, s.password); err != nil {
			return err
		}
		log.Printf("已播种示例账号:%s / %s(%s)", s.username, s.password, s.role)
	}
	return nil
}

// handleCards 依据版式 JSON 渲染答题卡;?format=png 返回位图,缺省返回可打印 SVG。
func handleCards(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req struct {
		Layout  card.Layout   `json:"layout"`
		Filling *card.Filling `json:"filling"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	book, err := req.Layout.BuildBook()
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	pageNo := 1
	if pv := r.URL.Query().Get("page"); pv != "" {
		if pageNo, err = strconv.Atoi(pv); err != nil || pageNo < 1 || pageNo > len(book) {
			httpError(w, http.StatusBadRequest, fmt.Errorf("page 须为 1~%d", len(book)))
			return
		}
	}
	w.Header().Set("X-Pages", strconv.Itoa(len(book)))
	sheet := book[pageNo-1]
	if r.URL.Query().Get("format") == "png" {
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, card.RenderPNG(sheet, req.Filling))
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = w.Write(card.RenderSVG(sheet, req.Filling))
}

// handleOMR 读卡:multipart 表单,字段 image=扫描件,layout=版式 JSON,threshold=otsu|adaptive。
func handleOMR(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("解析 multipart 失败: %w", err))
		return
	}
	file, sheet, opt, err := formLayoutAndImage(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("图像解码失败: %w", err))
		return
	}
	res, _, err := omr.Read(sheet, img, opt)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// formLayoutAndImage 从 multipart 表单提取图像文件与编译后的版式(单卷读卡用)。
func formLayoutAndImage(r *http.Request) (io.ReadCloser, *card.Sheet, omr.Options, error) {
	file, _, err := r.FormFile("image")
	if err != nil {
		return nil, nil, omr.Options{}, fmt.Errorf("缺少 image 字段: %w", err)
	}
	sheet, opt, err := formLayout(r)
	if err != nil {
		file.Close()
		return nil, nil, omr.Options{}, err
	}
	return file, sheet, opt, nil
}

// formLayout 从 multipart 表单提取并编译版式与读卡选项(不要求图像,建考用)。
func formLayout(r *http.Request) (*card.Sheet, omr.Options, error) {
	var layout card.Layout
	if err := json.Unmarshal([]byte(r.FormValue("layout")), &layout); err != nil {
		return nil, omr.Options{}, fmt.Errorf("layout 字段不是合法版式 JSON: %w", err)
	}
	sheet, err := layout.Build()
	if err != nil {
		return nil, omr.Options{}, err
	}
	return sheet, omr.Options{Threshold: r.FormValue("threshold")}, nil
}

// handleCreateExam 创建考试会话:multipart 字段 layout(版式 JSON)、key(客观题答案 JSON 数组)、
// roster(可选名册)、threshold(可选 otsu|adaptive)、images(多张整卷扫描件)。
// 服务端逐卷 OMR 判读 + 主观题切片 + 整卷回溯图,返回会话 ID。
func handleCreateExam(w http.ResponseWriter, r *http.Request, store *Store, bank *BankStore, u *auth.User) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("解析 multipart 失败: %w", err))
		return
	}
	sheet, opt, err := formLayout(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	var key []string
	if err := json.Unmarshal([]byte(r.FormValue("key")), &key); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("key 字段不是合法的答案数组: %w", err))
		return
	}
	if len(key) != len(sheet.Choice) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("参考答案 %d 道,与版式客观题 %d 道不一致", len(key), len(sheet.Choice)))
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["images"]) == 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 images 字段(至少一张整卷扫描件)"))
		return
	}
	var st []roster.Student
	if rb := r.FormValue("roster"); rb != "" {
		parsed, err := roster.Load([]byte(rb))
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		st = parsed
	}

	// 多页版式:主观题自动分页,每生 P 页连续上传,逐页判读后合并
	book, err := sheet.Layout.BuildBook()
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	pageCount := len(book)
	var imgs []image.Image
	for i, fh := range r.MultipartForm.File["images"] {
		f, err := fh.Open()
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("第 %d 张图像解码失败: %w", i+1, err))
			return
		}
		imgs = append(imgs, img)
	}
	if pageCount > 1 && len(imgs)%pageCount != 0 {
		httpError(w, http.StatusUnprocessableEntity,
			fmt.Errorf("版式共 %d 页:每名学生的整卷需连续上传 %d 页(当前 %d 张,须为 %d 的整数倍;扫描时按份出纸即可)", pageCount, pageCount, len(imgs), pageCount))
		return
	}

	var results []*omr.Result
	var slicesByStudent [][]omr.Slice
	var pageGroups [][]*image.Gray
	for si, group := range splitPageImages(imgs, pageCount) {
		var pages []pageScan
		for k, img := range group {
			ps, err := readPageScan(book, k, img, opt)
			if err != nil {
				httpError(w, http.StatusUnprocessableEntity,
					fmt.Errorf("第 %d 名学生第 %d/%d 页识别失败: %w", si+1, k+1, pageCount, err))
				return
			}
			pages = append(pages, ps)
		}
		merged := mergePageScans(pages)
		results = append(results, merged)
		slicesByStudent = append(slicesByStudent, slicesOfPageScans(pages, si+1, merged.RegNumber))
		rects := make([]*image.Gray, len(pages))
		for k, p := range pages {
			rects[k] = p.dbg.Rectified
		}
		pageGroups = append(pageGroups, rects)
	}

	markDuplicateRegs(nil, results) // 批内重答检测(同考号二次上传 → 告警进异常清单)
	ex, err := store.Create(NewExam{
		Layout: sheet.Layout, Key: key, Results: results, Slices: slicesByStudent,
		Pages: nil, PageGroups: pageGroups, Threshold: opt.Threshold, CreatedBy: u.Username, Subject: r.FormValue("subject"), Roster: st,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	// 校本题库回记:版式里来自题库的题标记「被本场考试使用」
	if bank != nil {
		for _, q := range ex.Layout.Questions {
			if q.BankID != "" {
				_ = bank.MarkUsed(q.BankID, ex.ID)
			}
		}
		for _, sub := range ex.Layout.Subjectives {
			if sub.BankID != "" {
				_ = bank.MarkUsed(sub.BankID, ex.ID)
			}
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":              ex.ID,
		"students":        len(results),
		"objectiveCount":  len(sheet.Choice),
		"subjectiveCount": len(sheet.SubjectiveBoxes),
		"pages":           pageCount,
		"threshold":       opt.Threshold,
		"roster":          len(st),
	})
}

// markDuplicateRegs 重答检测:同一考号再次出现(批内或与存量)不阻塞流程,
// 但给后到的卷打「疑似重答」告警,进考试详情的异常清单由人工裁决。
func markDuplicateRegs(existing []*omr.Result, batch []*omr.Result) {
	seen := map[string]int{} // 考号 → 首次出现的卷号(1-based)
	for i, r := range existing {
		if r != nil && r.RegValid && r.RegNumber != "" {
			if _, dup := seen[r.RegNumber]; !dup {
				seen[r.RegNumber] = i + 1
			}
		}
	}
	base := len(existing)
	for i, r := range batch {
		if r == nil || !r.RegValid || r.RegNumber == "" {
			continue
		}
		if first, dup := seen[r.RegNumber]; dup {
			r.Warnings = append(r.Warnings, fmt.Sprintf("考号 %s 与卷 %d 重复,疑似重答", r.RegNumber, first))
		} else {
			seen[r.RegNumber] = base + i + 1
		}
	}
}

func handleListExams(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	type summary struct {
		ID         string `json:"id"`
		CreatedAt  string `json:"createdAt"`
		CreatedBy  string `json:"createdBy"`
		Subject    string `json:"subject,omitempty"`
		Students   int    `json:"students"`
		Objective  int    `json:"objective"`
		Subjective int    `json:"subjective"`
		Published  bool   `json:"published"`
		Mine       bool   `json:"mine"` // 是否为当前账号创建的
	}
	out := []summary{} // 空列表也返回 [],避免 null 使前端 .map/.forOf 崩溃
	for _, ex := range store.List() {
		if !canOpenExam(u, ex) {
			continue // 学科教师只看任教班级+学科的考试
		}
		out = append(out, summary{
			ID: ex.ID, CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), CreatedBy: ex.CreatedBy,
			Subject: ex.Subject, Students: len(ex.Results),
			Objective: len(ex.Layout.Questions), Subjective: len(ex.Layout.Subjectives),
			Published: ex.Published, Mine: ex.CreatedBy == u.Username,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// examDetail 是工作台首屏所需的会话元数据。
type examDetail struct {
	ID         string                `json:"id"`
	CreatedAt  string                `json:"createdAt"`
	CreatedBy  string                `json:"createdBy"`
	Subject    string                `json:"subject,omitempty"`
	Students   int                   `json:"students"`
	Objective  []card.QuestionSpec   `json:"objective"`
	Key        []string              `json:"key"` // 客观题参考答案(供修正)
	Subjective []card.SubjectiveSpec `json:"subjective"`
	Progress   []questionProgress    `json:"progress"`
	Tasks      map[string]string     `json:"tasks,omitempty"`
	TaskMode   string                `json:"taskMode,omitempty"`   // "" | question | class
	ClassTasks map[string]string     `json:"classTasks,omitempty"` // 班级 → 教师账号
	Classes    []string              `json:"classes,omitempty"`    // 名册中出现的班级(按班分配下拉)
	Threshold  string                `json:"threshold"`
	Published  bool                  `json:"published"`
	CanEdit    bool                  `json:"canEditKey"` // 当前账号是否可修正参考答案
	Staff      []staffInfo           `json:"staff"`      // 可分配的阅卷教师(任务分配下拉)
	Absent     []absentInfo          `json:"absent"`     // 缺考名单(卷号+考号)
	Manual     int                   `json:"manual"`     // 手工补录卷数
	Warnings   []sheetWarning        `json:"warnings"`   // 异常卷告警汇总(重答/空白卡/漏涂等)
}

// sheetWarning 是一张卷的告警汇总(考试详情异常清单)。
type sheetWarning struct {
	Student int      `json:"student"` // 卷号(1-based)
	Reg     string   `json:"reg,omitempty"`
	Texts   []string `json:"texts"`
}

type absentInfo struct {
	Student int    `json:"student"`
	Reg     string `json:"reg"`
}

type staffInfo struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

type questionProgress struct {
	Question int     `json:"question"` // 1-based 主观题号
	Kind     string  `json:"kind"`
	Max      float64 `json:"max"`
	Graded   int     `json:"graded"`
	Total    int     `json:"total"`
	Teacher  string  `json:"teacher,omitempty"`  // 已分配的阅卷人
	Elective string  `json:"elective,omitempty"` // 选做组标签(版式 v3);非空即选做题
}

func handleExamDetail(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User, authStore *auth.Store) {
	ex, ok := store.openExamFor(r, u, true) // 详情供阅卷,教师全量
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	d := examDetail{
		ID: ex.ID, CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), CreatedBy: ex.CreatedBy,
		Subject: ex.Subject, Students: len(ex.Results), Objective: ex.Layout.Questions,
		Key: ex.ObjectiveKey, Subjective: ex.Layout.Subjectives, Tasks: ex.Tasks, Threshold: ex.Threshold,
		TaskMode: ex.TaskMode, ClassTasks: ex.ClassTasks, Classes: examClasses(ex),
		Published: ex.Published,
		CanEdit:   u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username,
	}
	for _, su := range authStore.List() {
		if su.Role == auth.RoleTeacher || su.Role == auth.RoleLeader || su.Role == auth.RoleAdmin {
			d.Staff = append(d.Staff, staffInfo{su.Username, su.DisplayName, string(su.Role)})
		}
	}
	for qi := range ex.Layout.Subjectives {
		p := questionProgress{Question: qi + 1, Total: len(ex.Results)}
		if qi < len(ex.Layout.Subjectives) {
			p.Kind = ex.Layout.Subjectives[qi].Kind
			p.Max = ex.Layout.Subjectives[qi].Score
			p.Elective = ex.Layout.Subjectives[qi].Elective
		}
		if ex.Tasks != nil {
			p.Teacher = ex.Tasks[fmt.Sprintf("q%d", qi+1)]
		}
		for si := range ex.Results {
			if subjectiveGraded(ex, si+1, qi+1) {
				p.Graded++
			}
		}
		d.Progress = append(d.Progress, p)
	}
	for si, res := range ex.Results {
		if res == nil {
			continue
		}
		if res.Manual {
			d.Manual++
		}
		if res.Absent {
			d.Absent = append(d.Absent, absentInfo{Student: si + 1, Reg: ex.AssignedReg(si + 1)})
		}
		if len(res.Warnings) > 0 {
			d.Warnings = append(d.Warnings, sheetWarning{Student: si + 1, Reg: ex.AssignedReg(si + 1), Texts: res.Warnings})
		}
	}
	writeJSON(w, http.StatusOK, d)
}

// partState 是切片列表中一道拆分小问的当前评分状态。
type partState struct {
	Part   int     `json:"part"`
	Score  float64 `json:"score"`
	Graded bool    `json:"graded"`
}

// electiveView 是切片列表里某生在某选做组的题号区涂选情况(版式 v3)。
type electiveView struct {
	Group  []int    `json:"group"`  // 组内主观题号(1-based,升序)
	Labels []string `json:"labels"` // 卷面全局题号(如 "23")
	Marks  []int    `json:"marks"`  // 该生涂点下标(0-based);漏涂为空、多涂多值
	Chosen int      `json:"chosen"` // 判分计入的主观题号;0 = 该组无一题计入
}

// handleSlices 列出某道主观题的全部学生切片(含当前评分状态与批注笔迹),按学生号排序。
func handleSlices(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, r.URL.Query().Get("grading") == "1") // 报告视图默认按任教班级过滤;grading=1 全量
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	q, err := strconv.Atoi(r.URL.Query().Get("q"))
	if err != nil || q < 1 || q > len(ex.Layout.Subjectives) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("q 参数必须是 1~%d 的主观题号", len(ex.Layout.Subjectives)))
		return
	}
	groups := score.ElectiveGroups(ex.Layout.Subjectives)
	var eg *score.ElectiveGroup
	if g := score.GroupOf(groups, q); g != nil {
		eg = g
	}
	type item struct {
		Student  int            `json:"student"`
		Reg      string         `json:"reg"`
		RegValid bool           `json:"regValid"`
		URL      string         `json:"url"`
		Graded   bool           `json:"graded"`
		Score    float64        `json:"score"`
		Mark     string         `json:"mark"`
		Ink      []score.Stroke `json:"ink,omitempty"`
		Missing  bool           `json:"missing"`         // 无切片图(手工补录卷),工作台显示占位
		Parts    []partState    `json:"parts,omitempty"` // 拆分小问的当前评分(版式 v3)
		Elective *electiveView  `json:"elective,omitempty"`
	}
	out := make([]item, 0, len(ex.Results))
	for si := range ex.Results {
		if ex.Results[si] == nil {
			continue // 教师视图下不可见学生
		}
		it := item{Student: si + 1, URL: fmt.Sprintf("/api/v1/exams/%s/slice/%d/%d", ex.ID, si+1, q)}
		if si < len(ex.Results) {
			it.Reg = ex.AssignedReg(si + 1)
			it.RegValid = ex.Results[si].RegValid || ex.Assignments[fmt.Sprintf("s%d", si+1)] != ""
		}
		if eg != nil {
			marks := []int{}
			if res := ex.Results[si]; res != nil && len(res.Electives) > 0 {
				marks = res.Electives[eg.Members[0]]
			}
			if marks == nil {
				marks = []int{}
			}
			labels := make([]string, len(eg.Members))
			for mi, mq := range eg.Members {
				labels[mi] = fmt.Sprintf("%d", len(ex.Layout.Questions)+mq)
			}
			// 判分计入的题:排除集合之外的那道;整组都不计(漏涂且无批阅)时为 0
			excluded := score.ElectiveExcluded(groups, ex.Layout.Subjectives, ex.Grades, ex.Results[si], si+1)
			chosen := 0
			for _, mq := range eg.Members {
				if !excluded[mq] {
					chosen = mq
					break
				}
			}
			it.Elective = &electiveView{Group: eg.Members, Labels: labels, Marks: marks, Chosen: chosen}
		}
		if g, ok := ex.Grades[score.GradeKey(si+1, q)]; ok {
			it.Graded, it.Score, it.Mark, it.Ink = g.Graded, g.Score, g.Mark, g.Ink
		}
		if spec := ex.Layout.Subjectives[q-1]; len(spec.Parts) > 0 {
			it.Parts = make([]partState, len(spec.Parts))
			var sum float64
			for pi := range spec.Parts {
				ps := partState{Part: pi + 1}
				if g, ok := ex.Grades[score.GradePartKey(si+1, q, pi+1)]; ok {
					ps.Score, ps.Graded = g.Score, g.Graded
					if g.Graded {
						sum += g.Score
					}
				}
				it.Parts[pi] = ps
			}
			it.Score = sum
			// 小问拆分题:题级标注(✓✗⚑/笔迹)同步存在每个小问上,读第 1 问
			if g, ok := ex.Grades[score.GradePartKey(si+1, q, 1)]; ok {
				it.Mark, it.Ink = g.Mark, g.Ink
			}
			_, it.Graded, _ = score.SubjectiveScore(spec, ex.Grades, si+1, q)
		}
		it.Missing = !fileExists(filepath.Join(store.SliceDir(ex.ID), fmt.Sprintf("s%02d_q%02d.png", si+1, q)))
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSliceImage 返回单张切片 PNG。
func handleSliceImage(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	id := r.PathValue("id")
	ex, ok := store.Get(id)
	if !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	p := filepath.Join(store.SliceDir(id),
		fmt.Sprintf("s%02s_q%02s.png", pad2(r.PathValue("student")), pad2(r.PathValue("q"))))
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, p)
}

func pad2(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%02d", n)
}

// handleSetGrade 写入一条主观题评分(含手写批注笔迹);part>0 时为小问评分(版式 v3)。
func handleSetGrade(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	var req struct {
		ExamID   string         `json:"examId"`
		Student  int            `json:"student"`
		Question int            `json:"question"`
		Part     int            `json:"part"` // 1-based 小问号;0 = 整题
		Score    float64        `json:"score"`
		Mark     string         `json:"mark"`
		Graded   *bool          `json:"graded"`
		Ink      []score.Stroke `json:"ink"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	graded := true
	if req.Graded != nil {
		graded = *req.Graded
	}
	// 阅卷走可见性门禁(全量,不按班级过滤)
	if ex, ok := store.Get(req.ExamID); !ok || !canOpenExam(u, ex) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	g := score.Grade{Score: req.Score, Mark: req.Mark, Graded: graded, Ink: req.Ink}
	var err error
	if req.Part > 0 {
		err = store.SetGradePart(req.ExamID, req.Student, req.Question, req.Part, g, u.Username)
	} else {
		err = store.SetGrade(req.ExamID, req.Student, req.Question, g, u.Username)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// subjectiveGraded 报告某生某道主观题是否完成评分(拆分小问时要求全部小问已阅)。
func subjectiveGraded(ex *Exam, student, question int) bool {
	if question < 1 || question > len(ex.Layout.Subjectives) {
		return false
	}
	_, done, _ := score.SubjectiveScore(ex.Layout.Subjectives[question-1], ex.Grades, student, question)
	return done
}

// handleExamReport 统分报告(附加按班级聚合的对比统计)。
func handleExamReport(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.openExamFor(r, u, false) // 报告按任教班级过滤
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	rep := score.Merge(ex.Layout.Questions, ex.ObjectiveKey, assignedResults(ex), ex.Layout.Subjectives, ex.Grades)
	writeJSON(w, http.StatusOK, struct {
		*score.Report
		ByClass []ClassStat `json:"byClass"`
	}{rep, computeClassStats(ex, rep)})
}

// handleScore 纯客观统分(无会话)。
func handleScore(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var req struct {
		Key     []string      `json:"key"`
		Results []*omr.Result `json:"results"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, score.GradeObjective(req.Key, req.Results))
}

func decodeJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
