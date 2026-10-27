package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
)

func cmdRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	layoutPath := fs.String("layout", "", "版式文件(.layout.json,必填)")
	imagePath := fs.String("image", "", "答题卡图像(PNG/JPEG,必填)")
	out := fs.String("out", "", "结果 JSON 输出路径(缺省仅打印)")
	debugDir := fs.String("debug", "", "调试产物目录(矫正图/叠加图)")
	th := fs.Float64("threshold-value", 0.42, "涂卡判定阈值(墨迹占比 0~1)")
	mode := fs.String("threshold", "otsu", "二值化方式:otsu | adaptive(强阴影/翻拍推荐)")
	gamma := fs.Float64("gamma", 0, "灰度伽马(<1 提亮阴影/褪色件;0=不启用)")
	median := fs.Int("median", 0, "中值滤波窗口(3/5 去椒盐噪声;0=不启用)")
	clahe := fs.Int("clahe", 0, "CLAHE 分块数(常用 8,弱光/低对比增墨;0=不启用)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// 目录批量模式:目录继承 ogctl.json 配置,结果写 results.csv
	if st, err := os.Stat(*imagePath); err == nil && st.IsDir() {
		return cmdReadDir(*imagePath, *layoutPath, *out, *th, *mode,
			omr.Options{Gamma: *gamma, Median: *median, CLAHE: *clahe})
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
	res, dbg, err := omr.Read(sheet, img, omr.Options{MarkThreshold: *th, Threshold: *mode,
		Gamma: *gamma, Median: *median, CLAHE: *clahe})
	if err != nil {
		return err
	}

	if *debugDir != "" {
		if err := os.MkdirAll(*debugDir, 0o755); err != nil {
			return err
		}
		if err := saveImage(filepath.Join(*debugDir, "rectified.png"), dbg.Rectified); err != nil {
			return err
		}
		if err := saveImage(filepath.Join(*debugDir, "overlay.png"), omr.RenderDebugOverlay(dbg, sheet, *th)); err != nil {
			return err
		}
		fmt.Printf("调试产物: %s{rectified,overlay}.png\n", *debugDir+string(filepath.Separator))
	}

	if *out != "" {
		if err := writeJSON(*out, res); err != nil {
			return err
		}
	}
	fmt.Printf("考号: %s(有效=%v)\n", res.RegNumber, res.RegValid)
	fmt.Printf("作答: %v\n", res.Answers)
	if len(res.Warnings) > 0 {
		fmt.Printf("警告 %d 条:\n", len(res.Warnings))
		for _, w := range res.Warnings {
			fmt.Println("  -", w)
		}
	}
	if *out != "" {
		fmt.Println("结果文件:", *out)
	}
	return nil
}

// loadImage 解码 PNG/JPEG,手机直拍等带 EXIF Orientation 的图像自动转正。
func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := raster.DecodeImage(f)
	if err != nil {
		return nil, fmt.Errorf("解码 %s 失败: %w", path, err)
	}
	return img, nil
}

func saveImage(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	return f.Close()
}
