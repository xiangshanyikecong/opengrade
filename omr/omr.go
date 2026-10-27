package omr

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/raster"
)

// Options 控制读卡行为;零值即合理默认。
type Options struct {
	// MarkThreshold 判定"已涂"的墨迹占比阈值(0~1)。零值取 0.42。
	MarkThreshold float64
	// Threshold 二值化方式:"otsu"(默认,全局)或 "adaptive"(局部自适应,
	// 推荐用于强阴影/污渍/翻拍光照不均的场景)。
	Threshold string
	// 可选灰度预处理链(见 Preprocess):中值去噪 → 伽马 → CLAHE。
	// 全零 = 不启用,行为与未加预处理链的旧版完全一致。
	Gamma float64
	Median int
	CLAHE int
}

// Preprocess 是判读前的灰度预处理链配置,可随考试持久化、逐卷复用。
// 与 Exam 上的 Threshold(二值化方式)配对构成完整读卡配置。
type Preprocess struct {
	Gamma  float64 `json:"gamma,omitempty"`  // 伽马(<1 提亮阴影/褪色件,>1 压暗);0=不启用
	Median int     `json:"median,omitempty"` // 中值滤波窗口(3/5,去椒盐噪声);0=不启用
	CLAHE  int     `json:"clahe,omitempty"`  // CLAHE 分块数(常用 8,弱光/低对比增墨);0=不启用
}

// Options 返回套用本预处理链的读卡选项;p 为 nil 时仅携带二值化方式。
func (p *Preprocess) Options(threshold string) Options {
	return Options{Threshold: threshold, Gamma: p.gamma(), Median: p.medianN(), CLAHE: p.claheN()}
}

func (p *Preprocess) gamma() float64 {
	if p == nil {
		return 0
	}
	return p.Gamma
}
func (p *Preprocess) medianN() int {
	if p == nil {
		return 0
	}
	return p.Median
}
func (p *Preprocess) claheN() int {
	if p == nil {
		return 0
	}
	return p.CLAHE
}

// preprocessGray 在二值化/定位前应用可选预处理链。
// 顺序:中值(去椒盐,防统计增强把噪点放大)→ 伽马(单调 LUT)→ CLAHE(对比增强)。
func preprocessGray(g *image.Gray, opt Options) *image.Gray {
	if opt.Median == 3 || opt.Median == 5 {
		g = raster.MedianGray(g, opt.Median)
	}
	if opt.Gamma > 0 && opt.Gamma != 1 {
		g = raster.GammaGray(g, opt.Gamma)
	}
	if opt.CLAHE > 0 {
		g = raster.CLAHEGray(g, opt.CLAHE, 0)
	}
	return g
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
	RegValid  bool     `json:"regValid"`            // 考号是否每一位都唯一判读成功;无考号区时恒 true
	RegSource string   `json:"regSource,omitempty"` // 考号来源:fill(填涂)/barcode(条码)/qr(二维码)
	RegMarks  [][]int  `json:"regMarks,omitempty"`  // 考号每位判涂数字明细(漏涂为空、多涂多值),供名册模糊匹配
	Answers   []string `json:"answers"`             // 每题:"A".."F"/"T"/"F";""=未涂;"AB"=多涂
	// Electives 是选做题号区的判涂明细(版式 v3):键 = 题号区所在主观题号(1-based),
	// 值 = 涂点下标(0-based,对应该组按题号升序的第几道题)。唯一涂选才参与判分;
	// 漏涂/多涂由统分层按批阅结果回退判定。
	Electives map[int][]int `json:"electives,omitempty"`
	Warnings  []string      `json:"warnings,omitempty"`
	Absent    bool          `json:"absent,omitempty"` // 缺考(手工标记或补录时标注):不计入统计、排名与选项分布
	Manual    bool          `json:"manual,omitempty"` // 手工补录卷(漏扫/答题卡作废后补录,无扫描原图与切片)
	Voided    bool          `json:"voided,omitempty"` // 作废卷(重扫替换/重复卷裁决后弃用的那张):不进报告与阅卷队列
	PageNo    int           `json:"pageNo,omitempty"` // 页点判读出的页号(多页卡;0=单页卡/旧卡无页点)
	// PageNos 是多页卡合并时逐页(按合并顺序)的页点页号;0=该页页号未知(旧数据/页点不可信)。
	// 供跨批残卷合并、卷种重读按真实页号对版式,以及整卷图按页号落盘命名。
	PageNos []int `json:"pageNos,omitempty"`

	// AB 卷(卷种识别):卷种点为主、二维码复合码冗余佐证;两者都拿不到时由服务端挂"待人工指派"。
	Variant     string `json:"variant,omitempty"`     // 卷种点判读结果:"A"/"B";非 AB 卡为空
	CodeShort   string `json:"codeShort,omitempty"`   // 二维码/条码复合码携带的考试短号(4 位数字);普通纯考号码为空
	CodeVariant string `json:"codeVariant,omitempty"` // 复合码携带的卷种 "A"/"B";与卷种点不一致时以点为准并告警
	// PendingVariant:卷种无法识别的卷(服务端置位)——客观题作答按所指派卷种重读前不进统计,
	// 主观题切片不受影响(AB 两版主观题区强制一致)。人工指派卷种后清除。
	PendingVariant bool `json:"pendingVariant,omitempty"`
}

// Debug 携带中间产物,供可视化排障(矫正图 / 叠加图 / 原始占比)。
type Debug struct {
	Rectified   *image.Gray
	Locators    [4]image.Point
	ChoiceRatio [][]float64
	RegRatio    [][]float64
	// Thresholds/RegThresholds 是逐行(客观题/考号位)自适应判读阈值,与
	// ChoiceRatio/RegRatio 同维度;回退全局阈值的行取值即全局阈值。
	// 供叠加图与排障使用,不进 Result(开放数据格式)。
	Thresholds    []float64
	RegThresholds []float64
}

// Read 完整读卡:灰度化 → Otsu 二值化 → 定位角标 → 透视矫正回 300 DPI 标准版面 → 涂卡判读。
// src 支持任意尺寸的扫描件/照片,内部分辨率无关。
func Read(sheet *card.Sheet, src image.Image, opt Options) (*Result, *Debug, error) {
	rect, locs, err := locateAndRectify(sheet, src, opt)
	if err != nil {
		return nil, nil, err
	}
	res, dbg, err := ReadRectified(sheet, rect, opt)
	if err != nil {
		return nil, nil, err
	}
	dbg.Rectified = rect
	dbg.Locators = locs
	return res, dbg, nil
}

// locateAndRectify 是 Read 的前半段:二值化 + 定位(含三级兜底)+ 单应矫正。
// 矫正结果与用哪一页的 Sheet 无关(四角定位块/页面几何全页一致),可复用于逐页判读。
func locateAndRectify(sheet *card.Sheet, src image.Image, opt Options) (*image.Gray, [4]image.Point, error) {
	opt = opt.withDefaults()

	gray := preprocessGray(ToGray(src), opt)
	bin := binarizeFor(gray, opt)

	locs, err := findLocators(bin, sheet)
	if err != nil {
		// 定位失败的三级兜底(不改变调用方选择的判读二值化方式):
		// 1) 局部自适应(强阴影/光照渐变);2) 墨迹百分位阈值(版面墨迹稀少时
		// Otsu 两类极不均衡会整体失衡,按"最暗 2% 为墨迹"切分可保定位块完整)。
		for _, alt := range []*bitmap{
			AdaptiveBinarize(gray, 0, 0.15),
			Binarize(gray, inkPercentile(gray, 0.02)),
		} {
			if l2, err2 := findLocators(alt, sheet); err2 == nil {
				locs, err = l2, nil
				break
			}
		}
	}
	if err != nil {
		return nil, [4]image.Point{}, err
	}
	// 拟合 标准版面→扫描图 的单应,再把扫描图重采样回标准版面网格
	h, err := FitHomography(sheet.Locators[:], locs[:])
	if err != nil {
		return nil, [4]image.Point{}, fmt.Errorf("几何矫正失败: %w", err)
	}
	rect := rectify(gray, h, sheet.Wpx, sheet.Hpx)
	return rect, locs, nil
}

// RectifyOnce 只做定位与透视矫正(不判读),供多页归组"矫正一次、逐页复用"。
func RectifyOnce(sheet *card.Sheet, src image.Image, opt Options) (*image.Gray, error) {
	rect, _, err := locateAndRectify(sheet, src, opt)
	return rect, err
}

// ReadRectified 在已矫正的标准版面灰度图上完整判读(等价 Read 的矫正后阶段):
// 涂卡判读 → 码制融合 → 空白卡检测。与 Read 搭配可实现"矫正一次、按不同页版式各读一遍"。
func ReadRectified(sheet *card.Sheet, rect *image.Gray, opt Options) (*Result, *Debug, error) {
	opt = opt.withDefaults()
	rbin := binarizeFor(rect, opt)

	res, dbg := readMarks(rbin, sheet, opt)
	if sheet.RegCodeKind != "" {
		// 码制通道:在矫正后的标准网格上解码,与填涂结果融合(以码为准)。
		// 码内容两种合法形态:纯考号(长度=RegDigits,旧/普通卡)或 AB 复合码
		// 「考试短号(4)+卷种位(1=A/2=B)+考号」(长度=5+RegDigits);考试短号核对在服务端做。
		codeVal, codeOK := decodeRegCode(rect, rbin, sheet)
		parsed := false
		if codeOK && allDigitsStr(codeVal) {
			switch {
			case len(codeVal) == sheet.Layout.RegDigits:
				mergeRegCode(res, sheet, codeVal, true)
				parsed = true
			case len(codeVal) == 5+sheet.Layout.RegDigits:
				res.CodeShort = codeVal[:4]
				switch codeVal[4] {
				case '1':
					res.CodeVariant = "A"
				case '2':
					res.CodeVariant = "B"
				}
				mergeRegCode(res, sheet, codeVal[5:], true)
				parsed = true
			}
		}
		if !parsed {
			mergeRegCode(res, sheet, "", false)
		}
	} else if res.RegValid {
		res.RegSource = "fill"
	}
	// 卷种双通道互核:卷种点为准;点判不出而码有 → 码兜底(冗余自愈);两者矛盾 → 告警人工复核
	if res.Variant == "" && res.CodeVariant != "" {
		res.Variant = res.CodeVariant
	} else if res.Variant != "" && res.CodeVariant != "" && res.Variant != res.CodeVariant {
		res.Warnings = append(res.Warnings, fmt.Sprintf("卷种点(%s 卷)与码内卷种位(%s 卷)不一致,已按卷种点判读,请人工复核", res.Variant, res.CodeVariant))
	}
	// 空白卡检测:考号有效但客观题全未作答(且无选做)→ 告警人工复核
	if res.RegValid && len(res.Answers) > 0 && len(res.Electives) == 0 {
		answered := false
		for _, a := range res.Answers {
			if a != "" {
				answered = true
				break
			}
		}
		if !answered {
			res.Warnings = append(res.Warnings, "全卷客观题未作答,疑似空白卡")
		}
	}
	dbg.Rectified = rect
	return res, dbg, nil
}

func allDigitsStr(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
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

// snapRow 印刷漂移自对齐:检测一行涂点印刷圆环的实际偏移,超过 snap 比例
// 即整体平移采样点。返回平移后的采样点与是否需要漂移告警。
func snapRow(bin *bitmap, pts []image.Point, r int) ([]image.Point, bool) {
	dx, dy, ok := estimateRowShift(bin, pts, r)
	if !ok {
		return pts, false
	}
	dist := math.Hypot(dx, dy)
	if dist <= alignSnapFrac*float64(r) {
		return pts, false
	}
	return shiftPoints(pts, dx, dy), dist > alignWarnFrac*float64(r)
}

// readMarks 在矫正后的二值图上,按共享几何逐圆判读。
// 判读阈值为"全局 + 逐行自适应"两级(见 adaptive.go):行内跳变统计给出该行
// 的局部阈值,干净行自动回退全局;全卡过半行显著偏移时追加复印件/褪色告警。
// 判读前逐行做印刷漂移自对齐(见 align.go)。
func readMarks(bin *bitmap, sheet *card.Sheet, opt Options) (*Result, *Debug) {
	th := opt.MarkThreshold
	res := &Result{RegValid: true}
	dbg := &Debug{
		ChoiceRatio: make([][]float64, len(sheet.Choice)),
		Thresholds:  make([]float64, len(sheet.Choice)),
	}
	deviated, totalRows := 0, len(sheet.Choice)
	driftWarns := 0

	sampleR := func(r int) int { return int(float64(r) * 0.8) } // 只看涂卡圆内部,避开描边

	// 页点(多页卡):第 k 点实心 = 第 k 页,供按考号归组后的页内排序与缺页告警
	if len(sheet.PageDots) > 0 {
		for i, p := range sheet.PageDots {
			if inkRatio(bin, p.X, p.Y, sheet.PageDotR) >= th {
				res.PageNo = i + 1
				break
			}
		}
	}

	// 卷种点(AB 卷):两点恰一实 → A(第 1 点)/B(第 2 点);两空或两实 → 留空,由服务端按"卷种待人工"处理
	if len(sheet.VariantDots) == 2 {
		solid, first := 0, -1
		for i, p := range sheet.VariantDots {
			if inkRatio(bin, p.X, p.Y, sheet.PageDotR) >= th {
				solid++
				first = i
			}
		}
		if solid == 1 {
			res.Variant = "A"
			if first == 1 {
				res.Variant = "B"
			}
		}
	}

	for qi, opts := range sheet.Choice {
		dbg.ChoiceRatio[qi] = make([]float64, len(opts))
		ratios := make([]float64, len(opts))
		sampled, warn := snapRow(bin, opts, sheet.ChoiceR)
		if warn {
			driftWarns++
		}
		for oi, p := range sampled {
			ratios[oi] = inkRatio(bin, p.X, p.Y, sampleR(sheet.ChoiceR))
			dbg.ChoiceRatio[qi][oi] = ratios[oi]
		}
		thQ := rowThreshold(ratios, th)
		dbg.Thresholds[qi] = thQ
		if diff := thQ - th; diff > adaptiveDeviation || diff < -adaptiveDeviation {
			deviated++
		}
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(opts))
		var marked []string
		for oi, ratio := range ratios {
			switch {
			case ratio >= thQ:
				marked = append(marked, letters[oi])
			case ratio >= thQ-0.12:
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

	if len(sheet.Reg) == 0 || !sheet.Layout.RegHasFill() {
		// 无考号区,或纯码制通道(卡面未印填涂区,不做涂卡判读)
		driftWarns += readElectives(bin, sheet, opt, res, dbg)
		maybeThresholdDeviationWarning(res, deviated, totalRows)
		maybeDriftWarning(res, driftWarns)
		return res, dbg
	}

	digits := make([]int, len(sheet.Reg))
	res.RegMarks = make([][]int, len(sheet.Reg))
	dbg.RegRatio = make([][]float64, len(sheet.Reg))
	dbg.RegThresholds = make([]float64, len(sheet.Reg))
	for ci, col := range sheet.Reg {
		sampled, warn := snapRow(bin, col, sheet.RegR)
		if warn {
			driftWarns++
		}
		dbg.RegRatio[ci] = make([]float64, len(col))
		ratios := make([]float64, len(col))
		for d, p := range sampled {
			ratios[d] = inkRatio(bin, p.X, p.Y, sampleR(sheet.RegR))
			dbg.RegRatio[ci][d] = ratios[d]
		}
		thC := rowThreshold(ratios, th)
		dbg.RegThresholds[ci] = thC
		if diff := thC - th; diff > adaptiveDeviation || diff < -adaptiveDeviation {
			deviated++
		}
		totalRows++
		marked := []int{}
		for d, ratio := range ratios {
			if ratio >= thC {
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
	maybeThresholdDeviationWarning(res, deviated, totalRows)
	maybeDriftWarning(res, driftWarns)
	return res, dbg
}

// maybeDriftWarning 汇总印刷漂移告警(逐行刷屏会淹没重点,只报总数)。
func maybeDriftWarning(res *Result, driftWarns int) {
	if driftWarns > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("检测到 %d 处印刷偏移(已自动对齐),边缘题建议人工复核", driftWarns))
	}
}

// maybeThresholdDeviationWarning 全卡过半行的自适应阈值显著偏离全局阈值时,
// 追加一条复印件/褪色告警 —— 整卡墨迹水平系统性漂移的典型特征;逐行刷屏
// 会淹没重点,故只做一次汇总提示。
func maybeThresholdDeviationWarning(res *Result, deviated, total int) {
	if total > 0 && deviated*2 > total {
		res.Warnings = append(res.Warnings, "逐题判读阈值整体偏移,疑似复印件或褪色打印,建议人工复核")
	}
}

// readElectives 判读选做题号区(版式 v3):逐涂点按墨迹占比判定,
// 结果挂到 res.Electives(键 = 题号区所在主观题号)。漏涂/多涂不判失败,
// 只给提示——统分层会按"组内恰一题已批阅则取该题"回退。
// 返回印刷漂移告警数(与客观题/考号区同一套自对齐)。
func readElectives(bin *bitmap, sheet *card.Sheet, opt Options, res *Result, dbg *Debug) (driftWarns int) {
	if len(sheet.Electives) == 0 {
		return 0
	}
	th := opt.MarkThreshold
	sampleR := int(float64(sheet.RegR) * 0.8) // 与考号涂点同规格,同样只看内部
	for _, row := range sheet.Electives {
		sampled, warn := snapRow(bin, row.Points, sheet.RegR)
		if warn {
			driftWarns++
		}
		marked := []int{}
		for oi, p := range sampled {
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
	return driftWarns
}
