package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

func cmdScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	keyPath := fs.String("key", "", "参考答案文件(key.json,必填)")
	results := fs.String("results", "", "识别结果 JSON,逗号分隔,每段支持通配符(必填)")
	out := fs.String("out", "", "报告 JSON 输出路径(缺省仅打印)")
	low := fs.Float64("low", 0.6, "讲评关注线:得分率低于该值的题目列入建议")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *results == "" {
		return fmt.Errorf("必须提供 -key 与 -results")
	}

	var keyFile struct {
		Answers []string `json:"answers"`
	}
	if err := readJSONFile(*keyPath, &keyFile); err != nil {
		return fmt.Errorf("读取参考答案失败: %w", err)
	}

	var list []*omr.Result
	for _, seg := range strings.Split(*results, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		matches, err := filepath.Glob(seg)
		if err != nil || len(matches) == 0 {
			matches = []string{seg}
		}
		for _, m := range matches {
			var r omr.Result
			if err := readJSONFile(m, &r); err != nil {
				return fmt.Errorf("读取结果 %s 失败: %w", m, err)
			}
			list = append(list, &r)
		}
	}

	rep := score.GradeObjective(keyFile.Answers, list)
	if *out != "" {
		if err := writeJSON(*out, rep); err != nil {
			return err
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "卷号\t考号\t考号有效\t得分/满分\n")
	for _, s := range rep.Students {
		fmt.Fprintf(w, "%d\t%s\t%v\t%.0f/%.0f\n", s.Index, s.RegNumber, s.RegValid, s.Score, s.Total)
	}
	_ = w.Flush() // 写 stdout,无后续动作
	fmt.Printf("共 %d 卷,均分 %.1f/%.0f\n", rep.Count, rep.Mean, rep.Total)
	lowQs := score.LowRateQuestions(rep, *low)
	if len(lowQs) > 0 {
		fmt.Printf("讲评建议(得分率 < %.0f%%):\n", *low*100)
		for _, st := range lowQs {
			fmt.Printf("  第 %d 题:得分率 %.0f%%\n", st.Index, st.Rate*100)
		}
	}
	return nil
}
