package score

// 选做题判分(版式 v3):同组题互为"多选一",卡面题号区涂点决定计哪一题。
// 智学网同类机制的关键语义在这里落地:
//   - 判分只取所选:学生涂了哪题,只有那题的批阅得分计入总分;
//   - 漏涂/多涂回退:组内恰有一题完成批阅时取该题(教师批了学生实际作答的那题),
//     否则整组计 0 分且不进该组任何一题的统计——宁可缺数据,不进脏数据;
//   - 卷面满分只计一组一题(取组内最高满分),及格线/优秀线等比例阈值随之修正。

import (
	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// ElectiveGroup 是一组互斥的选做题(版式 v3)。
type ElectiveGroup struct {
	Key     string // 建考时的组标签
	Members []int  // 组内主观题号(1-based,升序,与题号区涂点顺序一致)
}

// ElectiveGroups 汇总版式中的选做组(按组在卷面上首次出现的顺序)。
func ElectiveGroups(subjectives []card.SubjectiveSpec) []ElectiveGroup {
	idx := map[string]*ElectiveGroup{}
	var order []string
	for i, s := range subjectives {
		if s.Elective == "" {
			continue
		}
		g := idx[s.Elective]
		if g == nil {
			g = &ElectiveGroup{Key: s.Elective}
			idx[s.Elective] = g
			order = append(order, s.Elective)
		}
		g.Members = append(g.Members, i+1)
	}
	out := make([]ElectiveGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *idx[k])
	}
	return out
}

// GroupOf 返回主观题号 qNo(1-based)所属的选做组;非选做题返回 nil。
func GroupOf(groups []ElectiveGroup, qNo int) *ElectiveGroup {
	for gi := range groups {
		for _, q := range groups[gi].Members {
			if q == qNo {
				return &groups[gi]
			}
		}
	}
	return nil
}

// electiveChoice 返回某生在某组中判分计入的主观题号;0 = 该组无一题计入。
// 题号区唯一涂选 → 取所涂;否则组内恰一题完成批阅 → 取该题;再否则计 0。
func electiveChoice(g *ElectiveGroup, subjectives []card.SubjectiveSpec,
	grades map[string]Grade, res *omr.Result, student int) int {

	if res != nil && len(res.Electives) > 0 {
		if marks := res.Electives[g.Members[0]]; len(marks) == 1 && marks[0] >= 0 && marks[0] < len(g.Members) {
			return g.Members[marks[0]]
		}
	}
	// 漏涂/多涂(或手工补录卷无题号区数据):按批阅结果回退
	var graded []int
	for _, q := range g.Members {
		if _, done, _ := SubjectiveScore(subjectives[q-1], grades, student, q); done {
			graded = append(graded, q)
		}
	}
	if len(graded) == 1 {
		return graded[0]
	}
	return 0
}

// ElectiveExcluded 返回某生因"只取所选"而不计入统分的主观题集合(1-based)。
// groups 为 ElectiveGroups(subjectives) 的结果;res 可为 nil(手工补录卷)。
// 统分、个人逐题明细、个人知识点掌握度共用同一份排除口径。
func ElectiveExcluded(groups []ElectiveGroup, subjectives []card.SubjectiveSpec,
	grades map[string]Grade, res *omr.Result, student int) map[int]bool {

	out := map[int]bool{}
	for gi := range groups {
		chosen := electiveChoice(&groups[gi], subjectives, grades, res, student)
		for _, q := range groups[gi].Members {
			if q != chosen {
				out[q] = true
			}
		}
	}
	return out
}
