// Package font 提供零第三方依赖的 TrueType(.ttf/.ttc)解析与光栅化,
// 使答题卡位图渲染路径支持中文卷头/标签(此前仅 SVG 打印路径支持)。
// 仅实现 TrueType glyf 轮廓(学校常用字体均为 glyf;PostScript/CFF 的 OTTO 字体明确报错)。
package font

import (
	"fmt"
)

type table struct{ b []byte }

// Font 是一个已解析的 TrueType 字体。
type Font struct {
	data          []byte
	tabs          map[string]table
	unitsPerEm    int
	numGlyphs     int
	indexToLocFmt int
	numHMetrics   int
	advances      []uint16
	loca          []int
	lookup        func(rune) (int, bool)

	segCache  map[int][]Seg
	maskCache map[cacheKey]*Mask
}

func be16(b []byte, off int) int { return int(b[off])<<8 | int(b[off+1]) }
func be32(b []byte, off int) int {
	return int(b[off])<<24 | int(b[off+1])<<16 | int(b[off+2])<<8 | int(b[off+3])
}
func i16(b []byte, off int) int {
	v := be16(b, off)
	if v >= 0x8000 {
		v -= 0x10000
	}
	return v
}

// Parse 解析 TTF/TTC 字体数据;TTC 取第一个字体。
func Parse(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("字体数据过短")
	}
	if string(data[0:4]) == "ttcf" {
		if be32(data, 8) < 1 {
			return nil, fmt.Errorf("TTC 集合中无字体")
		}
		return parseSingle(data, be32(data, 12))
	}
	return parseSingle(data, 0)
}

func parseSingle(data []byte, off int) (*Font, error) {
	if off+12 > len(data) {
		return nil, fmt.Errorf("字体偏移越界")
	}
	ver := be32(data, off)
	if ver != 0x00010000 && ver != 0x74727565 { // 1.0 与 'true'
		return nil, fmt.Errorf("仅支持 TrueType(glyf)字体,收到版本 %08x(PostScript/CFF 请换用 .ttf)", ver)
	}
	numTables := be16(data, off+4)
	f := &Font{data: data, tabs: map[string]table{}}
	p := off + 12
	for i := 0; i < numTables; i++ {
		if p+16 > len(data) {
			return nil, fmt.Errorf("表目录越界")
		}
		tag := string(data[p : p+4])
		tOff, tLen := be32(data, p+8), be32(data, p+12)
		p += 16
		if tOff+tLen <= len(data) && tLen > 0 {
			f.tabs[tag] = table{data[tOff : tOff+tLen]}
		}
	}
	for _, need := range []string{"head", "maxp", "cmap", "loca", "glyf", "hmtx", "hhea"} {
		if _, ok := f.tabs[need]; !ok {
			return nil, fmt.Errorf("缺少必需表 %s", need)
		}
	}
	f.unitsPerEm = be16(f.tabs["head"].b, 18)
	if f.unitsPerEm <= 0 {
		return nil, fmt.Errorf("非法 unitsPerEm")
	}
	f.indexToLocFmt = i16(f.tabs["head"].b, 50)
	f.numGlyphs = be16(f.tabs["maxp"].b, 4)
	f.numHMetrics = be16(f.tabs["hhea"].b, 34)

	// hmtx:advance widths(超出 numberOfHMetrics 的字形沿用最后一个)
	hm := f.tabs["hmtx"].b
	f.advances = make([]uint16, f.numGlyphs)
	for g := 0; g < f.numGlyphs; g++ {
		idx := g
		if idx >= f.numHMetrics {
			idx = f.numHMetrics - 1
		}
		if idx*4+1 < len(hm) {
			f.advances[g] = uint16(be16(hm, idx*4))
		}
	}

	// loca
	loc := f.tabs["loca"].b
	f.loca = make([]int, f.numGlyphs+1)
	for i := 0; i <= f.numGlyphs; i++ {
		if f.indexToLocFmt == 1 {
			f.loca[i] = be32(loc, i*4)
		} else {
			f.loca[i] = be16(loc, i*2) * 2
		}
	}

	var err error
	if f.lookup, err = parseCmap(f.tabs["cmap"].b); err != nil {
		return nil, err
	}
	f.segCache = map[int][]Seg{}
	f.maskCache = map[cacheKey]*Mask{}
	return f, nil
}

// parseCmap 选取最优子表并返回 rune→glyphID 查询函数(支持 format 4 与 12)。
func parseCmap(b []byte) (func(rune) (int, bool), error) {
	numTables := be16(b, 2)
	type cand struct {
		score  int
		off    int
		format int
	}
	var best *cand
	for i := 0; i < numTables; i++ {
		pid, eid := be16(b, 4+i*8), be16(b, 4+i*8+2)
		off := be32(b, 4+i*8+4)
		if off+2 > len(b) {
			continue
		}
		format := be16(b, off)
		score := -1
		switch {
		case pid == 3 && eid == 10 && format == 12:
			score = 5
		case pid == 0 && format == 12:
			score = 4
		case pid == 3 && eid == 1 && format == 4:
			score = 3
		case pid == 0 && format == 4:
			score = 2
		case pid == 3 && eid == 1 && format == 0:
			score = 1 // 符号子表,BMP 内可用
		}
		if score >= 0 && (best == nil || score > best.score) {
			best = &cand{score: score, off: off, format: format}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("无可用 cmap 子表(仅支持 format 4/12)")
	}
	if best.format == 12 {
		return format12Lookup(b[best.off:]), nil
	}
	return format4Lookup(b[best.off:]), nil
}

func format4Lookup(b []byte) func(rune) (int, bool) {
	segCount := be16(b, 6) / 2
	ends := make([]int, segCount)
	starts := make([]int, segCount)
	deltas := make([]int, segCount)
	rangeOffs := make([]int, segCount)
	for i := 0; i < segCount; i++ {
		ends[i] = be16(b, 14+i*2)
	}
	for i := 0; i < segCount; i++ {
		starts[i] = be16(b, 16+segCount*2+i*2)
	}
	for i := 0; i < segCount; i++ {
		deltas[i] = i16(b, 16+segCount*4+i*2)
	}
	for i := 0; i < segCount; i++ {
		rangeOffs[i] = be16(b, 16+segCount*6+i*2)
	}
	rangeBase := 16 + segCount*6 // idRangeOffset[] 数组相对子表起点的字节位置
	return func(r rune) (int, bool) {
		c := int(r)
		if c > 0xFFFF {
			return 0, false
		}
		lo, hi := 0, segCount-1
		for lo <= hi {
			mid := (lo + hi) / 2
			if c > ends[mid] {
				lo = mid + 1
			} else if c < starts[mid] {
				hi = mid - 1
			} else {
				if rangeOffs[mid] == 0 {
					g := (c + deltas[mid]) & 0xFFFF
					return g, g != 0
				}
				// idRangeOffset 非零:glyphIdArray 以 idRangeOffset[mid] 条目自身位置为基准
				addr := rangeBase + mid*2 + rangeOffs[mid] + (c-starts[mid])*2
				if addr+1 >= len(b) {
					return 0, false
				}
				g := be16(b, addr)
				if g != 0 {
					g = (g + deltas[mid]) & 0xFFFF
				}
				return g, g != 0
			}
		}
		return 0, false
	}
}

func format12Lookup(b []byte) func(rune) (int, bool) {
	nGroups := be32(b, 12)
	group := func(i int) (start, end, gid int) {
		base := 16 + i*12
		return be32(b, base), be32(b, base+4), be32(b, base+8)
	}
	return func(r rune) (int, bool) {
		c := int(r)
		lo, hi := 0, nGroups-1
		for lo <= hi {
			mid := (lo + hi) / 2
			s, e, g := group(mid)
			if c > e {
				lo = mid + 1
			} else if c < s {
				hi = mid - 1
			} else {
				return g + (c - s), g != 0
			}
		}
		return 0, false
	}
}

// HasGlyph 报告字体是否包含该字符的字形。
func (f *Font) HasGlyph(r rune) bool {
	g, ok := f.lookup(r)
	return ok && g > 0 && g < f.numGlyphs
}

// Advance 返回字符在 em 单位下的宽度(像素 = Advance*sizePx/unitsPerEm)。
func (f *Font) Advance(r rune) int {
	g, ok := f.lookup(r)
	if !ok || g >= f.numGlyphs {
		if f.numGlyphs > 0 {
			return int(f.advances[0])
		}
		return 0
	}
	return int(f.advances[g])
}
