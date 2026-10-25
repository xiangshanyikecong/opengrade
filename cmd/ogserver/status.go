package main

// 考试阶段状态机(常规网上阅卷流程):待扫描 → 扫描中 → 异常待处理 → 阅卷中 → 待发布 → 已发布。
// 阶段一律从会话数据派生而非落盘字段 —— 数据即状态,不存在"忘了推进"的脏状态:
//   - 已扫 0 卷 → 待扫描(建考=建档,扫描是考后独立阶段);
//   - 名册已知且已扫不足 → 扫描中(还差几张一目了然);
//   - 存在未裁决异常卷(重答/空白卡/考号异常/缺页残卷,且未人工指派考号、未标缺考、未接受)→ 异常待处理;
//   - 主观题还有切片未评 → 阅卷中(缺考占位卷不进阅卷口径,无需给幽灵卷打分);
//   - 全部评完未发布 → 待发布;发布 → 已发布(发布后数据有变更会带「有变更」提示,需重新发布收口)。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/opengrade/opengrade/score"
)

// 阶段常量(前端按 stage 上色,按 label 直接展示)。
const (
	stageAwaiting = "awaiting_scan" // 待扫描
	stageScanning = "scanning"      // 扫描中
	stageAnomaly  = "anomalies"     // 异常待处理
	stageGrading  = "grading"       // 阅卷中
	stageReady    = "ready"         // 待发布
	stagePub      = "published"     // 已发布
)

// stageView 是阶段视图:列表与详情共用,数字供工作台"已扫/未评/异常"看板直接展示。
type stageView struct {
	Stage    string `json:"stage"`
	Label    string `json:"label"`
	Students int    `json:"students"`  // 已扫卷数(含缺考占位)
	Expected int    `json:"expected"`  // 应扫卷数(名册人数;0=名册未提供)
	Warnings int    `json:"warnings"`  // 未裁决异常卷数
	Absent   int    `json:"absent"`    // 缺考人数
	Manual   int    `json:"manual"`    // 手工补录卷数
	Ungraded int    `json:"ungraded"`  // 未评主观切片(份;缺考卷不计)
	Total    int    `json:"total"`     // 主观切片总数(份;纯客观卷为 0)
	Flagged  int    `json:"flagged"`   // 待复核(⚑)切片数
	Graded   bool   `json:"gradedAll"` // 主观题是否全部评完
	Dirty    bool   `json:"dirty"`     // 发布后数据有变更(补传/改分/改答案),需重新发布
}

// sheetResolved 判定一张异常卷是否已被人工裁决:
// 考号人工指派过(采纳推荐/手工确认)、已标缺考、AB 卷种已人工指派,
// 或缺页/页序类告警已被人工接受(残卷核对),视为处理完成,不再计入"异常待处理"。
// 卷种仍待定(PendingVariant)时一律未裁决:合并重判会重新挂起客观题作答,
// 残留的历史指派记录不是"已处理"的证据 —— 否则该生静默从报告/排名消失且无待办。
func sheetResolved(ex *Exam, student int) bool {
	if student >= 1 && student <= len(ex.Results) && ex.Results[student-1] != nil && ex.Results[student-1].PendingVariant {
		return false
	}
	if ex.Results[student-1].Absent {
		return true
	}
	if ex.Acked != nil && ex.Acked[fmt.Sprintf("s%d", student)] {
		return true
	}
	if ex.Assignments != nil && ex.Assignments[fmt.Sprintf("s%d", student)] != "" {
		return true
	}
	if ex.AssignVariant != nil && ex.AssignVariant[fmt.Sprintf("s%d", student)] != "" {
		return true // AB 卷:卷种已人工指派(客观题按该卷种重读,暂扣解除)
	}
	return false
}

// blockingWarning 判定一条告警是否「必须人工裁决」:
// 缺页/页序不可信/重答/卷种未识别/B 卡混入/考号无法定位/短号不符 等 → 阻断(进异常阶段、挡发布);
// 系统已自愈或已有确定判分口径的提示(码修复考号、多涂按错判、涂写偏浅)→ 信息类,
// 展示在异常清单但不驱动阶段机 —— 否则一张浅涂告警会把整场考试永远冻在「异常待处理」。
//
// 匹配必须是精确文案片段而非「已按」这样的宽泛子串(B-2 修复):
// 「页点不可信…已按到达顺序合并」「B 卡…已按 A 版式判读」「页间考号不一致,已按后者」
// 都含「已按」但都是必须人工裁决的阻断告警 —— 宽泛匹配曾把它们判成信息类,
// 发布门禁放行可疑作答,且这些卷被连带标成「已裁决」后,文案里引导的
// 「接受残卷」按钮反而不渲染(出口断了)。新增软告警文案时在此登记精确片段。
func blockingWarning(w string) bool {
	for _, soft := range []string{
		"涂写偏浅",                     // 浅涂提示(omr):建议复核但已有确定判读
		"按错判处理",                    // 多涂(omr):判分口径明确
		"判分按批阅结果计入",               // 选做题号区未唯一涂选(score):回退批阅结果
		"已按填涂考号",                   // 码解码失败,回退填涂考号(omr/regcode)
		"已按条码修复", "已按二维码修复",        // 填涂异常,以码为准(omr/regcode)
		"不一致,已按条码", "不一致,已按二维码",     // 填涂与码不一致,以码为准(omr/regcode)
		"已按卷种点判读",                   // 卷种点与码内卷种位不一致,以点为准(omr):确定口径
	} {
		if strings.Contains(w, soft) {
			return false
		}
	}
	return true
}

// hasBlockingWarning 报告一组告警里是否存在需要人工裁决的条目。
func hasBlockingWarning(ws []string) bool {
	for _, w := range ws {
		if blockingWarning(w) {
			return true
		}
	}
	return false
}

// examBlockingWarningSamples 取未裁决阻断性告警的样例文案(发布门禁报错给人看的"差什么")。
func examBlockingWarningSamples(ex *Exam, n int) string {
	var out []string
	for i := range ex.Results {
		res := ex.Results[i]
		if res == nil || res.Voided || !hasBlockingWarning(res.Warnings) || sheetResolved(ex, i+1) {
			continue
		}
		for _, w := range res.Warnings {
			if blockingWarning(w) {
				out = append(out, fmt.Sprintf("卷 %d:%s", i+1, w))
				break
			}
		}
		if len(out) >= n {
			break
		}
	}
	if len(out) == 0 {
		return "重答/空白卡/卷种未指派/缺页残卷"
	}
	return strings.Join(out, ";")
}

// activeResults 返回未作废的结果数(作废卷不计入已扫/应扫/进度口径)。
func activeCount(ex *Exam) int {
	n := 0
	for _, res := range ex.Results {
		if res != nil && !res.Voided {
			n++
		}
	}
	return n
}

// gradableCount 返回参与阅卷口径的卷数:未作废且非缺考(缺考占位卷没有作答可评)。
func gradableCount(ex *Exam) int {
	n := 0
	for _, res := range ex.Results {
		if res != nil && !res.Voided && !res.Absent {
			n++
		}
	}
	return n
}

// examStage 派生一场考试当前所处阶段与看板数字。
func examStage(ex *Exam) stageView {
	v := stageView{Students: activeCount(ex), Expected: len(ex.Roster)}
	gradedTotal := 0
	// 阅卷口径只数"应评"的切片:缺考占位卷不评(没有作答),选做题未被所选的组员不评
	// (此前两者都计入 Ungraded —— 缺考幽灵卷和未选做的题会把阶段永远卡在「阅卷中」)。
	groups := score.ElectiveGroups(ex.Layout.Subjectives)
	for si := range ex.Results {
		res := ex.Results[si]
		if res == nil || res.Voided || res.Absent {
			continue
		}
		excluded := score.ElectiveExcluded(groups, ex.Layout.Subjectives, ex.Grades, res, si+1)
		for qi := range ex.Layout.Subjectives {
			if excluded[qi+1] {
				continue
			}
			v.Total++
			if subjectiveGraded(ex, si+1, qi+1) {
				gradedTotal++
			}
		}
	}
	for si, res := range ex.Results {
		if res == nil || res.Voided {
			continue
		}
		if res.Absent {
			v.Absent++
		}
		if res.Manual {
			v.Manual++
		}
		if hasBlockingWarning(res.Warnings) && !sheetResolved(ex, si+1) {
			v.Warnings++
		}
	}
	for _, g := range ex.Grades {
		if g.Mark == "flag" {
			v.Flagged++
		}
	}
	v.Ungraded = v.Total - gradedTotal
	v.Graded = v.Total == 0 || v.Ungraded == 0
	v.Dirty = ex.Dirty

	switch {
	case ex.Published:
		v.Stage = stagePub
		v.Label = "已发布"
		if ex.Dirty {
			v.Label = "已发布·有变更"
		} else if v.Warnings > 0 || v.Ungraded > 0 {
			// 发布后补扫/半阅残留:阶段色保持已发布,但标签点出未收口的部分
			v.Label = fmt.Sprintf("已发布·有变更(未评 %d 异常 %d)", v.Ungraded, v.Warnings)
		}
	case len(ex.Results) == 0:
		v.Stage, v.Label = stageAwaiting, "待扫描"
	case v.Expected > 0 && v.Students < v.Expected:
		v.Stage, v.Label = stageScanning, fmt.Sprintf("扫描中 %d/%d", v.Students, v.Expected)
	case v.Warnings > 0:
		v.Stage, v.Label = stageAnomaly, fmt.Sprintf("异常待处理 %d", v.Warnings)
	case !v.Graded:
		v.Stage, v.Label = stageGrading, fmt.Sprintf("阅卷中 %d%%", pct(gradedTotal, v.Total))
	default:
		v.Stage, v.Label = stageReady, "待发布"
	}
	return v
}

func pct(a, b int) int {
	if b == 0 {
		return 100
	}
	return a * 100 / b
}

// gradeKeyRe 严格匹配评分键:整题 s{卷}-q{题} 或小问 s{卷}-q{题}p{问}。
// (fmt.Sscanf 不校验尾部输入,"s1-q2p3" 会被当整题键解析 —— 队列重复行的根源。)
var gradeKeyRe = regexp.MustCompile(`^s(\d+)-q(\d+)(?:p(\d+))?$`)

// flaggedView 是待复核队列的一行:第 q 题第 student 卷被标 ⚑,待组长/教师复核改分。
type flaggedView struct {
	Student  int     `json:"student"`
	Reg      string  `json:"reg,omitempty"`
	Question int     `json:"question"`
	Score    float64 `json:"score"`
}

// flaggedQueue 汇总 ⚑ 待复核切片(按题号排序)——阅卷标记必须有出口,否则闭环不存在。
// 拆分小问的题:⚑ 同步标在每个小问上,这里聚合为该卷该题一行,当前分 = 已评小问之和
// (全部小问评完即为真实题分),不再出现"一道拆分题 N 行重复、各行显示单问分"。
func flaggedQueue(ex *Exam) []flaggedView {
	type acc struct {
		score  float64
		whole  bool // 整题键直接给分
		hasAny bool
	}
	bySq := map[[2]int]*acc{}
	for key, g := range ex.Grades {
		if g.Mark != "flag" {
			continue
		}
		m := gradeKeyRe.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		s, _ := strconv.Atoi(m[1])
		q, _ := strconv.Atoi(m[2])
		if s < 1 || s > len(ex.Results) || ex.Results[s-1] == nil || ex.Results[s-1].Voided {
			continue // 作废卷不进复核队列
		}
		sq := [2]int{s, q}
		a := bySq[sq]
		if a == nil {
			a = &acc{}
			bySq[sq] = a
		}
		a.hasAny = true
		if m[3] == "" { // 整题键
			a.whole = true
			a.score = g.Score
		} else if !a.whole && g.Graded {
			a.score += g.Score
		}
	}
	out := make([]flaggedView, 0, len(bySq))
	for sq, a := range bySq {
		if !a.hasAny {
			continue
		}
		out = append(out, flaggedView{Student: sq[0], Question: sq[1], Reg: ex.AssignedReg(sq[0]), Score: a.score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Question != out[j].Question {
			return out[i].Question < out[j].Question
		}
		return out[i].Student < out[j].Student
	})
	return out
}
