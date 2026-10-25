package main

// 试卷版面分析(半自动出卡):设计器上传一页试卷照片/扫描件 → 检测题目构成 →
// 返回版式草稿 + 叠加标注图,设计器确认页人工微调后生成答题卡。
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
)

// handleLayoutAnalyze POST /api/v1/layout/analyze:multipart 字段 image(单页 PNG/JPEG)。
// 响应 {questions, subjectives, notes, skewDeg, overlay}:overlay 为叠加标注 JPEG 的 base64
// (data URL 前缀由前端拼)。错误(双栏/全黑/无文字行)走统一 400 文案。
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
	img, _, err := image.Decode(file)
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
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, res.Overlay, &jpeg.Options{Quality: 88}); err == nil {
			overlay = base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	a := res.Analysis
	writeJSON(w, http.StatusOK, map[string]any{
		"questions":   a.Questions,
		"subjectives": a.Subjectives,
		"notes":       a.Notes,
		"skewDeg":     a.SkewDeg,
		"overlay":     overlay,
	})
}
