package main

// 量产阅卷的补救语义(扫描通道的韧性):
//   - 补传扫描件:建考后中途扫完的一批卷续接进同一会话(断点续传),
//     逐卷 OMR 判读沿用会话已存的版式与二值化配置;单张失败不阻塞整批;
//   - 手工补录:漏扫/答题卡作废的学生,按考号手录客观题作答或登记缺考,
//     卷号续排,主观题评分键与普通卷一致(Grader 之外也能走 API 评分);
//   - 缺考标记:已扫描的作废卷可事后标记缺考,统计/排名/选项分布自动剔除。

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"net/http"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/omr"
)

// handleAppendScans 补传扫描件:POST /api/v1/exams/{id}/scans(multipart images=多张扫描件)。
func handleAppendScans(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("解析 multipart 失败: %w", err))
		return
	}
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 images 字段(至少一张整卷扫描件)"))
		return
	}
	book, err := ex.Layout.BuildBook() // A 卷(或唯一版式):逐页判读考号归组,单页每张一卷
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	ab, err := abOfExam(ex) // AB 卷上下文(非 AB 考试为 nil;复合码考试短号核对在此生效)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	opt := omr.Options{Threshold: ex.Threshold}
	var failed []scanFail
	var imgs []image.Image
	for i, fh := range files {
		f, err := fh.Open()
		if err != nil {
			failed = append(failed, scanFail{i + 1, err.Error()})
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			failed = append(failed, scanFail{i + 1, fmt.Sprintf("图像解码失败: %v", err)})
			continue
		}
		imgs = append(imgs, img)
	}
	// 按考号逐页归组:每页独立判读考号+页点页号,不再要求张数是页数的整数倍;
	// 单页判读失败只丢那一张,缺页只告警对应学生,不再连坐整批。
	ing := ingestScanImages(imgs, book, opt, len(ex.Results), ab)
	results, slices, pageGroups := ing.results, ing.slices, ing.pageGroups
	failed = append(failed, ing.failed...)
	notes := ing.notes
	if len(imgs) > 0 && len(results) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"added": 0, "failed": failed, "notes": notes})
		return
	}
	markDuplicateRegs(ex.Results, results) // 与存量/批内重复考号 → 告警(疑似重答)
	total, err := store.AppendScans(ex.ID, u.Username, results, slices, pageGroups)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": len(results), "students": total, "failed": failed, "notes": notes})
}

// handleManualEntry 手工补录:POST /api/v1/exams/{id}/manual
// {"reg":"00000006","absent":false,"answers":["A","B",...]}(answers 可缺省/部分)。
func handleManualEntry(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	var req struct {
		Reg     string   `json:"reg"`
		Absent  bool     `json:"absent"`
		Answers []string `json:"answers"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	student, err := store.AppendManual(ex.ID, req.Reg, req.Absent, req.Answers, u.Username)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"student": student})
}

// handleSetAbsentFlag 缺考标记:POST /api/v1/exams/{id}/absent {"student":3,"absent":true}。
func handleSetAbsentFlag(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok || !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	var req struct {
		Student int  `json:"student"`
		Absent  bool `json:"absent"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if err := store.SetAbsent(ex.ID, req.Student, req.Absent, u.Username); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
