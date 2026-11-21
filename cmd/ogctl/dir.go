package main

// 目录批量协议(吸收自 OMRChecker 的"目录继承模板"约定):
// `ogctl read -image <目录>` 批量判读该目录下全部扫描件。版式与读卡参数写在
// 目录里的 ogctl.json(或其任一祖先目录 —— 子目录分箱、父目录共享配置),
// 一场考试一个目录,换机器/换操作员都不用改命令行。结果写 results.csv。

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
)

// dirConfig 是 ogctl.json 的内容;零值字段用命令行旗标或缺省兜底。
type dirConfig struct {
	Layout         string  `json:"layout"`                 // 版式文件路径(相对配置文件所在目录)
	Threshold      string  `json:"threshold,omitempty"`    // otsu | adaptive
	ThresholdValue float64 `json:"thresholdValue,omitempty"` // 涂卡判定阈值(0 取缺省 0.42)
	Gamma          float64 `json:"gamma,omitempty"`
	Median         int     `json:"median,omitempty"`
	CLAHE          int     `json:"clahe,omitempty"`
}

// findDirConfig 从 dir 逐级向上找 ogctl.json;找不到返回 (nil, nil)。
func findDirConfig(dir string) (string, *dirConfig, error) {
	d := dir
	for {
		p := filepath.Join(d, "ogctl.json")
		if _, err := os.Stat(p); err == nil {
			var c dirConfig
			if err := readJSONFile(p, &c); err != nil {
				return "", nil, fmt.Errorf("读取 %s 失败: %w", p, err)
			}
			return p, &c, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", nil, nil
		}
		d = parent
	}
}

// cmdReadDir 批量判读目录下全部扫描件(PNG/JPEG,按文件名排序),结果写 results.csv。
func cmdReadDir(dir string, layoutFlag, outFlag string, th float64, mode string, opt omr.Options) error {
	cfgPath, cfg, err := findDirConfig(dir)
	if err != nil {
		return err
	}
	// 版式:命令行旗标优先,其次配置;路径相对配置文件所在目录解析
	layoutPath := layoutFlag
	if layoutPath == "" && cfg != nil {
		layoutPath = cfg.Layout
		if layoutPath != "" && !filepath.IsAbs(layoutPath) {
			layoutPath = filepath.Join(filepath.Dir(cfgPath), layoutPath)
		}
	}
	if layoutPath == "" {
		return fmt.Errorf("目录 %s 未找到 ogctl.json(或其中未配置 layout),且未提供 -layout", dir)
	}
	var layout card.Layout
	if err := readJSONFile(layoutPath, &layout); err != nil {
		return fmt.Errorf("读取版式失败: %w", err)
	}
	sheet, err := layout.Build()
	if err != nil {
		return err
	}
	// 读卡参数:旗标显式给出时覆盖配置(零值 = 未给)
	if cfg != nil {
		if mode == "otsu" && cfg.Threshold != "" {
			mode = cfg.Threshold
		}
		if th == 0.42 && cfg.ThresholdValue > 0 {
			th = cfg.ThresholdValue
		}
		if opt.Gamma == 0 {
			opt.Gamma = cfg.Gamma
		}
		if opt.Median == 0 {
			opt.Median = cfg.Median
		}
		if opt.CLAHE == 0 {
			opt.CLAHE = cfg.CLAHE
		}
	}
	opt.MarkThreshold = th
	opt.Threshold = mode

	var imgs []string
	for _, ext := range []string{".jpg", ".jpeg", ".png"} {
		ms, err := filepath.Glob(filepath.Join(dir, "*"+ext))
		if err != nil {
			return err
		}
		imgs = append(imgs, ms...)
	}
	// 大小写扩展名兜底(手机导出常见 .JPG)
	for _, ext := range []string{".JPG", ".JPEG", ".PNG"} {
		ms, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		imgs = append(imgs, ms...)
	}
	sort.Strings(imgs)
	if len(imgs) == 0 {
		return fmt.Errorf("目录 %s 下没有扫描件(jpg/jpeg/png)", dir)
	}

	csvPath := filepath.Join(dir, "results.csv")
	if outFlag != "" {
		csvPath = outFlag
	}
	f, err := os.Create(csvPath)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"file", "reg", "regValid", "answers", "warnings"})

	fmt.Printf("批量判读 %d 张(版式 %s,配置 %s):\n", len(imgs), layoutPath, cfgPath)
	bad := 0
	for _, p := range imgs {
		img, err := loadImage(p)
		if err != nil {
			bad++
			_ = w.Write([]string{filepath.Base(p), "", "false", "", "图像解码失败: " + err.Error()})
			continue
		}
		res, _, err := omr.Read(sheet, img, opt)
		if err != nil {
			bad++
			_ = w.Write([]string{filepath.Base(p), "", "false", "", err.Error()})
			continue
		}
		warn := strings.Join(res.Warnings, "; ")
		if warn != "" {
			bad++
		}
		_ = w.Write([]string{filepath.Base(p), res.RegNumber, fmt.Sprintf("%v", res.RegValid),
			strings.Join(res.Answers, "|"), warn})
	}
	w.Flush()
	fmt.Printf("完成: %d 张,其中 %d 张有告警/失败。结果: %s\n", len(imgs), bad, csvPath)
	return nil
}
