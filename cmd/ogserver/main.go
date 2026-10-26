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
	"errors"
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
	"strings"
	"time"

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
	School  *School
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
	school, err := LoadSchool(dataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化组织架构失败: %w", err)
	}
	store.School = school
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
	mux.HandleFunc("POST /api/v1/layout/analyze", staff(handleLayoutAnalyze)) // 试卷版面分析(半自动出卡)
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
	mux.HandleFunc("GET /api/v1/exams/{id}/slips.pdf", staff(withStore(handleSlipsPDF))) // 全班成绩条连排 PDF
	mux.HandleFunc("GET /api/v1/exams/{id}/knowledge", staff(withStore(handleKnowledgeGet)))
	mux.HandleFunc("POST /api/v1/exams/{id}/knowledge", staff(withStore(handleKnowledgeSet)))
	mux.HandleFunc("GET /api/v1/exams/{id}/practice", staff(withStore(handleStaffPractice)))
	mux.HandleFunc("POST /api/v1/exams/{id}/grade-convert", staff(withStore(handleGradeConvert)))
	// 纵向学情(跨考试按考号对齐)与扫描/补录补救语义
	mux.HandleFunc("GET /api/v1/trends", staff(withStore(handleStaffTrends)))
	mux.HandleFunc("GET /api/v1/aggregate", staff(withStore(handleAggregate))) // 年级总分工表(同场分组多科汇总)
	mux.HandleFunc("POST /api/v1/exams/{id}/scans", staff(withStore(handleAppendScans)))
	mux.HandleFunc("POST /api/v1/exams/{id}/manual", staff(withStore(handleManualEntry)))
	mux.HandleFunc("POST /api/v1/exams/{id}/manual-scores", staff(withStore(handleManualScores))) // 手工成绩批量直录
	mux.HandleFunc("POST /api/v1/exams/{id}/absent", staff(withStore(handleSetAbsentFlag)))
	mux.HandleFunc("POST /api/v1/exams/{id}/results/{student}/void", staff(withStore(handleVoidResult))) // 作废/恢复卷
	mux.HandleFunc("POST /api/v1/exams/{id}/meta", staff(withStore(handleExamMeta)))                     // 考试元数据/匿名开关
	mux.HandleFunc("POST /api/v1/exams/{id}/assign-variant", staff(withStore(handleAssignVariant)))      // AB 卷:人工指派卷种(重读客观题)
	mux.HandleFunc("GET /api/v1/exams/{id}/cards.pdf", staff(withStore(handleExamCardsPDF)))             // AB 卷个性化出卡(卷种点+复合码)
	mux.HandleFunc("POST /api/v1/exams/{id}/task-due", staff(withStore(handleTaskDue)))                  // 任务截止时间
	mux.HandleFunc("POST /api/v1/exams/{id}/appeals/{n}/resolve", staff(withStore(handleResolveAppeal))) // 裁决复核申请
	mux.HandleFunc("POST /api/v1/exams/{id}/appeals/block", staff(withStore(handleAppealBlock)))         // 复核申请资格:本场拉黑/解除
	mux.HandleFunc("GET /api/v1/exams/{id}/students/{reg}", staff(withStore(handleStudentDetailStaff)))  // 学生个体复核视图(逐题作答+改分+整卷)
	mux.HandleFunc("POST /api/v1/exams/{id}/results/{student}/ack", staff(withStore(handleSheetAck)))    // 残卷告警人工接受/撤销
	mux.HandleFunc("POST /api/v1/exams/{id}/flags/clear", staff(withStore(handleClearFlag)))             // 清除 ⚑ 待复核标记
	mux.HandleFunc("GET /api/v1/workload", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleWorkload(w, r, store, authStore, u) // 阅卷工作量统计
	}))

	// —— 校本题库(学校自录题;建考选题带出题型/答案/知识点) ——
	withBank := func(h func(http.ResponseWriter, *http.Request, *BankStore, *auth.User)) authedHandler {
		return func(w http.ResponseWriter, r *http.Request, u *auth.User) { h(w, r, bank, u) }
	}
	mux.HandleFunc("GET /api/v1/bank", staff(withBank(handleBankList)))
	mux.HandleFunc("GET /api/v1/bank/{id}", staff(withBank(handleBankGet)))
	mux.HandleFunc("POST /api/v1/bank", staff(withBank(handleBankCreate)))
	mux.HandleFunc("POST /api/v1/bank/{id}", staff(withBank(handleBankUpdate)))
	mux.HandleFunc("DELETE /api/v1/bank/{id}", staff(withBank(handleBankDelete)))
	mux.HandleFunc("POST /api/v1/bank/{id}/stem", staff(withBank(handleBankStemUpload)))
	mux.HandleFunc("GET /api/v1/bank/{id}/stem", staff(withBank(handleBankStemGet)))

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
	mux.HandleFunc("POST /api/v1/student/exams/{id}/appeals", studentOnly(withStore(handleCreateAppeal))) // 成绩复核申请

	// —— 组织架构与名册源(常规流程"组织树×角色×数据范围"的树那一层) ——
	mux.HandleFunc("GET /api/v1/school", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleSchoolGet(w, school, u)
	}))
	mux.HandleFunc("PUT /api/v1/school", adminOnly(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleSchoolSet(w, r, school, u)
	}))
	mux.HandleFunc("GET /api/v1/school/sessions", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleSchoolSessionsGet(w, school, u)
	}))
	mux.HandleFunc("PUT /api/v1/school/sessions", adminOnly(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleSchoolSessionsSet(w, r, school, u)
	}))
	mux.HandleFunc("GET /api/v1/school/appeal-bans", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleAppealBansGet(w, school, u)
	}))
	mux.HandleFunc("PUT /api/v1/school/appeal-bans", adminOnly(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleAppealBansSet(w, r, school, u)
	}))
	mux.HandleFunc("GET /api/v1/students", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleStudentsList(w, school, authStore, u)
	}))
	mux.HandleFunc("GET /api/v1/my/tasks", staff(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		handleMyTasks(w, r, store, school, u)
	}))

	if _, err := fs.Stat(os.DirFS("web"), "index.html"); err == nil {
		mux.Handle("GET /", http.FileServerFS(os.DirFS("web")))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "OpenGrade API 已启动。请打开 /login.html 登录。\n")
		})
	}

	return &Server{Handler: mux, Auth: authStore, Exams: store, Bank: bank, School: school}, nil
}

func main() {
	addr := flag.String("addr", ":8787", "监听地址")
	dataDir := flag.String("data", "data", "数据目录")
	seed := flag.Bool("seed-demo", false, "播种示例组织(默认管理员/组长/教师/学生/班级),便于快速评估")
	backupDir := flag.String("backup-dir", "", "备份目标目录(缺省 data/backups;建议指向 data/ 之外的物理盘;环境变量 OPENGRADE_BACKUP_DIR 兜底)")
	restoreFrom := flag.String("restore-backup", "", "从备份快照目录恢复 JSON 状态到数据目录(覆盖现状)后退出;例:-restore-backup /mnt/backup/20260101-030000")
	flagCookieSecure := flag.Bool("cookie-secure", false, "会话 Cookie 加 Secure 标记(HTTPS 部署时开启;局域网 HTTP 部署保持关闭,否则无法登录)")
	flag.Parse()
	if *restoreFrom != "" {
		n, err := RestoreBackup(*restoreFrom, *dataDir)
		if err != nil {
			log.Fatalf("恢复失败: %v", err)
		}
		log.Printf("已从 %s 恢复 %d 个 JSON 文件到 %s(重启服务前请先停服,避免运行中写入覆盖恢复数据)", *restoreFrom, n, *dataDir)
		return
	}
	cookieSecure = *flagCookieSecure || os.Getenv("OPENGRADE_COOKIE_SECURE") == "1"
	srv, err := newServer(*dataDir, *seed)
	if err != nil {
		log.Fatalf("%v", err)
	}
	// 备份目录:-backup-dir > OPENGRADE_BACKUP_DIR > data/backups。外部盘写入失败只警告不影响服务。
	dest := *backupDir
	if dest == "" {
		dest = os.Getenv("OPENGRADE_BACKUP_DIR")
	}
	StartBackupLoop(*dataDir, dest, 12*time.Hour, 10) // 启动即一次,此后每 12 小时,保留最近 10 份
	log.Printf("OpenGrade API 监听 %s", *addr)
	// 无超时的 http.Server 会被慢连接/半开连接无限占住句柄(A-7):
	// 读头要快(防护),读体放宽(多页 PDF 批量上传可达数百 MB),写放宽(成绩册/讲评 PDF 导出)。
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(httpSrv.ListenAndServe())
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
			// 服务端强制改密:带标记的账号(初始管理员/管理员重置/批量导入初始密码)
			// 在改密完成前只放行认证端点,其余 API 一律 403 —— 前端引导只是体验层,这里是闸门。
			if u.MustChangePassword {
				switch r.URL.Path {
				case "/api/v1/auth/password", "/api/v1/auth/me", "/api/v1/auth/logout":
				default:
					httpError(w, http.StatusForbidden, fmt.Errorf("须先修改初始密码后再使用"))
					return
				}
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
	// 手工成绩科目:免版式建考(体音美/实验/听力等无答题卡的成绩直录)
	if strings.TrimSpace(r.FormValue("manual")) == "1" {
		if strings.TrimSpace(r.FormValue("abAuto")) == "1" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("手工成绩考试没有答题卡,无法启用 AB 卷"))
			return
		}
		createManualExam(w, r, store, u)
		return
	}
	sheet, opt, err := formLayout(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	// 常规流程:建考=建档(版式+名册),参考答案与扫描件都可后补 ——
	// 未带答案时以空串占位(该题不判分),答案在考试详情用「修正参考答案」补齐后自动重统。
	var key []string
	if kv := strings.TrimSpace(r.FormValue("key")); kv != "" {
		if err := json.Unmarshal([]byte(kv), &key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("key 字段不是合法的答案数组: %w", err))
			return
		}
		if len(key) != len(sheet.Choice) {
			httpError(w, http.StatusBadRequest, fmt.Errorf("参考答案 %d 道,与版式客观题 %d 道不一致", len(key), len(sheet.Choice)))
			return
		}
	} else {
		key = make([]string, len(sheet.Choice))
	}

	// AB 卷(可选,启用方式二选一):
	//   abAuto=1 —— 自动生成 B 卷:A 卷客观题顺序随机打乱(错位排列),主观题区不变;
	//   layout2/order2 —— 高级手填 B 卷版式与题序对照(B 只能打乱客观题序,校验强制与 A 集合一致)。
	// 卷种点 + 复合码(考试短号+卷种位+考号)自动识别,作答入库即归一为规范题号;
	// 判分按规范题号共用一份答案键(B 卷作答归一后与 A 同键判分)。
	var (
		layoutB   *card.Layout
		keyBCanon []string
		orderB    []int
		ab        *abSupport
		shortID   string
	)
	abAuto := strings.TrimSpace(r.FormValue("abAuto")) == "1"
	lv2 := strings.TrimSpace(r.FormValue("layout2"))
	if abAuto {
		// 自动生成 B 卷与手填字段互斥:静默忽略旧字段会掩盖调用方的真实意图(如旧缓存页)
		for name, v := range map[string]string{"layout2": lv2, "key2": strings.TrimSpace(r.FormValue("key2")), "order2": strings.TrimSpace(r.FormValue("order2"))} {
			if v != "" {
				httpError(w, http.StatusBadRequest, fmt.Errorf("已选择自动生成 B 卷(abAuto),请不要再传 %s", name))
				return
			}
		}
	}
	if abAuto || lv2 != "" {
		la := sheet.Layout // 规范化后的 A 卷
		la.Variant = "A"
		var lb card.Layout
		if abAuto {
			if len(la.Questions) < 2 {
				httpError(w, http.StatusBadRequest, fmt.Errorf("自动生成 AB 卷需要至少 2 道客观题(打乱题序才有防抄袭意义)"))
				return
			}
			lbp, ob := deriveAB(&la, nil)
			lb, orderB = *lbp, ob
		} else {
			if err := json.Unmarshal([]byte(lv2), &lb); err != nil {
				httpError(w, http.StatusBadRequest, fmt.Errorf("layout2 字段不是合法版式 JSON: %w", err))
				return
			}
			orderB = nil
			if ov := strings.TrimSpace(r.FormValue("order2")); ov != "" {
				for _, part := range strings.Split(ov, ",") {
					n, err := strconv.Atoi(strings.TrimSpace(part))
					if err != nil {
						httpError(w, http.StatusBadRequest, fmt.Errorf("order2 题序对照须为逗号分隔的数字(如 3,1,4,2)"))
						return
					}
					orderB = append(orderB, n)
				}
			} else {
				for i := 1; i <= len(lb.Questions); i++ { // 缺省恒等映射(B 与 A 同题序,仅卷种不同)
					orderB = append(orderB, i)
				}
			}
		}
		lb.Variant = "B"
		// B 卷答案键:两卷共用同一份参考答案(规范题号序)。key2 缺省/空 = 按 A 卷答案
		// 依题序对照重排(自动同步);显式给出时必须与 A 卷一致,矛盾即 400 ——
		// AB 卷只打乱题序,内容不一致的 B 答案键只会造成误导(判分实际只用规范键)。
		var key2 []string
		if !abAuto {
			if kv2 := strings.TrimSpace(r.FormValue("key2")); kv2 != "" {
				if err := json.Unmarshal([]byte(kv2), &key2); err != nil {
					httpError(w, http.StatusBadRequest, fmt.Errorf("key2 字段不是合法的答案数组: %w", err))
					return
				}
			}
		}
		nonEmpty := false
		for _, k := range key2 { // 全空串视同缺省:前端网格一格没填(或旧客户端发 ["","",…])= 与 A 卷答案同步
			if k != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			key2 = nil
		}
		if len(key2) == 0 {
			key2 = make([]string, len(orderB))
			for i := range key2 {
				if orderB[i] >= 1 && orderB[i] <= len(key) {
					key2[i] = key[orderB[i]-1]
				}
			}
		}
		canon, err := validateAbLayouts(&la, &lb, key2, orderB)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		for i := range canon { // 规范序下两卷答案必须全等(自校验:AB 卷不允许异内容答案键)
			if canon[i] != key[i] {
				httpError(w, http.StatusBadRequest, fmt.Errorf(
					"B 卷答案与 A 卷答案在规范题号下不一致(第 %d 题:A 卷 %q / B 卷 %q):AB 卷只打乱题序,两卷共用同一份参考答案;请留空 B 卷答案(自动与 A 卷同步)或修正",
					i+1, key[i], canon[i]))
				return
			}
		}
		// 带卷种标记重建 A 卷(卷种点几何),并构造 AB 入库上下文
		sheetA, err := la.Build()
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("A 卷版式: %w", err))
			return
		}
		sheet, layoutB, keyBCanon = sheetA, &lb, canon
		shortID = store.NextShortID() // 建考入库前预发:复合码「考试短号核对」对本批扫描即生效
		ab = &abSupport{hasB: true, orderB: orderB, shortID: shortID}
	}

	// 多页版式:主观题自动分页,每生 P 页连续上传,逐页判读后合并
	book, err := sheet.Layout.BuildBook()
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if ab != nil {
		ab.booksB, err = layoutB.BuildBook()
		if err != nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("B 卷版式: %w", err))
			return
		}
	}
	pageCount := len(book)
	var imgs []image.Image
	if r.MultipartForm != nil {
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
	}
	// 按考号逐页归组入库:每页独立判读考号+页点页号+卷种,按考号分组合并。
	// 不再要求总张数是页数的整数倍 —— 卡纸跳页只产生缺页告警,不再全局错位。
	ing := ingestScanImages(imgs, book, opt, 0, ab)
	if len(imgs) > 0 && len(ing.results) == 0 {
		httpError(w, http.StatusUnprocessableEntity,
			fmt.Errorf("全部扫描件识别失败:%s", failSummary(ing.failed)))
		return
	}
	results, slicesByStudent, pageGroups := ing.results, ing.slices, ing.pageGroups

	var st []roster.Student
	if rb := r.FormValue("roster"); rb != "" {
		parsed, err := roster.Load([]byte(rb))
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		st = parsed
	}

	markDuplicateRegs(nil, nil, results) // 批内重答检测(同考号二次上传 → 告警进异常清单)
	session := strings.TrimSpace(r.FormValue("session"))
	if session != "" && !store.School.HasSession(session) {
		httpError(w, http.StatusBadRequest, fmt.Errorf("场次 %q 不在全校场次表中(先维护场次表)", session))
		return
	}
	ex, err := store.Create(NewExam{
		Layout: sheet.Layout, Key: key, Results: results, Slices: slicesByStudent,
		Pages: nil, PageGroups: pageGroups, Threshold: opt.Threshold, CreatedBy: u.Username, Subject: r.FormValue("subject"), Roster: st,
		ExamDate:  strings.TrimSpace(r.FormValue("examDate")),
		Term:      strings.TrimSpace(r.FormValue("term")),
		ExamType:  strings.TrimSpace(r.FormValue("examType")),
		ExamGroup: strings.TrimSpace(r.FormValue("examGroup")),
		Session:   session,
		LayoutB:   layoutB, KeyB: keyBCanon, OrderB: orderB, ShortID: shortID,
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
	stage := examStage(ex)
	resp := map[string]any{
		"id":              ex.ID,
		"students":        len(results),
		"objectiveCount":  len(sheet.Choice),
		"subjectiveCount": len(sheet.SubjectiveBoxes),
		"pages":           pageCount,
		"threshold":       opt.Threshold,
		"roster":          len(st),
		"stage":           stage.Stage,
		"stageLabel":      stage.Label,
	}
	if len(ing.failed) > 0 {
		resp["failed"] = ing.failed
	}
	if len(ing.notes) > 0 {
		resp["notes"] = ing.notes
	}
	writeJSON(w, http.StatusCreated, resp)
}

// failSummary 把失败清单压成一行人话(建考全失败时的 422 文案)。
func failSummary(failed []scanFail) string {
	if len(failed) == 0 {
		return "未知原因"
	}
	parts := make([]string, 0, len(failed))
	for i, f := range failed {
		if i >= 5 {
			parts = append(parts, fmt.Sprintf("…共 %d 条", len(failed)))
			break
		}
		parts = append(parts, fmt.Sprintf("第 %d 张:%s", f.Index, f.Error))
	}
	return strings.Join(parts, ";")
}

// markDuplicateRegs 重答检测:同一考号再次出现(批内或与存量)不阻塞流程,
// 但给后到的卷打「疑似重答」告警,进考试详情的异常清单由人工裁决。
// 存量考号取 AssignedReg(含人工指派)——异常卷被指派为 X 后,该生真卷再扫进来也能判重。
func markDuplicateRegs(ex *Exam, existing []*omr.Result, batch []*omr.Result) {
	seen := map[string]int{} // 考号 → 首次出现的卷号(1-based)
	regOfExisting := func(i int) string {
		if ex != nil {
			return ex.AssignedReg(i + 1)
		}
		if r := existing[i]; r != nil && r.RegValid {
			return r.RegNumber
		}
		return ""
	}
	for i := range existing {
		// 作废卷对外语义是"不存在":不计入存量,否则重扫替换后再补传会误报重答、且告警指向死卷
		if existing[i] != nil && !existing[i].Voided {
			if reg := regOfExisting(i); reg != "" {
				if _, dup := seen[reg]; !dup {
					seen[reg] = i + 1
				}
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
		ExamDate   string `json:"examDate,omitempty"` // 考试日期(教务口径;空=未填,用 CreatedAt)
		Term       string `json:"term,omitempty"`
		ExamType   string `json:"examType,omitempty"`
		ExamGroup  string `json:"examGroup,omitempty"`
		Students   int    `json:"students"`
		Objective  int    `json:"objective"`
		Subjective int    `json:"subjective"`
		Published  bool   `json:"published"`
		Mine       bool   `json:"mine"` // 是否为当前账号创建的
		Stage      string `json:"stage"`
		StageLabel string `json:"stageLabel"`
		Expected   int    `json:"expected"`
		Warnings   int    `json:"warnings"`
		Ungraded   int    `json:"ungraded"`
		Total      int    `json:"total"`
		Dirty      bool   `json:"dirty"`             // 发布后数据有变更(需重新发布收口)
		Appeals    int    `json:"appeals,omitempty"` // 待处理复核申请数(列表角标)
		Flagged    int    `json:"flagged,omitempty"` // ⚑ 待复核数(列表角标)
	}
	out := []summary{} // 空列表也返回 [],避免 null 使前端 .map/.forOf 崩溃
	for _, ex := range store.List() {
		if !canOpenExam(u, ex, store.School) {
			continue // 学科教师只看任教班级+学科的考试
		}
		stage := examStage(ex)
		pending := 0
		for _, a := range ex.Appeals {
			if a.Status == "open" {
				pending++
			}
		}
		out = append(out, summary{
			ID: ex.ID, CreatedAt: ex.CreatedAt.Format("2006-01-02 15:04"), CreatedBy: ex.CreatedBy,
			Subject: ex.Subject, ExamDate: ex.ExamDate, Term: ex.Term, ExamType: ex.ExamType, ExamGroup: ex.ExamGroup,
			Students:  len(ex.Results),
			Objective: len(ex.Layout.Questions), Subjective: len(ex.Layout.Subjectives),
			Published: ex.Published, Mine: ex.CreatedBy == u.Username,
			Stage: stage.Stage, StageLabel: stage.Label,
			Expected: stage.Expected, Warnings: stage.Warnings, Ungraded: stage.Ungraded, Total: stage.Total,
			Dirty: stage.Dirty, Appeals: pending, Flagged: stage.Flagged,
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
	TaskDue    map[string]string     `json:"taskDue,omitempty"`    // 任务截止时间:"q{n}" 或 "{班级}" → 日期
	Classes    []string              `json:"classes,omitempty"`    // 名册中出现的班级(按班分配下拉)
	Threshold  string                `json:"threshold"`
	Published  bool                  `json:"published"`
	CanEdit    bool                  `json:"canEditKey"` // 当前账号是否可修正参考答案
	Staff      []staffInfo           `json:"staff"`      // 可分配的阅卷教师(任务分配下拉)
	Absent     []absentInfo          `json:"absent"`     // 缺考名单(卷号+考号)
	Manual     int                   `json:"manual"`     // 手工补录卷数
	Warnings   []sheetWarning        `json:"warnings"`   // 异常卷告警汇总(重答/空白卡/漏涂等)
	Stage      stageView             `json:"stage"`      // 阶段状态机(待扫描/扫描中/异常/阅卷中/待发布/已发布)
	Flagged    []flaggedView         `json:"flagged"`    // ⚑ 待复核队列(组长复核出口)
	ScannedReg map[string]bool       `json:"scannedReg"` // 已扫到卷的考号集合(缺扫名单前端推导)
	Roster     []rosterRow           `json:"roster"`     // 名册(缺扫名单/班级口径展示)

	// 考试元数据 + 卷生命周期 + 复核申请
	ExamDate   string       `json:"examDate,omitempty"`
	Term       string       `json:"term,omitempty"`
	ExamType   string       `json:"examType,omitempty"`
	ExamGroup  string       `json:"examGroup,omitempty"`
	Session    string       `json:"session,omitempty"`    // 场次(监考冲突检测依据)
	Sessions   []SessionDef `json:"sessions"`             // 全校场次表(详情页场次下拉选项)
	Anonymous  bool         `json:"anonymous,omitempty"`  // 匿名阅卷开关
	ManualMode bool         `json:"manualMode,omitempty"` // 手工成绩考试(免版式建考)
	ManualFull float64      `json:"manualFull,omitempty"` // 手工成绩满分
	// ManualScores 手工科目已录分数回显:考号 → 分数(录入表核对"谁已录/录了多少")。
	ManualScores map[string]float64 `json:"manualScores,omitempty"`
	Duplicates   [][]int            `json:"duplicates"` // 同考号多份卷(每组卷号列表,人工裁决保留哪份)
	VoidedRows   []absentInfo       `json:"voidedRows"` // 已作废卷(可恢复)
	Appeals      []appealView       `json:"appeals"`    // 成绩复核申请(发布后学生发起)
	// AppealBlocked 本场复核拉黑(考号→原因);AppealBanned 全校黑名单 ∩ 本场名册(考号→原因)。
	AppealBlocked map[string]string `json:"appealBlocked,omitempty"`
	AppealBanned  map[string]string `json:"appealBanned,omitempty"`
	HasB          bool              `json:"hasB,omitempty"`    // AB 卷考试(配置了 B 卷版式)
	ShortID       string            `json:"shortId,omitempty"` // 考试短号(复合码前缀,个性化出卡用)
	KeyB          []string          `json:"keyB,omitempty"`    // B 卷客观题参考答案(规范题号序;详情页后补出口)
	OrderB        []int             `json:"orderB,omitempty"`  // B 卷题序对照(卡面第 i 题 → 规范题号)
	RegOf         map[string]string `json:"regOf"`             // 卷号 → 考号(重复卷表头等前端推导用)
}

// appealView 是复核申请的一行(n 为 1-based 序号,裁决接口用)。
type appealView struct {
	N          int    `json:"n"`
	Time       string `json:"time"`
	By         string `json:"by"`             // 考号
	Name       string `json:"name,omitempty"` // 姓名(名册带出)
	Reason     string `json:"reason"`
	Status     string `json:"status"`
	Reply      string `json:"reply,omitempty"`
	ResolvedBy string `json:"resolvedBy,omitempty"` // 裁决人账号
	ResolvedAt string `json:"resolvedAt,omitempty"` // 裁决时间
}

// rosterRow 是详情里的名册行(缺扫名单推导用,只带展示必需字段)。
type rosterRow struct {
	Reg     string   `json:"reg"`
	Name    string   `json:"name,omitempty"`
	Classes []string `json:"classes,omitempty"`
}

// sheetWarning 是一张卷的告警汇总(考试详情异常清单)。
type sheetWarning struct {
	Student  int      `json:"student"` // 卷号(1-based)
	Reg      string   `json:"reg,omitempty"`
	Texts    []string `json:"texts"`
	Resolved bool     `json:"resolved"`          // 已人工裁决(考号指派/缺考/卷种指派/接受残卷),前端置灰归档
	Soft     bool     `json:"soft"`              // 信息类告警(系统已自愈/已有判分口径):展示但不算待裁决
	Variant  string   `json:"variant,omitempty"` // 卷种(AB 卷):A/B;卷种判不出为空
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
		TaskMode: ex.TaskMode, ClassTasks: ex.ClassTasks, Classes: examClasses(ex), TaskDue: ex.TaskDue,
		Published: ex.Published,
		CanEdit:   u.Role == auth.RoleAdmin || u.Role == auth.RoleLeader || ex.CreatedBy == u.Username,
		ExamDate:  ex.ExamDate, Term: ex.Term, ExamType: ex.ExamType, ExamGroup: ex.ExamGroup,
		Session: ex.Session, Sessions: store.School.SessionsSnapshot(),
		Anonymous: ex.Anonymous, ManualMode: ex.ManualFull > 0, ManualFull: ex.ManualFull,
		Duplicates: ex.DupGroups(),
		VoidedRows: []absentInfo{},
		Appeals:    []appealView{},
		HasB:       ex.LayoutB != nil,
		ShortID:    ex.ShortID,
		KeyB:       ex.KeyB,
		OrderB:     ex.OrderB,
		RegOf:      map[string]string{},
	}
	for _, su := range authStore.List() {
		if su.Role == auth.RoleTeacher || su.Role == auth.RoleLeader || su.Role == auth.RoleAdmin {
			d.Staff = append(d.Staff, staffInfo{su.Username, su.DisplayName, string(su.Role)})
		}
	}
	active := gradableCount(ex) // 阅卷进度口径:缺考占位卷不进分母(没有需要评的作答)
	for qi := range ex.Layout.Subjectives {
		p := questionProgress{Question: qi + 1, Total: active}
		if qi < len(ex.Layout.Subjectives) {
			p.Kind = ex.Layout.Subjectives[qi].Kind
			p.Max = ex.Layout.Subjectives[qi].Score
			p.Elective = ex.Layout.Subjectives[qi].Elective
		}
		if ex.Tasks != nil {
			p.Teacher = ex.Tasks[fmt.Sprintf("q%d", qi+1)]
		}
		for si := range ex.Results {
			res := ex.Results[si]
			if res == nil || res.Voided || res.Absent {
				continue
			}
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
		if res.Voided {
			d.VoidedRows = append(d.VoidedRows, absentInfo{Student: si + 1, Reg: ex.AssignedReg(si + 1)})
			continue // 作废卷独立成列表,不混入缺考/异常/补录口径
		}
		if res.Manual {
			d.Manual++
		}
		if res.Absent {
			d.Absent = append(d.Absent, absentInfo{Student: si + 1, Reg: ex.AssignedReg(si + 1)})
		}
		if len(res.Warnings) > 0 {
			// 信息类告警(系统已自愈:码修复考号/多涂已判/浅涂)展示但不计入待裁决,不挡发布
			d.Warnings = append(d.Warnings, sheetWarning{Student: si + 1, Reg: ex.AssignedReg(si + 1), Texts: res.Warnings,
				Resolved: sheetResolved(ex, si+1) || !hasBlockingWarning(res.Warnings),
				Soft:     !hasBlockingWarning(res.Warnings), Variant: res.Variant})
		}
	}
	for i, a := range ex.Appeals {
		av := appealView{N: i + 1, Time: a.Time.Format("2006-01-02 15:04"), By: a.By, Reason: a.Reason, Status: a.Status, Reply: a.Reply,
			ResolvedBy: a.ResolvedBy}
		if a.ResolvedAt != nil {
			av.ResolvedAt = a.ResolvedAt.Format("2006-01-02 15:04")
		}
		av.Name = nameOfReg(ex, a.By)
		d.Appeals = append(d.Appeals, av)
	}
	if len(ex.AppealBlocked) > 0 {
		d.AppealBlocked = ex.AppealBlocked
	}
	// 全校复核黑名单 ∩ 本场名册:详情页在复核申请行上直接标注"全局拉黑"
	if bans := store.School.AppealBansSnapshot(); len(bans) > 0 {
		rosterRegs := map[string]bool{}
		for _, st := range ex.Roster {
			rosterRegs[st.Reg] = true
		}
		for i := range ex.Results {
			if ex.Results[i] != nil {
				rosterRegs[ex.AssignedReg(i+1)] = true
			}
		}
		for _, b := range bans {
			if rosterRegs[b.Reg] {
				if d.AppealBanned == nil {
					d.AppealBanned = map[string]string{}
				}
				d.AppealBanned[b.Reg] = b.Reason
			}
		}
	}
	// 手工科目已录分数回显:录入表区分"没录"与"录了但表单没回显导致重复录入"
	if ex.ManualFull > 0 {
		d.ManualScores = map[string]float64{}
		for i := range ex.Results {
			res := ex.Results[i]
			if res == nil || res.Voided || res.Absent {
				continue
			}
			reg := ex.AssignedReg(i + 1)
			if g, ok := ex.Grades[score.GradeKey(i+1, manualScoreQuestion)]; ok && g.Graded && reg != "" {
				d.ManualScores[reg] = g.Score
			}
		}
	}
	for i := range ex.Results {
		if ex.Results[i] != nil {
			d.RegOf[strconv.Itoa(i+1)] = ex.AssignedReg(i + 1)
		}
	}
	d.Stage = examStage(ex)
	d.Flagged = flaggedQueue(ex)
	d.ScannedReg = map[string]bool{}
	for i := range ex.Results {
		if res := ex.Results[i]; res != nil && !res.Voided {
			if reg := ex.AssignedReg(i + 1); reg != "" {
				d.ScannedReg[reg] = true
			}
		}
	}
	d.Roster = []rosterRow{} // 空态给 [],前端可安全 map
	for _, st := range ex.Roster {
		d.Roster = append(d.Roster, rosterRow{Reg: st.Reg, Name: st.Name, Classes: st.AllClasses()})
	}
	writeJSON(w, http.StatusOK, d)
}

// partState 是切片列表中一道拆分小问的当前评分状态。
type partState struct {
	Part   int     `json:"part"`
	Score  float64 `json:"score"`
	Graded bool    `json:"graded"`
	Ver    int     `json:"ver,omitempty"` // 乐观锁版本号
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
		Ver      int            `json:"ver,omitempty"` // 整题评分的乐观锁版本号
		Mark     string         `json:"mark"`
		Ink      []score.Stroke `json:"ink,omitempty"`
		Missing  bool           `json:"missing"`         // 无切片图(手工补录卷),工作台显示占位
		Parts    []partState    `json:"parts,omitempty"` // 拆分小问的当前评分(版式 v3)
		Elective *electiveView  `json:"elective,omitempty"`
	}
	out := make([]item, 0, len(ex.Results))
	for si := range ex.Results {
		if ex.Results[si] == nil || ex.Results[si].Voided {
			continue // 教师视图下不可见学生 / 作废卷不进阅卷队列
		}
		if ex.Results[si].Absent {
			continue // 缺考占位卷无作答可评:不进队列,也无需打分解锁阶段
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
			it.Graded, it.Score, it.Mark, it.Ink, it.Ver = g.Graded, g.Score, g.Mark, g.Ink, g.Ver
		}
		if spec := ex.Layout.Subjectives[q-1]; len(spec.Parts) > 0 {
			it.Parts = make([]partState, len(spec.Parts))
			var sum float64
			for pi := range spec.Parts {
				ps := partState{Part: pi + 1}
				if g, ok := ex.Grades[score.GradePartKey(si+1, q, pi+1)]; ok {
					ps.Score, ps.Graded, ps.Ver = g.Score, g.Graded, g.Ver
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
// student/q 必须是纯数字:此前 pad2 对非数字原样放行,Windows 下 "..%5C.." 之类的
// 路径段会借反斜杠越出 slices 目录读到任意 .png。
func handleSliceImage(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	id := r.PathValue("id")
	ex, ok := store.Get(id)
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	student, err1 := strconv.Atoi(r.PathValue("student"))
	q, err2 := strconv.Atoi(r.PathValue("q"))
	if err1 != nil || err2 != nil || student < 1 || q < 1 || student > 9999 || q > 999 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("切片编号不合法"))
		return
	}
	p := filepath.Join(store.SliceDir(id), fmt.Sprintf("s%02d_q%02d.png", student, q))
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, p)
}

// handleSetGrade 写入一条主观题评分(含手写批注笔迹);part>0 时为小问评分(版式 v3)。
// 乐观锁:目标已评过时必须携带匹配 ver(他人先改则 409);分数越界(负分/超满分)服务端拒绝。
// 响应携带写入后的新版本号,前端回填本地 ver(后续批注/改分/标记才能带上)。
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
		Ver      int            `json:"ver"` // 乐观锁版本号(读到的评分 ver)
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
	ex, ok := store.Get(req.ExamID)
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
		return
	}
	g := score.Grade{Score: req.Score, Mark: req.Mark, Graded: graded, Ink: req.Ink}
	var (
		newVer int
		err    error
	)
	if req.Part > 0 {
		newVer, err = store.SetGradePart(req.ExamID, req.Student, req.Question, req.Part, g, u.Username, req.Ver)
	} else {
		newVer, err = store.SetGrade(req.ExamID, req.Student, req.Question, g, u.Username, req.Ver)
	}
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   err.Error(),
				"current": map[string]any{"score": conflict.Current.Score, "ver": conflict.Current.Ver, "by": conflict.Current.By},
			})
			return
		}
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ver": newVer})
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
