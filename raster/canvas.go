package raster

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

// Canvas 是 8-bit 灰度画布(0=黑,255=白),绘图原语均带边界裁剪。
type Canvas struct {
	W, H int
	Pix  []uint8
}

// NewCanvas 创建全白画布。
func NewCanvas(w, h int) *Canvas {
	c := &Canvas{W: w, H: h, Pix: make([]uint8, w*h)}
	for i := range c.Pix {
		c.Pix[i] = 255
	}
	return c
}

// Set 设置单像素(越界忽略)。
func (c *Canvas) Set(x, y int, v uint8) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	c.Pix[y*c.W+x] = v
}

// FillRect 填充矩形(左上角 + 宽高)。
func (c *Canvas) FillRect(x, y, w, h int, v uint8) {
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+w, c.W), min(y+h, c.H)
	for py := y0; py < y1; py++ {
		base := py * c.W
		for px := x0; px < x1; px++ {
			c.Pix[base+px] = v
		}
	}
}

// FillCircle 填充实心圆。
func (c *Canvas) FillCircle(cx, cy, r float64, v uint8) {
	x0, x1 := int(cx-r)-1, int(cx+r)+2
	y0, y1 := int(cy-r)-1, int(cy+r)+2
	rr := r * r
	for py := y0; py <= y1; py++ {
		dy := float64(py) - cy
		for px := x0; px <= x1; px++ {
			dx := float64(px) - cx
			if dx*dx+dy*dy <= rr {
				c.Set(px, py, v)
			}
		}
	}
}

// Ring 绘制圆环描边(rInner <= d <= rOuter 的区域)。
func (c *Canvas) Ring(cx, cy, rOuter, rInner float64, v uint8) {
	x0, x1 := int(cx-rOuter)-1, int(cx+rOuter)+2
	y0, y1 := int(cy-rOuter)-1, int(cy+rOuter)+2
	ro2, ri2 := rOuter*rOuter, rInner*rInner
	for py := y0; py <= y1; py++ {
		dy := float64(py) - cy
		for px := x0; px <= x1; px++ {
			dx := float64(px) - cx
			d2 := dx*dx + dy*dy
			if d2 <= ro2 && d2 >= ri2 {
				c.Set(px, py, v)
			}
		}
	}
}

// Line 用 Bresenham 算法画线段,thickness 为方形笔刷宽度。
func (c *Canvas) Line(x0, y0, x1, y1, thickness int, v uint8) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	half := thickness / 2
	for {
		c.FillRect(x0-half, y0-half, thickness, thickness, v)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// Text 以左上角 (x,y) 绘制点阵文本,每个字体像素放大为 scale×scale 实心块。
// 不在字体表中的字符按空格宽度跳过。
func (c *Canvas) Text(x, y int, s string, scale int, v uint8) {
	cx := x
	for _, r := range []rune(s) {
		if g, ok := glyphBits[r]; ok {
			for row := 0; row < 7; row++ {
				for col := 0; col < 5; col++ {
					if g[row]&(1<<(4-col)) != 0 {
						c.FillRect(cx+col*scale, y+row*scale, scale, scale, v)
					}
				}
			}
		}
		cx += 6 * scale
	}
}

// TextCenter 以文本包围盒中心 (cx,cy) 绘制文本。
func (c *Canvas) TextCenter(cx, cy int, s string, scale int, v uint8) {
	w := TextWidth(s, scale)
	c.Text(cx-w/2, cy-(7*scale)/2, s, scale, v)
}

// TextWidth 返回文本渲染后的像素宽度。
func TextWidth(s string, scale int) int {
	return len([]rune(s)) * 6 * scale
}

// Bounds/At/ColorModel 使 Canvas 直接实现 image.Image,便于与标准库编解码器协作。
func (c *Canvas) Bounds() image.Rectangle { return image.Rect(0, 0, c.W, c.H) }

func (c *Canvas) At(x, y int) color.Color {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return color.Gray{Y: 255}
	}
	return color.Gray{Y: c.Pix[y*c.W+x]}
}

func (c *Canvas) ColorModel() color.Model { return color.GrayModel }

// BlendMask 以覆盖度 (0~255) 把一张位图混入画布:dst = dst + (v-dst)×cov/255。
// 用于字体光栅化的抗锯齿贴字。
func (c *Canvas) BlendMask(x, y, w, h int, cov []byte, v uint8) {
	for row := 0; row < h; row++ {
		dy := y + row
		if dy < 0 || dy >= c.H {
			continue
		}
		base := dy * c.W
		for col := 0; col < w; col++ {
			dx := x + col
			if dx < 0 || dx >= c.W {
				continue
			}
			a := int(cov[row*w+col])
			if a <= 0 {
				continue
			}
			i := base + dx
			c.Pix[i] = uint8((int(c.Pix[i])*(255-a) + int(v)*a) / 255)
		}
	}
}

// ToGray 转为标准库灰度图。
func (c *Canvas) ToGray() *image.Gray {
	g := image.NewGray(image.Rect(0, 0, c.W, c.H))
	copy(g.Pix, c.Pix)
	return g
}

// SavePNG 将画布写入 PNG 文件。
func (c *Canvas) SavePNG(path string) error {
	return SavePNG(c.ToGray(), path)
}

// SavePNG 将任意图像写入 PNG 文件。
func SavePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return f.Close()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
