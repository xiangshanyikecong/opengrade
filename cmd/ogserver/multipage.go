package main

// 多页版式(版式 v3 增量):A4 装不下时主观题自动分页,每页重印身份区
// (标题+页码+考号涂卡+条码/二维码),可独立判读归属。
// 扫描上传约定:每名学生 P 页连续(扫描仪按份出纸的自然顺序),逐页判读后合并。
//
// 合并语义:
//   - 考号取第一个判读成功的页(优先第 1 页);页间考号不一致记告警;
//   - 客观题作答按页拼接(客观题固定在第 1 页);
//   - 选做题号区键 = 全局主观题号(页内键 + 页前主观题偏移);
//   - 切片按页裁剪后全局题号重编(QuestionIndex += 页前偏移);
//   - 逐页告警加「第 k 页」前缀。

import (
	"fmt"
	"image"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// pageScan 是一页的判读产物。
type pageScan struct {
	res       *omr.Result
	dbg       *omr.Debug
	sheet     *card.Sheet
	subOffset int // 本页第 1 道主观题的全局偏移(0-based)
}

// subjectiveOffset 计算第 k 页(0-based)之前的主观题总数。
func subjectiveOffset(book []*card.Sheet, k int) int {
	n := 0
	for j := 0; j < k && j < len(book); j++ {
		n += len(book[j].SubjectiveBoxes)
	}
	return n
}

// readPageScan 读一页并附带全局偏移。
func readPageScan(book []*card.Sheet, k int, img image.Image, opt omr.Options) (pageScan, error) {
	res, dbg, err := omr.Read(book[k], img, opt)
	if err != nil {
		return pageScan{}, err
	}
	return pageScan{res: res, dbg: dbg, sheet: book[k], subOffset: subjectiveOffset(book, k)}, nil
}

// mergePageScans 把一名学生的多页判读合并为一条 Result。
func mergePageScans(pages []pageScan) *omr.Result {
	if len(pages) == 1 {
		return pages[0].res
	}
	primary := pages[0]
	for _, p := range pages {
		if p.res.RegValid { // 考号取第一个判读成功的页
			primary = p
			break
		}
	}
	merged := &omr.Result{
		RegNumber:   primary.res.RegNumber,
		RegValid:    primary.res.RegValid,
		RegSource:   primary.res.RegSource,
		RegMarks:    primary.res.RegMarks,
		Answers:     []string{},
		Variant:     primary.res.Variant,     // 卷种点判读(全卡一致,取首判成功页)
		CodeShort:   primary.res.CodeShort,   // AB 复合码携带的考试短号
		CodeVariant: primary.res.CodeVariant, // 复合码卷种位
	}
	for _, p := range pages {
		merged.Answers = append(merged.Answers, p.res.Answers...)
	}
	merged.Electives = map[int][]int{}
	for _, p := range pages { // 键换算为全局主观题号;先到先得(第 1 页优先)
		for q, marks := range p.res.Electives {
			gq := p.subOffset + q
			if _, ok := merged.Electives[gq]; !ok {
				merged.Electives[gq] = marks
			}
		}
	}
	if len(merged.Electives) == 0 {
		merged.Electives = nil
	}
	// 页间考号不一致 → 告警(以 primary 为准)
	if primary.res.RegValid {
		for k, p := range pages {
			if p.res != primary.res && p.res.RegValid && p.res.RegNumber != primary.res.RegNumber {
				merged.Warnings = append(merged.Warnings,
					fmt.Sprintf("第 %d 页考号 %s 与第 %d 页 %s 不一致,已按后者", k+1, p.res.RegNumber, primaryPageNo(pages, primary), primary.res.RegNumber))
			}
		}
	}
	for k, p := range pages {
		for _, w := range p.res.Warnings {
			merged.Warnings = append(merged.Warnings, fmt.Sprintf("第 %d 页 %s", k+1, w))
		}
	}
	return merged
}

func primaryPageNo(pages []pageScan, primary pageScan) int {
	for k, p := range pages {
		if p.res == primary.res {
			return k + 1
		}
	}
	return 1
}

// slicesOfPageScans 逐页切片并把题号重编为全局主观题号。
func slicesOfPageScans(pages []pageScan, student int, regNumber string) []omr.Slice {
	out := []omr.Slice{}
	for _, p := range pages {
		for _, sl := range omr.SliceRectified(p.dbg.Rectified, p.sheet, student, regNumber) {
			sl.QuestionIndex += p.subOffset
			out = append(out, sl)
		}
	}
	return out
}

// splitPageImages 把按页序连续上传的整卷图像按 P 页一组切分为逐生页组。
func splitPageImages(imgs []image.Image, pageCount int) [][]image.Image {
	groups := make([][]image.Image, 0, len(imgs)/max(1, pageCount))
	for i := 0; i < len(imgs); i += pageCount {
		end := i + pageCount
		if end > len(imgs) {
			end = len(imgs)
		}
		groups = append(groups, imgs[i:end])
	}
	return groups
}
