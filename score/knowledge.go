package score

// 知识点学情:把逐题得分率聚合成"知识点掌握度",这是智学网学情产品的数据地基。
// 社区版不做题库,但建考时给每道题打知识点标签后,就能出班级/个人掌握雷达 ——
// 完全复用已算出的逐题统计(QuestionStat),不引入任何新依赖。

import (
	"sort"

	"github.com/opengrade/opengrade/card"
)

// KnowledgeStat 是一个知识点的掌握度统计(班级维度)。
type KnowledgeStat struct {
	Name      string  `json:"name"`      // 知识点名
	Questions int     `json:"questions"` // 覆盖题数
	Max       float64 `json:"max"`       // 涉及总分
	Earned    float64 `json:"earned"`    // 实际得分合计(按题聚合)
	Rate      float64 `json:"rate"`      // 掌握度 = Earned/Max
	Answers   int     `json:"answers"`   // 作答样本数(题数×有效人数)
}

// knowledgeTagsOf 返回知识点标签 → 题号(1-based)的映射。
// questions 为客观题,subjectives 为主观题;标签取自建考录入。
type taggedQuestion struct {
	index int
	tags  []string
}

func taggedQuestions(questions []card.QuestionSpec, subjectives []card.SubjectiveSpec) []taggedQuestion {
	var out []taggedQuestion
	for i, q := range questions {
		if tags := card.SplitKnowledge(q.Knowledge); len(tags) > 0 {
			out = append(out, taggedQuestion{index: i + 1, tags: tags})
		}
	}
	base := len(questions)
	for i, s := range subjectives {
		if tags := card.SplitKnowledge(s.Knowledge); len(tags) > 0 {
			out = append(out, taggedQuestion{index: base + i + 1, tags: tags})
		}
	}
	return out
}

// KnowledgeStats 按知识点聚合全卷统计(掌握度 = 该知识点下所有题目的总得分率)。
// 掌握度按"题目分值加权"计算,更贴近真实教学口径;未被任何题标注的知识点不出现。
func KnowledgeStats(questions []card.QuestionSpec, subjectives []card.SubjectiveSpec, perQuestion []QuestionStat) []KnowledgeStat {
	byIndex := map[int]QuestionStat{}
	for _, q := range perQuestion {
		byIndex[q.Index] = q
	}
	type acc struct {
		max, earned float64
		nQ, nAns    int
	}
	accs := map[string]*acc{}
	for _, tq := range taggedQuestions(questions, subjectives) {
		q, ok := byIndex[tq.index]
		if !ok {
			continue
		}
		// 该题的实际得分合计 = 得分率 × 满分 × 有效样本数(客观用 Count,主观用已阅数)
		samples := q.Count
		if q.Kind == card.KindFillBlank || q.Kind == card.KindSubjective {
			samples = q.Graded
		}
		if samples <= 0 || q.Max <= 0 {
			continue
		}
		earned := q.Rate * q.Max * float64(samples)
		for _, name := range tq.tags {
			a := accs[name]
			if a == nil {
				a = &acc{}
				accs[name] = a
			}
			a.max += q.Max * float64(samples)
			a.earned += earned
			a.nQ++
			a.nAns += samples
		}
	}
	out := make([]KnowledgeStat, 0, len(accs))
	for name, a := range accs {
		ks := KnowledgeStat{Name: name, Questions: a.nQ, Max: a.max, Earned: a.earned, Answers: a.nAns}
		if a.max > 0 {
			ks.Rate = a.earned / a.max
		}
		out = append(out, ks)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate < out[j].Rate // 掌握度低的排前面,便于定位薄弱点
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// knowledgeIndex 返回 知识点名 → 题号列表(1-based),供个人掌握度计算。
func knowledgeIndex(questions []card.QuestionSpec, subjectives []card.SubjectiveSpec) map[string][]int {
	out := map[string][]int{}
	for _, tq := range taggedQuestions(questions, subjectives) {
		for _, name := range tq.tags {
			out[name] = append(out[name], tq.index)
		}
	}
	return out
}

// StudentKnowledge 计算单个学生在各知识点上的个人掌握度。
// perQuestionRate[qi] = 该生第 qi 题(1-based)的得分率;未作答/未阅的题不纳入分母。
// graded 标记该题是否计入(客观题始终计入;主观题须已阅)。
func StudentKnowledge(questions []card.QuestionSpec, subjectives []card.SubjectiveSpec,
	perQuestionRate map[int]float64) []KnowledgeStat {

	idx := knowledgeIndex(questions, subjectives)
	maxOf := map[int]float64{}
	for i := range questions {
		maxOf[i+1] = questions[i].Score
		if maxOf[i+1] <= 0 {
			maxOf[i+1] = 1
		}
	}
	for i := range subjectives {
		maxOf[len(questions)+i+1] = SubjectiveMax(subjectives[i])
	}
	out := make([]KnowledgeStat, 0, len(idx))
	for name, qs := range idx {
		ks := KnowledgeStat{Name: name}
		for _, qi := range qs {
			rate, ok := perQuestionRate[qi]
			if !ok {
				continue // 该题本生无有效得分(未阅/未答)
			}
			m := maxOf[qi]
			ks.Questions++
			ks.Max += m
			ks.Earned += rate * m
		}
		if ks.Max > 0 {
			ks.Rate = ks.Earned / ks.Max
		}
		out = append(out, ks)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate < out[j].Rate
		}
		return out[i].Name < out[j].Name
	})
	return out
}
