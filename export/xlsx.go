// Package export 生成对外交付物:成绩册 xlsx/csv 与讲评课件 pptx。
// 全部以标准库 archive/zip + 手写 OOXML 实现,保持零第三方依赖;
// 输出为最简但结构完整的 OPC 包(Excel/WPS/LibreOffice 均可打开)。
package export

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"

	"github.com/opengrade/opengrade/score"
)

// xmlEscape 转义 XML 文本。
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// zipWrite 向 zip 写入一个部件。
func zipWrite(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(content))
	return err
}

// ---- xlsx ----

// cellRef 把行列(0-based)转为 A1 引用。
func cellRef(col, row int) string {
	name := ""
	for c := col; c >= 0; c = c/26 - 1 {
		name = string(rune('A'+c%26)) + name
	}
	return fmt.Sprintf("%s%d", name, row+1)
}

// cell 生成一个单元格;字符串用 inlineStr,数字直出。
func cell(col, row int, v any) string {
	ref := cellRef(col, row)
	switch t := v.(type) {
	case string:
		if t == "" {
			return ""
		}
		return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, xmlEscape(t))
	case float64:
		return fmt.Sprintf(`<c r="%s"><v>%s</v></c>`, ref, trimFloat(t))
	case int:
		return fmt.Sprintf(`<c r="%s"><v>%d</v></c>`, ref, t)
	default:
		return ""
	}
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func rowXML(idx int, cells ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<row r="%d">`, idx+1)
	for _, c := range cells {
		b.WriteString(c)
	}
	b.WriteString("</row>")
	return b.String()
}

const sheetHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`

const sheetFooter = `</sheetData></worksheet>`

// ScoresXlsxZip 生成成绩册 xlsx(OPC zip 字节):含"成绩单"与"细目表"两个工作表。
// questionLabels 为逐题显示名(如 "1(单选)"),与 rep.PerQuestion 对齐。
func ScoresXlsxZip(rep *score.Report, questionLabels []string) ([]byte, error) {
	sheet1 := buildScoresSheet(rep)
	sheet2 := buildDetailSheet(rep, questionLabels)
	parts := [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets>
<sheet name="成绩单" sheetId="1" r:id="rId1"/>
<sheet name="细目表" sheetId="2" r:id="rId2"/>
</sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`},
		{"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>
<fills count="1"><fill><patternFill patternType="none"/></fill></fills>
<borders count="1"><border/></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs>
</styleSheet>`},
		{"xl/worksheets/sheet1.xml", sheet1},
		{"xl/worksheets/sheet2.xml", sheet2},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		if err := zipWrite(zw, p[0], p[1]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildScoresSheet(rep *score.Report) string {
	var rows []string
	rows = append(rows, rowXML(0,
		cell(0, 0, "卷号"), cell(1, 0, "考号"), cell(2, 0, "姓名"),
		cell(3, 0, "考号有效"), cell(4, 0, "客观"), cell(5, 0, "主观"),
		cell(6, 0, "总分"), cell(7, 0, "满分"), cell(8, 0, "待复核")))
	for i, s := range rep.Students {
		rows = append(rows, rowXML(i+1,
			cell(0, i+1, s.Index),
			cell(1, i+1, s.RegNumber),
			cell(2, i+1, ""), // 姓名由调用方按名册填充时可通过 Report 扩展;此处预留
			cell(3, i+1, fmt.Sprintf("%v", s.RegValid)),
			cell(4, i+1, s.Objective),
			cell(5, i+1, s.Subjective),
			cell(6, i+1, s.Score),
			cell(7, i+1, s.Total),
			cell(8, i+1, boolMark(s.Flagged))))
	}
	return sheetHeader + strings.Join(rows, "") + sheetFooter
}

func boolMark(b bool) string {
	if b {
		return "⚑"
	}
	return ""
}

func buildDetailSheet(rep *score.Report, labels []string) string {
	var rows []string
	rows = append(rows, rowXML(0,
		cell(0, 0, "题号"), cell(1, 0, "题型"), cell(2, 0, "满分"),
		cell(3, 0, "答对/已阅"), cell(4, 0, "均分"), cell(5, 0, "得分率")))
	for i, q := range rep.PerQuestion {
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		correct := ""
		mean := ""
		switch q.Kind {
		case "fillblank", "subjective":
			mean = trimFloat(q.Mean)
			correct = fmt.Sprintf("%d/%d", q.Graded, q.Count)
		default:
			correct = fmt.Sprintf("%d/%d", q.Correct, q.Count)
		}
		rows = append(rows, rowXML(i+1,
			cell(0, i+1, q.Index),
			cell(1, i+1, label),
			cell(2, i+1, q.Max),
			cell(3, i+1, correct),
			cell(4, i+1, mean),
			cell(5, i+1, q.Rate)))
	}
	return sheetHeader + strings.Join(rows, "") + sheetFooter
}

// ScoresCSV 输出 UTF-8 BOM 的 CSV(Excel 直接打开不乱码)。
func ScoresCSV(rep *score.Report) []byte {
	var b strings.Builder
	b.WriteString("\xEF\xBB\xBF") // BOM
	b.WriteString("卷号,考号,考号有效,客观,主观,总分,满分,待复核\r\n")
	for _, s := range rep.Students {
		mark := ""
		if s.Flagged {
			mark = "1"
		}
		fmt.Fprintf(&b, "%d,%s,%v,%.2f,%.2f,%.2f,%.2f,%s\r\n",
			s.Index, s.RegNumber, s.RegValid, s.Objective, s.Subjective, s.Score, s.Total, mark)
	}
	return []byte(b.String())
}
