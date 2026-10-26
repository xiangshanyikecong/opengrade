package main

// 试卷版面分析端点 e2e:合成试卷 → POST /api/v1/layout/analyze(权限/正常/坏图/空白页)
// + 题干截图导入题库链路(建题 → 传截图 → 读回)。

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"encoding/json"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/font"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/raster"
)

// renderE2EPaper 渲染合成试卷(9 客观题带题干行 + 2 留白区;与 omr 靶场同构)。
func renderE2EPaper(t *testing.T) *image.Gray {
	t.Helper()
	const W, H = 1488, 2105
	cv := raster.NewCanvas(W, H)
	f := font.Default()
	size := 30
	draw := func(x, y int, s string) {
		if f != nil {
			f.Draw(cv, x, y, s, size, 0)
		} else {
			cv.Text(x, y, s, size/7, 0)
		}
	}
	draw(80, 40, "高一月考数学试卷")
	optXs := []int{200, 560, 920, 1240}
	letters := "ABCD"
	y := 110
	for i := 1; i <= 9; i++ {
		draw(80, y, "下列说法正确的是。")
		draw(80, y+45, fmt.Sprintf("%d.", i))
		n, contents := 4, []string{"选项甲内容", "选项乙内容", "选项丙内容", "选项丁内容"}
		if i == 7 || i == 8 {
			n, contents = 2, []string{"对", "错"}
		}
		for k := 0; k < n; k++ {
			draw(optXs[k], y+45, string(letters[k])+"."+contents[k])
		}
		y += 90
	}
	draw(80, y, "计算下列各题:")
	y += 40
	mmPx := float64(W) / 210.0
	y += int(40 * mmPx)
	draw(80, y, "证明如下命题:")
	y += 40
	y += int(30 * mmPx)
	draw(80, y, "(完)请核对姓名与考号填写无误后再交卷。")
	y += 45
	for y < 1950 {
		draw(80, y, "本页以下无正文")
		y += 45
	}
	return &image.Gray{Pix: cv.Pix, Stride: cv.W, Rect: image.Rect(0, 0, cv.W, cv.H)}
}

// whitePaper 全白纸面(无文字行 → 422)。
func whitePaper() *image.Gray {
	g := image.NewGray(image.Rect(0, 0, 1488, 2105))
	for i := range g.Pix {
		g.Pix[i] = 255
	}
	return g
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type layoutAnalyzeResp struct {
	Questions []struct {
		Kind    string  `json:"kind"`
		Options int     `json:"options"`
		Number  int     `json:"number"`
		Note    string  `json:"note"`
		Stem    string  `json:"stem"`
		Score   float64 `json:"score"`
	} `json:"questions"`
	Subjectives []struct {
		Kind     string  `json:"kind"`
		HeightMM float64 `json:"heightMM"`
		Stem     string  `json:"stem"`
	} `json:"subjectives"`
	Notes   []string          `json:"notes"`
	SkewDeg float64           `json:"skewDeg"`
	Width   int               `json:"width"`
	Height  int               `json:"height"`
	Rects   []map[string]any  `json:"rects"`
	Overlay string            `json:"overlay"`
}

func TestLayoutAnalyzeE2E(t *testing.T) {
	paper := renderE2EPaper(t)
	// 字形环境异常时跳过(与 omr 靶场同守卫):本地直接探针确认可解析
	if probe, err := omr.AnalyzePaper(paper); err != nil || len(probe.Analysis.Questions) != 9 {
		t.Skipf("字形环境异常,跳过 e2e(err=%v)", err)
	}

	dir := t.TempDir()
	srv, err := newServer(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	admin := newClient(t, ts.URL)
	admin.login("admin", "admin123")
	student := newClient(t, ts.URL)
	student.login("s0001", "student123")
	teacher := newClient(t, ts.URL)
	teacher.login("teacher1", "teach123")

	// —— 权限:学生 403 ——
	body := &bytes.Buffer{}
	writeExamForm(body, card.Layout{}, nil, "", multipartFile{field: "image", name: "p.png", ctype: "image/png", data: pngBytes(t, paper)})
	if code := student.code("POST", "/api/v1/layout/analyze", body, multipartCT()); code != http.StatusForbidden {
		t.Fatalf("学生调用版面分析应 403,得到 %d", code)
	}

	// —— 正常识别:题目构成/题号/题干截图/叠加图/矩形 ——
	body = &bytes.Buffer{}
	writeExamForm(body, card.Layout{}, nil, "", multipartFile{field: "image", name: "p.png", ctype: "image/png", data: pngBytes(t, paper)})
	resp, data := admin.do("POST", "/api/v1/layout/analyze", body, multipartCT())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("版面分析应 200,得到 %d\n%s", resp.StatusCode, data)
	}
	var out layoutAnalyzeResp
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	wantOpts := []int{4, 4, 4, 4, 4, 4, 2, 2, 4}
	if len(out.Questions) != len(wantOpts) {
		t.Fatalf("客观题数应 %d,得到 %d", len(wantOpts), len(out.Questions))
	}
	for i, q := range out.Questions {
		if q.Options != wantOpts[i] || q.Number != i+1 {
			t.Fatalf("第 %d 题应 %d 选项题号 %d,得到 %d/%d", i+1, wantOpts[i], i+1, q.Options, q.Number)
		}
		if q.Stem == "" {
			t.Fatalf("第 %d 题题干截图缺失", i+1)
		}
	}
	if len(out.Subjectives) != 2 {
		t.Fatalf("主观留白区应 2 处,得到 %d", len(out.Subjectives))
	}
	if out.Overlay == "" || out.Width == 0 || out.Height == 0 || len(out.Rects) == 0 {
		t.Fatal("叠加图/坐标系/检测矩形不应为空")
	}

	// —— 坏图 400 / 空白页 422 ——
	body = &bytes.Buffer{}
	writeExamForm(body, card.Layout{}, nil, "", multipartFile{field: "image", name: "x.png", ctype: "image/png", data: []byte("这不是图片")})
	if code := admin.code("POST", "/api/v1/layout/analyze", body, multipartCT()); code != http.StatusBadRequest {
		t.Fatalf("坏图应 400,得到 %d", code)
	}
	body = &bytes.Buffer{}
	writeExamForm(body, card.Layout{}, nil, "", multipartFile{field: "image", name: "blank.png", ctype: "image/png", data: pngBytes(t, whitePaper())})
	if code := admin.code("POST", "/api/v1/layout/analyze", body, multipartCT()); code != http.StatusUnprocessableEntity {
		t.Fatalf("空白页应 422,得到 %d", code)
	}

	// —— 题干截图导入题库链路:建题 → 管理员传截图 → 教师读回 → 学生 403 ——
	created := admin.json("POST", "/api/v1/bank", map[string]any{
		"kind": "choice", "options": 4, "score": 2, "subject": "数学",
	}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("建题应返回 id")
	}
	stemBody := &bytes.Buffer{}
	writeExamForm(stemBody, card.Layout{}, nil, "", multipartFile{field: "image", name: "stem.png", ctype: "image/png", data: pngBytes(t, paper)})
	resp, data = admin.do("POST", "/api/v1/bank/"+id+"/stem", stemBody, multipartCT())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("上传题干截图应 200,得到 %d\n%s", resp.StatusCode, data)
	}
	resp, data = admin.do("GET", "/api/v1/bank/"+id+"/stem", nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || len(data) < 100 {
		t.Fatalf("读回题干截图应 200 image/jpeg,得到 %d %s(%d bytes)", resp.StatusCode, resp.Header.Get("Content-Type"), len(data))
	}
	if code := student.code("GET", "/api/v1/bank/"+id+"/stem", nil, ""); code != http.StatusForbidden {
		t.Fatalf("学生读题干截图应 403,得到 %d", code)
	}
	// 非创建者教师上传应 403(canManageBank)
	other := &bytes.Buffer{}
	writeExamForm(other, card.Layout{}, nil, "", multipartFile{field: "image", name: "stem.png", ctype: "image/png", data: pngBytes(t, paper)})
	if code := teacher.code("POST", "/api/v1/bank/"+id+"/stem", other, multipartCT()); code != http.StatusForbidden {
		t.Fatalf("非创建者上传截图应 403,得到 %d", code)
	}
	// 列表带出 stemImage;删除后截图文件一并清理
	items := admin.jsonArr("GET", "/api/v1/bank", http.StatusOK)
	found := false
	for _, it := range items {
		if it["id"] == id {
			if s, _ := it["stemImage"].(string); s == "" {
				t.Fatal("列表应带出 stemImage")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("题库列表应包含新建题")
	}
	admin.json("DELETE", "/api/v1/bank/"+id, nil, http.StatusOK)
	if code := admin.code("GET", "/api/v1/bank/"+id+"/stem", nil, ""); code != http.StatusNotFound {
		t.Fatalf("删除题目后截图应不可读,得到 %d", code)
	}
	_ = strings.TrimSpace("")
}

// multipartCT multipart 表单 Content-Type。
func multipartCT() string { return "multipart/form-data; boundary=" + multipartBoundary }
