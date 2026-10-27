// Package synth 模拟真实扫描/翻拍损伤:随机透视抖动 + 旋转 ±4° +
// 光照不均(0.85~1.15 线性渐变) + 高斯噪声,用于在无扫描仪的环境下
// 验证 OMR 内核的容错能力(工程验收的"容错靶场")。
package synth

import (
	"image"
	"math"
	"math/rand"

	"github.com/opengrade/opengrade/omr"
)

// Distort 把一张干净的答题卡灰度图变成"歪斜、明暗不均、带噪点"的模拟扫描件:
// 扫描帧比页面大一圈(四周 5% 留白),页面在帧内随机旋转 ±4°、透视抖动 ±2.5%,
// 角标永远不会被帧边裁掉 —— 与真实扫描仪/翻拍取景一致。rng 由调用方注入以保证可复现。
func Distort(src *image.Gray, rng *rand.Rand) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	padX := int(0.05 * float64(w))
	padY := int(0.05 * float64(h))
	outW, outH := w+2*padX, h+2*padY

	cx, cy := float64(w)/2, float64(h)/2         // 页面中心
	cox, coy := float64(outW)/2, float64(outH)/2 // 扫描帧中心

	// 页面→帧的放置:缩放 0.90(留白)后旋转 rot;采样需要反向映射 帧→页。
	rot := (rng.Float64()*8 - 4) * math.Pi / 180
	scale := 0.90
	invCos, invSin := math.Cos(-rot), math.Sin(-rot)
	sInv := 1 / scale
	jx, jy := 0.025*float64(w), 0.025*float64(h)

	// 用四组对应点拟合 帧→页 单应:帧角点(带抖动)→ 页面角点
	frameCorners := [4]image.Point{{0, 0}, {outW - 1, 0}, {0, outH - 1}, {outW - 1, outH - 1}}
	pageCorners := make([]image.Point, 4)
	for i, p := range frameCorners {
		dx := (float64(p.X) + (rng.Float64()*2-1)*jx - cox) * sInv
		dy := (float64(p.Y) + (rng.Float64()*2-1)*jy - coy) * sInv
		pageCorners[i] = image.Point{
			int(math.Round(invCos*dx - invSin*dy + cx)),
			int(math.Round(invSin*dx + invCos*dy + cy)),
		}
	}
	hm, err := omr.FitHomography(frameCorners[:], pageCorners)
	if err != nil {
		// 理论上不会发生(四点恒为凸四边形);保底返回原图
		return src
	}

	out := image.NewGray(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		base := y * outW
		for x := 0; x < outW; x++ {
			u, v := hm.Apply(float64(x), float64(y))
			val := omr.SampleBilinear(src, u, v)
			// 光照不均:横向线性渐变
			val *= 0.85 + 0.30*float64(x)/float64(outW)
			// 传感器噪声
			val += rng.NormFloat64() * 8
			switch {
			case val < 0:
				val = 0
			case val > 255:
				val = 255
			}
			out.Pix[base+x] = uint8(val)
		}
	}
	return out
}

// Drift 模拟打印/复印的逐行走位:把卡面按水平条带做随机横向平移(±maxPx),
// 条带间线性过渡防撕裂。定位角标与正文随所在条带一起偏移 —— 单应矫正吸收
// 角标处的全局几何后,离角标远的区域仍残留该偏移,正是印刷漂移自对齐
// (omr.align)要量出来修掉的对象。maxPx<=0 时原样返回。
func Drift(src *image.Gray, rng *rand.Rand, maxPx float64) *image.Gray {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if maxPx <= 0 {
		return src
	}
	const band = 40 // 条带高度(px):打印行走位的空间尺度
	n := h/band + 2
	offs := make([]float64, n)
	for i := range offs {
		offs[i] = (rng.Float64()*2 - 1) * maxPx
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		f := float64(y) / float64(band)
		i := int(f)
		if i > n-2 {
			i = n - 2
		}
		t := f - float64(i)
		dx := offs[i]*(1-t) + offs[i+1]*t
		base := y * w
		for x := 0; x < w; x++ {
			sx := int(float64(x) - dx + 0.5)
			if sx < 0 {
				sx = 0
			} else if sx >= w {
				sx = w - 1
			}
			out.Pix[base+x] = src.Pix[base+sx]
		}
	}
	return out
}
