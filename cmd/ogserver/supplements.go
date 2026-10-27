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
	"net/http"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
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
	opt := ex.omrOptions()
	var failed []scanFail
	var imgs []image.Image
	for i, fh := range files {
		f, err := fh.Open()
		if err != nil {
			failed = append(failed, scanFail{i + 1, err.Error()})
			continue
		}
		img, err := raster.DecodeImage(f)
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
		mergedInfo := crossBatchMerge(store, ex, &ing, book, ab, opt, u.Username, oldBase)
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
//
// B-1 修复后的装配语义:
//   - 存量已是完整卷(P 页)→ 不再合并:后扫到的该生页属于重扫,并入只会引入
//     重复页号/顶掉已有页(旧代码可截出 [p1,p2,p2] 式假完整卷),走重复卷裁决;
//   - 槽位装配:已知页号的页按页号入槽(同号时新批页优先 —— 重扫页更新鲜),
//     未知页号(旧卡/页点不可信)按到达序填入剩余空槽,并把该页按槽位版式重读,
//     消除"判读按到达位置、排序却垫底"的两义性(旧代码会让客观题整页错位判分);
//   - 剔除已并入存量的学生后,剩余学生的切片卷号按新位置重编
//     (旧代码烙着剔除前的卷号 → 阅卷端全 Missing、后续批次互相覆盖切片文件);
//   - 曾人工指派过卷种的卷,合并重判后按原指派重放(AssignVariant 重读客观题),
//     避免作答被暂扣(PendingVariant)却因残留指派被误判"已裁决"而静默消失。
func crossBatchMerge(store *Store, ex *Exam, ing *ingestResult, book []*card.Sheet, ab *abSupport, opt omr.Options, by string, oldBase int) []string {
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
		if len(oldRects) >= pageCount {
			continue // 存量完整:真重扫(整份/残页皆然),保留双卷交人工裁决,不并入
		}
		// 卷种口径:人工指派 > 存量判读 > 新批判读
		variant := ""
		if ex.AssignVariant != nil {
			variant = ex.AssignVariant[fmt.Sprintf("s%d", j)]
		}
		if variant == "" && old != nil {
			variant = old.Variant
		}
		if variant == "" {
			for _, ps := range newPages {
				if ps.res != nil && ps.res.Variant != "" {
					variant = ps.res.Variant
					break
				}
			}
		}
		// 候选页:存量(矫正图 + 入库记录页号,0=未知)与新批(判读产物 + 页号,0=未知)
		type cand struct {
			rect  *image.Gray // 存量页矫正图(重读原料)
			ps    *pageScan   // 新批页判读产物(几何一致时直接复用)
			no    int
			isNew bool
		}
		var cands []cand
		for k, rect := range oldRects {
			if rect == nil {
				continue
			}
			pn := 0
			if old != nil && k < len(old.PageNos) {
				pn = old.PageNos[k]
			}
			cands = append(cands, cand{rect: rect, no: pn})
		}
		for k, ps := range newPages {
			if ps.dbg == nil || ps.dbg.Rectified == nil {
				continue
			}
			pn := 0
			if k < len(ing.pageNos[i]) {
				pn = ing.pageNos[i][k]
			}
			c := cand{ps: &newPages[k], no: pn, isNew: true}
			cands = append(cands, c)
		}
		// 槽位装配:已知页号新批优先 → 已知页号存量 → 未知页号按到达序填空槽
		slots := make([]*cand, pageCount)
		placed := 0
		fill := func(c *cand) bool {
			if c.no >= 1 && c.no <= pageCount && slots[c.no-1] == nil {
				slots[c.no-1] = c
				placed++
				return true
			}
			return false
		}
		anyUnknown := false
		for k := range cands {
			if cands[k].isNew {
				fill(&cands[k])
			}
		}
		for k := range cands {
			if !cands[k].isNew {
				fill(&cands[k])
			}
		}
		for k := range cands {
			c := &cands[k]
			if c.no >= 1 && c.no <= pageCount {
				continue // 已按页号入槽(或与新批页同号被顶替,计入 dropped)
			}
			anyUnknown = true // 未知/越界页号:按到达序填入剩余空槽
			for s := 0; s < pageCount; s++ {
				if slots[s] == nil {
					slots[s] = c
					placed++
					break
				}
			}
		}
		dropped := len(cands) - placed
		// 逐槽判读:新批页几何与槽位一致则复用,否则(及全部存量页)按槽位版式重读
		var ordered []pageScan
		var orderedNos []int
		okAll := true
		for s := 0; s < pageCount; s++ {
			c := slots[s]
			if c == nil {
				continue
			}
			sheet := ab.variantBook(book, variant, s)
			if c.isNew && c.ps.sheet == sheet {
				ordered = append(ordered, *c.ps)
			} else if r, _, err := omr.ReadRectified(sheet, c.rect, opt); err == nil {
				ordered = append(ordered, pageScan{res: r, dbg: &omr.Debug{Rectified: c.rect}, sheet: sheet, subOffset: subjectiveOffset(book, s)})
			} else if c.isNew {
				ordered = append(ordered, *c.ps) // 新页按槽位重读失败:退回原判读
			} else {
				okAll = false // 存量页重读失败:不动旧卷,走追加+重答告警的老路
				break
			}
			orderedNos = append(orderedNos, s+1)
		}
		if !okAll {
			continue
		}
		// 告警:保留旧卷上仍然成立的告警,页序/缺页/重答类由合并结果重新推导
		var warns []string
		for _, wn := range old.Warnings {
			if strings.Contains(wn, "的卷仅识别到") || strings.Contains(wn, "疑似重答") || strings.Contains(wn, "疑似重扫或夹页") ||
				strings.Contains(wn, "页点不可信") || strings.Contains(wn, "合并后仍只有") {
				continue
			}
			warns = append(warns, wn)
		}
		if anyUnknown {
			warns = append(warns, "页点不可信(缺号/重复/旧卡),已按到达顺序合并——请人工核对该生各页顺序,确认无误可在异常清单「接受残卷」")
		}
		if dropped > 0 {
			warns = append(warns, fmt.Sprintf("考号 %s 识别到 %d 页(版式共 %d 页),%d 页未并入(重复/多余)——请人工核对", res.RegNumber, len(cands), pageCount, dropped))
		}
		if len(ordered) < pageCount {
			missing := []int{}
			for s := 1; s <= pageCount; s++ {
				if slots[s-1] == nil {
					missing = append(missing, s)
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
		// 卷种指派重放:人工裁决过的卷种是最高优先级事实,合并重判与它相悖时按它重读客观题
		if variant2 := ex.AssignVariant[fmt.Sprintf("s%d", j)]; variant2 != "" && (merged.PendingVariant || merged.Variant != variant2) {
			if err := store.AssignVariant(ex.ID, j, variant2, by); err != nil {
				notes = append(notes, fmt.Sprintf("考号 %s 合并后按原指派 %s 卷重读客观题失败:%v(该卷已回到异常清单,请重新指派)", res.RegNumber, variant2, err))
			}
		}
		if len(ordered) >= pageCount {
			notes = append(notes, fmt.Sprintf("考号 %s 的两批扫描页已自动合并为完整卷(第 %d 卷),已评分数保留", res.RegNumber, j))
		} else {
			notes = append(notes, fmt.Sprintf("考号 %s 的两批页已合并为第 %d 卷(%d/%d 页),继续补扫缺页会自动并入", res.RegNumber, j, len(ordered), pageCount))
		}
	}
	// 剔除已并入存量的学生,避免重复追加;剩余学生的切片卷号按剔除后的新位置重编
	// (切片文件名/评分键以卷号对齐,否则中间剔除后烙着旧卷号 → 阅卷端 Missing、互相覆盖)
	outResults := ing.results[:0]
	outSlices := ing.slices[:0]
	outGroups := ing.pageGroups[:0]
	outNos := ing.pageNos[:0]
	outPages := ing.pages[:0]
	outIdx := 0
	for i, k := range keep {
		if !k {
			continue
		}
		want := oldBase + outIdx + 1
		for si := range ing.slices[i] {
			ing.slices[i][si].StudentIndex = want
		}
		outResults = append(outResults, ing.results[i])
		outSlices = append(outSlices, ing.slices[i])
		outGroups = append(outGroups, ing.pageGroups[i])
		outNos = append(outNos, ing.pageNos[i])
		outPages = append(outPages, ing.pages[i])
		outIdx++
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
