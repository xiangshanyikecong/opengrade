package font

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/opengrade/opengrade/raster"
)

// Mask 是按 (gid,size) 缓存的字形覆盖度位图。
// OffX/OffY 是位图左上角相对"画笔位置(基线)"的偏移。
type Mask struct {
	W, H       int
	OffX, OffY int
	Cov        []byte // 0~255 覆盖度
}

type cacheKey struct{ gid, size int }

// mask 渲染并缓存字形位图:2×2 超采样 + 盒式降采样获得灰度抗锯齿。
func (f *Font) mask(gid, sizePx int) *Mask {
	key := cacheKey{gid, sizePx}
	if m, ok := f.maskCache[key]; ok {
		return m
	}
	segs, ok := f.segCache[gid]
	if !ok {
		segs, _ = f.glyphSegs(gid, 0, map[int]bool{})
		f.segCache[gid] = segs
	}
	if len(segs) == 0 {
		m := &Mask{}
		f.maskCache[key] = m
		return m
	}
	sc := float64(sizePx) / float64(f.unitsPerEm)
	xmin, xmax, ymin, ymax := 1e18, -1e18, 1e18, -1e18
	for _, s := range segs {
		for _, p := range [][2]float64{{s.X0, s.Y0}, {s.X1, s.Y1}} {
			xmin = min(xmin, p[0])
			xmax = max(xmax, p[0])
			ymin = min(ymin, p[1])
			ymax = max(ymax, p[1])
		}
	}
	bx0 := int(xmin*sc) - 1
	baseline := int(ymax*sc) + 1
	w := int(xmax*sc) - bx0 + 2
	h := baseline - int(ymin*sc) + 2
	if w <= 0 || h <= 0 || w*h > 4<<20 {
		m := &Mask{}
		f.maskCache[key] = m
		return m
	}

	// 2× 超采样二值栅格
	w2, h2 := w*2, h*2
	grid := make([]byte, w2*h2)
	// 展平二次贝塞尔为短直线后做非零环绕扫描线填充
	type e struct{ x0, y0, x1, y1, dir float64 }
	var edges []e
	addEdge := func(a, b [2]float64) {
		ay := (float64(baseline) - a[1]*sc) * 2
		by := (float64(baseline) - b[1]*sc) * 2
		if ay == by {
			return
		}
		dir := 1.0
		if by < ay {
			dir = -1
		}
		edges = append(edges, e{a[0]*sc*2 - float64(bx0*2), ay, b[0]*sc*2 - float64(bx0*2), by, dir})
	}
	for _, s := range segs {
		if !s.Quad {
			addEdge([2]float64{s.X0, s.Y0}, [2]float64{s.X1, s.Y1})
			continue
		}
		const steps = 6
		prev := [2]float64{s.X0, s.Y0}
		for i := 1; i <= steps; i++ {
			t := float64(i) / steps
			mt := 1 - t
			cur := [2]float64{
				mt*mt*s.X0 + 2*mt*t*s.QX + t*t*s.X1,
				mt*mt*s.Y0 + 2*mt*t*s.QY + t*t*s.Y1,
			}
			addEdge(prev, cur)
			prev = cur
		}
	}
	for row := 0; row < h2; row++ {
		yc := float64(row) + 0.5
		type xw struct{ x, dir float64 }
		var xs []xw
		for _, ed := range edges {
			y0, y1 := ed.y0, ed.y1
			if y0 > y1 {
				y0, y1 = y1, y0
			}
			if yc < y0 || yc >= y1 {
				continue
			}
			t := (yc - ed.y0) / (ed.y1 - ed.y0)
			xs = append(xs, xw{ed.x0 + t*(ed.x1-ed.x0), ed.dir})
		}
		if len(xs) == 0 {
			continue
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].x < xs[j].x })
		// 非零环绕:相邻交叉点之间的环绕数由左交叉点累积值决定
		wind := 0.0
		for i := 0; i < len(xs); i++ {
			xa := int(xs[i].x)
			wind += xs[i].dir
			if wind == 0 || i+1 >= len(xs) {
				continue
			}
			xb := int(xs[i+1].x)
			for x := max(0, xa); x < min(w2, xb); x++ {
				grid[row*w2+x] = 1
			}
		}
	}

	cov := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					sum += int(grid[(y*2+dy)*w2+x*2+dx])
				}
			}
			cov[y*w+x] = byte(sum * 255 / 4)
		}
	}
	m := &Mask{W: w, H: h, OffX: -bx0 + 1, OffY: -baseline, Cov: cov}
	f.maskCache[key] = m
	return m
}

// WidthPx 返回字符串在指定像素字号下的宽度。
func (f *Font) WidthPx(s string, sizePx int) int {
	w := 0
	for _, r := range s {
		w += f.Advance(r)
	}
	return int(float64(w) * float64(sizePx) / float64(f.unitsPerEm))
}

// Draw 以左上角 (x,yTop) 绘制文本(基线 = yTop + 0.88×字号)。
func (f *Font) Draw(c *raster.Canvas, x, yTop int, s string, sizePx int, v uint8) {
	ascent := int(float64(sizePx) * 0.88)
	pen := x
	for _, r := range s {
		if g, ok := f.lookup(r); ok && g > 0 && g < f.numGlyphs {
			m := f.mask(g, sizePx)
			if m.W > 0 {
				c.BlendMask(pen+m.OffX, yTop+ascent+m.OffY, m.W, m.H, m.Cov, v)
			}
		}
		pen += int(float64(f.Advance(r)) * float64(sizePx) / float64(f.unitsPerEm))
	}
}

// DrawCenter 以文本包围盒中心 (cx,cyTop) 绘制。
func (f *Font) DrawCenter(c *raster.Canvas, cx, cyTop int, s string, sizePx int, v uint8) {
	f.Draw(c, cx-f.WidthPx(s, sizePx)/2, cyTop, s, sizePx, v)
}

// ---- 字体发现与默认实例 ----

var (
	mu       sync.Mutex
	def      *Font
	defTried bool
)

// Discover 按优先级返回本机常见中文字体路径(OPENGRADE_FONT 环境变量优先)。
func Discover() []string {
	var cands []string
	if p := os.Getenv("OPENGRADE_FONT"); p != "" {
		cands = append(cands, p)
	}
	switch runtime.GOOS {
	case "windows":
		win := os.Getenv("WINDIR")
		if win == "" {
			win = `C:\Windows`
		}
		dir := filepath.Join(win, "Fonts")
		for _, n := range []string{"msyh.ttc", "msyhl.ttc", "simhei.ttf", "deng.ttf", "simsun.ttc", "msyhbd.ttc"} {
			cands = append(cands, filepath.Join(dir, n))
		}
	case "darwin":
		cands = append(cands,
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/Library/Fonts/Arial Unicode.ttf")
	default:
		for _, pat := range []string{
			"/usr/share/fonts/**/*CJK*",
			"/usr/share/fonts/**/wqy*",
			"/usr/share/fonts/**/NotoSansCJK*",
			"/usr/share/fonts/**/DroidSansFallback*",
			"/usr/share/fonts/**/SourceHanSans*",
		} {
			m, _ := filepath.Glob(pat)
			cands = append(cands, m...)
		}
	}
	return cands
}

// LoadFile 从磁盘加载字体。
func LoadFile(path string) (*Font, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return f, nil
}

// SetDefault 显式设置默认字体(测试与高级用法)。
func SetDefault(f *Font) {
	mu.Lock()
	defer mu.Unlock()
	def, defTried = f, true
}

// Default 惰性加载本机第一个可用中文字体;找不到返回 nil(调用方退化为内置点阵)。
func Default() *Font {
	mu.Lock()
	defer mu.Unlock()
	if defTried {
		return def
	}
	defTried = true
	for _, p := range Discover() {
		if f, err := LoadFile(p); err == nil {
			def = f
			break
		}
	}
	return def
}
