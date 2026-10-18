// Package card 定义答题卡版式模型与几何约束。
// 它是"出卡(可视化设计器/渲染)"与"读卡(OMR 识别/切片)"共享的同一份几何真源:
// 识别引擎不重新猜测版面,而是直接使用 Layout 编译出的像素坐标,
// 从机制上杜绝传统产品"排版模板与识别逻辑两套代码导致的错位"问题。
//
// 版式格式分两个版本:
//   - v1:M0 引入,仅客观单选题(choiceCount/options/columns);
//   - v2:M1 引入,题型化 questions[](choice/judge/multiple)+ 主观题作答区
//     subjectives[](fillblank/subjective),为切片阅卷提供几何契约。
//
// v1 文件在读取时自动规范化为 v2 内部表示,旧数据不受影响。
package card

import (
	"fmt"
	"image"
	"math"
)

// 页面与元素几何常量(单位:mm,渲染分辨率 300 DPI)。
const (
	PageWMM = 210.0
	PageHMM = 297.0
	DPI     = 300.0
	pxPerMM = DPI / 25.4

	locatorSideMM  = 5.0  // 四角实心方块边长
	locatorInsetMM = 10.0 // 方块左上角距页边距离

	choiceRMM = 1.7  // 客观题涂卡圆半径
	regRMM    = 1.55 // 考号涂卡圆半径
	strokeMM  = 0.35 // 涂卡圆描边宽

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

// QuestionSpec 描述一道客观题(涂卡判读)。
type QuestionSpec struct {
	Kind    string  `json:"kind"`              // choice | judge | multiple
	Options int     `json:"options,omitempty"` // choice/multiple: 2~6;judge 恒为 2
	Score   float64 `json:"score,omitempty"`   // 分值,缺省 1
}

// SubjectiveSpec 描述一道主观题:页面上是留白作答区,切片后进入人工/AI 阅卷流。
type SubjectiveSpec struct {
	Kind     string  `json:"kind"`               // fillblank | subjective
	Label    string  `json:"label,omitempty"`    // 题干提示(打印在标签行)
	Score    float64 `json:"score,omitempty"`    // 满分,缺省 fillblank=5、subjective=10
	HeightMM float64 `json:"heightMM,omitempty"` // 作答区高度,缺省 fillblank=16、subjective=42
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

type subGeom struct {
	qNum   mmPt // 题号文本中心(标签行左端)
	boxTop float64
	boxH   float64
	score  float64
	kind   string
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
	for _, s := range n.Subjectives {
		g.subjectives = append(g.subjectives, subGeom{
			qNum:   mmPt{marginX + 2, y + subLabelH/2},
			boxTop: y + subLabelH,
			boxH:   s.HeightMM,
			score:  s.Score,
			kind:   s.Kind,
		})
		y += subLabelH + s.HeightMM + subGapMM
	}
	return g, nil
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
	}
	return s, nil
}

func mmToPx(v float64) int { return int(math.Round(v * pxPerMM)) }

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
