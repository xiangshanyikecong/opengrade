package main

// 试卷版面分析(半自动出卡):设计器上传一页试卷照片/扫描件 → 检测题目构成 →
// 返回版式草稿 + 每题题干截图 + 叠加标注图,设计器确认页人工微调后生成答题卡/导入题库。
// 无状态只读端点:不落盘、不建考试,检测在内存完成即弃。

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"

	"github.com/opengrade/opengrade/auth"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
)

// handleLayoutAnalyze POST /api/v1/layout/analyze:multipart 字段 image(单页 PNG/JPEG)。
// 响应 {questions, subjectives, notes, skewDeg, width, height, rects, overlay}:
//   - questions/subjectives 内 stem 为题干截图 JPEG 的 base64(前端预览 + 导入题库);
//   - rects 为检测矩形(参照 width/height 坐标系,class 0 文字行 1 选项行 2 主观区 3 题干);
//   - overlay 为叠加标注 JPEG 的 base64(data URL 前缀由前端拼)。
//
// 错误(双栏无法切分/全黑/无文字行)走统一 4xx 文案。
func handleLayoutAnalyze(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("解析 multipart 失败: %w", err))
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("缺少 image 字段: %w", err))
		return
	}
	defer file.Close()
	img, err := raster.DecodeImage(file)
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("图像解码失败: %w", err))
		return
	}
	res, err := omr.AnalyzePaper(omr.ToGray(img))
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err)
		return
	}
	overlay := ""
	if res.Overlay != nil {
		overlay = jpegBase64(res.Overlay, 88)
	}
	a := res.Analysis
	questions := make([]map[string]any, len(a.Questions))
	for i, q := range a.Questions {
		m := map[string]any{"kind": q.Kind, "options": q.Options, "score": q.Score}
		if q.Number > 0 {
			m["number"] = q.Number
		}
		if q.Note != "" {
			m["note"] = q.Note
		}
		if q.StemImg != nil {
			m["stem"] = jpegBase64(q.StemImg, 80)
		}
		questions[i] = m
	}
	subjectives := make([]map[string]any, len(a.Subjectives))
	for i, s := range a.Subjectives {
		m := map[string]any{"kind": s.Kind, "heightMM": s.HeightMM}
		if s.Note != "" {
			m["note"] = s.Note
		}
		if s.StemImg != nil {
			m["stem"] = jpegBase64(s.StemImg, 80)
		}
		subjectives[i] = m
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"questions":   questions,
		"subjectives": subjectives,
		"notes":       a.Notes,
		"skewDeg":     a.SkewDeg,
		"width":       a.Width,
		"height":      a.Height,
		"rects":       res.Rects,
		"overlay":     overlay,
	})
}

// jpegBase64 灰度图编码为 base64 JPEG(题干小图/叠加图内联返回)。
func jpegBase64(g *image.Gray, quality int) string {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, g, &jpeg.Options{Quality: quality}); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
