package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// cmdKey 扫读教师答案卡:ogctl key -layout x.layout.json -image teacher.jpg
// 约定考号全 9(位数 = 版式考号位)的卷为答案卡(与 Web 端扫描上传的识别口径一致);
// 判读走同一条 OMR 管线,输出逐题作答(JSON 数组),可直接粘进建考答案框或
// POST /api/v1/exams/{id}/key。
func cmdKey(args []string) error {
	fs := flag.NewFlagSet("key", flag.ExitOnError)
	layoutPath := fs.String("layout", "", "版式文件(.layout.json,必填)")
	imagePath := fs.String("image", "", "答案卡图像(PNG/JPEG,必填)")
	th := fs.Float64("threshold-value", 0.42, "涂卡判定阈值(墨迹占比 0~1)")
	mode := fs.String("threshold", "otsu", "二值化方式:otsu | adaptive")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *layoutPath == "" || *imagePath == "" {
		return fmt.Errorf("必须提供 -layout 与 -image")
	}
	var layout card.Layout
	if err := readJSONFile(*layoutPath, &layout); err != nil {
		return fmt.Errorf("读取版式失败: %w", err)
	}
	sheet, err := layout.Build()
	if err != nil {
		return err
	}
	img, err := loadImage(*imagePath)
	if err != nil {
		return err
	}
	res, _, err := omr.Read(sheet, img, omr.Options{MarkThreshold: *th, Threshold: *mode})
	if err != nil {
		return err
	}
	if sheet.Layout.RegDigits > 0 {
		want := ""
		for i := 0; i < sheet.Layout.RegDigits; i++ {
			want += "9"
		}
		if res.RegNumber != want {
			fmt.Printf("警告: 考号判读为 %q,不是答案卡约定值 %s —— 请确认扫的是答案卡,或核对判读告警\n", res.RegNumber, want)
		}
	}
	ansJSON, _ := json.Marshal(res.Answers)
	fmt.Println(string(ansJSON))
	if len(res.Warnings) > 0 {
		fmt.Printf("警告 %d 条:\n", len(res.Warnings))
		for _, w := range res.Warnings {
			fmt.Println("  -", w)
		}
	}
	return nil
}
