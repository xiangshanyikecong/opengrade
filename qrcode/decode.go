// decode.go:从灰度区域解码 QR(数字模式)。与通用 QR 识别器的区别:
// 输入矩形已由答题卡透视矫正到标准网格、方向确定、区域里只有一个码,
// 因此跳过全局检索/旋转判定,直接在区域内找三个定位图形建立模块网格。
package qrcode

import (
	"fmt"
	"image"
	"strings"
)

// candidate 是一条扫描线上的定位图形候选:
// pos = 沿扫描方向的图案中心(粗黑段中心),cross = 另一轴坐标,unit = 单元宽度。
type candidate struct {
	pos, cross float64
	unit       float64
}

// DecodeRegion 在灰度图指定矩形内定位并解码 QR。
// rect 必须完整包含 QR 本体,且四周留有浅色静区(卡面渲染契约:两侧各 ≥2 模块)。
func DecodeRegion(gray *image.Gray, rect image.Rectangle) (string, error) {
	if rect.Dx() < 20 || rect.Dy() < 20 {
		return "", fmt.Errorf("二维码区域过小(%d×%d)", rect.Dx(), rect.Dy())
	}
	th := regionOtsu(gray, rect)
	isDark := func(x, y int) bool { return gray.GrayAt(x, y).Y <= th }

	// 横竖扫描线收集 1:1:3:1:1 定位图形候选
	var hCands, vCands []candidate
	for y := rect.Min.Y + 1; y < rect.Max.Y; y += 2 {
		line := make([]bool, rect.Dx())
		for x := 0; x < rect.Dx(); x++ {
			line[x] = isDark(rect.Min.X+x, y)
		}
		for _, p := range finderCenters(line) {
			hCands = append(hCands, candidate{pos: p.center + float64(rect.Min.X), cross: float64(y), unit: p.unit})
		}
	}
	for x := rect.Min.X + 1; x < rect.Max.X; x += 2 {
		line := make([]bool, rect.Dy())
		for y := 0; y < rect.Dy(); y++ {
			line[y] = isDark(x, rect.Min.Y+y)
		}
		for _, p := range finderCenters(line) {
			vCands = append(vCands, candidate{pos: p.center + float64(rect.Min.Y), cross: float64(x), unit: p.unit})
		}
	}
	if len(hCands) < 4 || len(vCands) < 4 {
		return "", fmt.Errorf("定位图形候选不足(横 %d / 竖 %d)", len(hCands), len(vCands))
	}

	midX := float64(rect.Min.X+rect.Max.X) / 2
	midY := float64(rect.Min.Y+rect.Max.Y) / 2
	hTop := cluster(hCands, func(c candidate) bool { return c.cross < midY })
	vLeft := cluster(vCands, func(c candidate) bool { return c.cross < midX })
	tlH := leftmost(hTop)
	tlV := leftmost(vLeft) // 垂直候选的 pos 即 y,最左簇=最上簇=TL
	if tlH == nil || tlV == nil {
		return "", fmt.Errorf("左上定位图形未找到")
	}
	// TR/BL 候选按 pos 取最极端的若干簇:数据区可能产生伪 1:1:3:1:1 图案,
	// 用「横竖尺寸互验 + 命中 v1~v4 合法尺寸」筛掉伪簇。
	topClusters := sortedDesc(hTop)
	leftClusters := sortedDesc(vLeft)
	var spec versionSpec
	var m, tlx, tly float64
	found := false
	for _, tr := range topClusters[:min(3, len(topClusters))] {
		for _, bl := range leftClusters[:min(3, len(leftClusters))] {
			if tr.pos == tlH.pos || bl.pos == tlV.pos {
				continue
			}
			mm := (tlH.unit + tr.unit + tlV.unit + bl.unit) / 4
			sizeH := (tr.pos - tlH.pos)/mm + 7
			sizeV := (bl.pos - tlV.pos)/mm + 7
			if absF(sizeH-sizeV) > 1.0 {
				continue
			}
			if s, ok := specBySize(roundF((sizeH + sizeV) / 2)); ok {
				spec, m, tlx, tly, found = s, mm, tlH.pos, tlV.pos, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return "", fmt.Errorf("定位图形互验失败(横 %d 簇/竖 %d 簇)", len(hTop), len(vLeft))
	}

	// 建立模块网格:定位图形中心 = 版面原点 + 3.5 模块
	x0 := tlx - 3.5*m
	y0 := tly - 3.5*m
	n := spec.size
	dark := make([][]bool, n)
	box := max(1, int(m*0.3)) // 采样盒远小于模块,避免邻模块串色
	for r := 0; r < n; r++ {
		dark[r] = make([]bool, n)
		for c := 0; c < n; c++ {
			cx := int(x0 + (float64(c)+0.5)*m)
			cy := int(y0 + (float64(r)+0.5)*m)
			if !image.Pt(cx, cy).In(rect) {
				return "", fmt.Errorf("模块 (%d,%d) 采样越界", r, c)
			}
			sum, cnt := 0, 0
			for dy := -box; dy <= box; dy++ {
				for dx := -box; dx <= box; dx++ {
					sum += int(gray.GrayAt(cx+dx, cy+dy).Y)
					cnt++
				}
			}
			dark[r][c] = float64(sum)/float64(cnt) <= float64(th)
		}
	}
	return DecodeGrid(dark)
}

// DecodeGrid 在已采样的模块矩阵上解码(true=黑);矩阵边长须为 21/25/29/33。
func DecodeGrid(dark [][]bool) (string, error) {
	n := len(dark)
	spec, ok := specBySize(n)
	if !ok {
		return "", fmt.Errorf("矩阵边长 %d 不是 v1~v4", n)
	}
	mask, err := readFormat(dark)
	if err != nil {
		return "", err
	}

	// 之字读取数据模块(跳过功能模块),按掩码还原
	funcMod := buildFuncMap(spec)
	total := spec.dataCW + spec.blocks*spec.blockEC
	bits := make([]bool, 0, total*8+8)
	up := true
	for col := n - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for i := 0; i < n; i++ {
			r := i
			if up {
				r = n - 1 - i
			}
			for c := 0; c < 2; c++ {
				cc := col - c
				if funcMod[r][cc] {
					continue
				}
				v := dark[r][cc]
				if maskBit(mask, r, cc) {
					v = !v
				}
				bits = append(bits, v)
			}
		}
		up = !up
	}
	if len(bits) < total*8 {
		return "", fmt.Errorf("数据模块不足:需要 %d 位,读到 %d 位", total*8, len(bits))
	}
	all := make([]byte, total)
	for i := 0; i < total; i++ {
		var b byte
		for j := 0; j < 8; j++ {
			b = b<<1 | boolByte(bits[i*8+j])
		}
		all[i] = b
	}

	// 解交织 → 分块 RS 纠错 → 重组数据码字
	data := make([]byte, 0, spec.dataCW)
	for b := 0; b < spec.blocks; b++ {
		blk := make([]byte, 0, spec.blockData+spec.blockEC)
		for j := 0; j < spec.blockData; j++ {
			blk = append(blk, all[j*spec.blocks+b])
		}
		for j := 0; j < spec.blockEC; j++ {
			blk = append(blk, all[spec.dataCW+j*spec.blocks+b])
		}
		fixed, err := rsDecode(blk, spec.blockEC)
		if err != nil {
			return "", fmt.Errorf("RS 纠错失败(块 %d):%w", b, err)
		}
		data = append(data, fixed...)
	}
	return parseNumeric(data)
}

// readFormat 从两份格式信息中读出掩码号;两份都过 BCH 校验且不一致、或都失败时报错。
func readFormat(dark [][]bool) (int, error) {
	n := len(dark)
	copy1 := make([]bool, 15)
	copy2 := make([]bool, 15)
	for i := 0; i <= 5; i++ {
		copy1[i] = dark[8][i]
	}
	copy1[6] = dark[8][7]
	copy1[7] = dark[8][8]
	copy1[8] = dark[7][8]
	for i := 9; i < 15; i++ {
		copy1[i] = dark[14-i][8]
	}
	for i := 0; i < 7; i++ {
		copy2[i] = dark[n-1-i][8]
	}
	for i := 7; i < 15; i++ {
		copy2[i] = dark[8][n-15+i]
	}
	read := func(cp []bool) (int, bool) {
		var v uint16
		for i := 14; i >= 0; i-- {
			v = v<<1 | uint16(boolByte(cp[i]))
		}
		v ^= 0x5412
		data := int(v >> 10)
		rem := data
		for i := 0; i < 10; i++ {
			rem = (rem << 1) ^ ((rem >> 9) * 0x537)
		}
		if uint16(data<<10|rem) != v {
			return 0, false
		}
		return data & 7, true
	}
	m1, ok1 := read(copy1)
	m2, ok2 := read(copy2)
	switch {
	case ok1 && ok2:
		if m1 != m2 {
			return 0, fmt.Errorf("两份格式信息不一致(%d vs %d)", m1, m2)
		}
		return m1, nil
	case ok1:
		return m1, nil
	case ok2:
		return m2, nil
	default:
		return 0, fmt.Errorf("格式信息 BCH 校验失败")
	}
}

// parseNumeric 解析数据码字里的数字模式负载。
func parseNumeric(data []byte) (string, error) {
	if len(data) < 2 {
		return "", fmt.Errorf("数据码字不足")
	}
	pos := 0
	read := func(n int) (int, error) {
		if pos+n > len(data)*8 {
			return 0, fmt.Errorf("位流提前结束")
		}
		v := 0
		for i := 0; i < n; i++ {
			bit := (data[(pos+i)/8] >> uint(7-(pos+i)%8)) & 1
			v = v<<1 | int(bit)
		}
		pos += n
		return v, nil
	}
	mode, err := read(4)
	if err != nil {
		return "", err
	}
	if mode != 1 {
		return "", fmt.Errorf("非数字模式(模式指示 %d)", mode)
	}
	count, err := read(10)
	if err != nil {
		return "", err
	}
	if count < 1 {
		return "", fmt.Errorf("计数异常(%d)", count)
	}
	var sb strings.Builder
	for i := 0; i+3 <= count; i += 3 {
		v, err := read(10)
		if err != nil {
			return "", err
		}
		if v > 999 {
			return "", fmt.Errorf("数字组越界(%d)", v)
		}
		fmt.Fprintf(&sb, "%03d", v)
	}
	switch count % 3 {
	case 1:
		v, err := read(4)
		if err != nil {
			return "", err
		}
		if v > 9 {
			return "", fmt.Errorf("数字越界(%d)", v)
		}
		sb.WriteByte(byte('0' + v))
	case 2:
		v, err := read(7)
		if err != nil {
			return "", err
		}
		if v > 99 {
			return "", fmt.Errorf("数字组越界(%d)", v)
		}
		fmt.Fprintf(&sb, "%02d", v)
	}
	if sb.Len() != count {
		return "", fmt.Errorf("解码长度 %d 与计数 %d 不符", sb.Len(), count)
	}
	return sb.String(), nil
}

// finderCenters 在一条二值线上找全部 1:1:3:1:1 游程模式,
// 返回各模式的中心(即中间粗黑段的中心)与单元宽度。
type finderHit struct {
	center float64
	unit   float64
}

func finderCenters(line []bool) []finderHit {
	type seg struct {
		start int
		w     int
		dark  bool
	}
	var segs []seg
	for i := 0; i < len(line); {
		j := i
		for j < len(line) && line[j] == line[i] {
			j++
		}
		segs = append(segs, seg{start: i, w: j - i, dark: line[i]})
		i = j
	}
	var out []finderHit
	for k := 0; k+4 < len(segs); k++ {
		s := segs[k : k+5]
		if !s[0].dark || s[1].dark || !s[2].dark || s[3].dark || !s[4].dark {
			continue
		}
		unit := float64(s[0].w+s[1].w+s[3].w+s[4].w) / 4
		if unit < 0.5 {
			continue
		}
		// 四个 1 宽单元彼此误差 ≤ 60%,中间 3 宽单元在 [2,4]×unit
		maxW := max(max(s[0].w, s[1].w), max(s[3].w, s[4].w))
		minW := min(min(s[0].w, s[1].w), min(s[3].w, s[4].w))
		if minW == 0 || float64(maxW)/float64(minW) > 1.6 {
			continue
		}
		mid := s[2].w
		if float64(mid) < 2*unit || float64(mid) > 4*unit {
			continue
		}
		// 模式前后需有浅色边界(静区/分隔带),避免贴边误报
		if k > 0 && float64(segs[k-1].w) < 0.4*unit {
			continue
		}
		if k+5 < len(segs) && float64(segs[k+5].w) < 0.4*unit {
			continue
		}
		center := float64(s[2].start) + float64(mid)/2
		out = append(out, finderHit{center: center, unit: unit})
	}
	return out
}

// cluster 过滤后按 pos 排序聚类(同一定位图形在相邻扫描线上产生成串候选,
// pos 抖动 ≤ 2~3px),返回各簇均值代表。leftmost/rightmost 取最左/最右簇。
type candCluster struct {
	pos, cross, unit float64
	count            int
}

func cluster(cands []candidate, keep func(candidate) bool) []candCluster {
	const tol = 3.0
	var kept []candidate
	for _, c := range cands {
		if keep(c) {
			kept = append(kept, c)
		}
	}
	for i := 0; i < len(kept); i++ {
		for j := i + 1; j < len(kept); j++ {
			if kept[j].pos < kept[i].pos {
				kept[i], kept[j] = kept[j], kept[i]
			}
		}
	}
	var out []candCluster
	for _, c := range kept {
		if len(out) > 0 && c.pos-out[len(out)-1].pos <= tol &&
			absF(c.cross-out[len(out)-1].cross) <= tol*3 {
			last := &out[len(out)-1]
			last.pos = (last.pos*float64(last.count) + c.pos) / float64(last.count+1)
			last.cross = (last.cross*float64(last.count) + c.cross) / float64(last.count+1)
			last.unit = (last.unit*float64(last.count) + c.unit) / float64(last.count+1)
			last.count++
			continue
		}
		out = append(out, candCluster{pos: c.pos, cross: c.cross, unit: c.unit, count: 1})
	}
	return out
}

func leftmost(cs []candCluster) *candCluster {
	if len(cs) == 0 {
		return nil
	}
	best := &cs[0]
	for i := range cs[1:] {
		if cs[i+1].pos < best.pos {
			best = &cs[i+1]
		}
	}
	return best
}

// sortedDesc 按 pos 降序返回副本(前几个即最右/最下簇)。
func sortedDesc(cs []candCluster) []candCluster {
	out := append([]candCluster{}, cs...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].pos > out[i].pos {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func specBySize(n int) (versionSpec, bool) {
	for _, v := range versions {
		if v.size == n {
			return v, true
		}
	}
	return versionSpec{}, false
}

func regionOtsu(gray *image.Gray, rect image.Rectangle) uint8 {
	var hist [256]int
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		base := y * gray.Stride
		for x := rect.Min.X; x < rect.Max.X; x++ {
			hist[gray.Pix[base+x]]++
		}
	}
	total := rect.Dx() * rect.Dy()
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

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func roundF(v float64) int { return int(v + 0.5) }
