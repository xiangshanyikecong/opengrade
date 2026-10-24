package omr

import (
	"fmt"
	"image"
	"math"

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

// findLocators 在二值图中定位四个实心方形定位角标,返回其在原图尺度下的中心点。
// 检测在降采样图上进行(连通域 BFS + 实心度/尺寸/象限三重先验),
// 对噪声、断线与轻微形变稳健。针对真实扫描的两种常见偏移问题加固:
//   - 扫描仪黑边/深色边框:先裁掉边缘墨迹密集条带再检测;
//   - 纸张偏移导致定位块贴边甚至被裁掉一角:期望位置容差放宽到 40% 图宽,
//     常规过滤无候选的象限再做「期望位置吸附近」(窗口内取最近的大块墨迹)。
func findLocators(bin *bitmap, sheet *card.Sheet) ([4]image.Point, error) {
	ds := bin.W / 1240
	if ds < 1 {
		ds = 1
	}
	small := downsample(bin, ds)
	ox, oy, small := trimFrame(small)
	blobs := connectedBlobs(small)

	expSide := float64(sheet.LocatorSidePx) / float64(ds)
	type cand struct {
		pt  image.Point
		val float64
	}
	var best [4]cand
	var have [4]bool
	for _, bl := range blobs {
		w, h := bl.w(), bl.h()
		if float64(w) < expSide*0.55 || float64(w) > expSide*2.0 ||
			float64(h) < expSide*0.55 || float64(h) > expSide*2.0 {
			continue
		}
		// 实心度与尺寸双闸门区分定位方块与涂卡圆:涂卡圆直径仅约 0.68×边长,
		// 先被上面的两维尺寸闸门拦住;方块在 JPEG/畸变啃角下实心度可跌到 ~0.84,
		// 阈值取 0.80(仍高于实心涂卡圆的 π/4≈0.785)。
		fill := float64(bl.area) / float64(w*h)
		if fill < 0.80 {
			continue
		}
		aspect := float64(w) / float64(h)
		if aspect < 0.45 || aspect > 2.2 { // 贴边裁切会让方块变扁/变窄,放宽纵横比
			continue
		}
		q := quadrant(bl.cx(), bl.cy(), small.W, small.H)
		if q < 0 {
			continue
		}
		expX := float64(sheet.Locators[q].X) / float64(ds)
		expY := float64(sheet.Locators[q].Y) / float64(ds)
		d := math.Hypot(bl.cx()-expX, bl.cy()-expY)
		if d > 0.40*float64(small.W) { // 纸张大幅平移(送纸偏斜/照片取景偏)容忍
			continue
		}
		if !have[q] || d < best[q].val {
			best[q] = cand{
				pt:  image.Point{int(math.Round(bl.cx())) * ds, int(math.Round(bl.cy())) * ds},
				val: d,
			}
			have[q] = true
		}
	}
	// 吸附近:常规闸门筛掉被裁切的定位块时,在其期望位置附近按"最近的大块墨迹"找回
	for q := 0; q < 4; q++ {
		if have[q] {
			continue
		}
		expX := float64(sheet.Locators[q].X) / float64(ds)
		expY := float64(sheet.Locators[q].Y) / float64(ds)
		win := 0.12 * float64(small.W)
		bestD := win
		for _, bl := range blobs {
			if float64(bl.area) < 0.22*expSide*expSide {
				continue // 至少要有一小块实心墨迹(裁剩的角)
			}
			d := math.Hypot(bl.cx()-expX, bl.cy()-expY)
			if d < bestD {
				bestD = d
				best[q] = cand{pt: image.Point{int(math.Round(bl.cx())) * ds, int(math.Round(bl.cy())) * ds}, val: d}
				have[q] = true
			}
		}
	}
	for q := 0; q < 4; q++ {
		if !have[q] {
			return [4]image.Point{}, fmt.Errorf(
				"未能在%s象限找到定位角标(需 %.0fpx 见方实心方块);定位块可能被扫描边缘裁切或遮挡——请检查扫描件边距后重试", quadName(q), expSide)
		}
	}
	var out [4]image.Point
	for q := 0; q < 4; q++ {
		// 降采样块中心补偿 + 黑边裁剪偏移还原
		out[q] = image.Point{best[q].pt.X + ox*ds + ds/2, best[q].pt.Y + oy*ds + ds/2}
	}
	return out, nil
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
