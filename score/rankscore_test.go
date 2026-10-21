package score

import "testing"

func TestConvertGrades(t *testing.T) {
	// 10 人:原始分 100,95,90,85,80,75,70,65,60,50
	raw := []float64{100, 95, 90, 85, 80, 75, 70, 65, 60, 50}
	regs := []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10"}
	rows := ConvertGrades(regs, raw, DefaultGradeScale())
	if len(rows) != 10 {
		t.Fatalf("len=%d", len(rows))
	}
	// 最高分赋分应为 100(A 等上界),最低分落入 E 等
	if rows[0].Converted != 100 {
		t.Fatalf("最高分赋分=%v", rows[0].Converted)
	}
	if rows[0].Grade != "A" {
		t.Fatalf("最高分等级=%s", rows[0].Grade)
	}
	// 结果按赋分降序,原始分最高者在前
	if rows[0].Reg != "01" {
		t.Fatalf("排序异常:%v", rows[0])
	}
	last := rows[len(rows)-1]
	if last.Raw != 50 {
		t.Fatalf("末位原始分=%v", last.Raw)
	}
	// 赋分单调不增
	for i := 1; i < len(rows); i++ {
		if rows[i].Converted > rows[i-1].Converted {
			t.Fatalf("赋分非单调:%v", rows)
		}
	}
	for _, r := range rows {
		t.Logf("%s 原始%v → %s %v 名次%d", r.Reg, r.Raw, r.Grade, r.Converted, r.Rank)
	}
}

func TestConvertGradesTies(t *testing.T) {
	raw := []float64{80, 80, 60, 60}
	regs := []string{"a", "b", "c", "d"}
	rows := ConvertGrades(regs, raw, DefaultGradeScale())
	// 同分赋分相同
	m := map[float64]float64{}
	for _, r := range rows {
		m[r.Raw] = r.Converted
	}
	if m[80] <= m[60] {
		t.Fatalf("80 分赋分应高于 60:%v", m)
	}
}

func TestConvertGradesEmpty(t *testing.T) {
	if r := ConvertGrades(nil, nil, DefaultGradeScale()); r != nil {
		t.Fatal("空输入应返回 nil")
	}
}

func TestFineGradeScaleCount(t *testing.T) {
	// 官方 3+1+2 口径:A/B/C/D 各 5 档 + E 单一等级 = 21 档
	s := FineGradeScale()
	if len(s.Bands) != 21 {
		t.Fatalf("细分档数 = %d,期望 21", len(s.Bands))
	}
	if last := s.Bands[len(s.Bands)-1]; last.Name != "E" {
		t.Fatalf("末档应为单一 E 等,得到 %q", last.Name)
	}
}
