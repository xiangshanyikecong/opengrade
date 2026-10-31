package omr

import (
	"image"

	"github.com/opengrade/opengrade/card"
)

// 一卡多人(拼版)切分:A3 横放纸印左右两份 A4 卡,扫描后先切回单卡再读卡。
// 切分必须发生在 locateAndRectify 之前 —— 拼版纸有 8 个定位角标,整图直喂
// findLocators 时,左卡的右上/右下角标与右卡的左上/左下角标分居四象限中线
// 两侧,四象限恰好各得一个候选,静默拟合成横跨两卡的四边形,矫正出无告警的
// 垃圾判读(卡间留白还可能顺带触发"疑似空白卡"误导)。

// imposeLocatorFrac 定位块边长占 A3 拼版纸宽的比例(5mm / 420mm):
// 由此按图宽推算角标期望尺寸,尺寸闸门以它为锚(与 findLocators 的
// standardExpect 同思路,但拼版纸宽是 A4 的两倍)。
const imposeLocatorFrac = card.LocatorSideMM / (2 * card.PageWMM)

// SplitImpose 把一张 A3 横放拼版扫描图切成左右两份单卡子图(裁切取全高)。
// 策略:收集角标候选(尺寸/实心度/方形四角闸门,不做象限唯一假设)→ 按图宽
// 中线聚成左右两组 → 两组都 ≥2 个候选且几何像双卡时,切在两组最内侧角标的
// 中点(限幅 42%~58% 图宽,两份卡等宽的物理约束)。
// 回退语义(全部带 note 说明,调用方直接透传上传响应):
//   - 候选凑不齐 → 按几何中线对半切(卡纸/极端光照下仍可用,异常卷走人工指派);
//   - 有候选但不像双卡(疑似把单卡页误传进拼版考试)→ 整页原样返回单卡。
// 返回的子图各自走 Read:定位/矫正/判读/码/页点对单卡语义零改动。
func SplitImpose(src image.Image) (left, right *image.Gray, notes []string) {
	gray := ToGray(src)
	return imposeSplitResult(gray, imposeCandidates(gray))
}

// imposeSplitResult 根据全部角标候选决定切分方式(聚类中点/单卡整页/几何对半)。
func imposeSplitResult(gray *image.Gray, cands []imposeCand) (left, right *image.Gray, notes []string) {
	W := gray.Rect.Dx()
	mid := float64(W) / 2
	var lcls, rcls []imposeCand
	for _, c := range cands {
		if c.cx < mid {
			lcls = append(lcls, c)
		} else {
			rcls = append(rcls, c)
		}
	}
	if len(lcls) >= 2 && len(rcls) >= 2 {
		maxL, minR := maxCx(lcls), minCx(rcls)
		// 双卡几何:左簇最右角标伸向中线(左卡右上角标 ≈47% 宽,阈值 35%)、
		// 右簇最左角标亦然(≈53%,阈值 65%),两簇最内侧角标间距 ≈ 卡间留白
		// (<12.5% 图宽)。单卡页的左角/右角虽也分居中线两侧,但间距 ≥ 半张
		// 卡宽,借此外形差自愈"单卡误传进拼版考试"。
		if maxL >= 0.70*mid && minR <= 1.30*mid && minR-maxL < 0.25*mid {
			// 限幅 42%~58% 图宽(两份等宽 A4 卡的物理约束)
			cut := min(0.58*float64(W), max(0.42*float64(W), (maxL+minR)/2))
			ci := int(cut)
			return cropHalf(gray, 0, ci), cropHalf(gray, ci, W), nil
		}
	}
	if len(cands) < 4 {
		m := int(mid)
		return cropHalf(gray, 0, m), cropHalf(gray, m, W),
			[]string{"拼版切分:定位角标候选不足,已按几何中线对半切分;切分异常的卷请单独重传"}
	}
	// 单侧成卡(≥3 个角标凑成一张卡的格局)、另一侧零角标:空白半版 ——
	// 名册奇数时末张 A3 的常态,裁出有卡一侧按单卡判读,不产异常卷。
	// 卡缘 = 最内侧角标 ± 12.5mm(定位块中心距卡缘,≈2.98% 图宽),留 1% 余量。
	if len(lcls) >= 3 && len(rcls) == 0 {
		cut := int(min(0.60*float64(W), max(0.45*float64(W), maxCx(lcls)+0.04*float64(W))))
		return cropHalf(gray, 0, cut), nil,
			[]string{"拼版切分:仅检出左半有卡(右半疑似空白半版),已按单卡判读"}
	}
	if len(rcls) >= 3 && len(lcls) == 0 {
		cut := int(min(0.55*float64(W), max(0.40*float64(W), minCx(rcls)-0.04*float64(W))))
		return nil, cropHalf(gray, cut, W),
			[]string{"拼版切分:仅检出右半有卡(左半疑似空白半版),已按单卡判读"}
	}
	return gray, nil, []string{"拼版切分:未检出左右双卡几何(疑似单卡页混入拼版考试),已按整页单卡判读"}
}

func maxCx(cs []imposeCand) float64 {
	m := -1.0
	for _, c := range cs {
		if c.cx > m {
			m = c.cx
		}
	}
	return m
}

func minCx(cs []imposeCand) float64 {
	m := -1.0
	for _, c := range cs {
		if m < 0 || c.cx < m {
			m = c.cx
		}
	}
	return m
}

// imposeCand 是一个角标候选(原图尺度坐标)。
type imposeCand struct{ cx, cy float64 }

// imposeCandidates 三级二值化 × 两级尺寸窗收集角标候选:
// 闸门与 findLocators 第 1 级同源(实心度/方形四角/纵横比),但不做象限唯一
// 假设、不做期望位置校验 —— 拼版纸上同象限天然有 2 个真角标。
func imposeCandidates(gray *image.Gray) []imposeCand {
	bins := []*bitmap{
		Binarize(gray, Otsu(gray)),
		AdaptiveBinarize(gray, 0, 0.15),
		Binarize(gray, inkPercentile(gray, 0.02)),
	}
	var best []imposeCand
	for _, bin := range bins {
		for _, win := range [][2]float64{{0.55, 2.0}, {0.40, 2.6}} {
			c := imposeCandidatesWin(bin, win[0], win[1])
			if len(c) > len(best) {
				best = c
			}
			if len(best) >= 8 { // 双卡各 4 个,拿满即可
				return best
			}
		}
		if len(best) >= 4 {
			return best
		}
	}
	return best
}

func imposeCandidatesWin(bin *bitmap, loK, hiK float64) []imposeCand {
	ds := bin.W / 1240
	if ds < 1 {
		ds = 1
	}
	small := downsample(bin, ds)
	ox, oy, small := trimFrame(small)
	blobs := connectedBlobs(small)
	expSide := float64(small.W) * imposeLocatorFrac
	var out []imposeCand
	for _, bl := range blobs {
		w, h := float64(bl.w()), float64(bl.h())
		if w < expSide*loK || w > expSide*hiK || h < expSide*loK || h > expSide*hiK {
			continue
		}
		if float64(bl.area)/(w*h) < 0.72 { // 实心方块;实涂圆 π/4≈0.785 但尺寸不达闸
			continue
		}
		if !squareCorners(small, bl) { // 四角墨迹:拦住涂卡圆/文字笔画
			continue
		}
		aspect := w / h
		if aspect < 0.45 || aspect > 2.2 {
			continue
		}
		// 降采样坐标 → 原图尺度(块中心补偿 + 黑边裁剪偏移,同 findLocators)
		out = append(out, imposeCand{
			cx: (bl.cx() + float64(ox)) * float64(ds),
			cy: (bl.cy() + float64(oy)) * float64(ds),
		})
	}
	// 近邻拒绝:真角标彼此相距 ≥ 半张卡宽(≈37×边长);裁切线虚线段是 2mm
	// 实心小方块,恰落在尺寸窗内且间距 0.8×边长 —— 有近邻者整链剔除,
	// 否则倾斜扫描下虚线段会顶替真角标,拟合出平移的网格。
	rejected := make([]bool, len(out))
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			dx, dy := out[i].cx-out[j].cx, out[i].cy-out[j].cy
			if dx*dx+dy*dy < (0.9*expSide*float64(ds))*(0.9*expSide*float64(ds)) {
				rejected[i], rejected[j] = true, true
			}
		}
	}
	kept := out[:0]
	for i, c := range out {
		if !rejected[i] {
			kept = append(kept, c)
		}
	}
	return kept
}

// cropHalf 裁出 [x0,x1) 全高的灰度子图。
func cropHalf(g *image.Gray, x0, x1 int) *image.Gray {
	if x0 < 0 {
		x0 = 0
	}
	if x1 > g.Rect.Dx() {
		x1 = g.Rect.Dx()
	}
	if x1 <= x0 {
		x1 = x0 + 1
	}
	w, h := x1-x0, g.Rect.Dy()
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := y*g.Stride + x0
		copy(out.Pix[y*w:(y+1)*w], g.Pix[src:src+w])
	}
	return out
}
