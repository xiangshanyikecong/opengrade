// 灰度预处理原语(判读前可选链:中值去噪 → 伽马 → CLAHE 增对比)。
// 全部纯 Go 实现,输入输出均为 *image.Gray;不满足启用条件的输入原样返回
// (共享底层数组,调用方不得就地修改)。
package raster

import (
	"image"
	"math"
	"sort"
)

// GammaGray 返回伽马校正副本:v' = 255·(v/255)^γ。
// γ<1 提亮阴影(褪色/阴影件的涂点增墨),γ>1 压暗高光;γ<=0 或 ==1 原样返回。
func GammaGray(g *image.Gray, gamma float64) *image.Gray {
	if gamma <= 0 || gamma == 1 {
		return g
	}
	lut := make([]uint8, 256)
	for i := range lut {
		lut[i] = uint8(255*math.Pow(float64(i)/255, gamma) + 0.5)
	}
	return mapGray(g, lut)
}

// MedianGray 返回 k×k 中值滤波副本(支持 3/5):去椒盐噪声、保留涂点边缘;
// 其它 k 原样返回。边界按钳制取样。
func MedianGray(g *image.Gray, k int) *image.Gray {
	if k != 3 && k != 5 {
		return g
	}
	w, h := g.Rect.Dx(), g.Rect.Dy()
	out := image.NewGray(image.Rect(0, 0, w, h))
	half := k / 2
	win := make([]uint8, 0, k*k)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			win = win[:0]
			for dy := -half; dy <= half; dy++ {
				yy := clampInt(y+dy, 0, h-1)
				for dx := -half; dx <= half; dx++ {
					xx := clampInt(x+dx, 0, w-1)
					win = append(win, g.Pix[yy*w+xx])
				}
			}
			sort.Slice(win, func(i, j int) bool { return win[i] < win[j] })
			out.Pix[y*w+x] = win[len(win)/2]
		}
	}
	return out
}

// CLAHEGray 返回限制对比度自适应直方图均衡副本:tiles×tiles 分块均衡,
// 每块按 clip 比例截断直方图尖峰后重分布,块间映射按像素位置双线性插值防块效应。
// 用于弱光/低对比/复印件在判读前增墨。tiles<=0 取 8;clip 取 0~1(0 取缺省 0.02)。
func CLAHEGray(g *image.Gray, tiles int, clip float64) *image.Gray {
	if tiles <= 0 {
		tiles = 8
	}
	if tiles > 32 {
		tiles = 32
	}
	if clip <= 0 || clip >= 1 {
		clip = 0.02
	}
	w, h := g.Rect.Dx(), g.Rect.Dy()
	xs := tileBounds(w, tiles)
	ys := tileBounds(h, tiles)

	// 逐块均衡映射表
	luts := make([][256]uint8, tiles*tiles)
	for ty := 0; ty < tiles; ty++ {
		for tx := 0; tx < tiles; tx++ {
			luts[ty*tiles+tx] = tileLUT(g, xs[tx], xs[tx+1], ys[ty], ys[ty+1], clip)
		}
	}

	// 块中心坐标(像素位置对 4 个邻近块中心的映射做双线性插值)
	cxs := make([]float64, tiles)
	cys := make([]float64, tiles)
	for t := 0; t < tiles; t++ {
		cxs[t] = float64(xs[t]+xs[t+1]) / 2
		cys[t] = float64(ys[t]+ys[t+1]) / 2
	}
	interp := func(cs []float64, v float64) (i0, i1 int, t float64) {
		if v <= cs[0] {
			return 0, 0, 0
		}
		if v >= cs[len(cs)-1] {
			return len(cs) - 1, len(cs) - 1, 0
		}
		for k := 0; k+1 < len(cs); k++ {
			if v < cs[k+1] {
				return k, k + 1, (v - cs[k]) / (cs[k+1] - cs[k])
			}
		}
		return len(cs) - 1, len(cs) - 1, 0
	}

	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		j0, j1, sy := interp(cys, float64(y)+0.5)
		base := y * w
		for x := 0; x < w; x++ {
			i0, i1, sx := interp(cxs, float64(x)+0.5)
			v := g.Pix[base+x]
			a := float64(luts[j0*tiles+i0][v])*(1-sx) + float64(luts[j0*tiles+i1][v])*sx
			b := float64(luts[j1*tiles+i0][v])*(1-sx) + float64(luts[j1*tiles+i1][v])*sx
			out.Pix[base+x] = uint8(a*(1-sy) + b*sy + 0.5)
		}
	}
	return out
}

// tileLUT 对一块区域做限对比直方图均衡,返回 256 项灰度映射。
func tileLUT(g *image.Gray, x0, x1, y0, y1 int, clip float64) [256]uint8 {
	var lut [256]uint8
	area := (x1 - x0) * (y1 - y0)
	if area <= 0 {
		for i := range lut {
			lut[i] = uint8(i)
		}
		return lut
	}
	var hist [256]int
	for y := y0; y < y1; y++ {
		base := y * g.Rect.Dx()
		for x := x0; x < x1; x++ {
			hist[g.Pix[base+x]]++
		}
	}
	limit := int(clip * float64(area))
	if limit < 1 {
		limit = 1
	}
	// 反复"截断尖峰+均摊超出量"(OpenCV clipHistogram 同款):单轮整数均摊
	// 会在小分块/极端分布(如整块常量)下把质量丢进 0,把整图 blown out。
	for iter := 0; iter < 16; iter++ {
		excess := 0
		for i, n := range hist {
			if n > limit {
				excess += n - limit
				hist[i] = limit
			}
		}
		if excess == 0 {
			break
		}
		share := excess / 256
		rem := excess % 256
		if share == 0 && rem == 0 {
			break
		}
		for i := range hist {
			hist[i] += share
		}
		for i := 0; i < rem; i++ {
			hist[i]++
		}
	}
	// CDF → 均衡映射;按组中点取 CDF(acc−hist/2),恒等分布回到中灰
	// 而非端点饱和,也顺带减小阶梯效应。
	acc, total := 0, area
	for i := range lut {
		acc += hist[i]
		mid := acc - hist[i]/2
		lut[i] = uint8(float64(mid) / float64(total) * 255)
	}
	return lut
}

// tileBounds 把 [0,n) 均分为 tiles 段(末段吸收余数),返回 tiles+1 个边界。
func tileBounds(n, tiles int) []int {
	bounds := make([]int, tiles+1)
	for i := 0; i <= tiles; i++ {
		bounds[i] = n * i / tiles
	}
	return bounds
}

// mapGray 按查找表映射灰度副本。
func mapGray(g *image.Gray, lut []uint8) *image.Gray {
	out := image.NewGray(g.Rect)
	for i, v := range g.Pix {
		out.Pix[i] = lut[v]
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
