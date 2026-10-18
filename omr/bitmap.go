// Package omr 实现高容错光学标记识别(OMR)内核:
// 全局 Otsu 二值化 → 四角实心方块定位(连通域 + 几何先验)→
// 单应变换透视矫正(容忍旋转/透视/拍摄视角)→ 依据与出卡同源的像素几何判读涂卡。
// 纯 Go、零第三方依赖,可嵌入任意 Go 服务或离线工具链。
package omr

import (
	"image"
	"math"
)

// bitmap 是 1-bit 语义的二值图(D[i]=1 表示墨迹)。
type bitmap struct {
	W, H int
	D    []byte
}

// ToGray 将任意图像转为 8-bit 灰度(ITU-R BT.601 亮度加权)。
func ToGray(src image.Image) *image.Gray {
	b := src.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, gr, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			lum := (299*r + 587*gr + 114*bl) / 1000 // 16-bit 域
			g.Pix[y*b.Dx()+x] = uint8(lum >> 8)
		}
	}
	return g
}

// Otsu 计算全局最佳二值化阈值(类间方差最大化)。
func Otsu(g *image.Gray) uint8 {
	var hist [256]int
	for _, p := range g.Pix {
		hist[p]++
	}
	total := len(g.Pix)
	sumAll := 0
	for i, n := range hist {
		sumAll += i * n
	}
	var wB, sumB int
	best, bestVar := 0.0, -1.0
	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF := total - wB
		if wF == 0 {
			break
		}
		sumB += t * hist[t]
		mB := float64(sumB) / float64(wB)
		mF := float64(sumAll-sumB) / float64(wF)
		v := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if v > bestVar {
			bestVar = v
			best = float64(t)
		}
	}
	return uint8(best)
}

// Binarize 按阈值 t 二值化(灰度 <= t 记为墨迹)。
func Binarize(g *image.Gray, t uint8) *bitmap {
	b := &bitmap{W: g.Rect.Dx(), H: g.Rect.Dy(), D: make([]byte, len(g.Pix))}
	for i, p := range g.Pix {
		if p <= t {
			b.D[i] = 1
		}
	}
	return b
}

// AdaptiveBinarize 用积分图实现 Bradley 局部自适应阈值:
// 每像素与其 win×win 邻域均值比较,低于 mean*(1-frac) 记为墨迹。
// 对强阴影、局部污渍、光照渐变远比全局 Otsu 稳健;win<=0 时按图宽自动取奇数窗口。
func AdaptiveBinarize(g *image.Gray, win int, frac float64) *bitmap {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	if win <= 0 {
		win = w / 20
		if win%2 == 0 {
			win++
		}
	}
	if win < 15 {
		win = 15
	}
	if frac <= 0 || frac >= 1 {
		frac = 0.15
	}
	// 积分图(含零行零列前缀),uint64 防 255*像素数 溢出
	integ := make([]uint64, (w+1)*(h+1))
	for y := 0; y < h; y++ {
		rowSum := uint64(0)
		for x := 0; x < w; x++ {
			rowSum += uint64(g.Pix[y*w+x])
			integ[(y+1)*(w+1)+x+1] = integ[y*(w+1)+x+1] + rowSum
		}
	}
	b := &bitmap{W: w, H: h, D: make([]byte, w*h)}
	r := win / 2
	for y := 0; y < h; y++ {
		y0, y1 := max(0, y-r), min(h-1, y+r)
		for x := 0; x < w; x++ {
			x0, x1 := max(0, x-r), min(w-1, x+r)
			area := uint64((x1 - x0 + 1) * (y1 - y0 + 1))
			sum := integ[(y1+1)*(w+1)+x1+1] - integ[y0*(w+1)+x1+1] -
				integ[(y1+1)*(w+1)+x0] + integ[y0*(w+1)+x0]
			mean := float64(sum) / float64(area)
			if float64(g.Pix[y*w+x]) < mean*(1-frac) {
				b.D[y*w+x] = 1
			}
		}
	}
	return b
}

// SampleBilinear 双线性采样;越界处返回 255(白),模拟纸外背景。
func SampleBilinear(g *image.Gray, x, y float64) float64 {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	if x < 0 || y < 0 || x > float64(w-1) || y > float64(h-1) {
		return 255
	}
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := min(x0+1, w-1), min(y0+1, h-1)
	fx, fy := x-float64(x0), y-float64(y0)
	p00 := float64(g.Pix[y0*w+x0])
	p10 := float64(g.Pix[y0*w+x1])
	p01 := float64(g.Pix[y1*w+x0])
	p11 := float64(g.Pix[y1*w+x1])
	return p00*(1-fx)*(1-fy) + p10*fx*(1-fy) + p01*(1-fx)*fy + p11*fx*fy
}

// downsample 按 ds×ds 块降采样(块内任一墨迹即记墨迹,略膨胀、利于定位)。
func downsample(b *bitmap, ds int) *bitmap {
	w := (b.W + ds - 1) / ds
	h := (b.H + ds - 1) / ds
	out := &bitmap{W: w, H: h, D: make([]byte, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			found := false
			for dy := 0; dy < ds && !found; dy++ {
				sy := y*ds + dy
				if sy >= b.H {
					break
				}
				for dx := 0; dx < ds; dx++ {
					sx := x*ds + dx
					if sx >= b.W {
						break
					}
					if b.D[sy*b.W+sx] == 1 {
						found = true
						break
					}
				}
			}
			if found {
				out.D[y*w+x] = 1
			}
		}
	}
	return out
}

// inkRatio 统计以 (cx,cy) 为圆心、半径 r 的圆域内墨迹占比。
func inkRatio(b *bitmap, cx, cy, r int) float64 {
	total, dark := 0, 0
	r2 := r * r
	for dy := -r; dy <= r; dy++ {
		y := cy + dy
		if y < 0 || y >= b.H {
			continue
		}
		for dx := -r; dx <= r; dx++ {
			x := cx + dx
			if x < 0 || x >= b.W {
				continue
			}
			if dx*dx+dy*dy <= r2 {
				total++
				if b.D[y*b.W+x] == 1 {
					dark++
				}
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(dark) / float64(total)
}
