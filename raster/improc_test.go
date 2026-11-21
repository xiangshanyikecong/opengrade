package raster

import (
	"image"
	"math"
	"math/rand"
	"testing"
)

func TestGammaGray(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 4, 1))
	for i := range g.Pix {
		g.Pix[i] = uint8(i * 60) // 0,60,120,180
	}
	g.Pix[3] = 255 // 端点
	// γ<1 提亮阴影:64 附近 ≈ 128
	out := GammaGray(g, 0.5)
	if v := out.Pix[1]; math.Abs(float64(v)-124) > 2 {
		t.Fatalf("γ=0.5 时 60 → %d, 期望 ≈124", v)
	}
	// 端点不变
	if out.Pix[0] != 0 || out.Pix[3] != 255 {
		t.Fatalf("伽马端点应不变: %v", out.Pix)
	}
	// 非法/恒等 γ 原样返回(零复制)
	if GammaGray(g, 0) != g || GammaGray(g, 1) != g {
		t.Fatal("γ<=0 或 =1 应原样返回")
	}
}

func TestMedianGray(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 9, 9))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	g.Pix[4*9+4] = 0 // 单点椒盐
	out := MedianGray(g, 3)
	if out.Pix[4*9+4] != 255 {
		t.Fatalf("孤立椒盐点应被中值滤除,得到 %d", out.Pix[4*9+4])
	}
	// 边缘保持:左半黑右半白的分界不被模糊
	g2 := image.NewGray(image.Rect(0, 0, 9, 9))
	for i := range g2.Pix {
		g2.Pix[i] = 255
	}
	for y := 0; y < 9; y++ {
		for x := 0; x < 5; x++ {
			g2.Pix[y*9+x] = 0
		}
	}
	out2 := MedianGray(g2, 3)
	if out2.Pix[4] != 0 || out2.Pix[5] != 255 {
		t.Fatalf("中值滤波应保边: %v %v", out2.Pix[4], out2.Pix[5])
	}
	if MedianGray(g, 4) != g {
		t.Fatal("不支持的窗口应原样返回")
	}
}

func TestCLAHEGray(t *testing.T) {
	// 低对比随机纹理(每块都能见到全部灰度级):均衡后动态范围应显著拉开
	g := image.NewGray(image.Rect(0, 0, 64, 64))
	rng := rand.New(rand.NewSource(7))
	for i := range g.Pix {
		g.Pix[i] = uint8(120 + rng.Intn(32)) // 120..151
	}
	out := CLAHEGray(g, 8, 0)
	lo, hi := 255, 0
	for _, v := range out.Pix {
		if int(v) < lo {
			lo = int(v)
		}
		if int(v) > hi {
			hi = int(v)
		}
	}
	if hi-lo <= 60 {
		t.Fatalf("CLAHE 应拉开低对比动态范围: [%d,%d]", lo, hi)
	}
	// 输出应保持确定性(同输入同输出)
	out2 := CLAHEGray(g, 8, 0)
	for i := range out2.Pix {
		if out2.Pix[i] != out.Pix[i] {
			t.Fatal("CLAHE 应确定性")
		}
	}
}
