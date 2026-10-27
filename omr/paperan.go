package omr

// 试卷版面分析(半自动出卡第一步):上传一页试卷照片/扫描件,检测「题目构成」——
// 客观题(题型/选项数/题号)与主观题(留白区高度),产出答题卡版式草稿 + 叠加标注图 +
// 每题题干截图,由教师在设计器确认页人工微调后生成答题卡/导入题库。纯 Go、零第三方依赖。
//
// 检测原理(无 OCR,封闭候选集模板匹配):
//   - 原图降采样统一尺度 → 亮区检测裁掉桌面/暗背景(照片) → 自适应二值化 →
//     倾斜角估计(投影方差最大化,±5°)→ 反向矫正 → 水平投影切文字行 →
//     行内垂直投影按间隙切字符簇(默认档 1.8mm;首选项簇以 A 开头但识别失败时细档
//     0.7mm 重切,化解紧排选项「A.1 B.2」被并簇导致的静默漏题);
//   - 选项行:允许题干簇在选项簇之前(「1.题干…… A.x B.y」同行题干),从首个「字形为 A」
//     的簇起要求字母连续递增(A = 新题,选项数=簇数,2 簇=判断;B~F 开头 = 上题换行续排);
//   - 判断题除 A/B 外还认「对/错」字形(封闭候选集相对比较,低置信出提示);
//   - 行首题号按数字模板读取(支持一簇内多位与多位拆簇,≤3 位),全局审计题号连续性:
//     跳变自动补占位题,让漏检显式可见而非后续题全部静默错位;
//   - 双栏版式:检测到栏缝后左右栏分别走上述管线再拼接(题号审计跨栏连续);
//     栏缝因倾斜不可靠切分时仍报错拒绝;
//   - 行间大段低墨迹竖向空白 = 主观题作答区,高度按 A4 宽度假定折算 mm;
//   - 每题题干区域(上一题块结束到本题选项行之间的文字行,或同行题干区间)从矫正后
//     灰度图裁出小图,供「导入题库(题干截图)」使用。
//
// 边界(写进 UI 提示):A4 单栏/标准双栏印刷体、倾斜 ≤±5°、印刷体;√/× 不识别;
// 填空题的下划线是墨迹,会被当文字行,需手工补;多选与单选结构相同,默认按单选导入。
// 所有误检由确认页人工兜底;真实试卷字体与模板字体的差异靠多字体模板集缓解。

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// PaperQuestion 是检测出的客观题草稿(题型/选项数/原卷题号;分值卷面不可靠,恒 1 待人工改)。
type PaperQuestion struct {
	Kind    string      `json:"kind"`             // choice | judge
	Options int         `json:"options"`          // 选项数(判断恒 2)
	Score   float64     `json:"score"`            // 恒 1,设计器确认页批量改
	Number  int         `json:"number,omitempty"` // 原卷题号(0=行首未识别出题号)
	Note    string      `json:"note,omitempty"`   // 该题的低置信/占位说明
	StemImg *image.Gray `json:"-"`                // 题干截图(handler 转 base64;导入题库用)
}

// PaperSubjective 是检测出的主观题留白区草稿。
type PaperSubjective struct {
	Kind     string      `json:"kind"`           // subjective
	HeightMM float64     `json:"heightMM"`       // 留白高度(已扣书写边距)
	Note     string      `json:"note,omitempty"` // 低置信说明
	StemImg  *image.Gray `json:"-"`              // 留白上方紧邻文字行截图
}

// PaperRect 是检测结果矩形(叠加图与前端定位用):class 0 文字行 1 客观题选项行 2 主观区 3 题干。
type PaperRect struct {
	X0    int `json:"x0"`
	Y0    int `json:"y0"`
	X1    int `json:"x1"`
	Y1    int `json:"y1"`
	Class int `json:"class"`
}

// PaperAnalysis 是一页试卷的检测草稿。
type PaperAnalysis struct {
	Questions   []PaperQuestion   `json:"questions"`
	Subjectives []PaperSubjective `json:"subjectives"`
	Notes       []string          `json:"notes"`
	SkewDeg     float64           `json:"skewDeg"` // 估计出的页面倾斜角(已反向矫正)
	Width       int               `json:"width"`   // 分析坐标系尺寸(rects 以此为参照)
	Height      int               `json:"height"`
}

// PaperResult 附带叠加标注图(灰度:黑框=客观题选项行、浅灰线=文字行、深灰框=主观区、
// 灰细框=题干区域)与全部检测矩形。
type PaperResult struct {
	Analysis *PaperAnalysis
	Overlay  *image.Gray
	Rects    []PaperRect
}

// 分析结果矩形(叠加图用):0 文字行 1 客观题选项行 2 主观区 3 题干。
type paperRect struct {
	x0, y0, x1, y1 int
	class          int
}

type paperCluster struct{ x0, x1, y0, y1 int }

func (c paperCluster) w() int { return c.x1 - c.x0 + 1 }
func (c paperCluster) h() int { return c.y1 - c.y0 + 1 }

type paperLine struct {
	y0, y1 int
}

func (l paperLine) h() int { return l.y1 - l.y0 + 1 }

// colLine 是一栏内已切分的文字行;clusters 为栏内局部 x 坐标(与 colBin 对应),
// 转全页绝对坐标时统一加 xOff。
type colLine struct {
	y0, y1   int
	xOff     int
	clusters []paperCluster
}

func (l colLine) pline() paperLine { return paperLine{y0: l.y0, y1: l.y1} }

const (
	glyphAccept  = 0.60 // 字形余弦相似度接受阈(封闭候选集,宁低勿漏:确认页兜底)
	digitAccept  = 0.68 // 数字字形接受阈(高于字母:碎片误配成数字的代价是题号错乱)
	dcAccept     = 0.55 // 对/错字形接受阈(汉字密度大,阈略低 + 相对比较兜底)
	tplN         = 24   // 字形模板归一化边长
	optGapMM     = 1.8  // 默认簇间隙:选项块之间 3~10mm,字间/词间距 <1.8mm 保持在同簇
	fineGapMM    = 0.7  // 细档簇间隙:部分并簇的紧排选项行重切用
	stemMaxLines = 5    // 单题题干最多聚合的文字行数
	auditMaxGap  = 30   // 题号跳变补占位的上限(超过视为误读,只提示不补)
)

// AnalyzePaper 分析一页试卷(灰度图)。A4 宽度假定:pxPerMM = 纸面图宽 / 210。
func AnalyzePaper(gray0 *image.Gray) (*PaperResult, error) {
	if gray0 == nil || gray0.Rect.Dx() < 300 || gray0.Rect.Dy() < 300 {
		return nil, fmt.Errorf("图像太小(宽高均需 ≥300px)")
	}
	a := &PaperAnalysis{Questions: []PaperQuestion{}, Subjectives: []PaperSubjective{}, Notes: []string{}}

	// 全黑/反色防御:纸面平均灰度过低说明不是正常白纸(黑版/底片/镜头盖),
	// 自适应二值化在黑纸+白角的边界会造出贯穿整页的假墨带,后续全是无意义输出
	var graySum int64
	for _, p := range gray0.Pix {
		graySum += int64(p)
	}
	if m := float64(graySum) / float64(len(gray0.Pix)); m < 80 {
		return nil, fmt.Errorf("图像平均灰度 %.0f,疑似全黑页或反色图,请上传正常纸面扫描件", m)
	}

	// 0. 统一分析尺度(超宽照片重采样)+ 照片纸面裁剪(裁掉桌面/暗背景,恢复 A4 宽度假定)
	gray := resampleGrayCap(gray0, 2400)
	gray = cropPaperRegion(gray)

	// 1. 二值化(矫正前),并做全黑/反色防御(墨迹占比过半说明不是正常纸面)
	bin0 := AdaptiveBinarize(gray, 0, 0.18)
	_, _, bin0 = trimFrame(bin0)
	despeckle(bin0)
	W := bin0.W
	pxMM := float64(W) / 210.0 // A4 假定:纸面图宽 ≈ 210mm(cropPaperRegion 已裁到纸边时成立)
	if ink := totalInk(bin0); ink > W*bin0.H/2 {
		return nil, fmt.Errorf("图像墨迹占比 %.0f%%,疑似全黑页或反色图,请上传正常纸面扫描件", 100*float64(ink)/float64(W*bin0.H))
	}
	a.Width, a.Height = W, bin0.H

	// 2. 双栏检测与分栏矫正。整页倾斜估计在双栏版式上会被「两栏网格对齐角」带偏
	//    (伪峰:旋转该角后两栏行网格恰好对齐,栏缝判定随之失效),所以先按最宽栏缝
	//    分半、各自估计倾角矫正,再用矫正后的行网格判定真假分栏 —— 任一侧 ≥70% 的行
	//    能在另一侧配对 = 单栏(短行/题干只出现在缝一侧很正常);大量失配 = 真分栏。
	var planes []colPlane
	overlayGray := gray
	band, hasBand := widestGutterBand(bin0)
	if hasBand && band[0] > W/8 && band[1] < W*7/8 {
		lb0 := cropCols(bin0, 0, band[0]-1)
		rb0 := cropCols(bin0, band[1]+1, W-1)
		skewL, skewR := estimateSkew(lb0), estimateSkew(rb0)
		lg := binarizePlane(rotateGray(cropGrayRegion(gray, 0, band[0]-1), -skewL))
		rg := binarizePlane(rotateGray(cropGrayRegion(gray, band[1]+1, W-1), -skewR))
		if !rowsAligned(lg, rg) {
			// 真分栏:左右栏各自的分析平面(矫正后),overlay 用分半矫正图拼回整页
			lgGray := rotateGray(cropGrayRegion(gray, 0, band[0]-1), -skewL)
			rgGray := rotateGray(cropGrayRegion(gray, band[1]+1, W-1), -skewR)
			planes = []colPlane{
				{xOff: 0, bin: lg, gray: lgGray},
				{xOff: band[1] + 1, bin: rg, gray: rgGray},
			}
			overlayGray = pastePlanes(gray, planes)
			a.SkewDeg = skewL
			a.Notes = append(a.Notes, "已按双栏版式识别(左栏→右栏拼接),请核对题序与栏间留白")
			for _, s := range []float64{skewL, skewR} {
				if math.Abs(s) > 5 {
					a.Notes = append(a.Notes, fmt.Sprintf("栏内倾斜约 %.1f°,超过 5° 检测上限,结果可能不准——请尽量摆正后重扫", s))
				}
			}
		}
	}
	if len(planes) == 0 {
		// 单栏:整页估计倾角 → 反向矫正(>0.2° 才重采样,避免无谓模糊)
		skew := estimateSkew(bin0)
		a.SkewDeg = skew
		if math.Abs(skew) > 5 {
			a.Notes = append(a.Notes, fmt.Sprintf("页面倾斜约 %.1f°,超过 5° 检测上限,结果可能不准——请尽量摆正后重扫", skew))
		}
		if math.Abs(skew) >= 0.2 {
			gray = rotateGray(gray, -skew) // estimateSkew 与 rotateGray 同号约定,归正取负
		}
		bin := binarizePlane(gray)
		planes = []colPlane{{xOff: 0, bin: bin, gray: gray}}
		overlayGray = gray
	}

	st := &pageState{a: a, pxMM: pxMM}
	for _, p := range planes {
		analyzeColumn(st, p)
	}

	if st.totalLines == 0 {
		return nil, fmt.Errorf("未检测到文字行:请确认上传的是试卷页面(印刷体、光照均匀)")
	}

	// 3. 题号连续性审计(跨栏全局):跳变自动补占位题,让漏检显式可见
	auditNumbers(a)

	// 4. 汇总提示
	if len(a.Questions) == 0 {
		a.Notes = append(a.Notes, "未检测到客观题选项行(判断题印「对/错」或 A/B 均可识别;仍无结果请手工补题)")
	} else {
		a.Notes = append(a.Notes, "多选与单选结构相同,已全部按单选导入——多选题请在表单中改题型;分值统一按 1 分导入,请用「批量设分值」修改")
	}

	rects := make([]PaperRect, len(st.rects))
	for i, r := range st.rects {
		rects[i] = PaperRect{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Class: r.class}
	}
	return &PaperResult{Analysis: a, Overlay: renderOverlay(overlayGray, st.rects), Rects: rects}, nil
}

// colPlane 是一个分析平面(单栏页=整页;双栏页=矫正后的半页),gray/bin 同尺寸同坐标系。
type colPlane struct {
	xOff int         // 在全页坐标系中的 x 偏移
	bin  *bitmap     // 矫正后二值图
	gray *image.Gray // 矫正后灰度(题干截图来源)
}

// binarizePlane 自适应二值化 + 裁边 + 去噪。
func binarizePlane(g *image.Gray) *bitmap {
	bin := AdaptiveBinarize(g, 0, 0.18)
	_, _, bin = trimFrame(bin)
	despeckle(bin)
	return bin
}

// widestGutterBand 页宽中段 25%~75% 内最宽的全高无墨竖带(≥3mm,候选栏缝)。
func widestGutterBand(b *bitmap) ([2]int, bool) {
	var none [2]int
	mm := float64(b.W) / 210.0
	colInk := make([]int, b.W)
	for i, v := range b.D {
		if v == 1 {
			colInk[i%b.W]++
		}
	}
	best, run, bestX := 0, 0, b.W/2
	for x := b.W / 4; x <= b.W*3/4; x++ {
		if colInk[x] == 0 {
			run++
			if run > best {
				best, bestX = run, x-run+1
			}
		} else {
			run = 0
		}
	}
	if best < int(3*mm) {
		return none, false
	}
	return [2]int{bestX, bestX + best - 1}, true
}

// rowsAligned 判定分半矫正后的两侧行网格是否对齐(单栏):在最优系统偏移下,任一侧
// ≥70% 的行能在另一侧 ±2mm 内配对成功即视为对齐(短行/标题/题干只出现在缝一侧很正常)。
// 各半页绕自身中心矫正,同一物理行在两侧落点差 tanθ×半页间距(±80px 内的系统平移),
// 所以按平移量扫描取最佳配对;真分栏两栏内容流彼此独立,任何平移都对不上。
func rowsAligned(lb, rb *bitmap) bool {
	lr := rowRunStarts(lb, 0, lb.W)
	rr := rowRunStarts(rb, 0, rb.W)
	if len(lr) < 3 || len(rr) < 3 {
		return false
	}
	tol := int(2 * float64(lb.W+rb.W) / 210.0)
	best := 0.0
	for d := -80; d <= 80; d++ {
		ml, mr := 0, 0
		for i, j := 0, 0; i < len(lr) && j < len(rr); {
			switch dd := lr[i] - (rr[j] + d); {
			case dd <= tol && dd >= -tol:
				ml++
				mr++
				i++
				j++
			case dd < 0:
				i++
			default:
				j++
			}
		}
		r := float64(ml) / float64(len(lr))
		if q := float64(mr) / float64(len(rr)); q > r {
			r = q
		}
		if r > best {
			best = r
		}
	}
	return best >= 0.75
}

// cropGrayRegion 裁出 [x0,x1] 列范围(含端点)的灰度子图。
func cropGrayRegion(g *image.Gray, x0, x1 int) *image.Gray {
	w := x1 - x0 + 1
	out := image.NewGray(image.Rect(0, 0, w, g.Rect.Dy()))
	for y := 0; y < g.Rect.Dy(); y++ {
		copy(out.Pix[y*w:(y+1)*w], g.Pix[y*g.Stride+x0:y*g.Stride+x1+1])
	}
	return out
}

// pastePlanes 把各分栏的矫正灰度按 xOff 拼回整页画布(叠加图底图)。
func pastePlanes(base *image.Gray, planes []colPlane) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, base.Rect.Dx(), base.Rect.Dy()))
	copy(out.Pix, base.Pix)
	for _, p := range planes {
		for y := 0; y < p.gray.Rect.Dy() && y < out.Rect.Dy(); y++ {
			copy(out.Pix[y*out.Stride+p.xOff:y*out.Stride+p.xOff+p.gray.Rect.Dx()],
				p.gray.Pix[y*p.gray.Stride:y*p.gray.Stride+p.gray.Rect.Dx()])
		}
	}
	return out
}

// pageState 聚合一页分析过程中的共享上下文。
type pageState struct {
	a          *PaperAnalysis
	pxMM       float64
	rects      []paperRect
	totalLines int
}

// analyzeColumn 对一个分析平面(单栏页=整页,双栏页=矫正后的半页)执行
// 行切分 → 簇切分 → 行分类 → 主观留白检测,结果追加进 pageState
// (题目跨栏拼接;矩形/题干坐标转全页绝对系,簇保持平面局部系)。
func analyzeColumn(st *pageState, p colPlane) {
	pxMM := st.pxMM
	a := st.a
	colBin, xOff := p.bin, p.xOff
	colW := colBin.W

	runs := segmentLines(colBin, pxMM)
	st.totalLines += len(runs)
	lines := make([]colLine, 0, len(runs))
	for _, r := range runs {
		ln := paperLine{y0: r.y0, y1: r.y1}
		cls := segmentClusters(colBin, ln, pxMM)
		lines = append(lines, colLine{y0: r.y0, y1: r.y1, xOff: xOff, clusters: cls})
		st.rects = append(st.rects, paperRect{xOff, r.y0, xOff + colW - 1, r.y1, 0})
	}

	lastOpt := -1   // 本栏最后一题在 a.Questions 中的下标(续排行归属)
	lastBlock := -1 // 本栏最后一个题块行下标(题干行聚合的左边界)
	for i := range lines {
		lc := classifyLine(colBin, &lines[i], pxMM)
		switch lc.kind {
		case lineNewQ:
			kind := "choice"
			if len(lc.letters) == 2 {
				kind = "judge" // 2 选项 = 判断
			}
			q := PaperQuestion{Kind: kind, Options: len(lc.letters), Score: 1, Number: lc.number}
			if lc.tight {
				q.Note = joinNote(q.Note, "选项间距较紧(已按细档重切),请核对选项数")
			}
			if lc.snapped {
				q.Note = joinNote(q.Note, "部分选项字形置信度偏低,已按字母序列回贴——请核对选项数")
			}
			if lowConfidence(lc.scores) {
				q.Note = joinNote(q.Note, "选项字母置信度偏低,请核对选项数")
			}
			q.StemImg = cropStemImg(p.gray, p.bin, st, stemRunRect(lines, lastBlock+1, i-1, lines[i], lc))
			a.Questions = append(a.Questions, q)
			lastOpt = len(a.Questions) - 1
			st.rects = append(st.rects, optLineRect(lines[i], lc))
			lastBlock = i
		case lineCont:
			if lastOpt < 0 {
				continue
			}
			q := &a.Questions[lastOpt]
			if int(lc.letters[0]-'A') > q.Options || q.Options+len(lc.letters) > 6 {
				continue // 字母接不上:按普通文本行处理
			}
			q.Options += len(lc.letters)
			if q.Kind == "judge" {
				q.Kind = "choice"
			}
			if lowConfidence(lc.scores) {
				q.Note = joinNote(q.Note, "换行续排选项置信度偏低,请核对选项数")
			}
			st.rects = append(st.rects, optLineRect(lines[i], lc))
			lastBlock = i
		case lineJudgeDC:
			q := PaperQuestion{Kind: "judge", Options: 2, Score: 1, Number: lc.number,
				Note: "按「对/错」字形识别为判断题,请核对题型"}
			q.StemImg = cropStemImg(p.gray, p.bin, st, stemRunRect(lines, lastBlock+1, i-1, lines[i], lc))
			a.Questions = append(a.Questions, q)
			lastOpt = len(a.Questions) - 1
			st.rects = append(st.rects, optLineRect(lines[i], lc))
			lastBlock = i
		}
	}

	subs, subRects, subStems, subNotes := detectSubjectiveBlanks(lines, pxMM, colBin.H, xOff, colW)
	for i := range subs {
		if subStems[i] != nil {
			subs[i].StemImg = cropStemImg(p.gray, p.bin, st, subStems[i])
		}
		a.Subjectives = append(a.Subjectives, subs[i])
	}
	st.rects = append(st.rects, subRects...)
	a.Notes = append(a.Notes, subNotes...)
}

// optLineRect 选项行检测矩形(从首个选项簇到最后一个簇,绝对坐标)。
func optLineRect(ln colLine, lc lineClass) paperRect {
	nu, os := lc.numUsed, lc.optStart
	if nu >= len(ln.clusters) {
		nu = 0
	}
	x0 := ln.clusters[nu].x0
	if os > nu && os < len(ln.clusters) {
		x0 = ln.clusters[os].x0
	}
	return paperRect{ln.xOff + x0, ln.y0, ln.xOff + ln.clusters[len(ln.clusters)-1].x1, ln.y1, 1}
}

// stemRunRect 聚合题目题干区域:上一题块结束到本选项行之间的文字行(取最近 ≤stemMaxLines 行),
// 若选项与题干同行再并入同行区间。无任何题干时返回 nil。坐标为全页绝对系。
func stemRunRect(lines []colLine, lo, hi int, cur colLine, lc lineClass) *image.Rectangle {
	if lo < 0 {
		lo = 0
	}
	if hi >= len(lines) {
		hi = len(lines) - 1
	}
	var r *image.Rectangle
	add := func(x0, y0, x1, y1 int) {
		if r == nil {
			r = &image.Rectangle{Min: image.Point{X: x0, Y: y0}, Max: image.Point{X: x1, Y: y1}}
			return
		}
		if x0 < r.Min.X {
			r.Min.X = x0
		}
		if y0 < r.Min.Y {
			r.Min.Y = y0
		}
		if x1 > r.Max.X {
			r.Max.X = x1
		}
		if y1 > r.Max.Y {
			r.Max.Y = y1
		}
	}
	// 前置文字行(取最近 ≤stemMaxLines 行;首页第一题会带上卷头,由确认页人工裁弃)
	if hi >= lo {
		if hi-lo+1 > stemMaxLines {
			lo = hi - stemMaxLines + 1
		}
		for i := lo; i <= hi; i++ {
			if e := lineExtent(lines[i]); e != nil {
				add(e.Min.X, e.Min.Y, e.Max.X, e.Max.Y)
			}
		}
	}
	// 同行题干:题号簇结束到首个选项簇之间的簇区间
	if lc.optStart > 0 && lc.optStart < len(cur.clusters) {
		x0 := cur.clusters[0].x0
		if lc.numUsed > 0 && lc.numUsed <= len(cur.clusters) {
			x0 = cur.clusters[lc.numUsed-1].x1 + 1
		}
		x1 := cur.clusters[lc.optStart].x0 - 1
		if x1 > x0 {
			add(cur.xOff+x0, cur.y0, cur.xOff+x1, cur.y1)
		}
	}
	return r
}

// lineExtent 文字行内容范围(簇外接框,绝对坐标);无簇返回 nil。
func lineExtent(ln colLine) *image.Rectangle {
	if len(ln.clusters) == 0 {
		return nil
	}
	x0, x1 := ln.clusters[0].x0, ln.clusters[0].x1
	for _, c := range ln.clusters[1:] {
		if c.x0 < x0 {
			x0 = c.x0
		}
		if c.x1 > x1 {
			x1 = c.x1
		}
	}
	return &image.Rectangle{Min: image.Point{X: ln.xOff + x0, Y: ln.y0}, Max: image.Point{X: ln.xOff + x1, Y: ln.y1}}
}

// cropStemImg 从矫正后灰度图裁题干小图;区域内几乎无墨迹(空白区间)时不产出。
func cropStemImg(gray *image.Gray, bin *bitmap, st *pageState, r *image.Rectangle) *image.Gray {
	if r == nil {
		return nil
	}
	binR := r.Intersect(image.Rect(0, 0, bin.W, bin.H))
	if binR.Dx() < 8 || binR.Dy() < 4 {
		return nil
	}
	ink := 0
	for y := binR.Min.Y; y <= binR.Max.Y; y++ {
		row := bin.D[y*bin.W : (y+1)*bin.W]
		for x := binR.Min.X; x <= binR.Max.X; x++ {
			ink += int(row[x])
		}
	}
	if ink < binR.Dx()*binR.Dy()/500 { // <0.2% 墨迹 = 空白区间,不是题干
		return nil
	}
	g := r.Intersect(gray.Rect)
	if g.Dx() < 8 || g.Dy() < 4 {
		return nil
	}
	const pad = 2
	x0, y0 := g.Min.X-pad, g.Min.Y-pad
	x1, y1 := g.Max.X+pad, g.Max.Y+pad
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 >= gray.Rect.Dx() {
		x1 = gray.Rect.Dx() - 1
	}
	if y1 >= gray.Rect.Dy() {
		y1 = gray.Rect.Dy() - 1
	}
	out := image.NewGray(image.Rect(0, 0, x1-x0+1, y1-y0+1))
	for y := y0; y <= y1; y++ {
		copy(out.Pix[(y-y0)*out.Stride:(y-y0)*out.Stride+out.Rect.Dx()],
			gray.Pix[y*gray.Stride+x0:y*gray.Stride+x0+out.Rect.Dx()])
	}
	st.rects = append(st.rects, paperRect{g.Min.X, g.Min.Y, g.Max.X, g.Max.Y, 3})
	return out
}

// auditNumbers 题号连续性审计:跳变(3→7)自动补占位题让漏检显式可见;重复出提示;
// 回退(双栏重新编号)静默重置。无题号(0)的题不参与。
func auditNumbers(a *PaperAnalysis) {
	out := make([]PaperQuestion, 0, len(a.Questions)+8)
	last := 0
	for _, q := range a.Questions {
		if q.Number == 0 {
			out = append(out, q)
			continue
		}
		if last > 0 {
			switch {
			case q.Number == last:
				q.Note = joinNote(q.Note, "题号与上一题重复,疑似误检,请核对")
			case q.Number > last+1:
				gap := q.Number - last - 1
				if gap > auditMaxGap {
					q.Note = joinNote(q.Note, fmt.Sprintf("题号从 %d 跳到 %d,疑似漏检过多或误读,请逐题核对", last, q.Number))
					a.Notes = append(a.Notes, fmt.Sprintf("题号 %d→%d 跳变过大,未自动补题,请人工核对", last, q.Number))
				} else {
					for k := last + 1; k < q.Number; k++ {
						out = append(out, PaperQuestion{Kind: "choice", Options: 4, Score: 1, Number: k,
							Note: fmt.Sprintf("原卷第 %d 题疑似漏检(题号跳变),已补占位——请核对或删除", k)})
					}
					a.Notes = append(a.Notes, fmt.Sprintf("题号 %d→%d 跳变,已补 %d 道占位题,请在确认页逐题核对", last, q.Number, gap))
				}
			}
		}
		last = q.Number
		out = append(out, q)
	}
	a.Questions = out
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + ";" + b
}

// estimateSkew 估计页面倾斜角(-6°~+6°,步长 0.5°):旋转行投影的行间方差最大化。
func estimateSkew(bin *bitmap) float64 {
	ds := bin.W / 700
	if ds < 1 {
		ds = 1
	}
	small := downsample(bin, ds)
	best, bestScore := 0.0, -1.0
	for a := -6.0; a <= 6.01; a += 0.5 {
		if s := rowProjectionVariance(small, a); s > bestScore {
			bestScore, best = s, a
		}
	}
	return best
}

// rowProjectionVariance 把全部墨迹像素按角度旋转后累进行直方图,返回方差(行越「成线」方差越大)。
func rowProjectionVariance(b *bitmap, deg float64) float64 {
	rad := deg * math.Pi / 180
	sin, cos := math.Sin(rad), math.Cos(rad)
	cx, cy := float64(b.W)/2, float64(b.H)/2
	span := b.W + b.H + 4
	off := b.W + 2
	rows := make([]int, span)
	for i, v := range b.D {
		if v != 1 {
			continue
		}
		x, y := float64(i%b.W), float64(i/b.W)
		ry := int(math.Round(-sin*(x-cx)+cos*(y-cy)+cy)) + off
		if ry < 0 || ry >= span {
			continue
		}
		rows[ry]++
	}
	sum, sum2 := 0.0, 0.0
	for _, v := range rows {
		f := float64(v)
		sum += f
		sum2 += f * f
	}
	n := float64(len(rows))
	m := sum / n
	return sum2/n - m*m
}

// rotateGray 绕中心旋转 deg 度(双线性采样,越界补白)。
func rotateGray(g *image.Gray, deg float64) *image.Gray {
	rad := deg * math.Pi / 180
	sin, cos := math.Sin(rad), math.Cos(rad)
	w, h := g.Rect.Dx(), g.Rect.Dy()
	cx, cy := float64(w)/2, float64(h)/2
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			sx := cos*dx + sin*dy + cx
			sy := -sin*dx + cos*dy + cy
			out.Pix[y*w+x] = uint8(SampleBilinear(g, sx, sy) + 0.5)
		}
	}
	return out
}

// resampleGray 双线性重采样到指定尺寸。
func resampleGray(g *image.Gray, w, h int) *image.Gray {
	sw, sh := g.Rect.Dx(), g.Rect.Dy()
	if w <= 0 || h <= 0 || sw <= 0 || sh <= 0 {
		return g
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	sx := float64(sw) / float64(w)
	sy := float64(sh) / float64(h)
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			out.Pix[y*w+x] = uint8(SampleBilinear(g, fx, fy) + 0.5)
		}
	}
	return out
}

// resampleGrayCap 宽度超过 maxW 时等比重采样(统一分析尺度,避免超大照片拖慢后续 O(角度) 步骤)。
func resampleGrayCap(g *image.Gray, maxW int) *image.Gray {
	if g.Rect.Dx() <= maxW {
		return g
	}
	h := int(float64(g.Rect.Dy()) * float64(maxW) / float64(g.Rect.Dx()))
	if h < 300 {
		h = 300
	}
	return resampleGray(g, maxW, h)
}

// cropPaperRegion 照片纸面裁剪:纸面亮、背景暗,在降采样图上按 Otsu 阈值取行列「亮占比」
// 的最长连续区间,裁回纸面;几乎不裁(≥97%)或裁得过小(<300px)时原样返回。
func cropPaperRegion(g *image.Gray) *image.Gray {
	small := resampleGrayCap(g, 1100)
	W, H := small.Rect.Dx(), small.Rect.Dy()
	t := Otsu(small)
	rows := make([]float64, H)
	cols := make([]float64, W)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if small.Pix[y*W+x] > t {
				rows[y]++
				cols[x]++
			}
		}
	}
	// 0.35 而非 0.5:页脚/表头重复排印会让某些列的笔画竖线叠到近半暗(如「本」字
	// 竖笔),阈值过紧会把这些正常内容列误判成背景,裁掉纸面;桌面暗列亮度 ~0.05,
	// 间隔依然足够。
	ry0, ry1, okR := longestRunF(rows, W, 0.35)
	cx0, cx1, okC := longestRunF(cols, H, 0.35)
	if !okR || !okC {
		return g
	}
	sc := float64(g.Rect.Dx()) / float64(W)
	outer := 0.005 // 外扩 0.5%,避免裁进纸面
	x0 := int(float64(cx0)*sc*(1-outer) + 0.5)
	x1 := int(float64(cx1+1)*sc*(1+outer) + 0.5)
	y0 := int(float64(ry0)*sc*(1-outer) + 0.5)
	y1 := int(float64(ry1+1)*sc*(1+outer) + 0.5)
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > g.Rect.Dx() {
		x1 = g.Rect.Dx()
	}
	if y1 > g.Rect.Dy() {
		y1 = g.Rect.Dy()
	}
	if (x1-x0)*(y1-y0)*100 >= g.Rect.Dx()*g.Rect.Dy()*97 {
		return g // 本来就是贴边图,不裁
	}
	if x1-x0 < 300 || y1-y0 < 300 {
		return g
	}
	out := image.NewGray(image.Rect(0, 0, x1-x0, y1-y0))
	for y := 0; y < y1-y0; y++ {
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], g.Pix[(y0+y)*g.Stride+x0:(y0+y)*g.Stride+x1])
	}
	return out
}

// longestRunF 最长连续「均值 ≥th」区间,返回 [首,尾] 与是否存在。total 为归一化分母
// (行序列=行宽,列序列=列高)。
func longestRunF(f []float64, total int, th float64) (int, int, bool) {
	best0, bestLen, cur := 0, 0, -1
	for i, v := range f {
		if v/float64(total) >= th {
			if cur < 0 {
				cur = i
			}
			if i-cur+1 > bestLen {
				bestLen, best0 = i-cur+1, cur
			}
		} else {
			cur = -1
		}
	}
	if bestLen == 0 {
		return 0, 0, false
	}
	return best0, best0 + bestLen - 1, true
}

// cropCols 裁出 [x0,x1] 列范围(含端点)的子位图(逐行拷贝)。
func cropCols(b *bitmap, x0, x1 int) *bitmap {
	w := x1 - x0 + 1
	out := &bitmap{W: w, H: b.H, D: make([]byte, w*b.H)}
	for y := 0; y < b.H; y++ {
		copy(out.D[y*w:(y+1)*w], b.D[y*b.W+x0:y*b.W+x1+1])
	}
	return out
}

// rowRunStarts 提取 x∈[x0,x1) 区域的文字行起点(行墨迹 ≥2% 区域宽)。
func rowRunStarts(b *bitmap, x0, x1 int) []int {
	if x1-x0 < 10 {
		return nil
	}
	th := (x1 - x0) * 2 / 100
	prev := false
	var starts []int
	for y := 0; y < b.H; y++ {
		n := 0
		for x := x0; x < x1; x++ {
			n += int(b.D[y*b.W+x])
		}
		cur := n >= th
		if cur && !prev {
			starts = append(starts, y)
		}
		prev = cur
	}
	return starts
}

// totalInk 统计整图墨迹像素数。
func totalInk(b *bitmap) int {
	n := 0
	for _, v := range b.D {
		n += int(v)
	}
	return n
}

// despeckle 去盐噪声:3×3 邻域内墨迹邻居 ≤1 的孤立墨点清除。笔画像素有 ≥2 个
// 邻居不受影响;照片噪声/压缩椒点(独立 1px)被清掉,否则行投影会被噪声填满。
func despeckle(b *bitmap) {
	if b.W < 3 || b.H < 3 {
		return
	}
	var kill []int
	for y := 1; y < b.H-1; y++ {
		for x := 1; x < b.W-1; x++ {
			i := y*b.W + x
			if b.D[i] != 1 {
				continue
			}
			n := int(b.D[i-1]) + int(b.D[i+1]) + int(b.D[i-b.W]) + int(b.D[i+b.W]) +
				int(b.D[i-b.W-1]) + int(b.D[i-b.W+1]) + int(b.D[i+b.W-1]) + int(b.D[i+b.W+1])
			if n <= 1 {
				kill = append(kill, i)
			}
		}
	}
	for _, i := range kill {
		b.D[i] = 0
	}
}

// segmentLines 水平投影切文字行:行墨迹 ≥2% 页宽为文字,行高 ≥1.6mm,合并 <0.6mm 碎缝。
func segmentLines(b *bitmap, pxMM float64) []paperLine {
	prof := make([]int, b.H)
	for i, v := range b.D {
		if v == 1 {
			prof[i/b.W]++
		}
	}
	th := b.W * 2 / 100
	mergeGap := int(0.6*pxMM) + 1
	minH := int(1.6 * pxMM)
	type run struct{ y0, y1 int }
	var runs []run
	for y := 0; y < b.H; {
		if prof[y] < th {
			y++
			continue
		}
		y0 := y
		for y < b.H && prof[y] >= th {
			y++
		}
		y1 := y - 1
		if len(runs) > 0 && y0-runs[len(runs)-1].y1 <= mergeGap {
			runs[len(runs)-1].y1 = y1
		} else {
			runs = append(runs, run{y0, y1})
		}
	}
	var out []paperLine
	for _, r := range runs {
		if r.y1-r.y0+1 < minH {
			continue
		}
		out = append(out, paperLine(r))
	}
	return out
}

// segmentClusters 行内垂直投影切簇(默认档 1.8mm 间隙)。
func segmentClusters(b *bitmap, ln paperLine, pxMM float64) []paperCluster {
	return segmentClustersGap(b, ln, pxMM, optGapMM)
}

// segmentClustersGap 行内垂直投影切簇:零墨列连续 ≥gapMM 即簇分界(选项块之间 3~10mm,
// 字间/词间距 <1.8mm 保持在同簇);簇内 y 收紧到实际墨迹范围。细档(0.7mm)用于紧排选项行重切。
func segmentClustersGap(b *bitmap, ln paperLine, pxMM float64, gapMM float64) []paperCluster {
	gapW := int(gapMM * pxMM)
	if gapW < 2 {
		gapW = 2
	}
	prof := make([]int, b.W)
	for y := ln.y0; y <= ln.y1; y++ {
		row := b.D[y*b.W : (y+1)*b.W]
		for x, v := range row {
			prof[x] += int(v)
		}
	}
	var out []paperCluster
	for x := 0; x < b.W; {
		if prof[x] == 0 {
			x++
			continue
		}
		x0 := x
		for x < b.W && prof[x] > 0 {
			x++
		}
		x1 := x - 1
		if len(out) > 0 && x0-out[len(out)-1].x1 <= gapW {
			out[len(out)-1].x1 = x1
			continue
		}
		out = append(out, paperCluster{x0: x0, x1: x1, y0: ln.y0, y1: ln.y1})
	}
	for i := range out {
		c := &out[i]
		y0, y1 := c.y1, c.y0
		for y := ln.y0; y <= ln.y1; y++ {
			for x := c.x0; x <= c.x1; x++ {
				if b.D[y*b.W+x] == 1 {
					if y < y0 {
						y0 = y
					}
					if y > y1 {
						y1 = y
					}
				}
			}
		}
		if y0 <= y1 {
			c.y0, c.y1 = y0, y1
		}
	}
	return out
}

const (
	lineText    = iota // 普通文字行(题干/说明/页脚)
	lineNewQ           // 选项行·新题(A 开头)
	lineCont           // 选项行·上题换行续排(B~F 开头)
	lineJudgeDC        // 判断题「对/错」行
)

// lineClass 是一行的分类结果。
type lineClass struct {
	kind     int
	letters  []byte // 选项行:各选项簇首字母
	scores   []float64
	number   int  // 行首题号(0=未识别)
	numUsed  int  // 题号消耗的簇数
	optStart int  // 选项簇起点(全簇下标;>numUsed = 有同行题干)
	tight    bool // 细档重切才识别成功(紧排)
	snapped  bool // 选项字母经序列回贴修正(低置信)
}

// classifyLine 行分类:默认档 → 未识别且首个候选簇以 A 开头时,先细档重切(部分并簇),
// 仍不行再在默认档的首个候选簇内定位选项(整行紧排并成一簇,任何簇级阈值都无法
// 同时分开「选项间」与「选项内」缝隙,只能按字形序列在簇内找)。
func classifyLine(b *bitmap, ln *colLine, pxMM float64) lineClass {
	lc, rem, retry := classifyClusters(b, ln.clusters, float64(ln.pline().h()), false)
	if lc.kind != lineText || !retry {
		return lc
	}
	fine := segmentClustersGap(b, ln.pline(), pxMM, fineGapMM)
	lc2, _, _ := classifyClusters(b, fine, float64(ln.pline().h()), true)
	if lc2.kind != lineText {
		return lc2
	}
	if len(rem) > 0 {
		if letters, scores, ok := intraClusterOptions(b, rem[0]); ok {
			// numUsed/optStart 用默认档口径(与 ln.clusters 同一簇列表,rect 索引才一致)
			return lineClass{kind: lineNewQ, letters: letters, scores: scores,
				number: lc.number, numUsed: lc.numUsed, optStart: lc.numUsed, tight: true}
		}
	}
	return lc2
}

// intraClusterOptions 在紧排并簇的单一簇内定位选项:按 1px 缝取字形框,首框为 A、
// 后续字形按 B,C,D… 连续出现(内容字形跳过)→ 选项数=命中的字母框数。
// 内容字形碎片恰好长得像下一个字母时会提前消耗序列,选项数可能偏少——确认页兜底。
func intraClusterOptions(b *bitmap, c paperCluster) ([]byte, []float64, bool) {
	boxes := glyphBoxesGap(b, c, 1, 32)
	var letters []byte
	var scores []float64
	expect := byte('A')
	for _, bx := range boxes {
		ch, sc := matchChar(b, bx, false)
		if sc >= glyphAccept && ch == expect {
			letters = append(letters, ch)
			scores = append(scores, sc)
			expect++
			if expect > 'F' {
				break
			}
		}
	}
	if len(letters) >= 2 {
		return letters, scores, true
	}
	return nil, nil, false
}

// classifyClusters 对给定簇序列做一轮行分类。
// 第三个返回值=「值得细档重切」:默认档未识别,但首个候选簇字形为 A(紧排选项被并簇的典型形态)。
func classifyClusters(b *bitmap, clusters []paperCluster, lineH float64, tight bool) (lineClass, []paperCluster, bool) {
	num, used := leadingNumber(b, clusters, lineH)
	rem := clusters[used:]
	if len(rem) == 0 {
		return lineClass{kind: lineText, number: num, numUsed: used}, rem, false
	}
	if letters, scores, j, snapped := findOptStart(b, rem); j >= 0 {
		return lineClass{kind: lineNewQ, letters: letters, scores: scores, number: num, numUsed: used,
			optStart: used + j, tight: tight, snapped: snapped}, rem, false
	}
	if letters, scores := leadingLetters(b, rem); len(letters) >= 2 && lettersConsecutive(letters) &&
		letters[0] >= 'B' && letters[0] <= 'F' {
		return lineClass{kind: lineCont, letters: letters, scores: scores, number: num, numUsed: used}, rem, false
	}
	if len(rem) == 2 && duiCuoPair(b, rem[0], rem[1]) {
		return lineClass{kind: lineJudgeDC, number: num, numUsed: used}, rem, false
	}
	g, sc := firstGlyph(b, rem[0])
	return lineClass{kind: lineText, number: num, numUsed: used}, rem, g == 'A' && sc >= glyphAccept
}

// findOptStart 在簇序列中找选项起点:首个字形为 A~F 字母、其后字母构成可修复的
// A 起点连续序列。允许题干簇在选项簇之前(「1.题干…… A.x B.y」同行题干),
// 起点过靠后(>6 簇)不认。小字号字形会误读(如 A→F、B→F),按位序用期望字母的
// 专属模板复核回贴(snapped=true 由调用方出低置信提示);回贴门槛见下。
func findOptStart(b *bitmap, cs []paperCluster) ([]byte, []float64, int, bool) {
	limit := len(cs)
	if limit > 6 {
		limit = 6
	}
	for j := 0; j < limit; j++ {
		g, sc := firstGlyph(b, cs[j])
		if g < 'A' || g > 'F' || sc < glyphAccept {
			continue
		}
		letters, scores := leadingLetters(b, cs[j:])
		if len(letters) >= 2 && lettersConsecutive(letters) {
			if letters[0] == 'A' {
				return letters, scores, j, false // 真正的 A 开头新题
			}
			continue // B~F 开头的连续序列是上题续排,交给续排通道
		}
		// 回贴门槛:多数位次本就正确(真选项行的误读只是个别字形)、最多修 2 位、
		// 期望字母得分接近该簇的最佳分 —— 防止文本行的碎片字形凑出假序列。
		if len(letters) >= 3 && 'A'+len(letters)-1 <= 'F' {
			correct := 0
			for i := range letters {
				if letters[i] == byte('A'+i) {
					correct++
				}
			}
			if correct*2 >= len(letters) {
				ok, repaired := true, 0
				for i := range letters {
					exp := byte('A' + i)
					if letters[i] == exp {
						continue
					}
					repaired++
					if repaired > 2 {
						ok = false
						break
					}
					if s := matchCharOne(b, cs[j+i], exp); s >= glyphAccept && s >= scores[i]-0.08 {
						scores[i] = s
						letters[i] = exp
					} else {
						ok = false
						break
					}
				}
				if ok {
					return letters, scores, j, true
				}
			}
		}
	}
	return nil, nil, -1, false
}

// matchCharOne 单字符匹配:指定模板字符在多档首框 + 宽度档下的最高余弦。
func matchCharOne(b *bitmap, c paperCluster, ch byte) float64 {
	best := 0.0
	for _, box := range firstGlyphHypotheses(b, c) {
		wFull := box.w()
		step := wFull / 12
		if step < 1 {
			step = 1
		}
		for w := wFull; w >= wFull*3/5 && w >= 2; w -= step {
			d := glyphDensity(b, paperCluster{x0: box.x0, x1: box.x0 + w - 1, y0: box.y0, y1: box.y1}, tplN)
			for _, set := range glyphSets() {
				if t, ok := set.chars[ch]; ok {
					if s := cosine(d, t); s > best {
						best = s
					}
				}
			}
		}
	}
	return best
}

// leadingNumber 识别行首题号:窄簇(≤3×行高)约束 + 数字模板匹配;支持一簇内多位
// 字形与多位拆簇("1" "2.")两种排印,最多 3 位。返回题号与消耗的簇数(0=无题号)。
// 单独的字母分数比大小不可靠(「4.」对 A 模板也能到 0.6),窄簇约束才是数字/字母的分界。
func leadingNumber(b *bitmap, cs []paperCluster, lineH float64) (int, int) {
	if len(cs) == 0 || lineH <= 0 {
		return 0, 0
	}
	num, digits, used := 0, 0, 0
	for used < len(cs) && used < 3 && digits < 3 {
		c := cs[used]
		if float64(c.w()) > 3.0*lineH {
			break
		}
		progressed := false
		for _, bx := range glyphBoxesGap(b, c, 1, 3-digits) {
			if digits >= 3 {
				break
			}
			if bx.h()*5 < c.h()*3 {
				continue // 矮框=句点/碎片(真数字占簇高 ≥60%)
			}
			ch, sc := matchChar(b, bx, true)
			if ch < '0' || ch > '9' || sc < digitAccept {
				break
			}
			num = num*10 + int(ch-'0')
			digits++
			progressed = true
		}
		if !progressed {
			break
		}
		used++
	}
	if digits == 0 || num == 0 {
		return 0, 0
	}
	return num, used
}

// leadingLetters 提取每个簇的首字形并做 A~F 模板匹配;任一簇首不是 A~F 则整行不作选项行。
func leadingLetters(b *bitmap, cs []paperCluster) ([]byte, []float64) {
	letters := make([]byte, 0, len(cs))
	scores := make([]float64, 0, len(cs))
	for _, c := range cs {
		g, sc := firstGlyph(b, c)
		if g < 'A' || g > 'F' {
			return nil, nil
		}
		letters = append(letters, g)
		scores = append(scores, sc)
	}
	return letters, scores
}

func lettersConsecutive(letters []byte) bool {
	for i := 1; i < len(letters); i++ {
		if letters[i] != letters[i-1]+1 {
			return false
		}
	}
	return true
}

func lowConfidence(scores []float64) bool {
	for _, s := range scores {
		if s < glyphAccept+0.06 {
			return true
		}
	}
	return false
}

// duiCuoPair 判断「对/错」双簇:首簇字形更像「对」、次簇更像「错」,均过阈且相对占优。
func duiCuoPair(b *bitmap, c0, c1 paperCluster) bool {
	s0d, s0c := duiCuoScores(b, c0)
	s1d, s1c := duiCuoScores(b, c1)
	return s0d >= dcAccept && s0d > s0c && s1c >= dcAccept && s1c > s1d
}

// duiCuoScores 返回簇首字形与「对」「错」模板的最佳余弦(无对错模板时全 0)。
// 与 firstGlyph 同理做多档首框假设。
func duiCuoScores(b *bitmap, c paperCluster) (float64, float64) {
	sd, sc := 0.0, 0.0
	for _, box := range firstGlyphHypotheses(b, c) {
		wFull := box.w()
		step := wFull / 12
		if step < 1 {
			step = 1
		}
		for w := wFull; w >= wFull*3/5 && w >= 2; w -= step {
			d := glyphDensity(b, paperCluster{x0: box.x0, x1: box.x0 + w - 1, y0: box.y0, y1: box.y1}, tplN)
			for _, set := range glyphSets() {
				if t, ok := set.duiCuo['对']; ok {
					if s := cosine(d, t); s > sd {
						sd = s
					}
				}
				if t, ok := set.duiCuo['错']; ok {
					if s := cosine(d, t); s > sc {
						sc = s
					}
				}
			}
		}
	}
	return sd, sc
}

// firstGlyph 取簇内第一个字形并与模板比对:字形内部的细投影缝会把一个字形切成几块
// (如 B 的碗部收窄处),单一缝宽档位的首框可能是碎片 —— 按 2/3/5px 三档缝宽各取首框
// 假设,匹配取各档最高分,正确的字形边界自然胜出。
func firstGlyph(b *bitmap, c paperCluster) (byte, float64) {
	best, bestScore := byte(0), 0.0
	for _, box := range firstGlyphHypotheses(b, c) {
		if ch, sc := matchChar(b, box, false); sc > bestScore {
			bestScore, best = sc, ch
		}
	}
	return best, bestScore
}

// firstGlyphHypotheses 不同缝宽档位下的首个字形框假设(去重)。1px 档必不可少:
// 「A.3」这类 A 与句点仅 1px 缝的簇,2px+ 档全取整簇框,字形无法孤立;而字形内部
// 细缝切碎的风险由「多档取最高分」自愈 —— 正确边界的假设总比碎片假设分高。
func firstGlyphHypotheses(b *bitmap, c paperCluster) []paperCluster {
	var out []paperCluster
	lastX1 := -1
	for _, gapW := range [...]int{1, 2, 3, 5} {
		boxes := glyphBoxesGap(b, c, gapW, 1)
		if len(boxes) == 0 || boxes[0].x1 == lastX1 {
			continue
		}
		lastX1 = boxes[0].x1
		out = append(out, boxes[0])
	}
	return out
}

// glyphBoxesGap 把簇按列投影 ≥gapW 无墨缝拆成字形框(至多 max 个)。缝宽语义:
// 2px 档分「字与字」;1px 档仅供题号细读(配合窄框门限滤句点)。
func glyphBoxesGap(b *bitmap, c paperCluster, gapW, max int) []paperCluster {
	if gapW < 1 {
		gapW = 1
	}
	if max < 1 {
		max = 1
	}
	prof := make([]int, c.w())
	for y := c.y0; y <= c.y1; y++ {
		row := b.D[y*b.W : (y+1)*b.W]
		for x := c.x0; x <= c.x1; x++ {
			prof[x-c.x0] += int(row[x])
		}
	}
	var out []paperCluster
	inInk, start, zeros := false, 0, 0
	for x := 0; x < c.w(); x++ {
		if prof[x] > 0 {
			if !inInk {
				inInk, start = true, x
			}
			zeros = 0
		} else if inInk {
			zeros++
			if zeros >= gapW {
				box := paperCluster{x0: c.x0 + start, x1: c.x0 + x - zeros, y0: c.y0, y1: c.y1}
				tightenBoxY(b, &box)
				out = append(out, box)
				inInk = false
				if len(out) >= max {
					return out
				}
			}
		}
	}
	if inInk {
		box := paperCluster{x0: c.x0 + start, x1: c.x0 + c.w() - 1, y0: c.y0, y1: c.y1}
		tightenBoxY(b, &box)
		out = append(out, box)
	}
	return out
}

// tightenBoxY 把字形框的 y 范围收紧到框内实际墨迹(簇高度带空边会拉低模板余弦)。
func tightenBoxY(b *bitmap, box *paperCluster) {
	y0, y1 := box.y1, box.y0
	for y := box.y0; y <= box.y1; y++ {
		row := b.D[y*b.W : (y+1)*b.W]
		for x := box.x0; x <= box.x1; x++ {
			if row[x] == 1 {
				if y < y0 {
					y0 = y
				}
				if y > y1 {
					y1 = y
				}
			}
		}
	}
	if y0 <= y1 {
		box.y0, box.y1 = y0, y1
	}
}

// matchChar 字形框与 A~F/0~9 模板在所有字体集上比对,取最佳。
// 句点常与字形粘连(「A.」「1.」):在 100%~60% 宽度档位取各模板最佳得分 ——
// 正确字符在某个去点档位上拿高分,带点的整盒只会压低正确字符的分数。
func matchChar(b *bitmap, box paperCluster, digitsOnly bool) (byte, float64) {
	wFull := box.w()
	step := wFull / 12
	if step < 1 {
		step = 1
	}
	best, bestScore := byte(0), 0.0
	for w := wFull; w >= wFull*3/5 && w >= 2; w -= step {
		d := glyphDensity(b, paperCluster{x0: box.x0, x1: box.x0 + w - 1, y0: box.y0, y1: box.y1}, tplN)
		for _, set := range glyphSets() {
			for ch, t := range set.chars {
				if digitsOnly && (ch < '0' || ch > '9') {
					continue
				}
				if s := cosine(d, t); s > bestScore {
					bestScore, best = s, ch
				}
			}
		}
	}
	return best, bestScore
}

// glyphDensity 提取位图区域、按外接框归一化到 N×N 的 0/1 密度向量。
func glyphDensity(b *bitmap, c paperCluster, n int) []float64 {
	w, h := c.w(), c.h()
	if w <= 0 || h <= 0 {
		return nil
	}
	out := make([]float64, n*n)
	for y := 0; y < n; y++ {
		sy := c.y0 + y*h/n
		if sy > c.y1 {
			sy = c.y1
		}
		for x := 0; x < n; x++ {
			sx := c.x0 + x*w/n
			if sx > c.x1 {
				sx = c.x1
			}
			if b.D[sy*b.W+sx] == 1 {
				out[y*n+x] = 1
			}
		}
	}
	return out
}

func cosine(a, b []float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

var (
	tplOnce sync.Once
	tplSets []glyphSet
)

// glyphSet 是一套字形模板:字母数字(16 字符)+ 可选的「对/错」汉字模板。
type glyphSet struct {
	chars  map[byte][]float64
	duiCuo map[rune][]float64
}

// glyphSets 字形模板集(懒构建缓存):本机 Discover 顺序加载至多 3 套互不重复字族的
// 字体(Windows 上典型为 雅黑/黑体/宋体,宋体 serif 与真实试卷最接近)+ 内置 5×7 点阵。
// 匹配时取各集最大余弦:封闭候选集,多集不会引入误判,只提升跨字体命中率。
func glyphSets() []glyphSet {
	tplOnce.Do(func() {
		var sets []glyphSet
		var loaded []string
		for _, p := range font.Discover() {
			if len(sets) >= 3 {
				break
			}
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)))
			dup := false
			for _, l := range loaded {
				if strings.HasPrefix(base, l) || strings.HasPrefix(l, base) {
					dup = true // 同字族不同字重(msyh / msyhl / msyhbd)只留一套
					break
				}
			}
			if dup {
				continue
			}
			f, err := font.LoadFile(p)
			if err != nil {
				continue
			}
			chars := buildCharSet("ABCDEF0123456789", func(cv *raster.Canvas, x, yTop int, s string) {
				f.Draw(cv, x, yTop, s, 44, 0)
			})
			if chars == nil {
				continue
			}
			sets = append(sets, glyphSet{chars: chars, duiCuo: buildDuiCuoSet(f)})
			loaded = append(loaded, base)
		}
		// 内置 5×7 点阵兜底(无系统字体时仍可用;ASCII only,无对错模板)
		if dm := buildCharSet("ABCDEF0123456789", func(cv *raster.Canvas, x, yTop int, s string) {
			cv.Text(x, yTop, s, 6, 0)
		}); dm != nil {
			sets = append(sets, glyphSet{chars: dm})
		}
		tplSets = sets
	})
	return tplSets
}

// glyphSetsUsable 是否有至少一套完整可用的字母数字模板(测试跳过守卫)。
func glyphSetsUsable() bool {
	for _, s := range glyphSets() {
		if len(s.chars) == 16 {
			return true
		}
	}
	return false
}

// buildCharSet 每字符独占一行渲染后按行带切分取框;渲染异常(字符行粘连)返回 nil。
func buildCharSet(chars string, render func(cv *raster.Canvas, x, yTop int, s string)) map[byte][]float64 {
	const size = 44
	bandH := size + 20
	cv := raster.NewCanvas(size+24, bandH*len(chars))
	for i := 0; i < len(chars); i++ {
		render(cv, 4, i*bandH+4, chars[i:i+1])
	}
	bin := canvasBitmap(cv)
	bands := inkBands(bin)
	if len(bands) != len(chars) {
		return nil
	}
	m := make(map[byte][]float64, len(chars))
	for i := 0; i < len(chars); i++ {
		c, ok := bandCluster(bin, bands[i])
		if !ok {
			continue
		}
		m[chars[i]] = glyphDensity(bin, c, tplN)
	}
	if len(m) != len(chars) {
		return nil
	}
	return m
}

// buildDuiCuoSet 用真字体渲染「对/错」模板;无字体或缺字形返回 nil(点阵不支持汉字)。
func buildDuiCuoSet(f *font.Font) map[rune][]float64 {
	if f == nil {
		return nil
	}
	runes := []rune{'对', '错'}
	for _, r := range runes {
		if !f.HasGlyph(r) {
			return nil
		}
	}
	const size = 44
	bandH := size + 20
	cv := raster.NewCanvas(size+24, bandH*len(runes))
	for i, r := range runes {
		f.Draw(cv, 4, i*bandH+4, string(r), size, 0)
	}
	bin := canvasBitmap(cv)
	bands := inkBands(bin)
	if len(bands) != len(runes) {
		return nil
	}
	m := make(map[rune][]float64, len(runes))
	for i, r := range runes {
		c, ok := bandCluster(bin, bands[i])
		if !ok {
			continue
		}
		m[r] = glyphDensity(bin, c, tplN)
	}
	if len(m) != len(runes) {
		return nil
	}
	return m
}

// canvasBitmap 画布转二值位图(墨迹=1)。
func canvasBitmap(cv *raster.Canvas) *bitmap {
	gray := &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
	return Binarize(gray, 160)
}

// inkBands 按行墨迹切行带 [y0,y1]。
func inkBands(b *bitmap) [][2]int {
	rowInk := make([]int, b.H)
	for i, v := range b.D {
		if v == 1 {
			rowInk[i/b.W]++
		}
	}
	var bands [][2]int
	for y := 0; y < b.H; {
		if rowInk[y] == 0 {
			y++
			continue
		}
		y0 := y
		for y < b.H && rowInk[y] > 0 {
			y++
		}
		bands = append(bands, [2]int{y0, y - 1})
	}
	return bands
}

// bandCluster 行带内墨迹外接框。
func bandCluster(b *bitmap, band [2]int) (paperCluster, bool) {
	cx0, cx1 := b.W, -1
	for y := band[0]; y <= band[1]; y++ {
		for x := 0; x < b.W; x++ {
			if b.D[y*b.W+x] == 1 {
				if x < cx0 {
					cx0 = x
				}
				if x > cx1 {
					cx1 = x
				}
			}
		}
	}
	if cx1 < cx0 {
		return paperCluster{}, false
	}
	return paperCluster{x0: cx0, x1: cx1, y0: band[0], y1: band[1]}, true
}

// detectSubjectiveBlanks 行间/页尾空白区 → 主观题草稿(高度扣书写边距,夹取 8~115mm)。
// 行距 <12mm 视为正常段落距离;页尾 ≥25mm 才算(普通下边距 12~20mm)。
// 同时返回每处空白上方紧邻行的题干矩形(供截图)与说明。
func detectSubjectiveBlanks(lines []colLine, pxMM float64, pageH int, xOff, colW int) ([]PaperSubjective, []paperRect, []*image.Rectangle, []string) {
	var subs []PaperSubjective
	var rects []paperRect
	var stems []*image.Rectangle
	var notes []string
	if len(lines) == 0 {
		return subs, rects, stems, notes
	}
	minGap := int(12 * pxMM)
	mkSub := func(y0, y1, above int) {
		hMM := float64(y1-y0+1)/pxMM - 4
		if hMM < 8 {
			return
		}
		note := ""
		if hMM > 115 {
			note = "空白区过高(疑似检测异常),请核对"
			hMM = 115
		}
		subs = append(subs, PaperSubjective{Kind: "subjective", HeightMM: math.Round(hMM*2) / 2, Note: note})
		rects = append(rects, paperRect{xOff, y0, xOff + colW - 1, y1, 2})
		if above >= 0 && above < len(lines) {
			stems = append(stems, lineExtent(lines[above]))
		} else {
			stems = append(stems, nil)
		}
	}
	for i := 1; i < len(lines); i++ {
		if gap := lines[i].y0 - lines[i-1].y1 - 1; gap >= minGap {
			mkSub(lines[i-1].y1+1, lines[i].y0-1, i-1)
		}
	}
	if tail := pageH - 1 - lines[len(lines)-1].y1; tail >= int(25*pxMM) {
		mkSub(lines[len(lines)-1].y1+1, pageH-1, len(lines)-1)
	}
	if len(subs) > 0 {
		notes = append(notes, fmt.Sprintf("检测到 %d 处留白作答区;填空题的下划线会被当作文字行,如有填空请手工补题", len(subs)))
	}
	return subs, rects, stems, notes
}

// renderOverlay 在灰度副本上画检测标注:黑框=客观题选项行、浅灰线=文字行、
// 深灰框=主观区、灰细框=题干区域。
func renderOverlay(gray *image.Gray, rects []paperRect) *image.Gray {
	out := image.NewGray(image.Rect(0, 0, gray.Rect.Dx(), gray.Rect.Dy()))
	copy(out.Pix, gray.Pix)
	W := out.Rect.Dx()
	drawRect := func(r paperRect, v uint8, thick int) {
		for t := 0; t < thick; t++ {
			x0, y0, x1, y1 := r.x0+t, r.y0+t, r.x1-t, r.y1-t
			if x0 > x1 || y0 > y1 {
				return
			}
			for x := x0; x <= x1; x++ {
				out.Pix[y0*W+x] = v
				out.Pix[y1*W+x] = v
			}
			for y := y0; y <= y1; y++ {
				out.Pix[y*W+x0] = v
				out.Pix[y*W+x1] = v
			}
		}
	}
	for _, r := range rects {
		switch r.class {
		case 0:
			drawRect(r, 200, 1)
		case 1:
			drawRect(r, 0, 2)
		case 2:
			drawRect(r, 90, 3)
		case 3:
			drawRect(r, 150, 1)
		}
	}
	return out
}
