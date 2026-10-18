// Package score 按参考答案对若干份识别结果统分。
// Grade 处理纯客观卷(等权重);Merge 把客观题 OMR 对分与主观题人工/AI 评分
// 合并为一张报告 —— 报告中的逐题得分率即"讲评备课"的入口数据。
package score

import (
	"fmt"
	"sort"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// Stroke 是一条手写批注笔迹:坐标为切片宽高的归一化值(0~1),随切片任意缩放。
type Stroke struct {
	Color string    `json:"color"` // red | green
	Pts   []float64 `json:"pts"`   // x0,y0,x1,y1,… 折线点
}

// Grade 是一条人工/AI 阅卷记录(主观题)。Graded 区分"0 分"与"未阅"。
type Grade struct {
	Score  float64  `json:"score"`
	Mark   string   `json:"mark,omitempty"` // check | cross | flag(工作台快捷标注)
	Graded bool     `json:"graded"`
	Ink    []Stroke `json:"ink,omitempty"` // 手写批注笔迹
}

// GradeKey 生成评分记录的存储键:"s{学生}-q{主观题}"。
func GradeKey(student, question int) string {
	return fmt.Sprintf("s%d-q%d", student, question)
}

// StudentLine 是单个学生的成绩行。
type StudentLine struct {
	Index      int     `json:"index"` // 1-based 卷号
	RegNumber  string  `json:"regNumber"`
	RegValid   bool    `json:"regValid"`
	Objective  float64 `json:"objective"`  // 客观题得分
	Subjective float64 `json:"subjective"` // 主观题得分(未阅部分计 0)
	Score      float64 `json:"score"`      // 总分
	Total      float64 `json:"total"`      // 卷面满分
	Flagged    bool    `json:"flagged"`    // 存在异常/待复核标记
}

// QuestionStat 是单题的全班作答统计。
type QuestionStat struct {
	Index     int     `json:"index"`               // 1-based 题号(客观题在前,主观题接续编号)
	Kind      string  `json:"kind"`                // choice | judge | multiple | fillblank | subjective
	Max       float64 `json:"max"`                 // 满分
	Correct   int     `json:"correct,omitempty"`   // 客观:答对人数
	Attempted int     `json:"attempted,omitempty"` // 客观:作答人数
	Mean      float64 `json:"mean,omitempty"`      // 主观:均分
	Graded    int     `json:"graded,omitempty"`    // 主观:已阅人数
	Count     int     `json:"count"`
	Rate      float64 `json:"rate"` // 客观=对率;主观=均分/满分
}

// Report 是一次统分的完整报告(开放 JSON 格式)。
type Report struct {
	Count       int            `json:"count"`
	Total       float64        `json:"total"` // 卷面满分
	Mean        float64        `json:"mean"`
	Students    []StudentLine  `json:"students"`
	PerQuestion []QuestionStat `json:"perQuestion"`
}

// GradeObjective 纯客观卷统分:每题等权重 1 分。key 与 results.Answers 一一对应。
func GradeObjective(key []string, results []*omr.Result) *Report {
	specs := make([]card.QuestionSpec, len(key))
	for i := range specs {
		specs[i] = card.QuestionSpec{Kind: card.KindChoice, Score: 1}
	}
	return Merge(specs, key, results, nil, nil)
}

// Merge 生成客观 + 主观合并报告。
// questions/objectives 与客观题一一对应(取自 sheet.Layout,已规范化),
// subjectives 与主观题一一对应;grades 的键为 GradeKey(student, question)。
func Merge(questions []card.QuestionSpec, objectiveKey []string, results []*omr.Result,
	subjectives []card.SubjectiveSpec, grades map[string]Grade) *Report {

	if grades == nil {
		grades = map[string]Grade{}
	}
	weight := func(qi int) float64 {
		if qi < len(questions) && questions[qi].Score > 0 {
			return questions[qi].Score
		}
		return 1
	}
	kindOf := func(qi int) string {
		if qi < len(questions) && questions[qi].Kind != "" {
			return questions[qi].Kind
		}
		return card.KindChoice
	}
	subMax := func(qi int) float64 {
		if qi < len(subjectives) && subjectives[qi].Score > 0 {
			return subjectives[qi].Score
		}
		return 10
	}
	subKind := func(qi int) string {
		if qi < len(subjectives) && subjectives[qi].Kind != "" {
			return subjectives[qi].Kind
		}
		return card.KindSubjective
	}

	r := &Report{}
	r.Total = 0
	for qi := range objectiveKey {
		r.Total += weight(qi)
	}
	for qi := range subjectives {
		r.Total += subMax(qi)
	}

	// res == nil 的学生(教师视图下按班级过滤掉的)不进入报告:
	// 索引保持原位(成绩单行号/评分键不变),Count 只统计在册学生。
	r.Students = make([]StudentLine, 0, len(results))
	for si, res := range results {
		if res == nil {
			continue
		}
		line := StudentLine{Index: si + 1, RegValid: true}
		line.RegNumber = res.RegNumber
		line.RegValid = res.RegValid
		for qi := 0; qi < len(objectiveKey) && qi < len(res.Answers); qi++ {
			if res.Answers[qi] == objectiveKey[qi] {
				line.Objective += weight(qi)
			}
		}
		for qi := range subjectives {
			if g, ok := grades[GradeKey(si+1, qi+1)]; ok && g.Graded {
				line.Subjective += g.Score
				if g.Mark == "flag" {
					line.Flagged = true
				}
			}
		}
		line.Score = line.Objective + line.Subjective
		line.Total = r.Total
		r.Students = append(r.Students, line)
		r.Mean += line.Score
	}
	r.Count = len(r.Students)
	if r.Count > 0 {
		r.Mean /= float64(r.Count)
	}

	// 客观题统计
	for qi := range objectiveKey {
		st := QuestionStat{Index: qi + 1, Kind: kindOf(qi), Max: weight(qi), Count: r.Count}
		for _, res := range results {
			if res == nil || qi >= len(res.Answers) {
				continue
			}
			ans := res.Answers[qi]
			if ans != "" {
				st.Attempted++
			}
			if ans == objectiveKey[qi] {
				st.Correct++
			}
		}
		if st.Count > 0 {
			st.Rate = float64(st.Correct) / float64(st.Count)
		}
		r.PerQuestion = append(r.PerQuestion, st)
	}
	// 主观题统计
	for qi := range subjectives {
		st := QuestionStat{Index: len(objectiveKey) + qi + 1, Kind: subKind(qi), Max: subMax(qi), Count: r.Count}
		var sum float64
		for si := range results {
			if g, ok := grades[GradeKey(si+1, qi+1)]; ok && g.Graded {
				st.Graded++
				sum += g.Score
			}
		}
		if st.Graded > 0 {
			st.Mean = sum / float64(st.Graded)
		}
		if m := subMax(qi); m > 0 {
			st.Rate = st.Mean / m
		}
		r.PerQuestion = append(r.PerQuestion, st)
	}
	return r
}

// LowRateQuestions 返回得分率低于 threshold 的题目(按得分率升序),
// 对应产品方案中的"高频丢分题"——讲评课件自动生成的数据源。
func LowRateQuestions(r *Report, threshold float64) []QuestionStat {
	var out []QuestionStat
	for _, st := range r.PerQuestion {
		if st.Rate < threshold {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rate < out[j].Rate })
	return out
}
