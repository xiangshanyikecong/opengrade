// Package card 定义答题卡版式模型与几何约束。
// 它是"出卡(可视化设计器/渲染)"与"读卡(OMR 识别/切片)"共享的同一份几何真源:
// 识别引擎不重新猜测版面,而是直接使用 Layout 编译出的像素坐标,
// 从机制上杜绝传统产品"排版模板与识别逻辑两套代码导致的错位"问题。
//
// 版式格式分两个版本:
//   - v1:M0 引入,仅客观单选题(choiceCount/options/columns);
//   - v2:M1 引入,题型化 questions[](choice/judge/multiple)+ 主观题作答区
//     subjectives[](fillblank/subjective),为切片阅卷提供几何契约。
//     v3 增量(向后兼容):主观题小问拆分 Parts[]、选做题组 Elective(题号区,判分只取所选)。
//
// v1 文件在读取时自动规范化为 v2 内部表示,旧数据不受影响。
package card

import (
	"fmt"
	"image"
	"math"
	"strings"
)

// 页面与元素几何常量(单位:mm,渲染分辨率 300 DPI)。
const (
	PageWMM = 210.0
	PageHMM = 297.0
	DPI     = 300.0
	pxPerMM = DPI / 25.4

	locatorSideMM  = 5.0  // 四角实心方块边长
	locatorInsetMM = 10.0 // 方块左上角距页边距离

	choiceRMM   = 1.7  // 客观题涂卡圆半径
	regRMM      = 1.55 // 考号涂卡圆半径
	electiveRMM = 1.55 // 选做题号区涂点半径(与考号涂点同规格)
	strokeMM    = 0.35 // 涂卡圆描边宽

	headerH    = 72.0 // 卷头高度(客观题区起始 y)
	rowPitchMM = 7.0  // 客观题行距
	optPitchMM = 9.0  // 选项间距
	subLabelH  = 5.0  // 主观题标签行高
	subGapMM   = 3.0  // 主观题区块间距

	marginX       = 14.0 // 版心左右边距
	bottomReserve = 14.0 // 底部预留(下角标 + 页脚)
)

// 题型常量。
const (
	KindChoice     = "choice"     // 单选
	KindJudge      = "judge"      // 判断(T/F 两涂点)
	KindMultiple   = "multiple"   // 多选(多涂合法)
	KindFillBlank  = "fillblank"  // 填空(主观切片)
	KindSubjective = "subjective" // 解答(主观切片)
)

// SplitKnowledge 把建考时录入的知识点标签串拆成去重后的标签列表。
// 分隔符兼容中英文顿号/逗号/分号/斜杠与竖线,便于教师随手输入。
func SplitKnowledge(s string) []string {
	if s == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '/', '|':
			return true
		}
		return false
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// QuestionSpec 描述一道客观题(涂卡判读)。
type QuestionSpec struct {
	Kind      string  `json:"kind"`                // choice | judge | multiple
	Options   int     `json:"options,omitempty"`   // choice/multiple: 2~6;judge 恒为 2
	Score     float64 `json:"score,omitempty"`     // 分值,缺省 1
	Knowledge string  `json:"knowledge,omitempty"` // 知识点标签(建考可录入,多个用 , / 、 分隔)
}

// SubjectivePart 是主观题下的小问(版式 v3):独立给分,阅卷端逐问评分后自动汇总到大题总分。
type SubjectivePart struct {
	Label string  `json:"label,omitempty"` // 小问标签,如 "(1)";缺省按序号显示
	Score float64 `json:"score"`           // 小问满分
}

// SubjectiveSpec 描述一道主观题:页面上是留白作答区,切片后进入人工/AI 阅卷流。
// 拆分小问时(Parts 非空),Score 由各小问分值求和得出(规范化阶段覆盖),作答区仍是
// 一整块(切片按大题裁剪),阅卷端逐小问给分。
// Elective 非空时表示选做题组(版式 v3):组标签相同的题互为"多选一"(如 22、23 二选一),
// 卡面在该组第一道题的标签行右端印题号区涂点,判分只取学生所涂题号。
type SubjectiveSpec struct {
	Kind      string           `json:"kind"`                // fillblank | subjective
	Label     string           `json:"label,omitempty"`     // 题干提示(打印在标签行)
	Score     float64          `json:"score,omitempty"`     // 满分,缺省 fillblank=5、subjective=10;有小问时=小问之和
	HeightMM  float64          `json:"heightMM,omitempty"`  // 作答区高度,缺省 fillblank=16、subjective=42
	Knowledge string           `json:"knowledge,omitempty"` // 知识点标签(建考可录入,多个用 , / 、 分隔)
	Parts     []SubjectivePart `json:"parts,omitempty"`     // 小问拆分(版式 v3):如一道 17 分解答题拆 6+5+6
	Elective  string           `json:"elective,omitempty"`  // 选做组标签(版式 v3):同组互斥,判分只取所涂题号
}

// Layout 是开放数据格式的版式定义:JSON 序列化即 .layout.json 文件内容,
// 可由社区自由读写、长期迁移,是"数据不属于厂商"承诺的载体之一。
type Layout struct {
	Version   int    `json:"version"`
	Title     string `json:"title"`
	RegDigits int    `json:"regDigits"`         // 考号位数,0 表示无考号区
	Columns   int    `json:"columns,omitempty"` // 客观题分栏数 1~4,缺省 2

	// v1 专用字段(Version==1 时生效,读取时规范化为 v2)
	ChoiceCount int `json:"choiceCount,omitempty"`
	Options     int `json:"options,omitempty"`

	// v2 字段
	Questions   []QuestionSpec   `json:"questions,omitempty"`
	Subjectives []SubjectiveSpec `json:"subjectives,omitempty"`
}

// DefaultLayout 返回 v2 示例版式:20 单选 + 5 判断 + 5 多选 + 2 填空 + 1 解答。
func DefaultLayout() Layout {
	return Layout{
		Version:   2,
		Title:     "OPENGRADE ANSWER SHEET",
		RegDigits: 8,
		Columns:   2,
		Questions: append(
			append(repQuestion(20, QuestionSpec{Kind: KindChoice, Options: 4, Score: 2}),
				repQuestion(5, QuestionSpec{Kind: KindJudge, Score: 2})...),
			repQuestion(5, QuestionSpec{Kind: KindMultiple, Options: 4, Score: 2})...),
		Subjectives: []SubjectiveSpec{
			{Kind: KindFillBlank, Score: 5, HeightMM: 16},
			{Kind: KindFillBlank, Score: 5, HeightMM: 16},
			{Kind: KindSubjective, Score: 10, HeightMM: 42},
		},
	}
}

func repQuestion(n int, q QuestionSpec) []QuestionSpec {
	out := make([]QuestionSpec, n)
	for i := range out {
		out[i] = q
	}
	return out
}

// OptionLetters 返回某客观题的选项标签序列(判读与统分共用的字母契约)。
func OptionLetters(kind string, options int) []string {
	if kind == KindJudge {
		return []string{"T", "F"} // 判断题用 T/F 作选项字母;√/× 待 CJK 字体就绪后启用
	}
	out := make([]string, options)
	for i := range out {
		out[i] = string(rune('A' + i))
	}
	return out
}

// normalize 把 v1/v2 版式统一为填充了缺省值的 v2 表示。
func (l Layout) normalize() (Layout, error) {
	switch l.Version {
	case 1:
		if l.ChoiceCount < 1 {
			return Layout{}, fmt.Errorf("客观题数量必须 >= 1")
		}
		opts := l.Options
		if opts == 0 {
			opts = 4
		}
		out := Layout{
			Version:   2,
			Title:     l.Title,
			RegDigits: l.RegDigits,
			Columns:   firstNonZero(l.Columns, 2),
			Questions: repQuestion(l.ChoiceCount, QuestionSpec{Kind: KindChoice, Options: opts, Score: 1}),
		}
		return out, nil
	case 2:
		out := l
		out.Columns = firstNonZero(l.Columns, 2)
		if len(l.Questions) == 0 && len(l.Subjectives) == 0 {
			return Layout{}, fmt.Errorf("版式为空:至少需要一道客观题或主观题")
		}
		for i := range out.Questions {
			q := &out.Questions[i]
			switch q.Kind {
			case KindJudge:
				q.Options = 2
			case KindChoice, KindMultiple:
				if q.Options == 0 {
					q.Options = 4
				}
			default:
				return Layout{}, fmt.Errorf("第 %d 题题型 %q 不合法(choice/judge/multiple)", i+1, q.Kind)
			}
			if q.Score == 0 {
				q.Score = 1
			}
		}
		for i := range out.Subjectives {
			s := &out.Subjectives[i]
			switch s.Kind {
			case KindFillBlank:
				s.HeightMM = firstNonZeroF(s.HeightMM, 16)
				s.Score = firstNonZeroF(s.Score, 5)
			case KindSubjective:
				s.HeightMM = firstNonZeroF(s.HeightMM, 42)
				s.Score = firstNonZeroF(s.Score, 10)
			default:
				return Layout{}, fmt.Errorf("第 %d 道主观题题型 %q 不合法(fillblank/subjective)", i+1, s.Kind)
			}
			if len(s.Parts) > 0 { // 小问拆分(版式 v3):大题满分 = 小问之和
				var sum float64
				for pi := range s.Parts {
					if s.Parts[pi].Score <= 0 {
						return Layout{}, fmt.Errorf("第 %d 道主观题第 %d 个小问分值必须 > 0", i+1, pi+1)
					}
					if s.Parts[pi].Label == "" {
						s.Parts[pi].Label = fmt.Sprintf("(%d)", pi+1)
					}
					sum += s.Parts[pi].Score
				}
				s.Score = sum
			}
			s.Elective = strings.TrimSpace(s.Elective)
		}
		// 选做组(版式 v3):同组标签的题互为"多选一"。组必须含 2~4 道题——
		// 少于 2 道不构成选择;多于 4 道题号区在标签行放不下。
		size := map[string]int{}
		order := []string{}
		for i := range out.Subjectives {
			k := out.Subjectives[i].Elective
			if k == "" {
				continue
			}
			if size[k] == 0 {
				order = append(order, k)
			}
			size[k]++
		}
		for _, k := range order {
			if size[k] < 2 {
				return Layout{}, fmt.Errorf("选做组 %q 只有 1 道题:同组至少需要两道题才能互为选做", k)
			}
			if size[k] > 4 {
				return Layout{}, fmt.Errorf("选做组 %q 有 %d 道题,最多支持 4 道(题号区宽度限制)", k, size[k])
			}
		}
		return out, nil
	default:
		return Layout{}, fmt.Errorf("不支持的版式版本 %d(支持 1 与 2)", l.Version)
	}
}

// Validate 校验版式能否在 A4 单页内合规排布。
func (l Layout) Validate() error {
	n, err := l.normalize()
	if err != nil {
		return err
	}
	if n.Columns < 1 || n.Columns > 4 {
		return fmt.Errorf("分栏数必须在 1~4 之间,当前 %d", n.Columns)
	}
	if n.RegDigits < 0 || n.RegDigits > 12 {
		return fmt.Errorf("考号位数必须在 0~12 之间,当前 %d", n.RegDigits)
	}
	for i, q := range n.Questions {
		if q.Options < 2 || q.Options > 6 {
			return fmt.Errorf("第 %d 题选项数必须在 2~6 之间,当前 %d", i+1, q.Options)
		}
	}
	objRows := int(math.Ceil(float64(len(n.Questions)) / float64(n.Columns)))
	maxRows := int(math.Floor((PageHMM - headerH - bottomReserve) / rowPitchMM))
	if objRows > maxRows {
		return fmt.Errorf("客观题量×分栏超出单页容量:每栏最多 %d 行,当前需要 %d 行(可增大分栏数)", maxRows, objRows)
	}
	// 混排分栏宽度校验:按该栏最宽题计算
	colW := (PageWMM - 2*marginX) / float64(n.Columns)
	objRowsF := float64(objRows)
	widest := make([]int, n.Columns)
	for i := range n.Questions {
		col := 0
		if objRowsF > 0 {
			col = i / objRows
		}
		if col < n.Columns {
			widest[col] = max(widest[col], n.Questions[i].Options)
		}
	}
	for col := 0; col < n.Columns; col++ {
		if widest[col] == 0 {
			continue
		}
		contentW := 12 + float64(widest[col]-1)*optPitchMM + 2*choiceRMM
		if contentW+4 > colW {
			return fmt.Errorf("分栏过窄:第 %d 栏最宽题内容 %.1fmm,分栏后仅 %.1fmm", col+1, contentW, colW)
		}
	}
	totalH := headerH + float64(objRows)*rowPitchMM + 4
	for _, s := range n.Subjectives {
		totalH += subLabelH + s.HeightMM + subGapMM
	}
	if totalH > PageHMM-bottomReserve {
		return fmt.Errorf("版面超出 A4 容量:需要 %.0fmm,可用 %.0fmm(可缩减主观题高度或题量)", totalH, PageHMM-bottomReserve)
	}
	return nil
}

type mmPt struct{ x, y float64 }

// electiveGeom 是一个选做组的题号区几何(mm):印在组内第一道主观题的标签行右端。
// 涂点从右往左排,每个涂点左侧印对应的卷面全局题号;首元素左侧印"题号"前缀。
type electiveGeom struct {
	members []int    // 组内主观题号(1-based,升序)
	labels  []string // 各涂点印刷的卷面全局题号(如 "23")
	points  []mmPt   // 各涂点圆心
	prefix  mmPt     // "题号"前缀文本中心
}

type subGeom struct {
	qNum     mmPt // 题号文本中心(标签行左端)
	boxTop   float64
	boxH     float64
	score    float64
	kind     string
	parts    []SubjectivePart // 小问分值(渲染得分框用;nil = 未拆分)
	elective *electiveGeom    // 非 nil = 本题为某选做组的第一道(题号区印在本题标签行)
}

// geom 是以 mm 为单位的版面几何(SVG 直接使用,转 px 后供 OMR/切片使用)。
type geom struct {
	locators                               [4]mmPt // 中心点,顺序 TL TR BL BR
	title                                  mmPt
	regLabel                               mmPt
	nameLabel                              mmPt
	nameBoxX, nameBoxY, nameBoxW, nameBoxH float64
	regRowLbl                              []mmPt
	reg                                    [][]mmPt
	choice                                 [][]mmPt // [题][选项] 圆心
	qNumLbl                                []mmPt
	optLbls                                [][]mmPt // [题][选项] 字母中心
	subjectives                            []subGeom
}

// geometry 计算版面全部几何要素(所有标签坐标均为文本包围盒中心)。
func (l Layout) geometry() (geom, error) {
	n, err := l.normalize()
	if err != nil {
		return geom{}, err
	}
	var g geom
	c := locatorInsetMM + locatorSideMM/2
	g.locators = [4]mmPt{{c, c}, {PageWMM - c, c}, {c, PageHMM - c}, {PageWMM - c, PageHMM - c}}
	g.title = mmPt{PageWMM / 2, 11}

	if n.RegDigits > 0 {
		g.regLabel = mmPt{26, 18.5}
		for k := 0; k < n.RegDigits; k++ {
			col := make([]mmPt, 10)
			for d := 0; d < 10; d++ {
				col[d] = mmPt{26 + float64(k)*6, 23 + float64(d)*4.2}
			}
			g.reg = append(g.reg, col)
		}
		for d := 0; d < 10; d++ {
			g.regRowLbl = append(g.regRowLbl, mmPt{20.5, 23 + float64(d)*4.2})
		}
		nx := 26 + float64(n.RegDigits)*6 + 10
		g.nameLabel = mmPt{nx + 4, 18.5}
		g.nameBoxX, g.nameBoxY, g.nameBoxW, g.nameBoxH = nx, 21, 62, 13
	} else {
		g.nameLabel = mmPt{26, 18.5}
		g.nameBoxX, g.nameBoxY, g.nameBoxW, g.nameBoxH = 26, 21, 62, 13
	}

	objRows := int(math.Ceil(float64(len(n.Questions)) / float64(n.Columns)))
	colPitch := (PageWMM - 2*marginX) / float64(n.Columns)
	for i := range n.Questions {
		col, row := i/objRows, i%objRows
		base := marginX + float64(col)*colPitch
		cy := headerH + float64(row)*rowPitchMM + 3.5
		letters := OptionLetters(n.Questions[i].Kind, n.Questions[i].Options)
		opts := make([]mmPt, len(letters))
		lbls := make([]mmPt, len(letters))
		for j := range letters {
			opts[j] = mmPt{base + 12 + float64(j)*optPitchMM, cy}
			lbls[j] = mmPt{base + 12 + float64(j)*optPitchMM - 4.5, cy}
		}
		g.choice = append(g.choice, opts)
		g.qNumLbl = append(g.qNumLbl, mmPt{base + 3.5, cy})
		g.optLbls = append(g.optLbls, lbls)
	}

	y := headerH + float64(objRows)*rowPitchMM + 4
	// 选做组:组标签 → 组内主观题号(1-based,按出现序);题号区印在组内第一道题上
	type eGroup struct{ members []int }
	eGroups := map[string]*eGroup{}
	var eOrder []string
	for i := range n.Subjectives {
		k := n.Subjectives[i].Elective
		if k == "" {
			continue
		}
		if eGroups[k] == nil {
			eGroups[k] = &eGroup{}
			eOrder = append(eOrder, k)
		}
		eGroups[k].members = append(eGroups[k].members, i+1)
	}
	electiveOf := map[int]*electiveGeom{} // 组内第一道主观题号 → 题号区几何
	for _, k := range eOrder {
		g := eGroups[k]
		eg := &electiveGeom{members: g.members}
		for _, q := range g.members {
			eg.labels = append(eg.labels, itoa(len(n.Questions)+q)) // 卷面全局题号
		}
		// 右端对齐排涂点:每个占位 slotW mm,涂点圆心距右边距留 1mm;题号印在涂点左侧
		const slotW, circleR, edgePad = 10.0, 1.55, 1.0
		for i := range g.members {
			cx := PageWMM - marginX - edgePad - circleR - float64(len(g.members)-1-i)*slotW
			eg.points = append(eg.points, mmPt{cx, 0})
			// 前缀与题号标签的 y 在下面按第一道题的标签行补齐
		}
		first := g.members[0]
		rowY := headerH + float64(objRows)*rowPitchMM + 4
		for i := 0; i < first-1; i++ {
			rowY += subLabelH + n.Subjectives[i].HeightMM + subGapMM
		}
		rowY += subLabelH / 2
		for i := range eg.points {
			eg.points[i].y = rowY
		}
		leftmostLbl := eg.points[0].x - circleR - 3.2
		eg.prefix = mmPt{leftmostLbl - 7.0, rowY}
		electiveOf[first] = eg
	}
	for _, s := range n.Subjectives {
		g.subjectives = append(g.subjectives, subGeom{
			qNum:     mmPt{marginX + 2, y + subLabelH/2},
			boxTop:   y + subLabelH,
			boxH:     s.HeightMM,
			score:    s.Score,
			kind:     s.Kind,
			parts:    s.Parts,
			elective: electiveOf[len(g.subjectives)+1],
		})
		y += subLabelH + s.HeightMM + subGapMM
	}
	return g, nil
}

// ElectiveRow 是选做题组印在卡面上的题号区(版式 v3):学生涂所选的题号,
// 判分时同组互斥、只取所选。印在该组第一道主观题的标签行右端。
type ElectiveRow struct {
	Subjective int           // 题号区所在主观题(1-based,组内第一道)
	Members    []int         // 组内全部主观题号(1-based,升序)
	Labels     []string      // 各涂点印刷的卷面题号(如 "23"),与 Points 一一对应
	Points     []image.Point // 各涂点圆心(px)
	R          int           // 涂点半径(px)
}

// Sheet 是 Layout 的像素编译结果(300 DPI 下的权威几何),OMR 与切片器据此工作。
type Sheet struct {
	Layout          Layout // 规范化后的 v2 版式
	Wpx, Hpx        int
	Locators        [4]image.Point // 定位方块中心(px),顺序 TL TR BL BR
	LocatorSidePx   int
	Choice          [][]image.Point // [题][选项] 涂卡圆心(px)
	ChoiceKinds     []string        // 每客观题题型(判读多选合法性用)
	ChoiceR         int
	Reg             [][]image.Point // [位][数字 0~9] 涂卡圆心(px);无考号区时为空
	RegR            int
	SubjectiveBoxes []image.Rectangle // 每道主观题作答区(px),切片器据此裁剪
	Electives       []ElectiveRow     // 选做题号区(版式 v3);无选做题时为空
}

// Build 校验并把 mm 几何编译为像素几何。
func (l Layout) Build() (*Sheet, error) {
	n, err := l.normalize()
	if err != nil {
		return nil, err
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	g, err := n.geometry()
	if err != nil {
		return nil, err
	}
	s := &Sheet{
		Layout:        n,
		Wpx:           mmToPx(PageWMM),
		Hpx:           mmToPx(PageHMM),
		LocatorSidePx: mmToPx(locatorSideMM),
		ChoiceR:       mmToPx(choiceRMM),
		RegR:          mmToPx(regRMM),
	}
	for i, p := range g.locators {
		s.Locators[i] = image.Point{mmToPx(p.x), mmToPx(p.y)}
	}
	for qi, q := range g.choice {
		row := make([]image.Point, len(q))
		for j, p := range q {
			row[j] = image.Point{mmToPx(p.x), mmToPx(p.y)}
		}
		s.Choice = append(s.Choice, row)
		s.ChoiceKinds = append(s.ChoiceKinds, n.Questions[qi].Kind)
	}
	for _, c := range g.reg {
		col := make([]image.Point, len(c))
		for d, p := range c {
			col[d] = image.Point{mmToPx(p.x), mmToPx(p.y)}
		}
		s.Reg = append(s.Reg, col)
	}
	for _, sg := range g.subjectives {
		x0, y0 := mmToPx(marginX), mmToPx(sg.boxTop)
		s.SubjectiveBoxes = append(s.SubjectiveBoxes,
			image.Rect(x0, y0, mmToPx(marginX+(PageWMM-2*marginX)), y0+mmToPx(sg.boxH)))
		if sg.elective != nil {
			row := ElectiveRow{Subjective: sg.elective.members[0], Members: sg.elective.members, Labels: sg.elective.labels, R: mmToPx(electiveRMM)}
			for _, p := range sg.elective.points {
				row.Points = append(row.Points, image.Point{mmToPx(p.x), mmToPx(p.y)})
			}
			s.Electives = append(s.Electives, row)
		}
	}
	return s, nil
}

func mmToPx(v float64) int { return int(math.Round(v * pxPerMM)) }

// boxRectMM 是渲染用的轴对齐矩形(mm 坐标)。
type boxRectMM struct{ x, y, w, h float64 }

// partScoreBoxes 计算一道拆分小问的主观题在作答区右上角的逐问得分框(mm):
// 框内印小问满分,评阅教师在框内/框旁写实际得分;切片随大题裁剪,阅卷端可见。
func partScoreBoxes(sg subGeom) []boxRectMM {
	if len(sg.parts) == 0 {
		return nil
	}
	const w, h, gap, inset = 13.0, 6.5, 2.0, 1.0
	out := make([]boxRectMM, len(sg.parts))
	for i := range out {
		out[i] = boxRectMM{
			x: PageWMM - marginX - inset - float64(len(sg.parts)-i)*w - float64(len(sg.parts)-1-i)*gap,
			y: sg.boxTop + inset,
			w: w, h: h,
		}
	}
	return out
}

// partsBreakdown 把小问分值渲染为紧凑拆解文本,如 "6+5+6"。
func partsBreakdown(parts []SubjectivePart) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('+')
		}
		b.WriteString(trimScore(p.Score))
	}
	return b.String()
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func firstNonZeroF(a, b float64) float64 {
	if a != 0 {
		return a
	}
	return b
}
