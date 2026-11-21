package omr

// 定位兼容层:打印缩放、扫描 DPI 偏差、角标粘连、贴边裁切、整角遮挡下找回定位角标。
// 检测阶梯(locate.go 按序调用,主路径命中即止,标准扫描行为不变):
//   1. 连通域三重闸门(标准折算期望几何);
//   2. 比例自校准后重跑闸门 —— 一对互证的实心方块反推全局缩放与各角期望位置;
//   3. 多尺度实心窗模板匹配(积分图滑窗)—— 粘连/裁切破坏连通域统计,但角标
//      内部的实心黑窗始终存在;
//   4. 期望位置吸附近(最近大块墨迹);
//   5. 相邻角标几何推算 —— ≥2 个角标在场即可按仿射/相似变换补出缺失角。

import (
	"image"
	"math"

	"github.com/opengrade/opengrade/card"
)

// locatorExpect 是定位检测的期望几何(降采样图坐标):期望边长与四角期望中心。
// 标准折算假设"纸张占满图宽且版面按 300DPI 出卡";实测校准用于一切不满足该假设的场合。
// inv 把降采样图坐标回代为版面坐标(300DPI px),供码区排除等版面先验使用。
type locatorExpect struct {
	side       float64                     // 期望边长(降采样 px)
	pos        [4][2]float64               // 四角期望中心,顺序 TL TR BL BR
	calibrated bool                        // 是否来自实测校准(决定距离闸门松紧)
	inv        func([2]float64) [2]float64 // 图坐标 → 版面坐标
}

// standardExpect 按 300DPI 标准版面折算期望几何(历史行为)。
func standardExpect(sheet *card.Sheet, ds int) *locatorExpect {
	e := &locatorExpect{
		side: float64(sheet.LocatorSidePx) / float64(ds),
		inv: func(p [2]float64) [2]float64 {
			return [2]float64{p[0] * float64(ds), p[1] * float64(ds)}
		},
	}
	for q := 0; q < 4; q++ {
		e.pos[q] = [2]float64{
			float64(sheet.Locators[q].X) / float64(ds),
			float64(sheet.Locators[q].Y) / float64(ds),
		}
	}
	return e
}

// calibratedExpect 比例自校准:从"实心方形"候选里拉出一对互证的角标,
// 反推全局缩放(small px / 版面 px)与其余各角期望位置。
// 候选只卡相对尺寸与方正度,不预设绝对大小,因此对打印缩放、
// 扫描 DPI、照片留白一律成立;粘连块实心度不足自然出局。
// 至少两个候选互相印证(得分 ≥2)才采纳,返回 nil 表示校准失败。
func calibratedExpect(blobs []blob, b *bitmap, sheet *card.Sheet, ds int) *locatorExpect {
	lo := 0.008 * float64(min(b.W, b.H)) // 定位块边长≈页宽 4.8%;52% 打印缩放×畸变留白后也有 ~1.1%
	hi := 0.12 * float64(min(b.W, b.H))
	var cands []blob
	for _, bl := range blobs {
		w, h := bl.w(), bl.h()
		if float64(w) < lo || float64(w) > hi || float64(h) < lo || float64(h) > hi {
			continue
		}
		if float64(bl.area)/float64(w*h) < 0.80 {
			continue
		}
		if !squareCorners(b, bl) { // 排除涂卡圆与成组涂点(实心度擦线的假方块)
			continue
		}
		asp := float64(w) / float64(h)
		if asp < 0.70 || asp > 1.40 { // 校准用比主闸门更严的方正度
			continue
		}
		cands = append(cands, bl)
	}
	const voteR = 0.15 // 互证半径(×图宽):容纳 ±4° 旋转下对角预测的偏差
	bestScore := 1
	var best *locatorExpect
	for _, c := range cands {
		cside := (float64(c.w()) + float64(c.h())) / 2
		for q := 0; q < 4; q++ {
			// 锚点假说必须与候选所在图像象限一致:右上区域的墨块不能当左上锚,
			// 否则真角标会互证出"假星座"并带偏全部期望位置(实测踩过)。
			if quadrant(c.cx(), c.cy(), b.W, b.H) != q {
				continue
			}
			// 假设 c 是第 q 角:k = 实测边长/版面边长,把版面坐标线性映射到本图
			k := cside / float64(sheet.LocatorSidePx)
			e := &locatorExpect{side: cside}
			score := 1
			for i := 0; i < 4; i++ {
				e.pos[i] = [2]float64{
					c.cx() + (float64(sheet.Locators[i].X)-float64(sheet.Locators[q].X))*k,
					c.cy() + (float64(sheet.Locators[i].Y)-float64(sheet.Locators[q].Y))*k,
				}
				if i == q {
					continue
				}
				for _, d := range cands {
					dside := (float64(d.w()) + float64(d.h())) / 2
					if dside < 0.5*cside || dside > 2.0*cside {
						continue
					}
					if math.Hypot(d.cx()-e.pos[i][0], d.cy()-e.pos[i][1]) < voteR*float64(b.W) {
						score++
						break
					}
				}
			}
			if score > bestScore {
				bestScore = score
				best = e
				best.calibrated = true
				// 逆映射:以锚点象限 q 为基准,尺度 k 的倒数回代版面坐标
				cx0, cy0 := c.cx(), c.cy()
				sq := sheet.Locators[q]
				invK := float64(sheet.LocatorSidePx) / cside
				best.inv = func(p [2]float64) [2]float64 {
					return [2]float64{
						float64(sq.X) + (p[0]-cx0)*invK,
						float64(sq.Y) + (p[1]-cy0)*invK,
					}
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	return best
}

// fitAnchorMap 由指定角标集拟合 降采样图坐标→版面坐标 的映射
// (≥3 角仿射精确解,2 角相似变换),供码区审计等版面先验使用。
func fitAnchorMap(sheet *card.Sheet, ds int, idxs []int, best *[4]locatorCand) (func([2]float64) [2]float64, bool) {
	src := make([][2]float64, len(idxs)) // 降采样图坐标
	dst := make([][2]float64, len(idxs)) // 版面坐标
	for i, q := range idxs {
		src[i] = [2]float64{float64(best[q].pt.X) / float64(ds), float64(best[q].pt.Y) / float64(ds)}
		dst[i] = [2]float64{float64(sheet.Locators[q].X), float64(sheet.Locators[q].Y)}
	}
	if len(idxs) >= 3 {
		m := [3][3]float64{{src[0][0], src[0][1], 1}, {src[1][0], src[1][1], 1}, {src[2][0], src[2][1], 1}}
		rx, ok1 := solve3(m, [3]float64{dst[0][0], dst[1][0], dst[2][0]})
		ry, ok2 := solve3(m, [3]float64{dst[0][1], dst[1][1], dst[2][1]})
		if !ok1 || !ok2 {
			return nil, false
		}
		return func(p [2]float64) [2]float64 {
			return [2]float64{rx[0]*p[0] + rx[1]*p[1] + rx[2], ry[0]*p[0] + ry[1]*p[1] + ry[2]}
		}, true
	}
	vsx, vsy := src[1][0]-src[0][0], src[1][1]-src[0][1]
	vtx, vty := dst[1][0]-dst[0][0], dst[1][1]-dst[0][1]
	den := vsx*vsx + vsy*vsy
	if den == 0 {
		return nil, false
	}
	a := (vtx*vsx + vty*vsy) / den
	bb := (vty*vsx - vtx*vsy) / den
	if math.Hypot(a, bb) == 0 || math.Abs(math.Atan2(bb, a)) > 12*math.Pi/180 {
		return nil, false // 旋转角超出扫描仪合理范围,锚点不可信
	}
	return func(p [2]float64) [2]float64 {
		dx, dy := p[0]-src[0][0], p[1]-src[0][1]
		return [2]float64{a*dx - bb*dy + dst[0][0], bb*dx + a*dy + dst[0][1]}
	}, true
}

// inRegCodeSheet 判断版面坐标是否落在考号码区内(外扩 3mm)。
func inRegCodeSheet(sheet *card.Sheet, sp [2]float64) bool {
	if sheet.RegCode == (image.Rectangle{}) {
		return false
	}
	const pad = 36 // 3mm @300DPI
	return sp[0] >= float64(sheet.RegCode.Min.X-pad) && sp[0] <= float64(sheet.RegCode.Max.X+pad) &&
		sp[1] >= float64(sheet.RegCode.Min.Y-pad) && sp[1] <= float64(sheet.RegCode.Max.Y+pad)
}

// inRegCode 判断"图坐标 p 经校准逆映射回版面"是否落在考号码区内。
// 仅对实测校准几何有意义(标准折算的逆映射不带缩放/平移信息,不可用于版面判断)。
func inRegCode(sheet *card.Sheet, exp *locatorExpect, p [2]float64) bool {
	if exp == nil || !exp.calibrated || exp.inv == nil {
		return false
	}
	return inRegCodeSheet(sheet, exp.inv(p))
}

// isBanned 报告 (cx,cy)(降采样坐标)是否落在任一已吊销假角标的 1×边长邻域内。
func isBanned(banned [][2]float64, cx, cy int, side float64) bool {
	for _, b := range banned {
		if math.Hypot(b[0]-float64(cx), b[1]-float64(cy)) < side {
			return true
		}
	}
	return false
}

// rescueTemplate 多尺度"实心方块窗"匹配(积分图滑窗):
// 对缺失象限,在期望位置附近用多个尺度的 s×s 窗口找墨迹占比最高的窗口。
// 与连通域统计不同,粘连块内部的角标实心窗照样命中;贴边裁切时可见部分
// 仍能容下一个较小尺度的实心窗,再按未裁切轴的实测边长反推中心。
// 候选按(实心度,距期望距离)排序,码区内与已确认角标邻域内的命中一律拒收。
func rescueTemplate(b *bitmap, exp *locatorExpect, have *[4]bool, best *[4]locatorCand, ds int, sheet *card.Sheet, banned [][2]float64) {
	ig := newIntegral(b)
	// 尺度按贴近 1.0 的顺序排列:同分时保留更接近期望边长的命中。
	// 下限 0.55:涂卡圆直径仅 0.68×边长,其内可容实心窗 ≤0.48×边长,
	// 0.55 起涂圆在数学上装不下匹配窗 —— 防止把考号区涂点认成角标。
	scales := []float64{1.0, 0.85, 1.2, 0.7, 1.4, 0.55, 1.7}
	const minFill = 0.93 // 实心方块窗;稠密文字块一般 <0.6
	type hit struct {
		fill, dist float64
		x0, y0, s  int
	}
	insertTop := func(hs []hit, h hit, n int) []hit {
		// 按(实心度降序,距离升序)保持至多 n 条:实心大区域内全窗命中,不能全收
		i := len(hs)
		for i > 0 && (hs[i-1].fill < h.fill || (hs[i-1].fill == h.fill && hs[i-1].dist > h.dist)) {
			i--
		}
		if i == n {
			return hs
		}
		hs = append(hs, hit{})
		copy(hs[i+1:], hs[i:])
		hs[i] = h
		if len(hs) > n {
			hs = hs[:n]
		}
		return hs
	}
	for q := 0; q < 4; q++ {
		if have[q] {
			continue
		}
		ex, ey := exp.pos[q][0], exp.pos[q][1]
		halfWin := 0.16 * float64(b.W) // 覆盖校准后残余的旋转/透视偏差
		var hits []hit
		for _, f := range scales {
			s := int(math.Round(exp.side * f))
			if s < 4 || s >= b.W || s >= b.H {
				continue
			}
			x0min := max(0, int(ex-float64(s)/2-halfWin))
			x0max := min(b.W-s, int(ex-float64(s)/2+halfWin))
			y0min := max(0, int(ey-float64(s)/2-halfWin))
			y0max := min(b.H-s, int(ey-float64(s)/2+halfWin))
			for y0 := y0min; y0 <= y0max; y0++ {
				for x0 := x0min; x0 <= x0max; x0++ {
					fill := ig.ratio(x0, y0, s)
					if fill < minFill {
						continue
					}
					dist := math.Hypot(float64(x0)+float64(s)/2-ex, float64(y0)+float64(s)/2-ey)
					hits = insertTop(hits, hit{fill, dist, x0, y0, s}, 8)
				}
			}
		}
		for _, h := range hits {
			cx, cy := h.x0+h.s/2, h.y0+h.s/2 // 窗内命中:窗中心即角标中心
			if isBanned(banned, cx, cy, exp.side) {
				continue
			}
			if inRegCode(sheet, exp, [2]float64{float64(cx), float64(cy)}) {
				continue
			}
			if nearFound(have, best, ds, cx, cy, exp.side) {
				continue
			}
			// 孤立性环检:真角标四周留白(粘连条带只占环带一小段);
			// 文字块/标签字周围全是墨,窗口再实心也不是角标。
			if isolationInk(b, cx, cy, exp.side) > 0.40 {
				continue
			}
			if eh, ev := touchesEdge(b, h.x0, h.y0, h.s); eh || ev {
				// 窗口贴边:角标可能被裁,按四向黑行程反推真实中心
				cx, cy = refineCenter(b, cx, cy, h.s)
			}
			best[q] = locatorCand{pt: image.Point{cx * ds, cy * ds}, val: -1}
			have[q] = true
			break
		}
	}
}

// nearFound 报告 (cx,cy)(降采样坐标)是否落在任一已确认角标的
// 2×边长邻域内(防跨角错配;best.pt 为原图尺度,先除回 ds)。
func nearFound(have *[4]bool, best *[4]locatorCand, ds, cx, cy int, side float64) bool {
	for i := 0; i < 4; i++ {
		if !have[i] {
			continue
		}
		if math.Hypot(float64(best[i].pt.X)/float64(ds)-float64(cx), float64(best[i].pt.Y)/float64(ds)-float64(cy)) < 2*side {
			return true
		}
	}
	return false
}

// isolationInk 以命中中心为心,统计 外框1.5×边长 − 内框1.0×边长 方环内的墨迹占比
// (只计图内部分)。环带恒在角标外侧:孤立角标环带接近全白;文字块/标签字邻域密集。
func isolationInk(b *bitmap, cx, cy int, side float64) float64 {
	outer := int(side * 0.75)
	inner := int(side * 0.5)
	total, ink := 0, 0
	for y := max(0, cy-outer); y <= min(b.H-1, cy+outer); y++ {
		for x := max(0, cx-outer); x <= min(b.W-1, cx+outer); x++ {
			if (x-cx <= inner && cx-x <= inner) && (y-cy <= inner && cy-y <= inner) {
				continue // 内框(角标本体)不计
			}
			total++
			ink += int(b.D[y*b.W+x])
		}
	}
	if total == 0 {
		return 0
	}
	return float64(ink) / float64(total)
}

// touchesEdge 报告匹配窗是否贴到图像边界(左右/上下任意一轴贴边即可能被裁)。
func touchesEdge(b *bitmap, x0, y0, s int) (horiz, vert bool) {
	return x0 <= 0 || x0+s >= b.W, y0 <= 0 || y0+s >= b.H
}

// refineCenter 从匹配窗中心向四向游走黑行程得到实测边界;
// 某轴贴边(被裁)时,以另一轴的实测边长为基准反推该轴中心。
// 行程设上限,防止把粘连进来的长条一路吞进来拉偏边界。
func refineCenter(b *bitmap, cx, cy, s int) (int, int) {
	const cap = 3.0
	walk := func(dx, dy int) (int, bool) {
		n := 0
		x, y := cx, cy
		for n < int(cap*float64(s)) {
			x += dx
			y += dy
			if x < 0 || y < 0 || x >= b.W || y >= b.H {
				return n, true // 撞到图像边界
			}
			if b.D[y*b.W+x] == 0 {
				return n, false
			}
			n++
		}
		return n, true // 行程超限,视同不可信
	}
	rL, boundL := walk(-1, 0)
	rR, boundR := walk(1, 0)
	rT, boundT := walk(0, -1)
	rB, boundB := walk(0, 1)
	eL, eR := cx-rL-1, cx+rR // eL/eR:黑区外侧的第一个白位(或边界)
	eT, eB := cy-rT-1, cy+rB
	sEst := s
	if !boundT && !boundB {
		sEst = eB - eT - 1
	} else if !boundL && !boundR {
		sEst = eR - eL - 1
	}
	cx2, cy2 := (eL+eR)/2, (eT+eB)/2
	if boundL && !boundR {
		cx2 = eR - 1 - sEst/2
	}
	if boundR && !boundL {
		cx2 = eL + 1 + sEst/2
	}
	if boundT && !boundB {
		cy2 = eB - 1 - sEst/2
	}
	if boundB && !boundT {
		cy2 = eT + 1 + sEst/2
	}
	return cx2, cy2
}

// synthesizeMissing 末级兜底:≥2 个角标在场时按已知版面几何推算缺失角标——
// 3 个在场取仿射精确解(扫描仪输出近似仿射,透视残差远小于涂卡圆半径),
// 2 个在场取相似变换(放弃剪切容差换"仍然能读")。
// 推算点只在该象限角标确实无墨可找(整角遮挡/裁没)时才会被走到。
func synthesizeMissing(sheet *card.Sheet, b *bitmap, ds int, have *[4]bool, best *[4]locatorCand) bool {
	var found []int
	for q := 0; q < 4; q++ {
		if have[q] {
			found = append(found, q)
		}
	}
	if len(found) < 2 {
		return false
	}
	sheetPt := func(q int) [2]float64 {
		return [2]float64{float64(sheet.Locators[q].X), float64(sheet.Locators[q].Y)}
	}
	smallPt := func(q int) [2]float64 {
		return [2]float64{float64(best[q].pt.X) / float64(ds), float64(best[q].pt.Y) / float64(ds)}
	}

	var mapFn func([2]float64) [2]float64
	if len(found) >= 3 {
		s := [3][2]float64{sheetPt(found[0]), sheetPt(found[1]), sheetPt(found[2])}
		t := [3][2]float64{smallPt(found[0]), smallPt(found[1]), smallPt(found[2])}
		// 锚点形状一致性:三对距离比例的极差过大说明锚点本身不可信,不推算
		var ratios []float64
		for i := 0; i < 3; i++ {
			for j := i + 1; j < 3; j++ {
				dsLen := math.Hypot(s[i][0]-s[j][0], s[i][1]-s[j][1])
				diLen := math.Hypot(t[i][0]-t[j][0], t[i][1]-t[j][1])
				if dsLen == 0 {
					return false
				}
				ratios = append(ratios, diLen/dsLen)
			}
		}
		lo, hiP := ratios[0], ratios[0]
		for _, r := range ratios {
			lo = math.Min(lo, r)
			hiP = math.Max(hiP, r)
		}
		if hiP > 1.45*lo {
			return false
		}
		// 仿射精确解:x' = a·x + b·y + c,y' = d·x + e·y + f(两个共享系数矩阵的三元方程组)
		m := [3][3]float64{{s[0][0], s[0][1], 1}, {s[1][0], s[1][1], 1}, {s[2][0], s[2][1], 1}}
		rx, ok1 := solve3(m, [3]float64{t[0][0], t[1][0], t[2][0]})
		ry, ok2 := solve3(m, [3]float64{t[0][1], t[1][1], t[2][1]})
		if !ok1 || !ok2 {
			return false
		}
		mapFn = func(p [2]float64) [2]float64 {
			return [2]float64{rx[0]*p[0] + rx[1]*p[1] + rx[2], ry[0]*p[0] + ry[1]*p[1] + ry[2]}
		}
	} else {
		s0, s1 := sheetPt(found[0]), sheetPt(found[1])
		t0, t1 := smallPt(found[0]), smallPt(found[1])
		vsx, vsy := s1[0]-s0[0], s1[1]-s0[1]
		vtx, vty := t1[0]-t0[0], t1[1]-t0[1]
		den := vsx*vsx + vsy*vsy
		if den == 0 {
			return false
		}
		a := (vtx*vsx + vty*vsy) / den  // k·cosθ
		bb := (vty*vsx - vtx*vsy) / den // k·sinθ
		if math.Hypot(a, bb) == 0 || math.Abs(math.Atan2(bb, a)) > 12*math.Pi/180 {
			return false // 旋转角超出扫描仪合理范围,锚点不可信
		}
		mapFn = func(p [2]float64) [2]float64 {
			dx, dy := p[0]-s0[0], p[1]-s0[1]
			return [2]float64{a*dx - bb*dy + t0[0], bb*dx + a*dy + t0[1]}
		}
	}

	filled := false
	for q := 0; q < 4; q++ {
		if have[q] {
			continue
		}
		p := mapFn(sheetPt(q))
		// 推算点允许略出画(裁边场景);离谱出画说明锚点有假,宁可不补
		if p[0] < -0.15*float64(b.W) || p[0] > 1.15*float64(b.W) ||
			p[1] < -0.15*float64(b.H) || p[1] > 1.15*float64(b.H) {
			continue
		}
		best[q] = locatorCand{
			pt:  image.Point{int(math.Round(p[0])) * ds, int(math.Round(p[1])) * ds},
			val: -1,
		}
		have[q] = true
		filled = true
	}
	return filled
}

// solve3 用克莱姆法则解 3×3 线性方程组。
func solve3(m [3][3]float64, rhs [3]float64) ([3]float64, bool) {
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	if math.Abs(det) < 1e-9 {
		return [3]float64{}, false
	}
	var out [3]float64
	for c := 0; c < 3; c++ {
		var mm [3][3]float64
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				mm[i][j] = m[i][j]
			}
			mm[i][c] = rhs[i]
		}
		out[c] = (mm[0][0]*(mm[1][1]*mm[2][2]-mm[1][2]*mm[2][1]) -
			mm[0][1]*(mm[1][0]*mm[2][2]-mm[1][2]*mm[2][0]) +
			mm[0][2]*(mm[1][0]*mm[2][1]-mm[1][1]*mm[2][0])) / det
	}
	return out, true
}

// integral 是二值图的积分图(含零行零列前缀),O(1) 查询任意矩形墨迹和。
type integral struct {
	w, h int
	s    []uint64
}

func newIntegral(b *bitmap) *integral {
	w, h := b.W, b.H
	ig := &integral{w: w, h: h, s: make([]uint64, (w+1)*(h+1))}
	for y := 0; y < h; y++ {
		rowSum := uint64(0)
		for x := 0; x < w; x++ {
			rowSum += uint64(b.D[y*w+x])
			ig.s[(y+1)*(w+1)+x+1] = ig.s[y*(w+1)+x+1] + rowSum
		}
	}
	return ig
}

// ratio 返回以 (x0,y0) 为左上角、边长 s 的正方形窗内墨迹占比。
func (ig *integral) ratio(x0, y0, s int) float64 {
	x1, y1 := x0+s-1, y0+s-1
	if x0 < 0 || y0 < 0 || x1 >= ig.w || y1 >= ig.h {
		return 0
	}
	sum := ig.s[(y1+1)*(ig.w+1)+x1+1] - ig.s[y0*(ig.w+1)+x1+1] -
		ig.s[(y1+1)*(ig.w+1)+x0] + ig.s[y0*(ig.w+1)+x0]
	return float64(sum) / float64(s*s)
}
