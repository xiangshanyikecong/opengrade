// Package roster 提供学生名册管理与考号模糊匹配:
// 当 OMR 判读考号无效(漏涂/多涂)时,用"可读位精确匹配"从名册推荐候选,
// 交由教师在阅卷工作台一键确认 —— 方案稿『考号智能容错重影匹配』的规则版
// (手写姓名 OCR 属后续增强)。
package roster

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Student 是名册中的一名学生。Reg 为纯数字考号(与答题卡位数一致)。
type Student struct {
	Reg   string `json:"reg"`
	Name  string `json:"name"`
	Class string `json:"class,omitempty"`
}

// Load 解析名册:支持 JSON 数组或 CSV(每行 reg,name[,class],首行表头自动跳过)。
func Load(data []byte) ([]Student, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var out []Student
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("名册 JSON 解析失败: %w", err)
		}
		if err := validate(out); err != nil {
			return nil, err
		}
		return out, nil
	}
	var out []Student
	for i, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			return nil, fmt.Errorf("名册第 %d 行格式不合法(应为 考号,姓名[,班级])", i+1)
		}
		if i == 0 && !isDigits(strings.TrimSpace(parts[0])) {
			continue // 表头
		}
		s := Student{Reg: strings.TrimSpace(parts[0]), Name: strings.TrimSpace(parts[1])}
		if len(parts) > 2 {
			s.Class = strings.TrimSpace(parts[2])
		}
		out = append(out, s)
	}
	if err := validate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func validate(students []Student) error {
	seen := map[string]bool{}
	for _, s := range students {
		if !isDigits(s.Reg) || s.Reg == "" {
			return fmt.Errorf("名册中考号 %q 必须为纯数字", s.Reg)
		}
		if s.Name == "" {
			return fmt.Errorf("考号 %s 缺少姓名", s.Reg)
		}
		if seen[s.Reg] {
			return fmt.Errorf("名册中考号 %s 重复", s.Reg)
		}
		seen[s.Reg] = true
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Suggest 依据 OMR 逐位判涂明细(regMarks,来自 omr.Result)从名册推荐候选。
// 规则:所有"唯一判读位"必须精确匹配;按匹配位数排序,取前 n 个;
// 位长不一致的名册条目直接跳过。
func Suggest(regMarks [][]int, students []Student, n int) []Student {
	digitsOf := func(reg string) []int {
		out := make([]int, len(reg))
		for i, r := range reg {
			out[i] = int(r - '0')
		}
		return out
	}
	type scored struct {
		s     Student
		score int
	}
	var cands []scored
	for _, st := range students {
		dg := digitsOf(st.Reg)
		if len(dg) != len(regMarks) {
			continue
		}
		ok := true
		score := 0
		determined := 0
		for ci, marks := range regMarks {
			if len(marks) == 0 {
				continue // 该位未判读,不加分不否决
			}
			determined++
			hit := false
			for _, m := range marks {
				if m == dg[ci] {
					hit = true
					break
				}
			}
			if !hit {
				ok = false
				break
			}
			score += len(marks) // 多涂位命中也计分(候选更可信)
		}
		if ok && determined > 0 { // 完全无可读位时没有证据,不给候选
			cands = append(cands, scored{st, score})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > n {
		cands = cands[:n]
	}
	out := make([]Student, len(cands))
	for i, c := range cands {
		out[i] = c.s
	}
	return out
}

// SuggestByText 按姓名或考号前缀过滤名册(工作台手动搜索用)。
func SuggestByText(students []Student, query string, n int) []Student {
	q := strings.TrimSpace(query)
	var out []Student
	for _, s := range students {
		if q == "" || strings.Contains(s.Name, q) || strings.HasPrefix(s.Reg, q) {
			out = append(out, s)
			if len(out) >= n {
				break
			}
		}
	}
	return out
}
