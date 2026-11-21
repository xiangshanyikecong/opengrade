package omr

import (
	"math"
	"image"
	"testing"
)

func TestFitHomographyIdentity(t *testing.T) {
	pts := []image.Point{{0, 0}, {100, 0}, {0, 100}, {100, 100}}
	h, err := FitHomography(pts, pts)
	if err != nil {
		t.Fatal(err)
	}
	u, v := h.Apply(37, 61)
	// NaN 与任何比较都为假:两个坐标都必须落在容差内才算通过(评审 homography_test.go:15)
	if !(math.Abs(u-37) <= 1e-6) || !(math.Abs(v-61) <= 1e-6) {
		t.Fatalf("恒等映射失败: (%f,%f)", u, v)
	}
}

func TestFitHomographyAffine(t *testing.T) {
	// 平移 + 2 倍缩放:单应可精确表达
	from := []image.Point{{0, 0}, {100, 0}, {0, 100}, {100, 100}}
	to := []image.Point{{10, 20}, {210, 20}, {10, 220}, {210, 220}}
	h, err := FitHomography(from, to)
	if err != nil {
		t.Fatal(err)
	}
	u, v := h.Apply(50, 50)
	if !(math.Abs(u-110) <= 1e-6) || !(math.Abs(v-120) <= 1e-6) {
		t.Fatalf("仿射映射失败: (%f,%f) 期望 (110,120)", u, v)
	}
}
