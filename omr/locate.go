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
// 对噪声、断线与轻微形变稳健;失败时返回携带诊断信息的错误。
func findLocators(bin *bitmap, sheet *card.Sheet) ([4]image.Point, error) {
	ds := bin.W / 1240
	if ds < 1 {
		ds = 1
	}
	small := downsample(bin, ds)
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
		if float64(w) < expSide*0.5 || float64(w) > expSide*2.0 ||
			float64(h) < expSide*0.5 || float64(h) > expSide*2.0 {
			continue
		}
		fill := float64(bl.area) / float64(w*h)
		if fill < 0.85 { // 实心方块≈1.0;实心涂卡圆≈π/4≈0.785,以此区分
			continue
		}
		aspect := float64(w) / float64(h)
		if aspect < 0.66 || aspect > 1.5 {
			continue
		}
		q := quadrant(bl.cx(), bl.cy(), small.W, small.H)
		if q < 0 {
			continue
		}
		expX := float64(sheet.Locators[q].X) / float64(ds)
		expY := float64(sheet.Locators[q].Y) / float64(ds)
		d := math.Hypot(bl.cx()-expX, bl.cy()-expY)
		if d > 0.25*float64(small.W) {
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
	for q := 0; q < 4; q++ {
		if !have[q] {
			return [4]image.Point{}, fmt.Errorf(
				"未能在%s象限找到定位角标(需要 %.0fpx 见方的实心方块);请确认图像完整、四角无遮挡", quadName(q), expSide)
		}
	}
	var out [4]image.Point
	for q := 0; q < 4; q++ {
		// 降采样块中心补偿
		out[q] = image.Point{best[q].pt.X + ds/2, best[q].pt.Y + ds/2}
	}
	return out, nil
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
