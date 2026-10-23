package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/opengrade/opengrade/card"
)

// layoutFlags 是 gen 共用的版式参数。
type layoutFlags struct {
	out        string
	title      string
	questions  int
	options    int
	columns    int
	reg        int
	regMode    string
	students   int
	seed       int64
	layoutFile string
	preLayout  *card.Layout // 直接注入版式(demo 使用),优先于以上全部参数
	// corruptStudent > 0 时,该学生卷的考号中段留空一位(模拟漏涂,
	// 用于演示名册模糊匹配/人工指派的异常卷自愈流程)
	corruptStudent int
}

func addLayoutFlags(fs *flag.FlagSet, lf *layoutFlags) {
	fs.StringVar(&lf.out, "o", "og_out", "输出目录")
	fs.StringVar(&lf.title, "title", "OPENGRADE ANSWER SHEET", "卷头标题(ASCII;中文标题请走 -layout-file)")
	fs.IntVar(&lf.questions, "questions", 40, "客观单选题数量(v1 模式)")
	fs.IntVar(&lf.options, "options", 4, "每题选项数(v1 模式)")
	fs.IntVar(&lf.columns, "columns", 2, "客观题分栏数")
	fs.IntVar(&lf.reg, "reg", 8, "考号位数(0=不设考号区)")
	fs.StringVar(&lf.regMode, "reg-mode", "fill", "考号通道:fill|barcode|qr|fill+barcode|fill+qr(条码/二维码印在卷头右侧)")
	fs.IntVar(&lf.students, "students", 0, "模拟涂卡学生份数(0=只出空白卡)")
	fs.Int64Var(&lf.seed, "seed", 1, "随机种子(复现实验)")
	fs.StringVar(&lf.layoutFile, "layout-file", "", "版式 JSON 文件(v1/v2 均可;指定后忽略 -questions/-options/-title)")
}

// resolveLayout 由命令行参数得到待出卡的版式(preLayout 优先,供 demo 注入内置版式)。
func resolveLayout(lf layoutFlags) (card.Layout, error) {
	if lf.preLayout != nil {
		return *lf.preLayout, nil
	}
	if lf.layoutFile != "" {
		var l card.Layout
		if err := readJSONFile(lf.layoutFile, &l); err != nil {
			return card.Layout{}, fmt.Errorf("读取版式文件失败: %w", err)
		}
		return l, nil
	}
	return card.Layout{
		Version:     1,
		Title:       lf.title,
		RegDigits:   lf.reg,
		RegMode:     lf.regMode,
		ChoiceCount: lf.questions,
		Options:     lf.options,
		Columns:     lf.columns,
	}, nil
}

// genResult 汇总一次出卡的产物,供 demo 复用。
type genResult struct {
	outDir   string
	sheet    *card.Sheet
	key      []string // 客观题参考答案
	fillings []card.Filling
	regNums  []string
}

// generate 出卡:写 layout.json / key.json / 空白 SVG / 各学生 PNG。
func generate(lf layoutFlags) (*genResult, error) {
	layout, err := resolveLayout(lf)
	if err != nil {
		return nil, err
	}
	sheet, err := layout.Build()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(lf.out, "students"), 0o755); err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewSource(lf.seed))
	key := randomAnswers(rng, sheet)

	gr := &genResult{outDir: lf.out, sheet: sheet, key: key}
	for i := 0; i < lf.students; i++ {
		regNum := fmt.Sprintf("%0*d", sheet.Layout.RegDigits, i+1)
		f := card.Filling{Answers: randomAnswers(rng, sheet)}
		for _, ch := range regNum {
			f.Reg = append(f.Reg, int(ch-'0'))
		}
		if lf.corruptStudent == i+1 && len(f.Reg) > 2 {
			// 模拟填涂漏一位:码制通道(条码/二维码)仍印真实考号,演示"码修复漏涂"
			f.RegUnfilled = make([]bool, len(f.Reg))
			f.RegUnfilled[len(f.Reg)/2] = true
		}
		gr.fillings = append(gr.fillings, f)
		gr.regNums = append(gr.regNums, regNum)
	}

	if err := writeJSON(filepath.Join(lf.out, "sheet.layout.json"), sheet.Layout); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(lf.out, "key.json"), map[string]any{"version": 1, "answers": key}); err != nil {
		return nil, err
	}
	// 空白卡矢量稿(打印母版)
	if err := os.WriteFile(filepath.Join(lf.out, "sheet.svg"), card.RenderSVG(sheet, nil), 0o644); err != nil {
		return nil, err
	}
	if lf.students == 0 {
		if err := card.RenderPNG(sheet, nil).SavePNG(filepath.Join(lf.out, "sheet.png")); err != nil {
			return nil, err
		}
	}
	for i, f := range gr.fillings {
		p := filepath.Join(lf.out, "students", fmt.Sprintf("card_%02d.png", i+1))
		if err := card.RenderPNG(sheet, &f).SavePNG(p); err != nil {
			return nil, err
		}
	}
	return gr, nil
}

func cmdGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	var lf layoutFlags
	addLayoutFlags(fs, &lf)
	if err := fs.Parse(args); err != nil {
		return err
	}
	gr, err := generate(lf)
	if err != nil {
		return err
	}
	fmt.Printf("版式: v%d,%d 道客观题 + %d 道主观题,%d 栏,考号 %d 位,输出 %d 份模拟学生卷\n",
		gr.sheet.Layout.Version, len(gr.sheet.Choice), len(gr.sheet.SubjectiveBoxes),
		gr.sheet.Layout.Columns, gr.sheet.Layout.RegDigits, lf.students)
	fmt.Printf("版式文件: %s\n", filepath.Join(gr.outDir, "sheet.layout.json"))
	fmt.Printf("参考答案: %s\n", filepath.Join(gr.outDir, "key.json"))
	fmt.Printf("打印母版: %s(浏览器打开可直接打印 A4)\n", filepath.Join(gr.outDir, "sheet.svg"))
	for i := range gr.fillings {
		fmt.Printf("学生卷 %2d: %s\n", i+1, filepath.Join(gr.outDir, "students", fmt.Sprintf("card_%02d.png", i+1)))
	}
	if lf.students == 0 {
		fmt.Printf("空白卡位图: %s\n", filepath.Join(gr.outDir, "sheet.png"))
	}
	return nil
}

// randomAnswers 按题型生成随机作答:choice/multiple 均匀选 A..;judge 取 T/F。
func randomAnswers(rng *rand.Rand, sheet *card.Sheet) []string {
	ans := make([]string, len(sheet.Choice))
	for qi, opts := range sheet.Choice {
		letters := card.OptionLetters(sheet.ChoiceKinds[qi], len(opts))
		ans[qi] = letters[rng.Intn(len(letters))]
	}
	return ans
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
