package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/opengrade/opengrade/card"
	"github.com/opengrade/opengrade/omr"
	"github.com/opengrade/opengrade/score"
)

func testReport() *score.Report {
	return score.Merge(
		[]card.QuestionSpec{
			{Kind: "choice", Score: 2},
			{Kind: "judge", Score: 2},
			{Kind: "multiple", Score: 2},
		},
		[]string{"A", "T", "AC"},
		[]*omr.Result{
			{RegNumber: "001", RegValid: true, Answers: []string{"A", "T", "AC"}},
			{RegNumber: "002", RegValid: true, Answers: []string{"A", "F", "AB"}},
		},
		[]card.SubjectiveSpec{{Kind: "fillblank", Score: 5}, {Kind: "subjective", Score: 10}},
		map[string]score.Grade{
			"s1-q1": {Score: 5, Graded: true, Mark: "check"},
			"s2-q1": {Score: 2, Graded: true},
		},
	)
}

func TestScoresXlsxZip(t *testing.T) {
	data, err := ScoresXlsxZip(testReport(), []string{"1(单选)", "2(判断)", "3(多选)", "4(填空)", "5(解答)"})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("不是合法 zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		if strings.HasSuffix(f.Name, ".xml") {
			dec := xml.NewDecoder(rc)
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				_ = tok
			}
		}
		rc.Close()
	}
	for _, want := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		if !names[want] {
			t.Fatalf("缺少部件 %s", want)
		}
	}
}

func TestScoresCSV(t *testing.T) {
	b := ScoresCSV(testReport())
	s := string(b)
	if !strings.HasPrefix(s, "\xEF\xBB\xBF") {
		t.Fatal("缺少 BOM")
	}
	if !strings.Contains(s, "001") || !strings.Contains(s, "卷号") {
		t.Fatalf("CSV 内容异常: %q", s)
	}
}

func TestReviewPPTXZip(t *testing.T) {
	slides := []ReviewSlide{
		{Title: "第 1 题(单选)得分率 0%", Lines: []string{"正确答案:B", "A:1人 B:0人 C:1人 D:0人"}},
		{Title: "第 5 题(解答)均分 2.5/10", Images: []ReviewImage{
			{Data: []byte("png-bytes"), Label: "典型满分:学生 1"},
			{Data: []byte("png-bytes2"), Label: "待改进:学生 2"},
		}},
	}
	data, err := ReviewPPTXZip("月考讲评", []string{"均分 12/80", "低分率题 3 道"}, slides)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("不是合法 zip: %v", err)
	}
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
		if strings.HasSuffix(f.Name, ".xml") {
			rc, _ := f.Open()
			dec := xml.NewDecoder(rc)
			for {
				_, err := dec.Token()
				if err != nil {
					break
				}
			}
			rc.Close()
		}
	}
	for _, want := range []string{
		"ppt/presentation.xml", "ppt/slides/slide1.xml", "ppt/slides/slide3.xml",
		"ppt/slideMasters/slideMaster1.xml", "ppt/theme/theme1.xml",
		"ppt/media/image1.png", "ppt/slides/_rels/slide3.xml.rels",
	} {
		if !have[want] {
			t.Fatalf("缺少部件 %s", want)
		}
	}
}

// TestScoresXlsxZipKnowledge 验证带知识点时追加第三张表且 XML 合法。
func TestScoresXlsxZipKnowledge(t *testing.T) {
	data, err := ScoresXlsxZip(testReport(), []string{"1", "2", "3", "4", "5"},
		[]score.KnowledgeStat{{Name: "函数", Questions: 2, Rate: 0.75, Max: 40, Earned: 30, Answers: 8}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("不是合法 zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["xl/worksheets/sheet3.xml"] {
		t.Fatal("带知识点时应含 sheet3.xml(知识点表)")
	}
	for _, want := range []string{"[Content_Types].xml", "xl/workbook.xml"} {
		if !names[want] {
			t.Fatalf("缺少部件 %s", want)
		}
	}
}

// TestImagePDF 验证最小 PDF 输出的结构完整性(头/尾/xref)。
func TestImagePDF(t *testing.T) {
	c := card.RenderPNG(mustSheet(t), &card.Filling{Reg: []int{1, 2, 3}})
	data, err := ImagePDF(c.ToGray(), 210, 297)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, "%PDF-1.4") {
		t.Fatal("缺少 PDF 头")
	}
	if !strings.HasSuffix(s, "%%EOF\n") {
		t.Fatal("缺少 PDF EOF 尾")
	}
	if !strings.Contains(s, "/Filter /DCTDecode") || !strings.Contains(s, "/DeviceGray") {
		t.Fatal("缺少 JPEG 图像 XObject")
	}
	// xref 偏移应落在文件内
	idx := strings.Index(s, "startxref")
	if idx < 0 {
		t.Fatal("缺少 startxref")
	}
}

func mustSheet(t *testing.T) *card.Sheet {
	t.Helper()
	l := card.DefaultLayout()
	s, err := l.Build()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
