package font

import "fmt"

// Seg 是字形轮廓的一段:直线 (X0,Y0)→(X1,Y1),或以 (QX,QY) 为控制点的二次贝塞尔。
type Seg struct {
	X0, Y0, X1, Y1 float64
	QX, QY         float64
	Quad           bool
}

// Outline 返回字符轮廓(字体单位,y 轴向上);字符缺失返回 false。
func (f *Font) Outline(r rune) ([]Seg, bool) {
	gid, ok := f.lookup(r)
	if !ok || gid == 0 || gid >= f.numGlyphs {
		return nil, false
	}
	segs, err := f.glyphSegs(gid, 0, map[int]bool{})
	if err != nil {
		return nil, false
	}
	return segs, true
}

type cpoint struct {
	x, y int
	on   bool
}

// glyphSegs 解析 glyf 字形(简单字形与组合字形,组合深度受 8 层保护)。
func (f *Font) glyphSegs(gid, depth int, seen map[int]bool) ([]Seg, error) {
	if depth > 8 {
		return nil, fmt.Errorf("组合字形嵌套过深")
	}
	if seen[gid] {
		return nil, nil
	}
	seen[gid] = true
	defer delete(seen, gid)
	if gid < 0 || gid >= f.numGlyphs {
		return nil, nil
	}
	lo, hi := f.loca[gid], f.loca[gid+1]
	glyf := f.tabs["glyf"].b
	if lo < 0 || hi > len(glyf) || lo >= hi {
		return nil, nil // 空字形
	}
	b := glyf[lo:hi]
	nCont := i16(b, 0)

	if nCont >= 0 {
		return parseSimpleGlyph(b, nCont)
	}
	return f.parseComposite(b, depth, seen)
}

func parseSimpleGlyph(b []byte, nCont int) ([]Seg, error) {
	endPts := make([]int, nCont)
	for i := 0; i < nCont; i++ {
		endPts[i] = be16(b, 10+i*2)
	}
	nPts := 0
	if nCont > 0 {
		nPts = endPts[nCont-1] + 1
	}
	p := 10 + nCont*2
	if p+2 > len(b) {
		return nil, fmt.Errorf("glyf 越界")
	}
	p += 2 + be16(b, p) // 跳过指令

	flags := make([]byte, 0, nPts)
	for len(flags) < nPts {
		if p >= len(b) {
			return nil, fmt.Errorf("flags 越界")
		}
		fl := b[p]
		p++
		flags = append(flags, fl)
		if fl&0x08 != 0 {
			if p >= len(b) {
				return nil, fmt.Errorf("flags repeat 越界")
			}
			for rep := int(b[p]); rep > 0; rep-- {
				flags = append(flags, fl)
			}
			p++
		}
	}

	pts := make([]cpoint, nPts)
	x := 0
	for i := 0; i < nPts; i++ {
		fl := flags[i]
		switch {
		case fl&0x02 != 0:
			d := int(b[p])
			if fl&0x10 == 0 {
				d = -d
			}
			p++
			x += d
		case fl&0x10 == 0:
			x += i16(b, p)
			p += 2
		}
		pts[i].x = x
	}
	y := 0
	for i := 0; i < nPts; i++ {
		fl := flags[i]
		switch {
		case fl&0x04 != 0:
			d := int(b[p])
			if fl&0x20 == 0 {
				d = -d
			}
			p++
			y += d
		case fl&0x20 == 0:
			y += i16(b, p)
			p += 2
		}
		pts[i].y = y
		pts[i].on = fl&0x01 != 0
	}

	var segs []Seg
	start := 0
	for _, end := range endPts {
		appendContour(pts[start:end+1], &segs)
		start = end + 1
	}
	return segs, nil
}

// appendContour 把一条轮廓点环转为直线/二次贝塞尔段
// (TrueType 语义:相邻两个 off-curve 点之间插入隐式 on-curve 中点)。
func appendContour(pts []cpoint, out *[]Seg) {
	n := len(pts)
	if n < 2 {
		return
	}
	start := -1
	for i := range pts {
		if pts[i].on {
			start = i
			break
		}
	}
	if start < 0 {
		// 全为 off-curve:在相邻中点处插入隐式 on 点
		np := make([]cpoint, 0, n*2)
		for i := 0; i < n; i++ {
			a, b := pts[i], pts[(i+1)%n]
			np = append(np, a, cpoint{(a.x + b.x) / 2, (a.y + b.y) / 2, true})
		}
		pts = np
		n = len(pts)
		start = 1
		if n < 3 {
			return
		}
	}
	line := func(a, b cpoint) {
		*out = append(*out, Seg{X0: float64(a.x), Y0: float64(a.y), X1: float64(b.x), Y1: float64(b.y)})
	}
	quad := func(a, c, b cpoint) {
		*out = append(*out, Seg{X0: float64(a.x), Y0: float64(a.y), X1: float64(b.x), Y1: float64(b.y),
			QX: float64(c.x), QY: float64(c.y), Quad: true})
	}
	cur := pts[start]
	consumed := 0
	i := start
	for consumed < n {
		i = (i + 1) % n
		p := pts[i]
		consumed++
		if p.on {
			line(cur, p)
			cur = p
			continue
		}
		j := (i + 1) % n
		q := pts[j]
		if q.on && consumed < n {
			quad(cur, p, q)
			cur = q
			i = j
			consumed++
		} else {
			mid := cpoint{(p.x + q.x) / 2, (p.y + q.y) / 2, true}
			quad(cur, p, mid)
			cur = mid
		}
	}
}

// parseComposite 解析组合字形(支持平移/缩放/2x2 矩阵;点匹配定位按零偏移退化)。
func (f *Font) parseComposite(b []byte, depth int, seen map[int]bool) ([]Seg, error) {
	var segs []Seg
	p := 10
	for {
		if p+4 > len(b) {
			break
		}
		flags := be16(b, p)
		gi := be16(b, p+2)
		p += 4
		dx, dy := 0, 0
		if flags&0x0001 != 0 {
			if p+4 > len(b) {
				break
			}
			dx, dy = i16(b, p), i16(b, p+2)
			p += 4
		} else {
			if p+2 > len(b) {
				break
			}
			dx, dy = int(int8(b[p])), int(int8(b[p+1]))
			p += 2
		}
		var a, bb, c, d float64 = 1, 0, 0, 1
		switch {
		case flags&0x0080 != 0: // 2x2
			if p+8 > len(b) {
				return segs, nil
			}
			a = float64(i16(b, p)) / 16384
			bb = float64(i16(b, p+2)) / 16384
			c = float64(i16(b, p+4)) / 16384
			d = float64(i16(b, p+6)) / 16384
			p += 8
		case flags&0x0040 != 0: // x/y 缩放
			if p+4 > len(b) {
				return segs, nil
			}
			a = float64(i16(b, p)) / 16384
			d = float64(i16(b, p+2)) / 16384
			p += 4
		case flags&0x0008 != 0: // 均匀缩放
			if p+2 > len(b) {
				return segs, nil
			}
			a = float64(i16(b, p)) / 16384
			d = a
			p += 2
		}
		sub, err := f.glyphSegs(gi, depth+1, seen)
		if err != nil {
			return nil, err
		}
		for _, s := range sub {
			tr := func(x, y float64) (float64, float64) {
				return a*x + c*y + float64(dx), bb*x + d*y + float64(dy)
			}
			ns := Seg{Quad: s.Quad}
			ns.X0, ns.Y0 = tr(s.X0, s.Y0)
			ns.X1, ns.Y1 = tr(s.X1, s.Y1)
			if s.Quad {
				ns.QX, ns.QY = tr(s.QX, s.QY)
			}
			segs = append(segs, ns)
		}
		if flags&0x0020 == 0 { // MORE_COMPONENTS
			break
		}
	}
	return segs, nil
}
