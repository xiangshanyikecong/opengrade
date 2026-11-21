package omr

import (
	"fmt"
	"image"
)

// Homography 是 3x3 单应矩阵(行主序 h11..h33,约定 h33=1)。
type Homography [9]float64

// FitHomography 用 4 组对应点拟合 from→to 的单应变换(DLT,固定 h33=1)。
// 答题卡场景中四点近乎共面且视角偏转有限,h33=1 的参数化是安全的。
func FitHomography(from, to []image.Point) (Homography, error) {
	if len(from) != 4 || len(to) != 4 {
		return Homography{}, fmt.Errorf("单应拟合需要 4 组对应点,收到 %d/%d", len(from), len(to))
	}
	// 8 未知量线性方程组(增广 8x9),高斯消元求唯一解
	A := make([][]float64, 8)
	row := 0
	for i := 0; i < 4; i++ {
		x, y := float64(from[i].X), float64(from[i].Y)
		u, v := float64(to[i].X), float64(to[i].Y)
		A[row] = []float64{x, y, 1, 0, 0, 0, -u * x, -u * y, u}
		A[row+1] = []float64{0, 0, 0, x, y, 1, -v * x, -v * y, v}
		row += 2
	}
	if err := solveLinear8(A); err != nil {
		return Homography{}, fmt.Errorf("单应求解退化(定位点共线或重复?): %w", err)
	}
	var h Homography
	// Gauss-Jordan 消元后,第 k 行在列 k 归一为 1,解向量位于增广列(第 8 列)
	for k := 0; k < 8; k++ {
		h[k] = A[k][8]
	}
	h[8] = 1
	return h, nil
}

// Apply 把 (x,y) 映射到目标平面。
func (h Homography) Apply(x, y float64) (float64, float64) {
	d := h[6]*x + h[7]*y + h[8]
	return (h[0]*x + h[1]*y + h[2]) / d, (h[3]*x + h[4]*y + h[5]) / d
}

// solveLinear8 用带部分主元的高斯消元解 8x8 线性方程组(增广矩阵 8x9)。
func solveLinear8(A [][]float64) error {
	const n = 8
	for col := 0; col < n; col++ {
		pivot := col
		for r := col + 1; r < n; r++ {
			if abs64(A[r][col]) > abs64(A[pivot][col]) {
				pivot = r
			}
		}
		if abs64(A[pivot][col]) < 1e-12 {
			return fmt.Errorf("矩阵奇异(主元为零,列 %d)", col)
		}
		A[col], A[pivot] = A[pivot], A[col]
		pv := A[col][col]
		for j := col; j <= n; j++ {
			A[col][j] /= pv
		}
		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			f := A[r][col]
			if f == 0 {
				continue
			}
			for j := col; j <= n; j++ {
				A[r][j] -= f * A[col][j]
			}
		}
	}
	return nil
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
