package omr_test

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
)

// 真实扫描回归集(吸收自 open-mcr 的 e2e fixture 思路):
// testdata/realscan/manifest.json 描述"真实异常扫描件 + 期望判读",防算法回归。
// synth 很强但终归是合成分布 —— 订书钉遮挡、边缘裁切、复印件、手机斜拍这类
// 真实损伤,只有真实样本能守住。样本随踩坑逐步积累,目录为空时跳过(脚手架先行)。
// 添加样本的完整指引见 docs/REALSCAN.md。

type realScanSample struct {
	Image         string   `json:"image"`                   // 扫描件文件名(相对 manifest 所在目录)
	Layout        string   `json:"layout"`                  // 版式 .layout.json(相对 manifest 所在目录)
	Reg           string   `json:"reg,omitempty"`           // 期望考号(空 = 不校验考号)
	Answers       []string `json:"answers,omitempty"`       // 期望逐题作答(空 = 不校验作答)
	AllowWarnings []string `json:"allowWarnings,omitempty"` // 允许出现的告警子串;其它告警一律算失败
	Note          string   `json:"note,omitempty"`          // 样本说明(什么损伤/为什么留着)
}

type realScanManifest struct {
	Samples []realScanSample `json:"samples"`
}

func TestRealScanRegression(t *testing.T) {
	dir := filepath.Join("testdata", "realscan")
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Skipf("无真实扫描回归集(testdata/realscan/manifest.json 不存在): %v", err)
	}
	var m realScanManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest 解析失败: %v", err)
	}
	if len(m.Samples) == 0 {
		t.Skip("回归集为空:按 docs/REALSCAN.md 添加真实异常扫描件后启用")
	}
	sheets := map[string]*card.Sheet{}
	bad := 0
	for i, s := range m.Samples {
		img := loadRealScan(t, filepath.Join(dir, s.Image))
		sheet := sheets[s.Layout]
		if sheet == nil {
			rawL, err := os.ReadFile(filepath.Join(dir, s.Layout))
			if err != nil {
				t.Fatalf("样本 %d 版式读取失败: %v", i+1, err)
			}
			var l card.Layout
			if err := json.Unmarshal(rawL, &l); err != nil {
				t.Fatalf("样本 %d 版式解析失败: %v", i+1, err)
			}
			built, err := l.Build()
			if err != nil {
				t.Fatalf("样本 %d 版式编译失败: %v", i+1, err)
			}
			sheets[s.Layout] = built
			sheet = built
		}
		res, _, err := omr.Read(sheet, img, omr.Options{})
		if err != nil {
			t.Errorf("样本 %d(%s)判读失败: %v", i+1, s.Image, err)
			bad++
			continue
		}
		if s.Reg != "" && res.RegNumber != s.Reg {
			t.Errorf("样本 %d(%s)考号 = %q, 期望 %q(警告 %v)", i+1, s.Image, res.RegNumber, s.Reg, res.Warnings)
			bad++
		}
		for qi, want := range s.Answers {
			if qi >= len(res.Answers) || res.Answers[qi] != want {
				got := "<越界>"
				if qi < len(res.Answers) {
					got = res.Answers[qi]
				}
				t.Errorf("样本 %d(%s)第 %d 题 = %q, 期望 %q", i+1, s.Image, qi+1, got, want)
				bad++
			}
		}
		for _, w := range res.Warnings {
			allowed := false
			for _, a := range s.AllowWarnings {
				if strings.Contains(w, a) {
					allowed = true
					break
				}
			}
			if !allowed {
				t.Errorf("样本 %d(%s)出现未预期告警: %s", i+1, s.Image, w)
				bad++
			}
		}
	}
	if bad > 0 {
		t.Fatalf("真实扫描回归 %d 处失败(样本见上;若为判读改进导致的期望变化,请人工核对后更新 manifest)", bad)
	}
	fmt.Printf("真实扫描回归: %d 个样本全部通过\n", len(m.Samples))
}

// loadRealScan 解码 PNG/JPEG(EXIF 方向自动转正)。
func loadRealScan(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", path, err)
	}
	defer f.Close()
	img, err := raster.DecodeImage(f)
	if err != nil {
		t.Fatalf("解码 %s 失败: %v", path, err)
	}
	return img
}
