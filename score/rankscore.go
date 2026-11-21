package score

import (
	"fmt"
	"sort"
)

// 新高考等级赋分(3+1+2 主流方案):把再选科目的卷面原始分按全样本排名
// 划分为 A~E 五个等级,再在等级内线性插值换算为赋分成绩。
// 学校可自定义各等级人数占比与赋分区间(见 GradeScale)。
//
// 这是统分后的"处理层":原始分照常统计,赋分只作为再选科目的呈现口径。

// GradeBand 是一个等级的人数占比与赋分区间。
type GradeBand struct {
	Name  string  `json:"name"`  // 等级名(A/B/C/D/E)
	Ratio float64 `json:"ratio"` // 人数占比,合计应为 1
	Low   float64 `json:"low"`   // 赋分下界
	High  float64 `json:"high"`  // 赋分上界
}

// GradeScale 是等级赋分方案。零值即用默认(3+1+2 五等)。
type GradeScale struct {
	Bands []GradeBand `json:"bands,omitempty"`
	Start float64     `json:"start,omitempty"` // 赋分起点(原分 0 的保底),缺省 30
}

// DefaultGradeScale 返回 3+1+2 通用五等划分(全省/全校适用):
//
//	A 前 15%(100~86) · B 次 35%(85~71) · C 次 35%(70~56) · D 次 13%(55~41) · E 末 2%(40~30)
func DefaultGradeScale() GradeScale {
	return GradeScale{Bands: []GradeBand{
		{Name: "A", Ratio: 0.15, Low: 86, High: 100},
		{Name: "B", Ratio: 0.35, Low: 71, High: 85},
		{Name: "C", Ratio: 0.35, Low: 56, High: 70},
		{Name: "D", Ratio: 0.13, Low: 41, High: 55},
		{Name: "E", Ratio: 0.02, Low: 30, High: 40},
	}, Start: 30}
}

// FineGradeScale 返回新高考 21 级细分档:官方 3+1+2 方案中 A/B/C/D 各等级内
// 按 3 分一档细分(各 5 档),E 等不细分(单一等级),合计 4×5+1=21 档。
// 用于更精细的赋分呈现。
func FineGradeScale() GradeScale {
	sub := func(name string, ratio float64, low, high float64) []GradeBand {
		var out []GradeBand
		steps := int((high-low)/3) + 1 // 3 分一档
		for i := 0; i < steps; i++ {
			lo := low + float64(3*i)
			hi := lo + 3
			if hi > high {
				hi = high
			}
			out = append(out, GradeBand{
				Name:  fmt.Sprintf("%s%d", name, i+1),
				Ratio: ratio / float64(steps),
				Low:   lo, High: hi,
			})
		}
		return out
	}
	bands := []GradeBand{}
	bands = append(bands, sub("A", 0.15, 86, 100)...)
	bands = append(bands, sub("B", 0.35, 71, 85)...)
	bands = append(bands, sub("C", 0.35, 56, 70)...)
	bands = append(bands, sub("D", 0.13, 41, 55)...)
	bands = append(bands, GradeBand{Name: "E", Ratio: 0.02, Low: 30, High: 40}) // E 等不细分
	return GradeScale{Bands: bands, Start: 30}
}

func (g GradeScale) bands() []GradeBand {
	if len(g.Bands) == 0 {
		return DefaultGradeScale().Bands
	}
	return g.Bands
}

// ConvertRow 是一个学生的原始分 → 赋分结果。
type ConvertRow struct {
	Reg       string  `json:"reg"`       // 考号(供跨表对齐)
	Raw       float64 `json:"raw"`       // 卷面原始分
	Grade     string  `json:"grade"`     // 等级(A~E)
	Converted float64 `json:"converted"` // 赋分成绩(取整)
	Rank      int     `json:"rank"`      // 原始分排名(并列同名次)
}

// ConvertGrades 按等级赋分方案换算一组原始分(raw 与 regs 一一对应)。
// 同分学生赋分相同(并列同名次,不拆散)。返回结果按赋分降序。
func ConvertGrades(regs []string, raw []float64, scale GradeScale) []ConvertRow {
	n := len(raw)
	if n == 0 {
		return nil
	}
	bands := scale.bands()

	// 原始分降序 → 排名(并列同名次)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return raw[idx[a]] > raw[idx[b]] })
	ranks := make([]int, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && raw[idx[j+1]] == raw[idx[i]] {
			j++
		}
		for k := i; k <= j; k++ {
			ranks[idx[k]] = i + 1
		}
		i = j + 1
	}

	// 各等级的累计人数边界
	cum := make([]int, len(bands))
	acc := 0
	for i, b := range bands {
		acc += int(float64(n)*b.Ratio + 0.5)
		cum[i] = acc
	}
	if cum[len(cum)-1] < n {
		cum[len(cum)-1] = n // 末等兜住剩余(四舍五入误差)
	}

	gradeOf := func(rank int) int { // rank 1-based
		for i, c := range cum {
			if rank <= c {
				return i
			}
		}
		return len(bands) - 1
	}

	rows := make([]ConvertRow, n)
	for i := range rows {
		gi := gradeOf(ranks[i])
		b := bands[gi]
		lo, hi := rawOfGrade(idx, raw, cum, gi)
		rows[i] = ConvertRow{Reg: regs[i], Raw: raw[i], Grade: b.Name, Rank: ranks[i]}
		rows[i].Converted = interpolate(raw[i], lo, hi, b)
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].Converted > rows[b].Converted })
	return rows
}

// rawOfGrade 统计某等级内原始分的最高/最低值(排除 0 分保底影响)。
func rawOfGrade(idx []int, raw []float64, cum []int, gi int) (lo, hi float64) {
	start := 0
	if gi > 0 {
		start = cum[gi-1]
	}
	end := cum[gi]
	if end > len(idx) {
		end = len(idx)
	}
	for k := start; k < end; k++ {
		v := raw[idx[k]]
		if k == start || v > hi {
			hi = v
		}
		if k == start || v < lo {
			lo = v
		}
	}
	return
}

// interpolate 在等级内线性插值:原始分越高,赋分越接近该等级上界。
// 等级内原始分区间退化(全员同分)时取等级上界。
func interpolate(v, lo, hi float64, b GradeBand) float64 {
	if hi <= lo {
		return round0(b.High)
	}
	t := (v - lo) / (hi - lo)
	return round0(b.Low + t*(b.High-b.Low))
}

func round0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int(v + 0.5))
}
