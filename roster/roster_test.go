package roster

import "testing"

func TestLoadCSV(t *testing.T) {
	data := []byte("考号,姓名,班级\n20250001,李雷,三年二班\n20250002,韩梅梅,三年二班\n")
	students, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(students) != 2 || students[0].Name != "李雷" || students[0].Class != "三年二班" {
		t.Fatalf("CSV 解析错误: %+v", students)
	}
}

func TestLoadJSONAndValidate(t *testing.T) {
	students, err := Load([]byte(`[{"reg":"20250001","name":"李雷"}]`))
	if err != nil || len(students) != 1 {
		t.Fatalf("JSON 解析失败: %v", err)
	}
	if _, err := Load([]byte(`[{"reg":"20x5","name":"李雷"}]`)); err == nil {
		t.Fatal("非数字考号应报错")
	}
	if _, err := Load([]byte("[{\"reg\":\"1\",\"name\":\"A\"},{\"reg\":\"1\",\"name\":\"B\"}]")); err == nil {
		t.Fatal("重复考号应报错")
	}
}

func TestSuggest(t *testing.T) {
	students := []Student{
		{Reg: "20250001", Name: "李雷"},
		{Reg: "20250101", Name: "韩梅梅"},
		{Reg: "20250002", Name: "王小明"},
	}
	// 8 位考号第 5 位漏涂(其余 7 位可读):2025_001 → 命中李雷
	regMarks := [][]int{
		{2}, {0}, {2}, {5}, {}, {0}, {0}, {1},
	}
	got := Suggest(regMarks, students, 3)
	if len(got) == 0 || got[0].Name != "李雷" {
		t.Fatalf("应推荐李雷,得到 %+v", got)
	}
	// 多涂位:第 8 位可能 1 或 7 → 命中李雷(…001),其余无
	regMarks2 := [][]int{{2}, {0}, {2}, {5}, {0}, {0}, {0}, {1, 7}}
	got2 := Suggest(regMarks2, students, 3)
	if len(got2) == 0 || got2[0].Name != "李雷" {
		t.Fatalf("多涂位应仍推荐李雷,得到 %+v", got2)
	}
	// 完全不可读(全空)→ 不应给出候选
	regMarks3 := make([][]int, 8)
	got3 := Suggest(regMarks3, students, 3)
	if len(got3) != 0 {
		t.Fatalf("全空明细不应推荐: %+v", got3)
	}
}

func TestSuggestByText(t *testing.T) {
	students := []Student{{Reg: "20250001", Name: "李雷"}, {Reg: "20250101", Name: "韩梅梅"}}
	if got := SuggestByText(students, "李", 5); len(got) != 1 || got[0].Name != "李雷" {
		t.Fatalf("按姓名搜索失败: %+v", got)
	}
	if got := SuggestByText(students, "2025010", 5); len(got) != 1 || got[0].Name != "韩梅梅" {
		t.Fatalf("按考号前缀搜索失败: %+v", got)
	}
}
