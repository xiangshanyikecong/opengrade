package omr

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/opengrade/opengrade/card"
)

type blob struct {
	area                   int
	minX, maxX, minY, maxY int
	sumX, sumY             float64
}

func (b blob) w() int      { return b.maxX - b.minX + 1 }
func (b blob) h() int      { return b.maxY - b.minY + 1 }
func (b blob) cx() float64 { return b.sumX / float64(b.area) }
func (b blob) cy() float64 { return b.sumY / float64(b.area) }

// locatorCand 是一个象限的定位候选:pt 为原图尺度坐标,val 为到期望位置的距离(-1=兼容层推算)。
type locatorCand struct {
	pt  image.Point
	val float64
}

// findLocators 在二值图中定位四个实心方形定位角标,返回其在原图尺度下的中心点。
// 检测在降采样图上进行,按兼容层阶梯逐级兜底(见 locate_rescue.go),
// 任一级集齐四角即止,标准扫描始终走第 1 级、行为与历史一致:
//  1. 连通域三重闸门(实心度/尺寸/象限+期望距离);先裁掉扫描仪黑边再检测;
//  2. 比例自校准后重跑闸门(打印缩放/扫描 DPI/照片留白导致标准折算失准时);
//  3. 多尺度实心窗模板匹配(角标粘连、贴边裁切破坏连通域统计时);
//  4. 期望位置吸附近(窗口内取最近的大块墨迹,裁剩的角);
//  5. 相邻角标几何推算(≥2 角在场,推算被完全遮挡/裁没的角)。
func findLocators(bin *bitmap, sheet *card.Sheet) ([4]image.Point, error) {
	ds := bin.W / 1240
	if ds < 1 {
		ds = 1
	}
	small := downsample(bin, ds)
	ox, oy, small := trimFrame(small)
	blobs := connectedBlobs(small)

	var best [4]locatorCand
	var have [4]bool

	std := standardExpect(sheet, ds)
	var cal *locatorExpect
	var banned [][2]float64 // 审计吊销的假角标位置(降采样坐标),重找轮次全员拒收
	runLadder := func() {
		runGates(blobs, small, std, &have, &best, ds, sheet, banned)
		if countMissing(&have) > 0 {
			if cal == nil {
				cal = calibratedExpect(blobs, small, sheet, ds)
			}
			if cal != nil {
				runGates(blobs, small, cal, &have, &best, ds, sheet, banned)
			}
		}
		if countMissing(&have) > 0 {
			rescueTemplate(small, std, &have, &best, ds, sheet, banned)
			if cal != nil {
				rescueTemplate(small, cal, &have, &best, ds, sheet, banned)
			}
		}
		if countMissing(&have) > 0 {
			suckNear(blobs, small, std, &have, &best, ds, sheet, banned)
			if cal != nil {
				suckNear(blobs, small, cal, &have, &best, ds, sheet, banned)
			}
		}
	}
	// 码区审计循环:条码/QR 图形充满实心方块(QR finder 与定位块几乎同构),
	// 任何阶段都可能被它顶替;留一法用其余真实角标回代,判入码区即吊销并重找。
	for round := 0; round < 3; round++ {
		runLadder()
		dropped := auditRegCode(sheet, ds, &have, &best)
		if len(dropped) == 0 {
			break
		}
		banned = append(banned, dropped...)
		cal = nil // 被吊销的假角标可能正是校准锚点,旧校准作废重算
	}
	if countMissing(&have) > 0 {
		synthesizeMissing(sheet, small, ds, &have, &best)
	}

	var miss []string
	for q := 0; q < 4; q++ {
		if !have[q] {
			miss = append(miss, quadName(q))
		}
	}
	if len(miss) > 0 {
		return [4]image.Point{}, fmt.Errorf(
			"未能在%s象限定位角标(已尝试:连通域过滤、比例自校准、多尺度模板匹配、位置吸附、相邻角标推算);定位块可能被扫描边缘裁切、遮挡或与墨迹粘连——请检查扫描件边距后重试",
			strings.Join(miss, "/"))
	}
	var out [4]image.Point
	for q := 0; q < 4; q++ {
		// 降采样块中心补偿 + 黑边裁剪偏移还原
		out[q] = image.Point{best[q].pt.X + ox*ds + ds/2, best[q].pt.Y + oy*ds + ds/2}
	}
	return out, nil
}

func countMissing(have *[4]bool) int {
	n := 0
	for q := 0; q < 4; q++ {
		if !have[q] {
			n++
		}
	}
	return n
}

// auditRegCode 用留一法审计已找到的"角标":对其余已确认角标拟合 图→版面 映射
// (≥3 仿射/2 相似,与缩放无关),把被检点回代版面坐标,落入考号码区(外扩 3mm)
// 即吊销 —— 条码/QR 图形充满实心方块(QR finder 与定位块几乎同构)。
// 返回吊销位置(供黑名单),无吊销返回空。
func auditRegCode(sheet *card.Sheet, ds int, have *[4]bool, best *[4]locatorCand) [][2]float64 {
	if sheet.RegCode == (image.Rectangle{}) {
		return nil
	}
	var dropped [][2]float64
	for q := 0; q < 4; q++ {
		if !have[q] {
			continue
		}
		var others []int
		for i := 0; i < 4; i++ {
			if have[i] && i != q {
				others = append(others, i)
			}
		}
		if len(others) < 2 {
			continue
		}
		fn, ok := fitAnchorMap(sheet, ds, others, best)
		if !ok {
			continue
		}
		p := [2]float64{float64(best[q].pt.X) / float64(ds), float64(best[q].pt.Y) / float64(ds)}
		if inRegCodeSheet(sheet, fn(p)) {
			have[q] = false
			dropped = append(dropped, p)
		}
	}
	return dropped
}

// runGates 按期望几何跑连通域多重闸门,只填当前缺失的象限(不覆盖已找到的):
//   - 尺寸闸门 0.55~2.0×期望边长:拦住大部分涂卡圆(直径≈0.68×边长);
//   - 实心度闸门 0.80:方块在 JPEG/畸变啃角下可跌到 ~0.84,圆仅 π/4≈0.785;
//   - 四角墨迹检查:实心方块四角必黑,涂卡圆/成组涂点四角露白 —— 实心度
//     擦线(噪声把圆顶过 0.80)时的第二道保险;
//   - 纵横比 0.45~2.2:贴边裁切会让方块变扁/变窄;
//   - 码区排除:回代版面坐标落在考号码区(条码/QR)内的一律不认;
//   - 期望距离:标准折算用 0.40×图宽(纸张大幅平移容忍);
//     校准几何用 0.18×图宽(残余不确定性只有旋转/透视,收紧扣死假候选)。
func runGates(blobs []blob, small *bitmap, exp *locatorExpect, have *[4]bool, best *[4]locatorCand, ds int, sheet *card.Sheet, banned [][2]float64) {
	distFrac := 0.40
	if exp.calibrated {
		distFrac = 0.18
	}
	var local [4]locatorCand
	var lh [4]bool
	for _, bl := range blobs {
		w, h := bl.w(), bl.h()
		if float64(w) < exp.side*0.55 || float64(w) > exp.side*2.0 ||
			float64(h) < exp.side*0.55 || float64(h) > exp.side*2.0 {
			continue
		}
		fill := float64(bl.area) / float64(w*h)
		if fill < 0.80 {
			continue
		}
		if !squareCorners(small, bl) {
			continue
		}
		aspect := float64(w) / float64(h)
		if aspect < 0.45 || aspect > 2.2 {
			continue
		}
		q := quadrant(bl.cx(), bl.cy(), small.W, small.H)
		if q < 0 {
			continue
		}
		if isBanned(banned, int(bl.cx()), int(bl.cy()), exp.side) {
			continue
		}
		if inRegCode(sheet, exp, [2]float64{bl.cx(), bl.cy()}) {
			continue
		}
		d := math.Hypot(bl.cx()-exp.pos[q][0], bl.cy()-exp.pos[q][1])
		if d > distFrac*float64(small.W) {
			continue
		}
		if !lh[q] || d < local[q].val {
			local[q] = locatorCand{
				pt:  image.Point{int(math.Round(bl.cx())) * ds, int(math.Round(bl.cy())) * ds},
				val: d,
			}
			lh[q] = true
		}
	}
	for q := 0; q < 4; q++ {
		if !have[q] && lh[q] {
			best[q] = local[q]
			have[q] = true
		}
	}
}

// squareCorners 检查连通域包围盒四角是否为墨(≥3/4 角黑):
// 实心方块的必要特征;涂卡圆与成组涂点四角露白,借此区分。
// 容忍一角缺损(畸变啃角/轻微裁切)。
func squareCorners(b *bitmap, bl blob) bool {
	dark := func(x, y int) bool {
		n := 0
		for dy := 0; dy < 2; dy++ {
			for dx := 0; dx < 2; dx++ {
				xx, yy := x+dx, y+dy
				if xx < b.W && yy < b.H && b.D[yy*b.W+xx] == 1 {
					n++
				}
			}
		}
		return n >= 2
	}
	c := 0
	if dark(bl.minX, bl.minY) {
		c++
	}
	if dark(bl.maxX-1, bl.minY) {
		c++
	}
	if dark(bl.minX, bl.maxY-1) {
		c++
	}
	if dark(bl.maxX-1, bl.maxY-1) {
		c++
	}
	return c >= 3
}

// suckNear 吸附近:各闸门筛掉被裁切的定位块时,在其期望位置附近按"最近的大块墨迹"找回。
func suckNear(blobs []blob, small *bitmap, exp *locatorExpect, have *[4]bool, best *[4]locatorCand, ds int, sheet *card.Sheet, banned [][2]float64) {
	for q := 0; q < 4; q++ {
		if have[q] {
			continue
		}
		win := 0.12 * float64(small.W)
		bestD := win
		for _, bl := range blobs {
			if float64(bl.area) < 0.22*exp.side*exp.side {
				continue // 至少要有一小块实心墨迹(裁剩的角)
			}
			if !squareCorners(small, bl) {
				continue // 裁剩的角也该有黑角;整圆涂点四角露白,不认
			}
			if isBanned(banned, int(bl.cx()), int(bl.cy()), exp.side) {
				continue
			}
			if inRegCode(sheet, exp, [2]float64{bl.cx(), bl.cy()}) {
				continue // 码区图形(条码/QR)不得充当定位块
			}
			d := math.Hypot(bl.cx()-exp.pos[q][0], bl.cy()-exp.pos[q][1])
			if d < bestD {
				bestD = d
				best[q] = locatorCand{
					pt:  image.Point{int(math.Round(bl.cx())) * ds, int(math.Round(bl.cy())) * ds},
					val: d,
				}
				have[q] = true
			}
		}
	}
}

// trimFrame 裁掉二值图四周的墨迹密集条带(扫描仪黑边/复印深色边框),
// 返回裁剪偏移与裁剪后的图。扫描黑边会连成巨大连通域,既污染检测
// 又常把贴边的定位块吞进去,必须先剥离。
func trimFrame(b *bitmap) (int, int, *bitmap) {
	const dense = 0.5   // 边缘行/列墨迹占比超过此值视为边框
	const maxCut = 0.08 // 每边最多裁 8% 边长
	cutL, cutR, cutT, cutB := 0, 0, 0, 0
	rowInk := func(y int) float64 {
		n := 0
		for x := 0; x < b.W; x++ {
			n += int(b.D[y*b.W+x])
		}
		return float64(n) / float64(b.W)
	}
	colInk := func(x int) float64 {
		n := 0
		for y := 0; y < b.H; y++ {
			n += int(b.D[y*b.W+x])
		}
		return float64(n) / float64(b.H)
	}
	for cutT < int(maxCut*float64(b.H)) && rowInk(cutT) > dense {
		cutT++
	}
	for cutB < int(maxCut*float64(b.H)) && rowInk(b.H-1-cutB) > dense {
		cutB++
	}
	for cutL < int(maxCut*float64(b.W)) && colInk(cutL) > dense {
		cutL++
	}
	for cutR < int(maxCut*float64(b.W)) && colInk(b.W-1-cutR) > dense {
		cutR++
	}
	if cutL+cutR >= b.W || cutT+cutB >= b.H {
		return 0, 0, b
	}
	if cutL+cutR+cutT+cutB == 0 {
		return 0, 0, b
	}
	out := &bitmap{W: b.W - cutL - cutR, H: b.H - cutT - cutB, D: make([]byte, 0, (b.W-cutL-cutR)*(b.H-cutT-cutB))}
	for y := cutT; y < b.H-cutB; y++ {
		out.D = append(out.D, b.D[y*b.W+cutL:y*b.W+b.W-cutR]...)
	}
	return cutL, cutT, out
}

// connectedBlobs 对二值图做 4-连通域标记,返回全部连通域的统计量。
func connectedBlobs(b *bitmap) []blob {
	visited := make([]bool, len(b.D))
	var blobs []blob
	queue := make([]int, 0, 1024)
	for start := 0; start < len(b.D); start++ {
		if b.D[start] == 0 || visited[start] {
			continue
		}
		visited[start] = true
		queue = queue[:0]
		queue = append(queue, start)
		var cur blob
		cur.minX, cur.maxX = b.W, -1
		cur.minY, cur.maxY = b.H, -1
		for head := 0; head < len(queue); head++ {
			idx := queue[head]
			x, y := idx%b.W, idx/b.W
			cur.area++
			cur.sumX += float64(x)
			cur.sumY += float64(y)
			if x < cur.minX {
				cur.minX = x
			}
			if x > cur.maxX {
				cur.maxX = x
			}
			if y < cur.minY {
				cur.minY = y
			}
			if y > cur.maxY {
				cur.maxY = y
			}
			for _, n := range [4]int{idx - 1, idx + 1, idx - b.W, idx + b.W} {
				if n < 0 || n >= len(b.D) {
					continue
				}
				// 行边界防串:左右邻居必须同行
				if (n == idx-1 || n == idx+1) && n/b.W != idx/b.W {
					continue
				}
				if b.D[n] == 1 && !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
		}
		blobs = append(blobs, cur)
	}
	return blobs
}

// quadrant 返回点所属象限:0=TL 1=TR 2=BL 3=BR;-1 表示靠近中线(不归属)。
func quadrant(x, y float64, w, h int) int {
	mx, my := float64(w)/2, float64(h)/2
	if math.Abs(x-mx) < 0.05*float64(w) || math.Abs(y-my) < 0.05*float64(h) {
		return -1
	}
	if x < mx && y < my {
		return 0
	}
	if x >= mx && y < my {
		return 1
	}
	if x < mx && y >= my {
		return 2
	}
	return 3
}

func quadName(q int) string { return [4]string{"左上", "右上", "左下", "右下"}[q] }
