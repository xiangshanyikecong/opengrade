package score

import (
	"strings"

	"github.com/opengrade/opengrade/card"
)

// 客观题单题判定结论(全系统唯一判分口径:统分/班级逐题率/错题本共用 Evaluate)。
const (
	VerdictCorrect    = "correct"    // 全对
	VerdictPartial    = "partial"    // 多选漏选得部分分
	VerdictIncorrect  = "incorrect"  // 答错(含漏选未得分)
	VerdictUnanswered = "unanswered" // 未作答
)

// FullScore 返回该题的满分:评分规则 Correct 优先,否则 QuestionSpec.Score(缺省 1)。
// 卷面满分、逐题满分与判分必须同源,否则带 Correct 覆盖的分节会算出错误总分。
func FullScore(spec card.QuestionSpec, rule card.ScoringRule) float64 {
	if rule.Correct > 0 {
		return rule.Correct
	}
	if spec.Score > 0 {
		return spec.Score
	}
	return 1
}

// Evaluate 按评分规则判定一道客观题,返回 (得分, 判定结论)。
// 空参考答案的题不判分(调用方保证不传入);传统语义(rule 零值)下:
// 答对得满分、其余 0 分,与旧的精确串匹配完全一致。
//
// 多选题(len(key)>1 且 Kind=multiple):
//   - 精确命中 → 满分;
//   - 所选为参考答案真子集(漏选)→ 按 rule.MultiPartial 给部分分
//     (ratio=按正确选项占比 / half=满分一半 / weighted=按 OptionScores
//     选项分值占比折算到满分,未配置时退化为 ratio);
//   - 出现错选选项 → rule.MultiWrong(zero=0 分 / incorrect=按 Incorrect 计,可倒扣)。
func Evaluate(spec card.QuestionSpec, rule card.ScoringRule, answer, key string) (float64, string) {
	full := FullScore(spec, rule)
	if answer == "" {
		return rule.Unmarked, VerdictUnanswered
	}
	if answer == key {
		return full, VerdictCorrect
	}
	if len(key) > 1 && spec.Kind == card.KindMultiple {
		keySet := map[rune]bool{}
		for _, r := range key {
			keySet[r] = true
		}
		wrong := false
		nHit := 0
		for _, r := range answer {
			if keySet[r] {
				nHit++
			} else {
				wrong = true
			}
		}
		if wrong {
			if rule.MultiWrong == card.MultiWrongIncorrect {
				return rule.Incorrect, VerdictIncorrect
			}
			return 0, VerdictIncorrect
		}
		// 漏选(真子集):nHit < len(keySet)
		switch rule.MultiPartial {
		case card.MultiPartialRatio:
			return full * float64(nHit) / float64(len(keySet)), VerdictPartial
		case card.MultiPartialHalf:
			return full / 2, VerdictPartial
		case card.MultiPartialWeighted:
			if w := weightedEarned(spec, answer, key, full); w >= 0 {
				return w, VerdictPartial
			}
			return full * float64(nHit) / float64(len(keySet)), VerdictPartial
		}
		return 0, VerdictIncorrect
	}
	return rule.Incorrect, VerdictIncorrect
}

// weightedEarned 按 OptionScores 计算漏选得分:满分 × Σ所选分值/Σ答案分值。
// OptionScores 未配置/答案分值非正时返回 -1(调用方退化为 ratio)。
func weightedEarned(spec card.QuestionSpec, answer, key string, full float64) float64 {
	if len(spec.OptionScores) == 0 {
		return -1
	}
	scoreOf := func(s string) float64 {
		var sum float64
		for _, r := range s {
			idx := optionIndex(r)
			if idx >= 0 && idx < len(spec.OptionScores) {
				sum += spec.OptionScores[idx]
			}
		}
		return sum
	}
	keySum := scoreOf(key)
	if keySum <= 0 {
		return -1
	}
	return full * scoreOf(answer) / keySum
}

// optionIndex 把选项字母('A'..'F')转成 0-based 下标,非法字符返回 -1。
func optionIndex(r rune) int {
	if r >= 'A' && r <= 'F' {
		return int(r - 'A')
	}
	return -1
}

// VerdictLabel 返回判定结论的中文短标签(解释表/前端复用)。
func VerdictLabel(v string) string {
	switch v {
	case VerdictCorrect:
		return "对"
	case VerdictPartial:
		return "部分对"
	case VerdictIncorrect:
		return "错"
	case VerdictUnanswered:
		return "未答"
	}
	return strings.TrimSpace(v)
}
