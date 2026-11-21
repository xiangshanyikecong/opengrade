package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// cmdSlice 从扫描件中裁出主观题切片(离线批处理路径;在线路径走 ogserver)。
func cmdSlice(args []string) error {
	fs := flag.NewFlagSet("slice", flag.ExitOnError)
	layoutPath := fs.String("layout", "", "版式文件(.layout.json,必填)")
	imagePath := fs.String("image", "", "整卷扫描件(PNG/JPEG,必填)")
	out := fs.String("o", "slices", "切片输出目录")
	student := fs.Int("student", 1, "学生卷序号(用于命名)")
	threshold := fs.String("threshold", "otsu", "二值化方式:otsu | adaptive")
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
	res, dbg, err := omr.Read(sheet, img, omr.Options{Threshold: *threshold})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	slices := omr.SliceRectified(dbg.Rectified, sheet, *student, res.RegNumber)
	for _, sl := range slices {
		p := filepath.Join(*out, fmt.Sprintf("s%02d_q%02d.png", sl.StudentIndex, sl.QuestionIndex))
		if err := saveImage(p, sl.Image); err != nil {
			return err
		}
		fmt.Printf("切片: %s(%s,考号 %s)\n", p, sl.QuestionKind, sl.RegNumber)
	}
	if len(slices) == 0 {
		fmt.Println("该版式没有主观题作答区,未产生切片")
	}
	return nil
}
