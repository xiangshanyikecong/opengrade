package main

// 多页扫描按考号逐页归组(P0 修复:替代"每 P 张连续"纯位置切块):
//   - 逐页独立判读考号(身份区每页重印,几何全页一致 → 矫正一次、逐页复用);
//   - 页点标记(多页卡卷头右上一排小方块,第 k 点实心=第 k 页)给出机器可读页号;
//   - 按考号分组 → 组内按页号排序合并;张数不再要求是页数的整数倍,
//     扫描仪卡纸跳页/少传一页只影响对应学生并给出「缺第 N 页」告警,
//     不再把后面所有学生的页全部错位;
//   - 每页身份区已印考号与条码,归组完全使用它 —— 一页乱序不再污染整批。
// 兼容回退:版式无考号通道,或整批考号全部不可读且张数恰为整数倍时,退回位置分组。

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"sort"
	"strings"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// scanFail 是一张(或一组)扫描件的失败记录:batch 内 1-based 张序 + 原因。
type scanFail struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// keyCard 是批内识别出的教师答案卡(考号全 9 的约定卷,吸收自 open-mcr 的
// key sheet):不进学生卷流,由服务端录入客观题参考答案。
type keyCard struct {
	SheetNo int      // 批内答案卡序号(1-based,反馈提示用)
	Answers []string // 判读出的逐题作答(AB 卷已归一到规范题号序)
}

// ingestResult 是一批扫描件的入库产物。
type ingestResult struct {
	results        []*omr.Result
	slices         [][]omr.Slice
	pageGroups     [][]*image.Gray
	pages          [][]pageScan // 每生的逐页判读(跨批残卷合并用;appendStudent 顺带记录)
	pageNos        [][]int      // 每生逐页(按合并序)的真实页号;0=未知(页点不可信/旧卡)
	mergedStudents []int        // 跨批合并进的存量卷号(响应提示用)
	failed         []scanFail
	notes          []string       // 缺页/多余页/回退说明,前端展示在扫描反馈里
	keyCards       []keyCard      // 教师答案卡(考号全 9),交 applyKeyCards 录入参考答案
	overlays       [][][]byte     // 与 pageGroups 平行:逐生逐页判读叠加图(PNG 编码;nil=未生成)
	overlaySeq     int            // 正常卷叠加图抽样计数器(异常卷必存,正常卷 1/20)
}

// pageIdent 是一页扫描件的"身份判读":矫正图 + 考号 + 页点页号 + 卷种(AB 卷)。
type pageIdent struct {
	rect     *image.Gray
	reg      string
	regValid bool
	pageNo   int
	variant  string // 卷种点/复合码判读:"A"/"B"/""
}

// ingestScanImages 把一批整卷扫描件判读、归组、合并为逐生结果。
// 书 book 为 A 卷(或唯一版式)逐页编译产物;opt 沿用会话二值化配置;ab 为 AB 上下式(非 AB 为 nil)。
// base 是本批之前的存量卷数:补传场景切片/回溯图的全局卷号 = base + 批内序号。
func ingestScanImages(imgs []image.Image, book []*card.Sheet, opt omr.Options, base int, ab *abSupport) ingestResult {
	out := ingestResult{failed: []scanFail{}, notes: []string{}}
	pageCount := len(book)

	fail := func(idx int, err error) {
		out.failed = append(out.failed, scanFail{idx, err.Error()})
	}

	if pageCount <= 1 { // 单页版式:每张即一份完整卷
		for i, img := range imgs {
			rect, err := omr.RectifyOnce(book[0], img, opt)
			if err != nil {
				fail(i+1, fmt.Errorf("识别失败: %w", err))
				continue
			}
			res, dbg, err := omr.ReadRectified(book[0], rect, opt)
			if err != nil {
				fail(i+1, fmt.Errorf("判读失败: %w", err))
				continue
			}
			// AB 卷的 B 卡重读与作答归一统一在 appendStudent 内做(同一张矫正图按 B 版式重读后归一),
			// 这里只处理"B 卡混入未配 B 版式的考试"告警。
			if res.Variant == "B" && (ab == nil || !ab.hasB) {
				res.Warnings = append(res.Warnings, "卷面为 B 卡但本场未配置 B 卷版式,已按 A 版式判读,客观题作答可疑——请核对纸质卷或作废")
			}
			ps := pageScan{res: res, dbg: dbg, sheet: book[0]}
			out.appendStudent([]pageScan{ps}, base, ab, opt, nil, nil)
		}
		return out
	}

	// 版式没有考号通道(无涂卡也无码):无法按考号归组,退回「每生连续 P 页」位置配对
	if book[0].Layout.RegDigits == 0 && book[0].Layout.RegCodeKind() == "" {
		out.notes = append(out.notes, "版式无考号区,按「每生连续 P 页」位置配对")
		if len(imgs)%pageCount != 0 {
			out.failed = append(out.failed, scanFail{0, fmt.Sprintf(
				"版式共 %d 页且无考号区:需按每生连续 %d 页上传(当前 %d 张,须为 %d 的整数倍)", pageCount, pageCount, len(imgs), pageCount)})
			return out
		}
		for si, group := range splitPageImages(imgs, pageCount) {
			var pages []pageScan
			for k, img := range group {
				ps, err := readPageScan(book, k, img, opt)
				if err != nil {
					fail(si*pageCount+k+1, fmt.Errorf("识别失败: %w", err))
					break
				}
				pages = append(pages, ps)
			}
			if len(pages) != pageCount {
				continue
			}
			out.appendStudent(pages, base, ab, opt,
				[]string{"版式无考号区,按「每生连续 P 页」位置配对——请抽查核对各卷归属"}, nil)
		}
		return out
	}

	// —— 逐页身份判读:矫正一次,判考号 + 页点页号 + 卷种(身份区几何全页/全卷种一致)——
	idents := make([]pageIdent, len(imgs))
	regOK := 0
	for i, img := range imgs {
		rect, err := omr.RectifyOnce(book[0], img, opt)
		if err != nil {
			fail(i+1, fmt.Errorf("识别失败: %w", err))
			continue
		}
		id, _, err := omr.ReadRectified(book[0], rect, opt)
		if err != nil {
			fail(i+1, fmt.Errorf("判读失败: %w", err))
			continue
		}
		idents[i] = pageIdent{rect: rect, reg: id.RegNumber, regValid: id.RegValid, pageNo: id.PageNo, variant: id.Variant}
		if id.RegValid && id.RegNumber != "" {
			regOK++
		}
	}

	// 整批考号全不可读:旧卡(无码且涂卡失败)/极端扫描质量 —— 张数恰为整数倍时退回位置分组
	if regOK == 0 {
		if len(imgs) == 0 {
			return out
		}
		aligned := true
		for _, id := range idents {
			if id.rect == nil {
				aligned = false
				break
			}
		}
		if aligned && len(imgs)%pageCount == 0 {
			out.notes = append(out.notes, "本批未能判读出考号,已按「每生连续 P 页」位置配对,请抽查核对归属")
			for si := 0; si*pageCount < len(imgs); si++ {
				var pages []pageScan
				for k := 0; k < pageCount; k++ {
					id := idents[si*pageCount+k]
					res, _, err := omr.ReadRectified(book[k], id.rect, opt)
					if err != nil {
						fail(si*pageCount+k+1, fmt.Errorf("判读失败: %w", err))
						break
					}
					pages = append(pages, pageScan{res: res, dbg: &omr.Debug{Rectified: id.rect}, sheet: book[k], subOffset: subjectiveOffset(book, k)})
				}
				if len(pages) != pageCount {
					continue
				}
				out.appendStudent(pages, base, ab, opt,
					[]string{"本批未能判读出考号,已按「每生连续 P 页」位置配对,请抽查核对归属"}, nil)
			}
			return out
		}
		out.failed = append(out.failed, scanFail{0, "本批扫描件均未识别出考号,且张数不是版式页数的整数倍,无法归组——请检查扫描质量或改用带条码/二维码的答题卡"})
		return out
	}

	// —— 按考号归组 → 组内按页点页号排序合并 → 按卷种选版式判读并归一 ——
	type member struct{ idx, pageNo int }
	groups := map[string][]member{}
	var order []string // 考号首次出现序(结果顺序跟随扫描顺序)
	for i, id := range idents {
		if id.rect == nil {
			continue // 已按张记 failed
		}
		if !id.regValid || id.reg == "" {
			fail(i+1, fmt.Errorf("该页未能识别考号(涂卡与码均无效),无法自动归组——请重扫此页或对该生手工补录"))
			continue
		}
		if _, seen := groups[id.reg]; !seen {
			order = append(order, id.reg)
		}
		groups[id.reg] = append(groups[id.reg], member{i, id.pageNo})
	}

	for _, reg := range order {
		members := groups[reg]
		// 组内卷种:取第一个判出的(同一份卡的各页卷种点必然一致)
		variant := ""
		for _, m := range members {
			if idents[m.idx].variant != "" {
				variant = idents[m.idx].variant
				break
			}
		}
		var groupWarns []string // 随卷落库的告警(进异常清单,阶段机可见)
		if variant == "B" && (ab == nil || !ab.hasB) {
			variant = "" // B 卡混入非 AB/未配 B 版式的考试:按 A 判读并挂告警,交人工裁决
			groupWarns = append(groupWarns, "卷面为 B 卡但本场未配置 B 卷版式,已按 A 版式判读,客观题作答可疑——请核对纸质卷或作废")
		}
		// 页点可信判定:组内每页都有页号、且互不重复
		seen := map[int]bool{}
		pagesKnown, pagesUnique := 0, true
		for _, m := range members {
			if m.pageNo >= 1 && m.pageNo <= pageCount {
				pagesKnown++
				if seen[m.pageNo] {
					pagesUnique = false
				}
				seen[m.pageNo] = true
			}
		}
		trustPages := pagesKnown == len(members) && pagesUnique
		sorted := append([]member(nil), members...)
		if trustPages {
			sort.Slice(sorted, func(a, b int) bool { return sorted[a].pageNo < sorted[b].pageNo })
		} else if pageCount > 1 && len(members) > 1 {
			groupWarns = append(groupWarns, "页点不可信(缺号/重复/旧卡),已按到达顺序合并——请人工核对该生各页顺序,确认无误可在异常清单「接受残卷」")
		}

		// 多页(重扫/夹页):页点可信时按页号取前 P 页,否则按到达序取前 P 页
		take := sorted
		if len(sorted) > pageCount {
			take = sorted[:pageCount]
			msg := fmt.Sprintf("考号 %s 识别到 %d 页(版式共 %d 页),疑似重扫或夹页,已合并其中 %d 页,请人工核对", reg, len(sorted), pageCount, pageCount)
			out.notes = append(out.notes, msg)
			groupWarns = append(groupWarns, msg)
		}

		var pages []pageScan
		batchFail := false
		for pos, m := range take {
			id := idents[m.idx]
			k := pos // 页点不可信:按到达序落位
			if trustPages {
				k = m.pageNo - 1
			}
			sheet := ab.variantBook(book, variant, k) // AB:B 卷页用 B 版式几何(客观题网格随卷种不同)
			res, _, err := omr.ReadRectified(sheet, id.rect, opt)
			if err != nil {
				fail(m.idx+1, fmt.Errorf("考号 %s 第 %d 页判读失败: %w", reg, pos+1, err))
				batchFail = true
				break
			}
			pages = append(pages, pageScan{res: res, dbg: &omr.Debug{Rectified: id.rect}, sheet: sheet, subOffset: subjectiveOffset(book, k)})
		}
		if batchFail || len(pages) == 0 {
			continue
		}
		// 逐页真实页号(供按页号落盘/重读对齐;不可信时全 0 = 按到达位置)
		pageNos := make([]int, len(take))
		for pos, m := range take {
			if trustPages {
				pageNos[pos] = m.pageNo
			}
		}
		if len(pages) < pageCount { // 缺页告警:落库进异常清单(此前只活在一次性回执里,刷新即蒸发)
			msg := fmt.Sprintf("考号 %s 的卷仅识别到 %d/%d 页", reg, len(pages), pageCount)
			if trustPages {
				missing := []int{}
				for p := 1; p <= pageCount; p++ {
					if !seen[p] {
						missing = append(missing, p)
					}
				}
				msg += "(缺第 "
				for i, p := range missing {
					if i > 0 {
						msg += "、"
					}
					msg += fmt.Sprintf("%d", p)
				}
				msg += " 页——请将该生的全部页整份重扫上传,系统会把两批页自动合并成一份完整卷)"
			} else {
				msg += "(旧卡无页点标记,请人工核对该生各页是否齐全后「接受残卷」或补扫)"
			}
			out.notes = append(out.notes, msg)
			groupWarns = append(groupWarns, msg)
		}
		out.appendStudent(pages, base, ab, opt, groupWarns, pageNos)
	}
	return out
}

// appendStudent 完成一个学生的合并:结果、切片(全局题号重编)、整卷回溯图组;
// warns 随卷落库(缺页/页序/B 卡混入等,进异常清单驱动阶段机),pageNos 记录逐页真实页号。
// studentNo 为全局学生号(存量卷数 + 批内序号),切片文件名/评分键以它对齐。
func (io *ingestResult) appendStudent(pages []pageScan, base int, ab *abSupport, opt omr.Options, warns []string, pageNos []int) {
	merged := mergePageScans(pages)
	if ab != nil && ab.hasB && len(ab.booksB) > 0 && merged.Variant == "B" {
		// B 卷页若仍以 A 版式判读(单页/位置分组回退路径),在同一矫正图上按 B 版式补读后重合并;
		// 补读失败的页保持 A 几何结果并挂告警(作答可疑,交人工复核)。
		// 对齐必须按真实页号(缺第 1 页的残卷 pages[0] 实为第 2 页):按组内下标对齐会用
		// 错页几何重读且必然"成功",客观题作答被垃圾覆盖(2026-10-25 复查抓出)。页号未知
		// (页点不可信,pageNos 全 0)时按到达位置回退,与入库选几何的口径一致。
		swapped := false
		fellBack := false
		for k := range pages {
			pageNo := k + 1
			if len(pageNos) == len(pages) && pageNos[k] >= 1 {
				pageNo = pageNos[k]
			}
			if pageNo > len(ab.booksB) {
				continue
			}
			if pages[k].sheet != ab.booksB[pageNo-1] && pages[k].dbg != nil && pages[k].dbg.Rectified != nil {
				if r2, _, err2 := omr.ReadRectified(ab.booksB[pageNo-1], pages[k].dbg.Rectified, opt); err2 == nil {
					pages[k] = pageScan{res: r2, dbg: pages[k].dbg, sheet: ab.booksB[pageNo-1], subOffset: pages[k].subOffset}
					swapped = true
				} else {
					fellBack = true
				}
			}
		}
		if swapped {
			merged = mergePageScans(pages)
		}
		if fellBack {
			merged.Warnings = append(merged.Warnings, "B 卷按 A 几何回退判读(补读失败),客观题作答可疑——请人工核对")
		}
		merged.Answers = reorderAnswers(merged.Answers, ab.orderB) // 卡面序 → 规范题号序(仅此一处,单页路径不再预归一)
	}
	abFinalizeResult(merged, ab)
	merged.Warnings = append(merged.Warnings, warns...)
	if len(pageNos) == len(pages) {
		merged.PageNos = pageNos
	}
	// 教师答案卡(考号全 9 的约定卷):不进学生卷流,交 applyKeyCards 录入参考答案。
	// 硬校验:考号必须逐位精确判读(RegValid 保证唯一判读),否则按正常学生卷走;
	// 卷种待指派的卡不充当答案卡(身份不明)。
	if len(pages) > 0 {
		if digits := pages[0].sheet.Layout.RegDigits; digits > 0 && merged.RegValid &&
			merged.RegNumber == strings.Repeat("9", digits) && !merged.PendingVariant {
			io.keyCards = append(io.keyCards, keyCard{SheetNo: len(io.keyCards) + 1, Answers: merged.Answers})
			return
		}
	}
	io.overlays = append(io.overlays, renderOverlays(pages, merged, opt, io))
	io.results = append(io.results, merged)
	io.slices = append(io.slices, slicesOfPageScans(pages, base+len(io.results), merged.RegNumber))
	rects := make([]*image.Gray, len(pages))
	for k, p := range pages {
		rects[k] = p.dbg.Rectified
	}
	io.pageGroups = append(io.pageGroups, rects)
	io.pages = append(io.pages, pages)
	io.pageNos = append(io.pageNos, merged.PageNos)
}

// overlaySampleEvery 正常卷判读叠加图的抽样间隔(异常卷必存,不受抽样影响)。
const overlaySampleEvery = 20

// renderOverlays 渲染一名学生逐页的判读叠加图并编码为 PNG(吸收自 OMRChecker
// 的 CheckedOMRs:复核时直接看"机器看到了什么",处理争议极快)。
// 异常卷(有告警)必存;正常卷按 1/overlaySampleEvery 抽样存档。
// 逐页产物与 pages 对齐,无矫正图/编码失败的页为 nil。
func renderOverlays(pages []pageScan, merged *omr.Result, opt omr.Options, io *ingestResult) [][]byte {
	sampled := io.overlaySeq%overlaySampleEvery == 0
	io.overlaySeq++
	if len(merged.Warnings) == 0 && !sampled {
		return nil
	}
	out := make([][]byte, len(pages))
	for k, p := range pages {
		if p.dbg == nil || p.dbg.Rectified == nil || p.dbg.ChoiceRatio == nil {
			continue // 位置分组回退等极少数无逐泡比值的页:不出叠加图
		}
		ov := omr.RenderDebugOverlay(p.dbg, p.sheet, opt.MarkThreshold)
		if ov == nil {
			continue
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, ov); err != nil {
			continue
		}
		out[k] = buf.Bytes()
	}
	return out
}

// mergeStudentOnly 供跨批残卷合并复用:只做合并与 AB 归一,不追加进批次产物。
func mergeStudentOnly(pages []pageScan, ab *abSupport, opt omr.Options, warns []string, pageNos []int) *omr.Result {
	ing := ingestResult{}
	ing.appendStudent(pages, 0, ab, opt, warns, pageNos)
	if len(ing.results) == 0 {
		return nil
	}
	return ing.results[0]
}
