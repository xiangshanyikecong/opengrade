package font

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/opengrade/opengrade/raster"
)

// ---- 手工构造最小 TrueType 字体:gid1='A'(三角),gid2='中'(环形,验证非零环绕挖洞) ----

func w16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.BigEndian, v) }
func w32(b *bytes.Buffer, v uint32) { _ = binary.Write(b, binary.BigEndian, v) }
func wi16(b *bytes.Buffer, v int16) { _ = binary.Write(b, binary.BigEndian, v) }

func buildTestFont() []byte {
	// gid1 'A':单轮廓三角,3 个 on-curve 点
	g1 := func() []byte {
		var b bytes.Buffer
		wi16(&b, 1)
		wi16(&b, 0)
		wi16(&b, 0)
		wi16(&b, 1000)
		wi16(&b, 1400)
		w16(&b, 2) // endPts[0]=2
		w16(&b, 0) // 指令长度
		for i := 0; i < 3; i++ {
			b.WriteByte(0x01) // on-curve,坐标均为 int16 增量
		}
		for _, v := range []int16{0, 500, 500} {
			wi16(&b, v)
		}
		for _, v := range []int16{0, 1400, -1400} {
			wi16(&b, v)
		}
		return b.Bytes()
	}()
	// gid2 '中':外圈逆时针 + 内圈顺时针(非零环绕挖洞)
	g2 := func() []byte {
		var b bytes.Buffer
		wi16(&b, 2)
		wi16(&b, 0)
		wi16(&b, 0)
		wi16(&b, 1000)
		wi16(&b, 1000)
		w16(&b, 3) // 轮廓 1:点 0..3
		w16(&b, 7) // 轮廓 2:点 4..7
		w16(&b, 0)
		for i := 0; i < 8; i++ {
			b.WriteByte(0x01)
		}
		for _, v := range []int16{0, 1000, 0, -1000, 300, 0, 400, 0} {
			wi16(&b, v) // x 增量
		}
		for _, v := range []int16{0, 0, 1000, 0, -700, 400, 0, -400} {
			wi16(&b, v) // y 增量
		}
		return b.Bytes()
	}()

	glyf := append(append([]byte{}, g1...), g2...) // gid0 空
	loca := []uint32{0, 0, uint32(len(g1)), uint32(len(glyf))}

	var cmap bytes.Buffer
	w16(&cmap, 0) // version
	w16(&cmap, 1) // numTables
	w16(&cmap, 3) // platformID
	w16(&cmap, 1) // encodingID
	w32(&cmap, 12)
	w16(&cmap, 4)      // format
	w16(&cmap, 32)     // length(3 段)
	w16(&cmap, 0)      // language
	w16(&cmap, 6)      // segCountX2
	w16(&cmap, 8)      // searchRange
	w16(&cmap, 1)      // entrySelector
	w16(&cmap, 0xFFFE) // rangeShift(占位,解析器不读)
	w16(&cmap, 0x41)   // end[0]
	w16(&cmap, 0x4E2D)
	w16(&cmap, 0xFFFF)
	w16(&cmap, 0) // reservedPad
	w16(&cmap, 0x41)
	w16(&cmap, 0x4E2D)
	w16(&cmap, 0xFFFF)
	wi16(&cmap, 1-0x41)   // idDelta[0] → gid1
	wi16(&cmap, 2-0x4E2D) // idDelta[1] → gid2
	wi16(&cmap, 1)        // idDelta[2] → 0
	w16(&cmap, 0)
	w16(&cmap, 0)
	w16(&cmap, 0)

	var head bytes.Buffer
	w32(&head, 0x00010000)
	w32(&head, 0)
	w32(&head, 0)
	w32(&head, 0x5F0F3CF5)
	w16(&head, 0)
	w16(&head, 1000) // unitsPerEm @18
	head.Write(make([]byte, 16))
	wi16(&head, 0)
	wi16(&head, 0)
	wi16(&head, 1000)
	wi16(&head, 1400)
	w16(&head, 0)
	w16(&head, 0)
	wi16(&head, 0)
	wi16(&head, 1) // indexToLocFormat = long @50
	wi16(&head, 0)

	var hhea bytes.Buffer
	w32(&hhea, 0x00010000)
	wi16(&hhea, 880)
	wi16(&hhea, -120)
	wi16(&hhea, 0)
	w16(&hhea, 1000)
	wi16(&hhea, 0)              // minLSB
	wi16(&hhea, 0)              // minRSB
	wi16(&hhea, 0)              // xMaxExtent
	wi16(&hhea, 1)              // caretSlopeRise
	wi16(&hhea, 0)              // caretSlopeRun
	wi16(&hhea, 0)              // caretOffset
	hhea.Write(make([]byte, 8)) // 保留字段
	wi16(&hhea, 0)              // metricDataFormat
	w16(&hhea, 3)               // numberOfHMetrics @34

	var maxp bytes.Buffer
	w32(&maxp, 0x00010000)
	w16(&maxp, 3)
	maxp.Write(make([]byte, 26))

	var hmtx bytes.Buffer
	for _, adv := range []uint16{1000, 700, 1000} {
		w16(&hmtx, adv)
		w16(&hmtx, 0)
	}

	var locaBuf bytes.Buffer
	for _, v := range loca {
		w32(&locaBuf, v)
	}

	tables := map[string][]byte{
		"cmap": cmap.Bytes(), "glyf": glyf, "head": head.Bytes(),
		"hhea": hhea.Bytes(), "hmtx": hmtx.Bytes(), "loca": locaBuf.Bytes(), "maxp": maxp.Bytes(),
	}
	names := []string{"cmap", "glyf", "head", "hhea", "hmtx", "loca", "maxp"}
	var out bytes.Buffer
	w32(&out, 0x00010000)
	w16(&out, uint16(len(names)))
	w16(&out, 16*4)
	w16(&out, 2)
	w16(&out, 16*(4-4))
	off := uint32(12 + 16*len(names))
	// 记录区(表按 tag 排序,实际数据区紧随其后)
	type rec struct {
		name string
		data []byte
		off  uint32
	}
	var recs []rec
	for _, n := range names {
		recs = append(recs, rec{n, tables[n], off})
		off += uint32((len(tables[n]) + 3) &^ 3)
	}
	for _, r := range recs {
		out.Write([]byte(r.name))
		w32(&out, 0)
		w32(&out, r.off)
		w32(&out, uint32(len(r.data)))
	}
	for _, r := range recs {
		out.Write(r.data)
		for pad := (len(r.data) % 4); pad != 0 && pad < 4; pad++ {
			out.WriteByte(0)
		}
	}
	return out.Bytes()
}

func mustTestFont(t *testing.T) *Font {
	t.Helper()
	f, err := Parse(buildTestFont())
	if err != nil {
		t.Fatalf("解析手工字体失败: %v", err)
	}

	return f
}

func TestOutline(t *testing.T) {
	f := mustTestFont(t)
	if !f.HasGlyph('A') || !f.HasGlyph('中') {
		t.Fatal("cmap 应命中 A 与 中")
	}
	if f.HasGlyph('Z') {
		t.Fatal("Z 不应在字体中")
	}
	segsA, ok := f.Outline('A')
	if !ok || len(segsA) != 3 {
		t.Fatalf("'A' 应为 3 段直线,得到 %d 段(ok=%v)", len(segsA), ok)
	}
	segsC, ok := f.Outline('中')
	if !ok || len(segsC) != 8 {
		t.Fatalf("'中' 应为 8 段直线,得到 %d 段", len(segsC))
	}
	if adv := f.Advance('A'); adv != 700 {
		t.Fatalf("'A' advance = %d, 期望 700", adv)
	}
}

func TestMaskHole(t *testing.T) {
	f := mustTestFont(t)
	m := f.mask(2, 40) // '中'
	if m.W == 0 {
		t.Fatal("mask 为空")
	}
	// 外圈应有覆盖;洞中心应无覆盖
	edgeInk, centerInk := 0, 0
	for _, p := range m.Cov {
		if p > 128 {
			edgeInk++
		}
	}
	cx, cy := m.W/2, m.H/2
	if m.Cov[cy*m.W+cx] > 0 {
		centerInk++
	}
	if edgeInk < 20 {
		t.Fatalf("外圈墨迹过少: %d", edgeInk)
	}
	if centerInk != 0 {
		t.Fatal("环形字中心应被挖空(非零环绕失效)")
	}
}

func TestDrawString(t *testing.T) {
	f := mustTestFont(t)
	c := raster.NewCanvas(400, 60)
	f.Draw(c, 10, 5, "A中", 32, 0)
	ink := 0
	for _, p := range c.Pix {
		if p < 128 {
			ink++
		}
	}
	if ink == 0 {
		t.Fatal("绘制后无墨迹")
	}
	if w := f.WidthPx("A中", 32); w < 40 || w > 80 {
		t.Fatalf("WidthPx = %d,期望 40~80", w)
	}
}

// TestSystemFontDiscovery 冒烟测试:本机存在中文字体时渲染真实字符串。
func TestSystemFontDiscovery(t *testing.T) {
	f := Default()
	if f == nil {
		t.Skip("本机未发现可用中文字体,跳过")
	}
	c := raster.NewCanvas(600, 60)
	f.Draw(c, 5, 8, "高一(3)班 月考答题卡", 30, 0)
	ink := 0
	for _, p := range c.Pix {
		if p < 128 {
			ink++
		}
	}
	if ink < 50 {
		t.Fatalf("系统字体渲染墨迹过少: %d", ink)
	}
}
