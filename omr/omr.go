package omr

import (
	"fmt"
	"image"
	"strings"

	"github.com/opengrade/opengrade/card"
)

// Options 控制读卡行为;零值即合理默认。
type Options struct {
	// MarkThreshold 判定"已涂"的墨迹占比阈值(0~1)。零值取 0.42。
	MarkThreshold float64
	// Threshold 二值化方式:"otsu"(默认,全局)或 "adaptive"(局部自适应,
	// 推荐用于强阴影/污渍/翻拍光照不均的场景)。
	Threshold string
}

func (o Options) withDefaults() Options {
	if o.MarkThreshold <= 0 || o.MarkThreshold >= 1 {
		o.MarkThreshold = 0.42
	}
	if o.Threshold != "adaptive" {
		o.Threshold = "otsu"
	}
	return o
}

func binarizeFor(gray *image.Gray, opt Options) *bitmap {
	if opt.Threshold == "adaptive" {
		return AdaptiveBinarize(gray, 0, 0.15)
	}
	return Binarize(gray, Otsu(gray))
}

// Result 是一张答题卡的识别结果(开放数据格式的一部分,JSON 可长期留存)。
type Result struct {
	RegNumber string   `json:"regNumber,omitempty"` // 判读成功的考号
	RegValid  bool     `json:"regValid"`            // 考号是否每一位都唯一判读成功;无考号区时恒为 true
	RegMarks  [][]int  `json:"regMarks,omitempty"`  // 考号每位判涂数字明细(漏涂为空、多涂多值),供名册模糊匹配
	Answers   []string `json:"answers"`             // 每题:"A".."F"/"T"/"F";""=未涂;"AB"=多涂
	// Electives 是选做题号区的判涂明细(版式 v3):键 = 题号区所在主观题号(1-based),
	// 值 = 涂点下标(0-based,对应该组按题号升序的第几道题)。唯一涂选才参与判分;
	// 漏涂/多涂由统分层按批阅结果回退判定。
	Electives map[int][]int `json:"electives,omitempty"`
	Warnings  []string      `json:"warnings,omitempty"`
	Absent    bool          `json:"absent,omitempty"` // 缺考(手工标记或补录时标注):不计入统计、排名与选项分布
	Manual    bool          `json:"manual,omitempty"` // 手工补录卷(漏扫/答题卡作废后补录,无扫描原图与切片)
}

// Debug 携带中间产物,供可视化排障(矫正图 / 叠加图 / 原始占比)。
type Debug struct {
	Rectified   *image.Gray
	Locators    [4]image.Point
	ChoiceRatio [][]float64
	RegRatio    [][]float64
}

// Read 完整读卡:灰度化 → Otsu 二值化 → 定位角标 → 透视矫正回 300 DPI 标准版面 → 涂卡判读。
// src 支持任意尺寸的扫描件/照片,内部分辨率无关。
func Read(sheet *card.Sheet, src image.Image, opt Options) (*Result, *Debug, error) {
	opt = opt.withDefaults()

	gray := ToGray(src)
	bin := binarizeFor(gray, opt)

	locs, err := findLocators(bin, sheet)
	if err != nil {
		return nil, nil, err
	}
	// 拟合 标准版面→扫描图 的单应,再把扫描图重采样回标准版面网格
	h, err := FitHomography(sheet.Locators[:], locs[:])
	if err != nil {
		return nil, nil, fmt.Errorf("几何矫正失败: %w", err)
	}
	rect := rectify(gray, h, sheet.Wpx, sheet.Hpx)
	rbin := binarizeFor(rect, opt)

	res, dbg := readMarks(rbin, sheet, opt)
	dbg.Rectified = rect
	dbg.Locators = locs
	return res, dbg, nil
}

func rectify(gray *image.Gray, h Homography, w, hh int) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, w, hh))
	for y := 0; y < hh; y++ {
		base := y * w
		for x := 0; x < w; x++ {
			u, v := h.Apply(float64(x), float64(y))
			out.Pix[base+x] = uint8(SampleBilinear(gray, u, v) + 0.5)
		}
	}
	return out
}

// readMarks 在矫正后的二值图上,按共享几何逐圆判读。
func readMarks(bin *bitmap, sheet *card.Sheet, opt Options) (*Result, *Debug) {
	th := opt.MarkThreshold
	res := &Result{RegValid: true}
	dbg := &Debug{ChoiceRatio: make([][]float64, len(sheet.Choice))}

	sampleR := func(r int) int { return int(float64(r) * 0.8) } // 只看涂卡圆内部,避开描边

	for qi, opts := range sheet.Choice {
		dbg.ChoiceRatio[qi] = make([]float64, len(opts))
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(opts))
		var marked []string
		for oi, p := range opts {
			ratio := inkRatio(bin, p.X, p.Y, sampleR(sheet.ChoiceR))
			dbg.ChoiceRatio[qi][oi] = ratio
			switch {
			case ratio >= th:
				marked = append(marked, letters[oi])
			case ratio >= th-0.12:
				res.Warnings = append(res.Warnings, fmt.Sprintf("第 %d 题 %s 选项涂写偏浅,建议人工复核", qi+1, letters[oi]))
			}
		}
		switch {
		case len(marked) == 0:
			res.Answers = append(res.Answers, "")
		case len(marked) == 1:
			res.Answers = append(res.Answers, marked[0])
		case sheet.ChoiceKinds[qi] == card.KindMultiple:
			// 多选题:多涂是合法作答,原样输出,由统分环节与参考答案精确比对
			res.Answers = append(res.Answers, strings.Join(marked, ""))
		default:
			res.Answers = append(res.Answers, strings.Join(marked, ""))
			res.Warnings = append(res.Warnings, fmt.Sprintf("第 %d 题多涂(%s),按错判处理", qi+1, strings.Join(marked, "")))
		}
	}

	if len(sheet.Reg) == 0 {
		readElectives(bin, sheet, opt, res, dbg)
		return res, dbg
	}

	digits := make([]int, len(sheet.Reg))
	res.RegMarks = make([][]int, len(sheet.Reg))
	dbg.RegRatio = make([][]float64, len(sheet.Reg))
	for ci, col := range sheet.Reg {
		dbg.RegRatio[ci] = make([]float64, len(col))
		marked := []int{}
		for d, p := range col {
			ratio := inkRatio(bin, p.X, p.Y, sampleR(sheet.RegR))
			dbg.RegRatio[ci][d] = ratio
			if ratio >= th {
				marked = append(marked, d)
			}
		}
		res.RegMarks[ci] = marked
		if len(marked) == 1 {
			digits[ci] = marked[0]
		} else {
			res.RegValid = false
			if len(marked) == 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("考号第 %d 位漏涂,请人工定位", ci+1))
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("考号第 %d 位多涂,请人工定位", ci+1))
			}
		}
	}
	if res.RegValid {
		var sb strings.Builder
		for _, d := range digits {
			sb.WriteByte(byte('0' + d))
		}
		res.RegNumber = sb.String()
	}
	readElectives(bin, sheet, opt, res, dbg)
	return res, dbg
}

// readElectives 判读选做题号区(版式 v3):逐涂点按墨迹占比判定,
// 结果挂到 res.Electives(键 = 题号区所在主观题号)。漏涂/多涂不判失败,
// 只给提示——统分层会按"组内恰一题已批阅则取该题"回退。
func readElectives(bin *bitmap, sheet *card.Sheet, opt Options, res *Result, dbg *Debug) {
	if len(sheet.Electives) == 0 {
		return
	}
	th := opt.MarkThreshold
	sampleR := int(float64(sheet.RegR) * 0.8) // 与考号涂点同规格,同样只看内部
	for _, row := range sheet.Electives {
		marked := []int{}
		for oi, p := range row.Points {
			if inkRatio(bin, p.X, p.Y, sampleR) >= th {
				marked = append(marked, oi)
			}
		}
		if res.Electives == nil {
			res.Electives = map[int][]int{}
		}
		res.Electives[row.Subjective] = marked
		if len(marked) != 1 {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("第 %s 题选做题号区未唯一涂选(%d 处),判分按批阅结果计入", row.Labels[0], len(marked)))
		}
	}
}
