package card

import "fmt"

// 评分方案(吸收自 OMRChecker 的 DEFAULT/section 计分模型):
// 支持漏选部分分、多解加权、答错/不答计分(可倒扣)、按题号分节套用不同规则。
// nil/零值方案 = 传统语义:全对得满分,其余 0 分 —— 旧版式 JSON 无需迁移。

// 多选题漏选(所选为参考答案真子集)的计分方式。
const (
	MultiPartialNone     = ""       // 漏选不得分(默认,精确匹配语义)
	MultiPartialRatio    = "ratio"  // 按所选正确选项占比给分
	MultiPartialHalf     = "half"   // 漏选得满分的一半
	MultiPartialWeighted = "weighted" // 按 QuestionSpec.OptionScores 的选项分值占比给分
)

// 多选题出现错选选项时的处理。
const (
	MultiWrongZero      = ""          // 0 分(默认)
	MultiWrongIncorrect = "incorrect" // 按 Incorrect 计(可负=倒扣)
)

// ScoringRule 是一段题号区间的评分规则。零值 = 传统精确匹配语义。
type ScoringRule struct {
	// Correct 答对得分;缺省(0)用该题 QuestionSpec.Score。
	Correct float64 `json:"correct,omitempty"`
	// Incorrect 答错得分(可为负=倒扣);缺省 0。
	Incorrect float64 `json:"incorrect,omitempty"`
	// Unmarked 未作答得分;缺省 0。
	Unmarked float64 `json:"unmarked,omitempty"`
	// MultiPartial 多选漏选计分:见 MultiPartial* 常量。
	MultiPartial string `json:"multiPartial,omitempty"`
	// MultiWrong 多选错选处理:见 MultiWrong* 常量。
	MultiWrong string `json:"multiWrong,omitempty"`
}

// ScoringSection 按题号闭区间套用规则;从前往后第一个命中的区间生效,
// 未命中任何区间用 Default。
type ScoringSection struct {
	From int         `json:"from"` // 1-based 起始题号(含)
	To   int         `json:"to"`   // 1-based 结束题号(含)
	Rule ScoringRule `json:"rule"`
}

// ScoringScheme 是全卷评分方案,挂在 Layout 上随开放数据格式走。
type ScoringScheme struct {
	Default  ScoringRule      `json:"default"`
	Sections []ScoringSection `json:"sections,omitempty"`
}

// RuleFor 返回题号(1-based)命中的评分规则。
func (s *ScoringScheme) RuleFor(qNo int) ScoringRule {
	if s == nil {
		return ScoringRule{}
	}
	for _, sec := range s.Sections {
		if qNo >= sec.From && qNo <= sec.To {
			return sec.Rule
		}
	}
	return s.Default
}

// Validate 校验方案的基本合法性(建考/保存方案时调用)。
func (s *ScoringScheme) Validate(nQuestions int) error {
	if s == nil {
		return nil
	}
	check := func(r ScoringRule, where string) error {
		switch r.MultiPartial {
		case MultiPartialNone, MultiPartialRatio, MultiPartialHalf, MultiPartialWeighted:
		default:
			return fmt.Errorf("%s: multiPartial 取值非法 %q", where, r.MultiPartial)
		}
		switch r.MultiWrong {
		case MultiWrongZero, MultiWrongIncorrect:
		default:
			return fmt.Errorf("%s: multiWrong 取值非法 %q", where, r.MultiWrong)
		}
		return nil
	}
	if err := check(s.Default, "default"); err != nil {
		return err
	}
	for i, sec := range s.Sections {
		if sec.From < 1 || sec.To < sec.From || (nQuestions > 0 && sec.From > nQuestions) {
			return fmt.Errorf("第 %d 个分节题号区间 [%d,%d] 非法(共 %d 题)", i+1, sec.From, sec.To, nQuestions)
		}
		if err := check(sec.Rule, fmt.Sprintf("分节 [%d,%d]", sec.From, sec.To)); err != nil {
			return err
		}
	}
	return nil
}
