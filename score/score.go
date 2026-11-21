// Package score 按参考答案对若干份识别结果统分。
// Grade 处理纯客观卷(等权重);Merge 把客观题 OMR 对分与主观题人工/AI 评分
// 合并为一张报告 —— 报告中的逐题得分率即"讲评备课"的入口数据。
package score

import (
	"fmt"
	"math"
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
// Ver 是乐观锁版本号:每次写入自增,改分请求携带旧版本号,不匹配即 409(防双开互踩)。
// By 是评分操作人账号(工作量统计用;历史数据为空)。
type Grade struct {
	Score  float64  `json:"score"`
	Mark   string   `json:"mark,omitempty"` // check | cross | flag(工作台快捷标注)
	Graded bool     `json:"graded"`
	Ink    []Stroke `json:"ink,omitempty"` // 手写批注笔迹
	Ver    int      `json:"ver,omitempty"`
	By     string   `json:"by,omitempty"`
}

// GradeKey 生成评分记录的存储键:"s{学生}-q{主观题}"。
func GradeKey(student, question int) string {
	return fmt.Sprintf("s%d-q%d", student, question)
}

// GradePartKey 生成小问(版式 v3 拆分)评分记录的存储键:"s{学生}-q{题}p{小问}"。
func GradePartKey(student, question, part int) string {
	return fmt.Sprintf("s%d-q%dp%d", student, question, part)
}

// PartStat 是一道拆分小问的作答统计(挂在 QuestionStat.Parts 下)。
type PartStat struct {
	Index  int     `json:"index"` // 小问序号(1-based)
	Label  string  `json:"label,omitempty"`
	Max    float64 `json:"max"`
	Graded int     `json:"graded,omitempty"` // 该小问已阅人数(不论大题是否阅完)
	Mean   float64 `json:"mean,omitempty"`
	Rate   float64 `json:"rate"` // = Mean/Max
}

// StudentLine 是单个学生的成绩行。
type StudentLine struct {
	Index      int     `json:"index"` // 1-based 卷号
	RegNumber  string  `json:"regNumber"`
	Name       string  `json:"name,omitempty"`      // 姓名(handler 按名册注入,导出用)
	ClassInfo  string  `json:"classInfo,omitempty"` // 班级(handler 按名册注入,走班多班用 | 连接)
	RegValid   bool    `json:"regValid"`
	Objective  float64 `json:"objective"`           // 客观题得分
	Subjective float64 `json:"subjective"`          // 主观题得分(未阅部分计 0)
	Score      float64 `json:"score"`               // 总分
	Total      float64 `json:"total"`               // 卷面满分
	Flagged    bool    `json:"flagged"`             // 存在异常/待复核标记
	Absent     bool    `json:"absent,omitempty"`    // 缺考:保留行以供名册对齐,不计入统计与排名
	Rank       int     `json:"rank"`                // 全卷排名(并列同名次,1 起)
	ClassRank  int     `json:"classRank,omitempty"` // 班级排名(handler 按名册注入)
}

// QuestionStat 是单题的全班作答统计。
type QuestionStat struct {
	Index          int            `json:"index"`               // 1-based 题号(客观题在前,主观题接续编号)
	Kind           string         `json:"kind"`                // choice | judge | multiple | fillblank | subjective
	Max            float64        `json:"max"`                 // 满分
	Correct        int            `json:"correct,omitempty"`   // 客观:答对人数
	Attempted      int            `json:"attempted,omitempty"` // 客观:作答人数
	Mean           float64        `json:"mean,omitempty"`      // 主观:均分
	Graded         int            `json:"graded,omitempty"`    // 主观:已阅人数
	Count          int            `json:"count"`
	Rate           float64        `json:"rate"`                     // 客观=对率;主观=均分/满分
	ScoreRate      float64        `json:"scoreRate,omitempty"`      // 客观=平均得分率(部分分/倒扣下与对率分离;全对全错时=对率)
	Difficulty     float64        `json:"difficulty"`               // 难度系数(=得分率)
	Discrimination float64        `json:"discrimination,omitempty"` // 区分度(高分组率−低分组率)
	Options        map[string]int `json:"options,omitempty"`        // 客观题选项人数分布(含"未答")
	Partial        int            `json:"partial,omitempty"`        // 客观:部分分人数(多选漏选等)
	Parts          []PartStat     `json:"parts,omitempty"`          // 拆分小问的逐问统计(版式 v3)
	Elective       string         `json:"elective,omitempty"`       // 选做组标签(版式 v3);非空即选做题
	Chosen         int            `json:"chosen,omitempty"`         // 选做:实际选做该题的人数(统计分母)
}

// ScoreSegment 是分数段分布的一个区间。
type ScoreSegment struct {
	Low   float64 `json:"low"`   // 下界(含)
	High  float64 `json:"high"`  // 上界(不含;最高段取到满分)
	Count int     `json:"count"` // 人数
	Rate  float64 `json:"rate"`  // 占比
}

// Stats 是全卷成绩的统计摘要(对齐传统成绩分析报表的核心维度)。
type Stats struct {
	Count    int            `json:"count"`
	Total    float64        `json:"total"`    // 卷面满分
	Mean     float64        `json:"mean"`     // 平均分
	MeanRate float64        `json:"meanRate"` // 得分率
	Max      float64        `json:"max"`      // 最高分
	Min      float64        `json:"min"`      // 最低分
	Median   float64        `json:"median"`   // 中位数
	StdDev   float64        `json:"stdDev"`   // 标准差(总体)
	Range    float64        `json:"range"`    // 极差(最高−最低)
	PassRate float64        `json:"passRate"` // 及格率(≥及格线)
	GoodRate float64        `json:"goodRate"` // 优秀率(≥优秀线)
	LowRate  float64        `json:"lowRate"`  // 低分率(<低分线)
	PassLine float64        `json:"passLine"` // 及格线
	GoodLine float64        `json:"goodLine"` // 优秀线
	LowLine  float64        `json:"lowLine"`  // 低分线
	Segments []ScoreSegment `json:"segments"` // 分数段分布
}

// Report 是一次统分的完整报告(开放 JSON 格式)。
type Report struct {
	Count       int            `json:"count"`
	AbsentCount int            `json:"absentCount,omitempty"` // 缺考人数(不计入 Count 与统计)
	Total       float64        `json:"total"`                 // 卷面满分
	Mean        float64        `json:"mean"`
	Students    []StudentLine  `json:"students"`
	PerQuestion []QuestionStat `json:"perQuestion"`
	Stats       *Stats         `json:"stats,omitempty"` // 统计摘要(排名/标准差/及格率/分数段)
}

// Thresholds 是可配置的成绩阈值(按卷面满分比例,缺省 0.6/0.85/0.4)。
type Thresholds struct {
	Pass float64 `json:"pass,omitempty"` // 及格线比例
	Good float64 `json:"good,omitempty"` // 优秀线比例
	Low  float64 `json:"low,omitempty"`  // 低分线比例
}

func (t Thresholds) pass() float64 {
	if t.Pass > 0 && t.Pass <= 1 { // 允许 1.0(满分及格);仅排除 0 与越界值
		return t.Pass
	}
	return 0.6
}
func (t Thresholds) good() float64 {
	if t.Good > 0 && t.Good <= 1 {
		return t.Good
	}
	return 0.85
}
func (t Thresholds) low() float64 {
	if t.Low > 0 && t.Low <= 1 {
		return t.Low
	}
	return 0.4
}

// GradeObjective 纯客观卷统分:每题等权重 1 分。key 与 results.Answers 一一对应。
func GradeObjective(key []string, results []*omr.Result) *Report {
	specs := make([]card.QuestionSpec, len(key))
	for i := range specs {
		specs[i] = card.QuestionSpec{Kind: card.KindChoice, Score: 1}
	}
	return Merge(specs, key, results, nil, nil, nil)
}

// Merge 生成客观 + 主观合并报告。
// questions/objectives 与客观题一一对应(取自 sheet.Layout,已规范化),
// subjectives 与主观题一一对应;grades 的键为 GradeKey(student, question)。
// scheme 为全卷评分方案(取自 Layout.Scoring;nil = 传统精确匹配语义),
// 客观题判分、卷面满分与逐题统计都经它解析,保证口径同源。
func Merge(questions []card.QuestionSpec, objectiveKey []string, results []*omr.Result,
	subjectives []card.SubjectiveSpec, grades map[string]Grade, scheme *card.ScoringScheme) *Report {

	if grades == nil {
		grades = map[string]Grade{}
	}
	ruleFor := func(qi int) card.ScoringRule { return scheme.RuleFor(qi + 1) }
	specFor := func(qi int) card.QuestionSpec {
		if qi < len(questions) {
			return questions[qi]
		}
		return card.QuestionSpec{Kind: card.KindChoice}
	}
	weight := func(qi int) float64 {
		if qi < len(questions) {
			return FullScore(questions[qi], ruleFor(qi))
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
		if qi < len(subjectives) {
			return SubjectiveMax(subjectives[qi])
		}
		return 10
	}
	subKind := func(qi int) string {
		if qi < len(subjectives) && subjectives[qi].Kind != "" {
			return subjectives[qi].Kind
		}
		return card.KindSubjective
	}
	// 选做题(版式 v3):卷面满分只计每组一题(取组内最高满分),其余组员不占满分
	groups := ElectiveGroups(subjectives)
	inElective := map[int]bool{}
	for _, g := range groups {
		for _, q := range g.Members {
			inElective[q] = true
		}
	}

	r := &Report{}
	r.Total = 0
	for qi := range objectiveKey {
		if objectiveKey[qi] == "" {
			continue // 答案未录(后补窗口):该题不判分也不占满分,杜绝"白卷对空键记满分"
		}
		r.Total += weight(qi)
	}
	for qi := range subjectives {
		if !inElective[qi+1] {
			r.Total += subMax(qi)
		}
	}
	for _, g := range groups {
		best := 0.0
		for _, q := range g.Members {
			if m := subMax(q - 1); m > best {
				best = m
			}
		}
		r.Total += best
	}

	// res == nil 的学生(教师视图下按班级过滤掉的)不进入报告:
	// 索引保持原位(成绩单行号/评分键不变),Count 只统计在册学生。
	nQ := len(objectiveKey) + len(subjectives)
	earned := make([][]float64, len(results)) // [si][qi] 该生第 qi 题得分
	valid := make([][]bool, len(results))     // [si][qi] 该生该题是否有有效作答(主观题未阅则无效)

	r.Students = make([]StudentLine, 0, len(results))
	for si, res := range results {
		if res == nil || res.Voided || res.PendingVariant { // 作废卷/卷种待指派卷与被过滤(nil)的行都不进报告
			continue
		}
		if res.Absent { // 缺考:保留行供名册/趋势对齐,不进入统计与排名
			r.AbsentCount++
			r.Students = append(r.Students, StudentLine{Index: si + 1, RegNumber: res.RegNumber, RegValid: res.RegValid, Absent: true, Total: r.Total})
			continue
		}
		line := StudentLine{Index: si + 1, RegValid: true}
		line.RegNumber = res.RegNumber
		line.RegValid = res.RegValid
		excluded := ElectiveExcluded(groups, subjectives, grades, res, si+1) // 选做题:只取所选,未选不计分
		row := make([]float64, nQ)
		ok := make([]bool, nQ)
		for qi := 0; qi < len(objectiveKey) && qi < len(res.Answers); qi++ {
			if objectiveKey[qi] == "" {
				continue // 空键题不判分:作答与未答都不给分,也不计入有效作答
			}
			ok[qi] = true
			earned, _ := Evaluate(specFor(qi), ruleFor(qi), res.Answers[qi], objectiveKey[qi])
			line.Objective += earned
			row[qi] = earned
		}
		for qi := range subjectives {
			if excluded[qi+1] {
				continue // 未被所选的选做题:得分与作答状态都不进统计
			}
			if s, done, flagged := SubjectiveScore(subjectives[qi], grades, si+1, qi+1); done {
				line.Subjective += s
				row[len(objectiveKey)+qi] = s
				ok[len(objectiveKey)+qi] = true
				if flagged {
					line.Flagged = true
				}
			}
		}
		line.Score = line.Objective + line.Subjective
		line.Total = r.Total
		earned[si] = row
		valid[si] = ok
		r.Students = append(r.Students, line)
		r.Mean += line.Score
	}
	r.Count = len(r.Students) - r.AbsentCount // 实考人数(缺考不计入统计口径)
	if r.Count > 0 {
		r.Mean /= float64(r.Count)
	}

	// 客观题统计(含选项人数分布);空键题(答案未录)不进入逐题统计。
	// 判定走 Evaluate(与统分同口径):Correct=得满分人数,Partial=部分分人数,
	// ScoreRate=平均得分率(全对全错语义下与对率相等)。
	for qi := range objectiveKey {
		if objectiveKey[qi] == "" {
			continue
		}
		spec, rule := specFor(qi), ruleFor(qi)
		st := QuestionStat{Index: qi + 1, Kind: kindOf(qi), Max: FullScore(spec, rule), Count: r.Count, Options: map[string]int{}}
		if qi < len(questions) {
			for _, l := range card.OptionLetters(kindOf(qi), questions[qi].Options) {
				st.Options[l] = 0
			}
		}
		dist := map[string]int{}
		var sumEarned float64
		for _, res := range results {
			if res == nil || res.Absent || res.Voided || res.PendingVariant || qi >= len(res.Answers) {
				continue
			}
			ans := res.Answers[qi]
			if ans != "" {
				st.Attempted++
				dist[ans]++
			} else {
				dist["未答"]++
			}
			earned, verdict := Evaluate(spec, rule, ans, objectiveKey[qi])
			sumEarned += earned
			switch verdict {
			case VerdictCorrect:
				st.Correct++
			case VerdictPartial:
				st.Partial++
			}
		}
		for k, v := range dist {
			st.Options[k] = v
		}
		if st.Count > 0 {
			st.Rate = float64(st.Correct) / float64(st.Count)
			st.ScoreRate = sumEarned / (st.Max * float64(st.Count))
		}
		st.Difficulty = st.ScoreRate
		r.PerQuestion = append(r.PerQuestion, st)
	}
	// 主观题统计(拆分小问时附逐问统计,完成度按"全部小问已阅"口径;
	// 选做题只统计所选该题的学生,统计分母 = 实际选做人数)
	for qi := range subjectives {
		spec := &subjectives[qi]
		qNo := qi + 1
		st := QuestionStat{Index: len(objectiveKey) + qi + 1, Kind: subKind(qi), Max: subMax(qi), Count: r.Count}
		g := GroupOf(groups, qNo)
		var sum float64
		for si := range results {
			res := results[si]
			if res == nil || res.Absent || res.Voided || res.PendingVariant {
				continue
			}
			if g != nil {
				if electiveChoice(g, subjectives, grades, res, si+1) != qNo {
					continue // 学生选了同组另一题:不进本题统计
				}
				st.Chosen++
			}
			if s, done, _ := SubjectiveScore(*spec, grades, si+1, qNo); done {
				st.Graded++
				sum += s
			}
		}
		if st.Graded > 0 {
			st.Mean = sum / float64(st.Graded)
		}
		if m := subMax(qi); m > 0 {
			st.Rate = st.Mean / m
		}
		st.Difficulty = st.Rate
		if g != nil {
			st.Elective = g.Key
		}
		if len(spec.Parts) > 0 {
			st.Parts = make([]PartStat, len(spec.Parts))
			for pi, part := range spec.Parts {
				ps := PartStat{Index: pi + 1, Label: part.Label, Max: part.Score}
				var psum float64
				for si := range results {
					res := results[si]
					// 与主统计同口径:过滤(nil)/缺考/作废/卷种待指派卷不进小问统计;
					// 选做题只统计"判分计入本题"的学生(涂点或批阅回退),口径同上
					if res == nil || res.Absent || res.Voided || res.PendingVariant {
						continue
					}
					if g != nil && electiveChoice(g, subjectives, grades, res, si+1) != qNo {
						continue
					}
					if g2, ok := grades[GradePartKey(si+1, qi+1, pi+1)]; ok && g2.Graded {
						ps.Graded++
						psum += g2.Score
					}
				}
				if ps.Graded > 0 {
					ps.Mean = psum / float64(ps.Graded)
				}
				if part.Score > 0 {
					ps.Rate = ps.Mean / part.Score
				}
				st.Parts[pi] = ps
			}
		}
		r.PerQuestion = append(r.PerQuestion, st)
	}

	finalize(r, earned, valid)
	return r
}

// finalize 计算逐题区分度、学生排名与全卷统计摘要,挂到报告上。
// earned/valid 与 results 同索引;过滤掉的学生(earned 行缺失)与缺考行不参与。
func finalize(r *Report, earned [][]float64, valid [][]bool) {
	// 逐题区分度:按总分取高分组(前 27%)与低分组(后 27%),
	// 区分度 = 高分组的得分率 − 低分组的得分率。
	ordered := make([]int, 0, len(earned))
	for si := range earned {
		if earned[si] == nil { // 被过滤(nil)或缺考的行不进入分组
			continue
		}
		ordered = append(ordered, si)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return totalOf(earned[ordered[i]]) > totalOf(earned[ordered[j]])
	})
	groupN := int(float64(len(ordered))*0.27 + 0.5)
	if groupN < 1 && len(ordered) >= 4 {
		groupN = 1
	}
	for qi := range r.PerQuestion {
		st := &r.PerQuestion[qi]
		// 列下标用题目原始题号还原:PerQuestion 是紧凑序列(空答案键题不进),
		// 与 earned/valid 的"原位列"在有空键题时错位,按下标直取会把第 N 题的
		// 区分度算到第 N-1 题头上(或因空键列恒无效而归零)。
		col := st.Index - 1
		if groupN <= 0 || len(ordered) < 4 || st.Max <= 0 || col < 0 {
			continue
		}
		hi, hn, lo, ln := 0.0, 0, 0.0, 0
		for _, si := range ordered[:groupN] {
			if col < len(valid[si]) && valid[si][col] {
				hi += earned[si][col]
				hn++
			}
		}
		for _, si := range ordered[len(ordered)-groupN:] {
			if col < len(valid[si]) && valid[si][col] {
				lo += earned[si][col]
				ln++
			}
		}
		if hn > 0 && ln > 0 {
			st.Discrimination = hi/float64(hn)/st.Max - lo/float64(ln)/st.Max
		}
	}

	// 学生排名:并列同名次(标准竞赛排名法);缺考行无排名。
	byScore := make([]int, 0, len(r.Students))
	for i, l := range r.Students {
		if !l.Absent {
			byScore = append(byScore, i)
		}
	}
	sort.Slice(byScore, func(i, j int) bool { return r.Students[byScore[i]].Score > r.Students[byScore[j]].Score })
	for i := 0; i < len(byScore); {
		j := i
		for j+1 < len(byScore) && r.Students[byScore[j+1]].Score == r.Students[byScore[i]].Score {
			j++
		}
		for k := i; k <= j; k++ {
			r.Students[byScore[k]].Rank = i + 1
		}
		i = j + 1
	}

	// 全卷统计摘要(缺考行不计入)
	present := make([]StudentLine, 0, len(r.Students))
	for _, l := range r.Students {
		if !l.Absent {
			present = append(present, l)
		}
	}
	r.Stats = computeStats(present, r.Total, Thresholds{})
}

func totalOf(row []float64) float64 {
	var s float64
	for _, v := range row {
		s += v
	}
	return s
}

// computeStats 汇总全卷统计(均分/中位数/标准差/及格优秀低分率/分数段)。
// 分数段按满分每 10% 一档(共 10 档),前端可直接画柱状图。
func computeStats(lines []StudentLine, total float64, th Thresholds) *Stats {
	st := &Stats{Count: len(lines), Total: total,
		PassLine: total * th.pass(), GoodLine: total * th.good(), LowLine: total * th.low()}
	if st.Count == 0 || total <= 0 {
		st.Segments = segmentsOf(nil, total)
		return st
	}
	scores := make([]float64, 0, len(lines))
	var sum, sumSq, pass, good, low float64
	st.Max, st.Min = lines[0].Score, lines[0].Score
	for _, l := range lines {
		s := l.Score
		scores = append(scores, s)
		sum += s
		sumSq += s * s
		if s > st.Max {
			st.Max = s
		}
		if s < st.Min {
			st.Min = s
		}
		if s >= st.PassLine {
			pass++
		}
		if s >= st.GoodLine {
			good++
		}
		if s < st.LowLine {
			low++
		}
	}
	n := float64(st.Count)
	st.Mean = sum / n
	st.MeanRate = st.Mean / total
	st.Range = st.Max - st.Min
	variance := sumSq/n - st.Mean*st.Mean
	if variance < 0 {
		variance = 0
	}
	st.StdDev = math.Sqrt(variance)
	st.PassRate = pass / n
	st.GoodRate = good / n
	st.LowRate = low / n
	sort.Float64s(scores)
	st.Median = medianOf(scores)
	st.Segments = segmentsOf(scores, total)
	return st
}

// medianOf 返回已排序切分的中位数(偶数取中间两数均值)。
func medianOf(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// segmentsOf 把分数按满分每 10% 一档聚合;最高档闭合到满分。
func segmentsOf(sorted []float64, total float64) []ScoreSegment {
	const buckets = 10
	step := total / buckets
	var segs []ScoreSegment
	for i := 0; i < buckets; i++ {
		lo := step * float64(i)
		hi := step * float64(i+1)
		segs = append(segs, ScoreSegment{Low: lo, High: hi})
	}
	if total <= 0 {
		return segs
	}
	for _, s := range sorted {
		idx := int(s / step)
		if idx >= buckets {
			idx = buckets - 1 // 满分归入最高段
		}
		if idx < 0 {
			idx = 0
		}
		segs[idx].Count++
	}
	if len(sorted) > 0 {
		for i := range segs {
			segs[i].Rate = float64(segs[i].Count) / float64(len(sorted))
		}
	}
	return segs
}

// SubjectiveMax 返回主观题满分:拆分小问时为小问之和,否则取 spec.Score(缺省 10)。
func SubjectiveMax(spec card.SubjectiveSpec) float64 {
	if len(spec.Parts) > 0 {
		var sum float64
		for _, p := range spec.Parts {
			sum += p.Score
		}
		return sum
	}
	if spec.Score > 0 {
		return spec.Score
	}
	return 10
}

// SubjectiveScore 汇总一名学生一道主观题的得分与完成状态(统分/阅卷进度/报表共用):
//   - 未拆小问:主评分键已阅即完成;
//   - 拆分小问(版式 v3):全部小问均已阅才算完成,得分为小问之和——
//     半阅不进统计,避免把半截分摊进均分。
//
// 返回 (得分, 是否完成, 是否存在待复核标记)。
func SubjectiveScore(spec card.SubjectiveSpec, grades map[string]Grade, student, question int) (sum float64, done bool, flagged bool) {
	if len(spec.Parts) == 0 {
		g, exists := grades[GradeKey(student, question)]
		return g.Score, exists && g.Graded, exists && g.Mark == "flag"
	}
	for pi := range spec.Parts {
		g, exists := grades[GradePartKey(student, question, pi+1)]
		if !exists || !g.Graded {
			return 0, false, false
		}
		sum += g.Score
		if g.Mark == "flag" {
			flagged = true
		}
	}
	return sum, true, flagged
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
