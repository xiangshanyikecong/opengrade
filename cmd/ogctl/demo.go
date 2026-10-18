package main

import (
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/export"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/roster"
	"github.com/opengrade/opengrade/score"
	"github.com/opengrade/opengrade/synth"
)

// cmdDemo 跑通产品核心叙事的最小闭环:
// 出卡 → 模拟真实扫描损伤 → OMR 客观判读 → 异常卷名册推荐 → 主观题切片 →
// 模拟人工阅卷 → 合并统分 → 讲评建议 → 成绩册 xlsx/csv + 讲评课件 pptx。
func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	var lf layoutFlags
	fs.StringVar(&lf.out, "o", "demo_output", "输出目录")
	fs.StringVar(&lf.layoutFile, "layout-file", "", "自定义版式 JSON(缺省使用内置 v2 示例版式)")
	fs.IntVar(&lf.students, "students", 5, "模拟学生份数")
	fs.Int64Var(&lf.seed, "seed", 1, "随机种子(复现实验)")
	lf.corruptStudent = 3 // 第 3 卷模拟考号漏涂,演示异常卷自愈
	if err := fs.Parse(args); err != nil {
		return err
	}
	if lf.layoutFile == "" {
		def := card.DefaultLayout()
		lf.preLayout = &def
	}
	fmt.Println("== OpenGrade 端到端演示(v2:客观 OMR + 异常卷自愈 + 主观切片阅卷)==")
	fmt.Printf("① 生成 %d 份模拟学生卷(300 DPI,第 %d 卷模拟考号漏涂)…\n", lf.students, lf.corruptStudent)
	gr, err := generate(lf)
	if err != nil {
		return err
	}
	fmt.Printf("   版式 v%d:%d 道客观题(choice/judge/multiple)+ %d 道主观题(切片阅卷)\n",
		gr.sheet.Layout.Version, len(gr.sheet.Choice), len(gr.sheet.SubjectiveBoxes))

	// 学生名册:全部真名学生 + 两个相近考号干扰项(考验推荐精度)
	rosterStudents := []roster.Student{
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 1), Name: "李雷", Class: "三年二班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 2), Name: "韩梅梅", Class: "三年二班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 3), Name: "王小明", Class: "三年二班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 4), Name: "赵小红", Class: "三年一班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 5), Name: "孙小刚", Class: "三年一班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 15), Name: "干扰项甲", Class: "三年三班"},
		{Reg: fmt.Sprintf("%0*d", gr.sheet.Layout.RegDigits, 35), Name: "干扰项乙", Class: "三年三班"},
	}
	writeJSON(filepath.Join(gr.outDir, "roster.json"), rosterStudents)

	rng := rand.New(rand.NewSource(lf.seed*31 + 7))
	var results []*omr.Result
	var allSlices [][]omr.Slice
	var recognized, expect int

	for i, f := range gr.fillings {
		cleanPath := filepath.Join(gr.outDir, "students", fmt.Sprintf("card_%02d.png", i+1))
		clean, err := loadImage(cleanPath)
		if err != nil {
			return err
		}
		gray := omr.ToGray(clean)
		scan := synth.Distort(gray, rng)
		scanPath := filepath.Join(gr.outDir, "students", fmt.Sprintf("scan_%02d.jpg", i+1))
		if err := saveJPEG(scanPath, scan, 82); err != nil {
			return err
		}
		res, dbg, err := omr.Read(gr.sheet, scan, omr.Options{})
		if err != nil {
			return fmt.Errorf("学生 %d 识别失败: %w", i+1, err)
		}
		results = append(results, res)
		if err := writeJSON(filepath.Join(gr.outDir, "students", fmt.Sprintf("result_%02d.json", i+1)), res); err != nil {
			return err
		}
		// 主观题切片(来自矫正图)
		slices := omr.SliceRectified(dbg.Rectified, gr.sheet, i+1, res.RegNumber)
		allSlices = append(allSlices, slices)
		if err := os.MkdirAll(filepath.Join(gr.outDir, "slices"), 0o755); err != nil {
			return err
		}
		for _, sl := range slices {
			p := filepath.Join(gr.outDir, "slices", fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
			if err := saveImage(p, sl.Image); err != nil {
				return err
			}
		}
		// 识别质量对账:与真值涂卡逐题比对
		for qi, ans := range f.Answers {
			expect++
			if qi < len(res.Answers) && res.Answers[qi] == ans {
				recognized++
			}
		}
	}
	fmt.Println("   每份卷已施加:随机透视 + ±4° 旋转 + 光照不均 + 高斯噪声 + JPEG 压缩")
	if n := len(allSlices); n > 0 && len(allSlices[0]) > 0 {
		fmt.Printf("   主观题切片:每卷 %d 片,共 %d 片 → %s/slices/\n",
			len(allSlices[0]), len(allSlices)*len(allSlices[0]), gr.outDir)
	}

	// 异常卷自愈演示:考号漏涂的卷 → 名册"可读位匹配"推荐 → 自动确认
	assignments := map[string]string{}
	for i, res := range results {
		if res.RegValid {
			continue
		}
		sug := roster.Suggest(res.RegMarks, rosterStudents, 3)
		fmt.Printf("\n② 异常卷自愈:学生 %d 考号无效(逐位明细 %v)\n", i+1, formatRegMarks(res.RegMarks))
		for k, c := range sug {
			fmt.Printf("   候选 %d:%s %s(%s)\n", k+1, c.Reg, c.Name, c.Class)
		}
		if len(sug) > 0 {
			assignments[fmt.Sprintf("s%d", i+1)] = sug[0].Reg
			fmt.Printf("   → 工作台中一键确认(演示取首选):指派为 %s %s\n", sug[0].Reg, sug[0].Name)
		}
	}
	// 应用指派后的结果副本(真实系统中由 ogserver 完成)
	assigned := make([]*omr.Result, len(results))
	for i, res := range results {
		cp := *res
		if reg, ok := assignments[fmt.Sprintf("s%d", i+1)]; ok {
			cp.RegNumber = reg
			cp.RegValid = true
		}
		assigned[i] = &cp
	}

	// 模拟人工阅卷:随机给分 + 标注(真实场景中由 Grader Studio 工作台完成)
	grades := map[string]score.Grade{}
	subjectives := gr.sheet.Layout.Subjectives
	for si := range gr.fillings {
		for qi := range subjectives {
			ratio := []float64{1, 0.8, 0.6, 0.4, 0.2}[rng.Intn(5)]
			mark := ""
			switch r := rng.Intn(10); {
			case r < 6:
				mark = "check"
			case r < 8:
				mark = "cross"
			}
			grades[score.GradeKey(si+1, qi+1)] = score.Grade{
				Score:  float64(int(subjectives[qi].Score*ratio*2)) / 2,
				Mark:   mark,
				Graded: true,
			}
		}
	}

	rep := score.Merge(gr.sheet.Layout.Questions, gr.key, assigned, subjectives, grades)
	if err := writeJSON(filepath.Join(gr.outDir, "report.json"), rep); err != nil {
		return err
	}

	fmt.Println("\n③ 合并统分结果(客观 OMR 对分 + 主观人工分):")
	w := tabWriter()
	fmt.Fprintln(w, "卷号\t涂卡考号\t最终考号\t客观\t主观\t总分\t标记")
	for i, s := range rep.Students {
		fmt.Fprintf(w, "%d\t%s\t%s\t%.0f\t%.1f\t%.1f/%.0f\t%s\n",
			s.Index, gr.regNums[i], s.RegNumber, s.Objective, s.Subjective, s.Score, rep.Total, flagText(s.Flagged))
	}
	w.Flush()
	fmt.Printf("   卷面满分 %.0f,均分 %.1f\n", rep.Total, rep.Mean)

	fmt.Printf("\n④ 引擎识别对账:客观涂卡判读 %d/%d = %.1f%%\n", recognized, expect, 100*float64(recognized)/float64(expect))

	// 讲评建议 + 讲评课件
	lowQs := score.LowRateQuestions(rep, 0.6)
	fmt.Println("\n⑤ 讲评建议(得分率 < 60%)与讲评课件:")
	if len(lowQs) == 0 {
		fmt.Println("   本卷无低分率题目")
	}
	var slides []export.ReviewSlide
	for i, st := range lowQs {
		line := ""
		switch st.Kind {
		case card.KindFillBlank, card.KindSubjective:
			line = fmt.Sprintf("第 %d 题(%s):均分 %.1f/%.0f", st.Index, kindCN(st.Kind), st.Mean, st.Max)
		default:
			line = fmt.Sprintf("第 %d 题(%s):得分率 %.0f%%", st.Index, kindCN(st.Kind), st.Rate*100)
		}
		if i < 8 {
			fmt.Println("   " + line)
		}
		if i < 12 {
			slides = append(slides, buildDemoSlide(gr, assigned, grades, st, line))
		}
	}
	overview := []string{
		fmt.Sprintf("卷数 %d · 卷面满分 %.0f · 均分 %.1f", rep.Count, rep.Total, rep.Mean),
		fmt.Sprintf("得分率低于 60%% 的题目 %d 道", len(lowQs)),
	}
	pptx, err := export.ReviewPPTXZip("试卷讲评 · 演示考试", overview, slides)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gr.outDir, "review.pptx"), pptx, 0o644); err != nil {
		return err
	}
	xlsx, err := export.ScoresXlsxZip(rep, questionLabels(gr.sheet, rep))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gr.outDir, "report.xlsx"), xlsx, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gr.outDir, "report.csv"), export.ScoresCSV(rep), 0o644); err != nil {
		return err
	}
	if len(lowQs) > 8 {
		fmt.Printf("   …另有 %d 题低于 60%%\n", len(lowQs)-8)
	}
	fmt.Printf("   讲评课件已生成:%s/review.pptx(低分率题 + 主观题典型切片)\n", gr.outDir)
	fmt.Printf("   成绩册已生成:%s/report.xlsx、report.csv\n", gr.outDir)

	fmt.Printf("\n全部产物已写入 %s/(学生卷、模拟扫描件、识别结果、切片、合并报告、成绩册、讲评课件)\n", gr.outDir)
	return nil
}

// questionLabels 生成逐题显示名。
func questionLabels(sheet *card.Sheet, rep *score.Report) []string {
	out := make([]string, len(rep.PerQuestion))
	nObj := len(sheet.Choice)
	for i, q := range rep.PerQuestion {
		if q.Index <= nObj {
			out[i] = fmt.Sprintf("%d(%s)", q.Index, kindCN(sheet.ChoiceKinds[q.Index-1]))
		} else {
			out[i] = fmt.Sprintf("%d(%s)", q.Index, kindCN(sheet.Layout.Subjectives[q.Index-nObj-1].Kind))
		}
	}
	return out
}

// buildDemoSlide 组装单题讲评页:客观题给选项分布,主观题给典型切片。
func buildDemoSlide(gr *genResult, results []*omr.Result, grades map[string]score.Grade, st score.QuestionStat, headline string) export.ReviewSlide {
	rs := export.ReviewSlide{Title: headline}
	if st.Index <= len(gr.sheet.Choice) {
		qi := st.Index - 1
		letters := card.OptionLetters(gr.sheet.ChoiceKinds[qi], len(gr.sheet.Choice[qi]))
		dist := map[string]int{}
		for _, res := range results {
			if qi < len(res.Answers) && res.Answers[qi] != "" {
				dist[res.Answers[qi]]++
			}
		}
		rs.Lines = append(rs.Lines, fmt.Sprintf("正确答案:%s", gr.key[qi]))
		for _, l := range letters {
			note := ""
			if l == gr.key[qi] {
				note = " ← 正确"
			}
			rs.Lines = append(rs.Lines, fmt.Sprintf("选 %s:%d 人%s", l, dist[l], note))
		}
		return rs
	}
	// 主观题:最高分/最低分切片
	sq := st.Index - len(gr.sheet.Choice)
	type sg struct {
		student int
		s       float64
	}
	var g []sg
	for si := range gr.fillings {
		if grade, ok := grades[score.GradeKey(si+1, sq)]; ok && grade.Graded {
			g = append(g, sg{si + 1, grade.Score})
		}
	}
	if len(g) > 0 {
		sort.Slice(g, func(i, j int) bool { return g[i].s > g[j].s })
		top, low := g[0], g[len(g)-1]
		for _, p := range []sg{top, low} {
			b, err := os.ReadFile(filepath.Join(gr.outDir, "slices", fmt.Sprintf("s%02d_q%02d.png", p.student, sq)))
			if err != nil {
				continue
			}
			label := "典型满分"
			if p.student == low.student && len(g) > 1 {
				label = "待改进"
			}
			rs.Images = append(rs.Images, export.ReviewImage{Data: b, Label: fmt.Sprintf("%s:考号 %s", label, gr.regNums[p.student-1])})
		}
	}
	return rs
}

func formatRegMarks(m [][]int) string {
	parts := make([]string, len(m))
	for i, marks := range m {
		if len(marks) == 0 {
			parts[i] = "_"
			continue
		}
		s := ""
		for _, d := range marks {
			s += string(rune('0' + d))
		}
		parts[i] = s
	}
	return strings.Join(parts, "")
}

func tabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
}

func saveJPEG(path string, img image.Image, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	return f.Close()
}

func kindCN(k string) string {
	switch k {
	case card.KindJudge:
		return "判断"
	case card.KindMultiple:
		return "多选"
	case card.KindFillBlank:
		return "填空"
	case card.KindSubjective:
		return "解答"
	default:
		return "单选"
	}
}

func flagText(f bool) string {
	if f {
		return "⚑"
	}
	return ""
}
