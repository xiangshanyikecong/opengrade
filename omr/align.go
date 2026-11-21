package omr

import (
	"image"
	"math"
	"sort"
)

// 印刷漂移自对齐(改良自 OMRChecker auto_align 思路):
// 单应只建模四个定位角标定义的全局几何,同一版面在打印/复印时的逐行走位
// 与局部缩放在矫正后仍会残留,表现为离定位角标远的题判读抖动。
// 这里"量出来而不是配出来":逐行(题)/列(考号位)检出各泡的印刷圆环实际
// 位置,与理论圆心求残差,行内取中位数,偏移超阈值即把该行采样点整体平移。
//
// 检测用"泡圈连通域"而非环带质心:考号区/题号区印有数字标签、题号、条码,
// 环带(≤1.35r)必然混入这些相邻内容,盲质心会系统性偏移(实测可达 0.5r);
// 而泡圈是从泡位种子出发的连通域,包围盒接近 2r 见方,数字/条码条带/文字
// 由尺寸与长宽比闸门排除。

const (
	alignSnapFrac = 0.15 // 行偏移超过 r×该比例才平移(更小属亚像素抖动,不动)
	alignWarnFrac = 0.80 // 行偏移超过 r×该比例才告警:被测量并修正的整体位移
	                     // (光照不均下角标质心带偏、远端残留数 px)是自对齐的
	                     // 正常工作状态,不应打扰人工;只有偏移大到泡圈闸门都
	                     // 接近失效(≥0.8r)时,修正结果才值得人工留意
	alignMinR     = 6    // 涂点半径小于该值(异常小版面)不做对齐,泡圈过小不可信
)

// estimateRowShift 估计一组涂点的印刷偏移:逐泡定位印刷泡圈连通域,
// 取"实测圆心 − 理论圆心"的行内中位数。可靠样本不足 2 个时返回 false。
func estimateRowShift(bin *bitmap, pts []image.Point, r int) (dx, dy float64, ok bool) {
	if r < alignMinR || len(pts) < 2 {
		return 0, 0, false
	}
	var xs, ys []float64
	for _, p := range pts {
		cx, cy, found := matchBubbleComponent(bin, p, r)
		if !found {
			continue
		}
		xs = append(xs, cx-float64(p.X))
		ys = append(ys, cy-float64(p.Y))
	}
	if len(xs) < 2 {
		return 0, 0, false
	}
	return medianOf(xs), medianOf(ys), true
}

// matchBubbleComponent 在理论泡位附近找"印刷泡"连通域,返回其质心。
// 从距泡位最近的墨迹种子洪泛,三重闸门确认是泡圈:
//   - 包围盒宽高均在 [1.4r, 3.4r](圆环/实心泡口径),长宽比 ≤ 1.6;
//   - 质心距理论位置 ≤ 0.8r;
//   - 连通域未触及探查窗口边缘(触及 = 条码/邻泡粘连等越界大物,不可信)。
//
// 数字标签、题号等小字与泡圈不连通,种子优先命中环线(距泡位 r 处)而
// 不会选到它们;泡圈缺失(漏印/损伤)时找不到合格连通域,返回 false。
func matchBubbleComponent(bin *bitmap, p image.Point, r int) (float64, float64, bool) {
	const win = 3.6 // 探查窗口半径(×r)
	half := int(win * float64(r))
	x0, y0 := p.X-half, p.Y-half
	x1, y1 := p.X+half, p.Y+half

	// 种子:距泡位最近的墨迹像素(环线在 r 处,先于更远的文字被命中)
	seed := image.Point{-1, -1}
	for rad := 1; rad <= int(0.7*float64(r)); rad++ {
		found := false
		for dy := -rad; dy <= rad && !found; dy++ {
			for dx := -rad; dx <= rad; dx++ {
				if max(abs(dx), abs(dy)) != rad { // 只扫当前半径的方环
					continue
				}
				x, y := p.X+dx, p.Y+dy
				if x < 0 || y < 0 || x >= bin.W || y >= bin.H {
					continue
				}
				if bin.D[y*bin.W+x] == 1 {
					seed, found = image.Point{x, y}, true
					break
				}
			}
		}
		if found {
			break
		}
	}
	if seed.X < 0 {
		return 0, 0, false
	}

	// 洪泛连通域(4 连通),窗口内统计包围盒与质心
	visited := make(map[image.Point]bool)
	stack := []image.Point{seed}
	visited[seed] = true
	var sx, sy, n float64
	minX, minY, maxX, maxY := seed.X, seed.Y, seed.X, seed.Y
	touchEdge := false
	for len(stack) > 0 {
		q := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sx += float64(q.X)
		sy += float64(q.Y)
		n++
		if q.X < minX {
			minX = q.X
		}
		if q.X > maxX {
			maxX = q.X
		}
		if q.Y < minY {
			minY = q.Y
		}
		if q.Y > maxY {
			maxY = q.Y
		}
		if q.X <= x0 || q.X >= x1 || q.Y <= y0 || q.Y >= y1 {
			touchEdge = true
		}
		for _, d := range [4]image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := q.X+d.X, q.Y+d.Y
			if nx < x0 || nx >= x1 || ny < y0 || ny >= y1 {
				continue
			}
			np := image.Point{nx, ny}
			if !visited[np] && bin.D[ny*bin.W+nx] == 1 {
				visited[np] = true
				stack = append(stack, np)
			}
		}
	}
	if touchEdge || n < 8 {
		return 0, 0, false
	}
	bw, bh := maxX-minX+1, maxY-minY+1
	if bw < 2*int(1.4*float64(r)/2) { // 占位,下方统一判
		return 0, 0, false
	}
	if float64(bw) < 1.4*float64(r) || float64(bw) > 3.4*float64(r) ||
		float64(bh) < 1.4*float64(r) || float64(bh) > 3.4*float64(r) {
		return 0, 0, false
	}
	if bw > 3*bh || bh > 3*bw { // 长宽比失衡:条码条带/连笔画
		return 0, 0, false
	}
	cx, cy := sx/n, sy/n
	if math.Hypot(cx-float64(p.X), cy-float64(p.Y)) > 0.8*float64(r) {
		return 0, 0, false
	}
	return cx, cy, true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func medianOf(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// shiftPoints 把涂点整体平移 (dx,dy)(四舍五入到整像素)。
func shiftPoints(pts []image.Point, dx, dy float64) []image.Point {
	out := make([]image.Point, len(pts))
	ox, oy := math.Round(dx), math.Round(dy)
	for i, p := range pts {
		out[i] = image.Point{X: p.X + int(ox), Y: p.Y + int(oy)}
	}
	return out
}
