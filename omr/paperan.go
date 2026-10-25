package omr

// 试卷版面分析(半自动出卡第一步):上传一页试卷照片/扫描件,检测「题目构成」——
// 客观题(题型/选项数)与主观题(留白区高度),产出答题卡版式草稿 + 叠加标注图,
// 由教师在设计器确认页人工微调后生成答题卡。纯 Go、零第三方依赖。
//
// 检测原理(无 OCR,封闭候选集模板匹配):
//   - 自适应二值化 → 倾斜角估计(投影方差最大化,±5°)→ 水平投影切文字行 →
//     行内垂直投影按间隙切字符簇;
//   - 选项行:2~6 个等高簇、簇首字形与 A~F 模板逐一比对(归一化余弦),
//     字母连续递增;首字母 A = 新题(选项数=簇数,2 簇=判断),B~F 开头 = 上题换行续排;
//   - 行间大段低墨迹竖向空白 = 主观题作答区,高度按 A4 宽度假定折算 mm。
//
// 边界(写进 UI 提示):A4 单栏假定、倾斜 ≤±5°、印刷体;判断题请印 A/B 选项
// (印「对/错」或 √/× 不识别);填空题的下划线是墨迹,会被当文字行,需手工补;
// 多选与单选结构相同,默认按单选导入。所有误检由确认页人工兜底。

import (
	"fmt"
	"image"
	"math"
	"sync"

	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/raster"
)

// PaperQuestion 是检测出的客观题草稿(题型/选项数;分值卷面不可靠,恒 1 待人工改)。
type PaperQuestion struct {
	Kind    string  `json:"kind"`           // choice | judge
	Options int     `json:"options"`        // 选项数(判断恒 2)
	Score   float64 `json:"score"`          // 恒 1,设计器确认页批量改
	Note    string  `json:"note,omitempty"` // 该题的低置信说明
}

// PaperSubjective 是检测出的主观题留白区草稿。
type PaperSubjective struct {
	Kind     string  `json:"kind"`           // subjective
	HeightMM float64 `json:"heightMM"`       // 留白高度(已扣书写边距)
	Note     string  `json:"note,omitempty"` // 低置信说明
}

// PaperAnalysis 是一页试卷的检测草稿。
type PaperAnalysis struct {
	Questions   []PaperQuestion   `json:"questions"`
	Subjectives []PaperSubjective `json:"subjectives"`
	Notes       []string          `json:"notes"`
	SkewDeg     float64           `json:"skewDeg"` // 估计出的页面倾斜角(已反向矫正)
}

// PaperResult 附带叠加标注图(灰度:黑框=客观题选项行、浅灰线=文字行、深灰框=主观区)。
type PaperResult struct {
	Analysis *PaperAnalysis
	Overlay  *image.Gray
}

// 分析结果矩形(叠加图用):0 文字行 1 客观题选项行 2 主观区。
type paperRect struct {
	x0, y0, x1, y1 int
	class          int
}

type paperCluster struct{ x0, x1, y0, y1 int }

func (c paperCluster) w() int { return c.x1 - c.x0 + 1 }
func (c paperCluster) h() int { return c.y1 - c.y0 + 1 }

type paperLine struct {
	y0, y1   int
	clusters []paperCluster
}

const (
	glyphAccept = 0.60 // 字形余弦相似度接受阈(封闭候选集,宁低勿漏:确认页兜底)
	tplN        = 24   // 字形模板归一化边长
)

// AnalyzePaper 分析一页试卷(灰度图)。A4 宽度假定:pxPerMM = 图宽 / 210。
func AnalyzePaper(gray *image.Gray) (*PaperResult, error) {
	if gray == nil || gray.Rect.Dx() < 300 || gray.Rect.Dy() < 300 {
		return nil, fmt.Errorf("图像太小(宽高均需 ≥300px)")
	}
	a := &PaperAnalysis{Questions: []PaperQuestion{}, Subjectives: []PaperSubjective{}, Notes: []string{}}

	// 全黑/反色防御:纸面平均灰度过低说明不是正常白纸(黑版/底片/镜头盖),
	// 自适应二值化在黑纸+白角的边界会造出贯穿整页的假墨带,后续全是无意义输出
	var graySum int64
	for _, p := range gray.Pix {
		graySum += int64(p)
	}
	if m := float64(graySum) / float64(len(gray.Pix)); m < 80 {
		return nil, fmt.Errorf("图像平均灰度 %.0f,疑似全黑页或反色图,请上传正常纸面扫描件", m)
	}

	// 1. 裁扫描黑边 → 倾斜估计 → 反向矫正(>0.2° 才重采样,避免无谓模糊)
	bin := AdaptiveBinarize(gray, 0, 0.18)
	_, _, bin = trimFrame(bin)
	despeckle(bin)
	bin0 := bin // 旋转前二值图(双栏检查备用:伪倾斜矫正会把竖直栏缝抹平,见下)
	skew := estimateSkew(bin)
	a.SkewDeg = skew
	if math.Abs(skew) > 5 {
		a.Notes = append(a.Notes, fmt.Sprintf("页面倾斜约 %.1f°,超过 5° 检测上限,结果可能不准——请尽量摆正后重扫", skew))
	}
	if math.Abs(skew) >= 0.2 {
		gray = rotateGray(gray, -skew) // 反向旋转归正(estimateSkew 与 rotateGray 同号约定,归正取负)
	}
	bin = AdaptiveBinarize(gray, 0, 0.18)
	_, _, bin = trimFrame(bin)
	despeckle(bin)
	W := bin.W
	pxMM := float64(W) / 210.0 // A4 假定:整幅图宽 ≈ 210mm(贴边扫描/裁到纸边时成立)
	// 全黑/反色防御:墨迹占比过半说明不是正常纸面(黑页/底片),后续全是无意义输出
	if ink := totalInk(bin); ink > bin.W*bin.H/2 {
		return nil, fmt.Errorf("图像墨迹占比 %.0f%%,疑似全黑页或反色图,请上传正常纸面扫描件", 100*float64(ink)/float64(bin.W*bin.H))
	}

	// 2. 双栏护栏:中缝出现全高无墨的竖向空带(两侧都有内容)= 双栏排版,单栏假定失效。
	// 旋转前后各查一次:双栏版式会让倾斜估计出伪峰(两栏行网格互不相关),伪矫正
	// 反而把竖直栏缝抹平 —— 旋转前的图上栏缝仍是干净的竖带。
	if twoColumnLikely(bin0) || twoColumnLikely(bin) {
		return nil, fmt.Errorf("疑似双栏/分栏排版,暂不支持——请使用单栏 A4 试卷,或逐栏裁剪后分次识别")
	}

	// 3. 水平投影切文字行 → 行内切簇
	lines, lineRects := segmentLines(bin, pxMM)
	if len(lines) == 0 {
		return nil, fmt.Errorf("未检测到文字行:请确认上传的是试卷页面(印刷体、光照均匀)")
	}
	rects := make([]paperRect, 0, len(lines)+16)
	rects = append(rects, lineRects...)
	for i := range lines {
		lines[i].clusters = segmentClusters(bin, lines[i], pxMM)
	}

	// 4. 行分类:选项行(新题/续排)与普通行
	var qs []PaperQuestion
	lastOpt := -1 // 最后一个客观题下标(续排行归属)
	suspectGlyph := false
	for _, ln := range lines {
		start := 0
		if _, used := leadingNumber(bin, ln.clusters, float64(ln.y1-ln.y0+1)); used > 0 {
			start = used // 跳过行首题号簇
		}
		letters, scores := leadingLetters(bin, ln.clusters[start:])
		if len(letters) < 2 || !lettersConsecutive(letters) {
			continue
		}
		if letters[0] == 'A' {
			kind := "choice"
			if len(letters) == 2 {
				kind = "judge" // 2 选项 = 判断(印「对/错」的卷面不识别,见 UI 提示)
			}
			qs = append(qs, PaperQuestion{Kind: kind, Options: len(letters), Score: 1})
			lastOpt = len(qs) - 1
			rects = append(rects, paperRect{ln.clusters[start].x0, ln.y0, ln.clusters[len(ln.clusters)-1].x1, ln.y1, 1})
		} else if lastOpt >= 0 && int(letters[0]-'A') <= qs[lastOpt].Options && qs[lastOpt].Options+len(letters) <= 6 {
			// B~F 开头:上一题选项换行续排(A/B 在第一行,C/D 在第二行)
			qs[lastOpt].Options += len(letters)
			if qs[lastOpt].Kind == "judge" {
				qs[lastOpt].Kind = "choice"
			}
			rects = append(rects, paperRect{ln.clusters[start].x0, ln.y0, ln.clusters[len(ln.clusters)-1].x1, ln.y1, 1})
		}
		suspectGlyph = suspectGlyph || lowConfidence(scores)
	}
	if suspectGlyph {
		a.Notes = append(a.Notes, "部分选项字母置信度偏低,选项数可能有误——请在表单里逐题核对")
	}
	a.Questions = qs

	// 5. 行间/页尾大段空白 = 主观题作答区(填空的下划线是墨迹,不会误判成空白)
	subs, subRects, subNotes := detectSubjectiveBlanks(lines, pxMM, bin.H)
	a.Subjectives = subs
	a.Notes = append(a.Notes, subNotes...)
	rects = append(rects, subRects...)

	// 6. 汇总提示
	if len(qs) == 0 {
		a.Notes = append(a.Notes, "未检测到客观题选项行(如判断题印的是「对/错」请改印 A/B,或手工补题)")
	} else {
		a.Notes = append(a.Notes, "多选与单选结构相同,已全部按单选导入——多选题请在表单中改题型;分值统一按 1 分导入,请批量修改")
	}

	return &PaperResult{Analysis: a, Overlay: renderOverlay(gray, rects)}, nil
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

// twoColumnLikely 双栏判定:中线附近存在 ≥3mm 宽的全高无墨竖带、两侧都有文字行,
// 且两侧行网格相互独立(行数差 ≥2 或行起点错位 >2mm)—— 真分栏的内容流彼此独立;
// 单栏试卷里对齐的选项间隙两侧行一一对齐,不算栏。
func twoColumnLikely(b *bitmap) bool {
	mm := float64(b.W) / 210.0
	colInk := make([]int, b.W)
	for i, v := range b.D {
		if v == 1 {
			colInk[i%b.W]++
		}
	}
	mid := b.W / 2
	best, run, bestX := 0, 0, mid
	for x := mid - int(30*mm); x <= mid+int(30*mm); x++ {
		if x < 0 || x >= b.W {
			continue
		}
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
		return false
	}
	leftRows := rowRunStarts(b, 0, bestX)
	rightRows := rowRunStarts(b, bestX+best, b.W)
	if len(leftRows) < 3 || len(rightRows) < 3 {
		return false
	}
	// 行网格配对:起点错位 ≤2mm 视为同一行。任一侧 ≥70% 的行能在另一侧配对成功
	// = 单栏试卷(短行/标题/题干只出现在间隙一侧很正常);两侧行网格都大量对不上
	// = 真分栏(两栏内容流彼此独立)。
	matchedL, matchedR := 0, 0
	tol := int(2 * mm)
	for i, j := 0, 0; i < len(leftRows) && j < len(rightRows); {
		switch d := leftRows[i] - rightRows[j]; {
		case d <= tol && d >= -tol:
			matchedL++
			matchedR++
			i++
			j++
		case d < 0:
			i++
		default:
			j++
		}
	}
	if matchedL*4 >= 3*len(leftRows) || matchedR*4 >= 3*len(rightRows) {
		return false
	}
	return true
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

func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
func segmentLines(b *bitmap, pxMM float64) ([]paperLine, []paperRect) {
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
	var rects []paperRect
	for _, r := range runs {
		if r.y1-r.y0+1 < minH {
			continue
		}
		out = append(out, paperLine{y0: r.y0, y1: r.y1})
		rects = append(rects, paperRect{0, r.y0, b.W - 1, r.y1, 0})
	}
	return out, rects
}

// segmentClusters 行内垂直投影切簇:零墨列连续 ≥1.8mm 即簇分界(选项块之间 3~10mm,
// 字间/词间距 <1.8mm 保持在同簇);簇内 y 收紧到实际墨迹范围。
func segmentClusters(b *bitmap, ln paperLine, pxMM float64) []paperCluster {
	gapW := int(1.8 * pxMM)
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

// leadingNumber 识别行首题号簇:必须窄(≤3×行高,题号 1~2 位数字),且首字形在
// 数字模板下得分达标 —— 单独的字母分数比大小不可靠(「4.」对 A 模板也能到 0.6),
// 窄簇约束才是数字/字母的分界。返回题号与消耗的簇数(0=无题号)。
func leadingNumber(b *bitmap, cs []paperCluster, lineH float64) (int, int) {
	if len(cs) == 0 || lineH <= 0 {
		return 0, 0
	}
	c := cs[0]
	if float64(c.w()) > 3.0*lineH {
		return 0, 0
	}
	g, sc := firstGlyph(b, c)
	if g < '0' || g > '9' || sc < glyphAccept {
		return 0, 0
	}
	num := int(g - '0')
	used := 1
	if len(cs) > 1 { // 两位数拆成两簇的情况("1" "2.")
		c2 := cs[1]
		if float64(c2.w()) <= 3.0*lineH {
			if g2, sc2 := firstGlyph(b, c2); g2 >= '0' && g2 <= '9' && sc2 >= glyphAccept {
				num = num*10 + int(g2-'0')
				used = 2
			}
		}
	}
	if num == 0 {
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

// firstGlyph 取簇内第一个字形(列投影按 ≥0.25mm 无墨缝分字)并与模板比对。
func firstGlyph(b *bitmap, c paperCluster) (byte, float64) {
	gapW := int(0.25 * float64(b.W) / 210.0)
	if gapW < 2 {
		gapW = 2
	}
	prof := make([]int, c.w())
	for y := c.y0; y <= c.y1; y++ {
		row := b.D[y*b.W : (y+1)*b.W]
		for x := c.x0; x <= c.x1; x++ {
			prof[x-c.x0] += int(row[x])
		}
	}
	gx0 := -1
	for x := 0; x < c.w(); x++ {
		if prof[x] > 0 {
			gx0 = c.x0 + x
			break
		}
	}
	if gx0 < 0 {
		return 0, 0
	}
	zeros, gx1 := 0, c.x1
	for x := gx0 - c.x0; x < c.w(); x++ {
		if prof[x] == 0 {
			zeros++
			if zeros >= gapW {
				gx1 = c.x0 + x - zeros
				break
			}
		} else {
			zeros = 0
		}
	}
	if gx1 < gx0 {
		gx1 = gx0
	}
	// 句点常与字母粘连(「A.」的点):在 100%~60% 宽度档位取各模板最佳得分 ——
	// 正确字母在某个去点档位上拿高分,带点的整盒只会压低正确字母的分数。
	wFull := gx1 - gx0 + 1
	step := wFull / 12
	if step < 1 {
		step = 1
	}
	best, bestScore := byte(0), 0.0
	for w := wFull; w >= wFull*3/5 && w >= 2; w -= step {
		d := glyphDensity(b, paperCluster{x0: gx0, x1: gx0 + w - 1, y0: c.y0, y1: c.y1}, tplN)
		for ch, t := range glyphTemplates() {
			if s := cosine(d, t); s > bestScore {
				bestScore, best = s, ch
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
	tplOnce   sync.Once
	tplMap    map[byte][]float64
	tplUsable bool
)

// glyphTemplates 渲染 A~F、0~9 字形模板(font.Default;无系统字体退化为内置 5x7 点阵,
// 合成靶场同样走该路径渲染 → 模板与靶图自洽)。每字符独占一行渲染,按行带切分取框。
func glyphTemplates() map[byte][]float64 {
	tplOnce.Do(func() {
		chars := "ABCDEF0123456789"
		size := 44
		bandH := size + 20
		cv := raster.NewCanvas(size+24, bandH*len(chars))
		f := font.Default()
		for i, ch := range chars {
			if f != nil {
				f.Draw(cv, 4, i*bandH+4, string(ch), size, 0)
			} else {
				cv.Text(4, i*bandH+4, string(ch), 6, 0) // 内置 5x7 点阵,scale 6 = 30px 高
			}
		}
		gray := &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
		bin := Binarize(gray, 160)
		rowInk := make([]int, bin.H)
		for i, v := range bin.D {
			if v == 1 {
				rowInk[i/bin.W]++
			}
		}
		var bands [][2]int
		for y := 0; y < bin.H; {
			if rowInk[y] == 0 {
				y++
				continue
			}
			y0 := y
			for y < bin.H && rowInk[y] > 0 {
				y++
			}
			bands = append(bands, [2]int{y0, y - 1})
		}
		if len(bands) != len(chars) {
			return // 渲染异常(字符行粘连):模板不可用,匹配恒低分,行按普通文本处理
		}
		tplMap = make(map[byte][]float64, len(chars))
		for i, ch := range chars {
			cx0, cx1 := bin.W, -1
			for y := bands[i][0]; y <= bands[i][1]; y++ {
				for x := 0; x < bin.W; x++ {
					if bin.D[y*bin.W+x] == 1 {
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
				continue
			}
			tplMap[byte(ch)] = glyphDensity(bin, paperCluster{x0: cx0, x1: cx1, y0: bands[i][0], y1: bands[i][1]}, tplN)
			tplUsable = true
		}
	})
	if !tplUsable {
		return nil
	}
	return tplMap
}

// detectSubjectiveBlanks 行间/页尾空白区 → 主观题草稿(高度扣书写边距,夹取 8~115mm)。
// 行距 <12mm 视为正常段落距离;页尾 ≥25mm 才算(普通下边距 12~20mm)。
func detectSubjectiveBlanks(lines []paperLine, pxMM float64, pageH int) ([]PaperSubjective, []paperRect, []string) {
	var subs []PaperSubjective
	var rects []paperRect
	var notes []string
	if len(lines) == 0 {
		return subs, rects, notes
	}
	minGap := int(12 * pxMM)
	pageW := int(210 * pxMM)
	mkSub := func(y0, y1 int) {
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
		rects = append(rects, paperRect{0, y0, pageW - 1, y1, 2})
	}
	for i := 1; i < len(lines); i++ {
		if gap := lines[i].y0 - lines[i-1].y1 - 1; gap >= minGap {
			mkSub(lines[i-1].y1+1, lines[i].y0-1)
		}
	}
	if tail := pageH - 1 - lines[len(lines)-1].y1; tail >= int(25*pxMM) {
		mkSub(lines[len(lines)-1].y1+1, pageH-1)
	}
	if len(subs) > 0 {
		notes = append(notes, fmt.Sprintf("检测到 %d 处留白作答区;填空题的下划线会被当作文字行,如有填空请手工补题", len(subs)))
	}
	return subs, rects, notes
}

// renderOverlay 在灰度副本上画检测标注:黑框=客观题选项行、浅灰线=文字行、深灰框=主观区。
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
		}
	}
	return out
}
