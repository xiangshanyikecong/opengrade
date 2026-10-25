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
	"sort"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// handleAppendScans 补传扫描件:POST /api/v1/exams/{id}/scans(multipart images=多张扫描件)。
// 多页版式下先做跨批残卷合并:同一考号已有缺页残卷、且本批又扫到该生的页时,
// 两批页按真实页号拼成一份完整卷,原地替换旧卷(卷号不变 → 已评分数自动保留),
// 不再把同一学生拆成两个残卷、也不再误报「疑似重答」。
func handleAppendScans(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围);如需代扫请找建卷人"))
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
	oldBase := len(ex.Results)
	ing := ingestScanImages(imgs, book, opt, oldBase, ab)
	results, slices, pageGroups, pageNos := ing.results, ing.slices, ing.pageGroups, ing.pageNos
	failed = append(failed, ing.failed...)
	notes := ing.notes
	if len(imgs) > 0 && len(results) == 0 && len(ing.mergedStudents) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"added": 0, "failed": failed, "notes": notes})
		return
	}

	// —— 跨批残卷合并(多页版式)——
	if len(book) > 1 {
		mergedInfo := crossBatchMerge(store, ex, &ing, book, ab, opt, u.Username)
		notes = append(notes, mergedInfo...)
		results, slices, pageGroups, pageNos = ing.results, ing.slices, ing.pageGroups, ing.pageNos
	}

	markDuplicateRegs(ex, ex.Results, results) // 与存量(含人工指派考号)/批内重复 → 告警(疑似重答)
	total, err := store.AppendScans(ex.ID, u.Username, oldBase, results, slices, pageGroups, pageNos)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	resp := map[string]any{"added": len(results), "students": total, "failed": failed, "notes": notes}
	if len(ing.mergedStudents) > 0 {
		resp["merged"] = ing.mergedStudents
	}
	writeJSON(w, http.StatusOK, resp)
}

// findScanStudentByReg 找到持有该考号的活跃扫描卷(1-based):跳过作废/手工补录卷。
func findScanStudentByReg(ex *Exam, reg string) int {
	for i := range ex.Results {
		res := ex.Results[i]
		if res == nil || res.Voided || res.Manual {
			continue
		}
		if ex.AssignedReg(i+1) == reg {
			return i + 1
		}
	}
	return 0
}

// crossBatchMerge 把批内"考号与存量残卷相同"的学生页并入存量卷(原地替换),
// 并从批次产物里剔除已合并的学生。返回给操作员的人话说明。
func crossBatchMerge(store *Store, ex *Exam, ing *ingestResult, book []*card.Sheet, ab *abSupport, opt omr.Options, by string) []string {
	pageCount := len(book)
	var notes []string
	keep := make([]bool, len(ing.results))
	for i := range keep {
		keep[i] = true
	}
	for i, res := range ing.results {
		if res == nil || !res.RegValid || res.RegNumber == "" {
			continue
		}
		j := findScanStudentByReg(ex, res.RegNumber)
		if j == 0 {
			continue
		}
		old := ex.Results[j-1]
		oldRects := store.loadPageGroups(ex.ID, j)
		if len(oldRects) == 0 {
			continue // 存量是手工补录或没有矫正图:走重复卷裁决路径
		}
		newPages := ing.pages[i]
		if len(oldRects) >= pageCount && len(newPages) >= pageCount {
			continue // 两份都完整:真重扫,保留双卷交人工裁决(重复卷入口)
		}
		// 重建存量各页:按入库时记录的真实页号选版式重读(无记录按位置)
		oldNos := old.PageNos
		var combined []pageScan
		var nos []int
		appendPage := func(rect *image.Gray, pageNo, pos int) bool {
			k := pageNo - 1
			if k < 0 || k >= pageCount {
				k = pos // 页号未知/越界:按到达位置
				pageNo = 0
			}
			r, _, err := omr.ReadRectified(book[k], rect, opt)
			if err != nil {
				return false
			}
			combined = append(combined, pageScan{res: r, dbg: &omr.Debug{Rectified: rect}, sheet: book[k], subOffset: subjectiveOffset(book, k)})
			nos = append(nos, pageNo)
			return true
		}
		okAll := true
		for k, rect := range oldRects {
			if rect == nil {
				continue
			}
			pn := 0
			if k < len(oldNos) {
				pn = oldNos[k]
			}
			if !appendPage(rect, pn, k) {
				okAll = false
				break
			}
		}
		if !okAll {
			continue // 存量页重读失败:不动旧卷,走追加+重答告警的老路
		}
		for k, ps := range newPages {
			if ps.dbg == nil || ps.dbg.Rectified == nil {
				continue
			}
			pn := 0
			if k < len(ing.pageNos[i]) {
				pn = ing.pageNos[i][k]
			}
			combined = append(combined, ps)
			nos = append(nos, pn)
		}
		// 按真实页号稳定排序(0=未知,保持相对顺序垫底);重扫凑出超页时截前 P 页
		idx := make([]int, len(combined))
		for k := range idx {
			idx[k] = k
		}
		sort.SliceStable(idx, func(a, b int) bool {
			na, nb := nos[idx[a]], nos[idx[b]]
			if na == 0 {
				na = pageCount + 1
			}
			if nb == 0 {
				nb = pageCount + 1
			}
			return na < nb
		})
		ordered := make([]pageScan, len(idx))
		orderedNos := make([]int, len(idx))
		for pos, k := range idx {
			ordered[pos], orderedNos[pos] = combined[k], nos[k]
		}
		if len(ordered) > pageCount {
			ordered, orderedNos = ordered[:pageCount], orderedNos[:pageCount]
		}
		// 告警:保留旧卷上仍然成立的告警,重算缺页
		var warns []string
		for _, wn := range old.Warnings {
			if strings.Contains(wn, "的卷仅识别到") || strings.Contains(wn, "疑似重答") || strings.Contains(wn, "疑似重扫或夹页") {
				continue // 缺页/重答/夹页类告警由合并结果重新推导
			}
			warns = append(warns, wn)
		}
		if len(ordered) < pageCount {
			seen := map[int]bool{}
			for _, n := range orderedNos {
				if n >= 1 && n <= pageCount {
					seen[n] = true
				}
			}
			missing := []int{}
			for p := 1; p <= pageCount; p++ {
				if !seen[p] {
					missing = append(missing, p)
				}
			}
			warns = append(warns, fmt.Sprintf("考号 %s 的卷合并后仍只有 %d/%d 页(缺第 %s 页)——继续补扫该生缺页,或核对后「接受残卷」",
				res.RegNumber, len(ordered), pageCount, joinInts(missing, "、")))
		}
		merged := mergeStudentOnly(ordered, ab, opt, warns, orderedNos)
		if merged == nil {
			continue
		}
		slices := slicesOfPageScans(ordered, j, merged.RegNumber)
		rects := make([]*image.Gray, len(ordered))
		for k, ps := range ordered {
			rects[k] = ps.dbg.Rectified
		}
		if err := store.ReplaceResult(ex.ID, j, merged, slices, rects, by); err != nil {
			continue // 落盘失败:退回追加路径(该生会走重复卷裁决)
		}
		keep[i] = false
		ing.mergedStudents = append(ing.mergedStudents, j)
		if len(ordered) >= pageCount {
			notes = append(notes, fmt.Sprintf("考号 %s 的两批扫描页已自动合并为完整卷(第 %d 卷),已评分数保留", res.RegNumber, j))
		} else {
			notes = append(notes, fmt.Sprintf("考号 %s 的两批扫描页已合并为第 %d 卷(%d/%d 页),继续补扫缺页会自动并入", res.RegNumber, j, len(ordered), pageCount))
		}
	}
	// 剔除已并入存量的学生,避免重复追加
	outResults := ing.results[:0]
	outSlices := ing.slices[:0]
	outGroups := ing.pageGroups[:0]
	outNos := ing.pageNos[:0]
	outPages := ing.pages[:0]
	for i, k := range keep {
		if !k {
			continue
		}
		outResults = append(outResults, ing.results[i])
		outSlices = append(outSlices, ing.slices[i])
		outGroups = append(outGroups, ing.pageGroups[i])
		outNos = append(outNos, ing.pageNos[i])
		outPages = append(outPages, ing.pages[i])
	}
	ing.results, ing.slices, ing.pageGroups, ing.pageNos, ing.pages = outResults, outSlices, outGroups, outNos, outPages
	return notes
}

func joinInts(xs []int, sep string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, sep)
}

// handleManualEntry 手工补录:POST /api/v1/exams/{id}/manual
// {"reg":"00000006","absent":false,"answers":["A","B",...]}(answers 可缺省/部分)。
func handleManualEntry(w http.ResponseWriter, r *http.Request, store *Store, u *auth.User) {
	ex, ok := store.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
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
	if !ok {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在"))
		return
	}
	if !canOpenExam(u, ex, store.School) {
		httpError(w, http.StatusNotFound, fmt.Errorf("考试不存在或无权访问该考试(不在你的任教/管辖范围)"))
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
