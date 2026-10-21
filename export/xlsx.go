// Package export 生成对外交付物:成绩册 xlsx/csv 与讲评课件 pptx。
// 全部以标准库 archive/zip + 手写 OOXML 实现,保持零第三方依赖;
// 输出为最简但结构完整的 OPC 包(Excel/WPS/LibreOffice 均可打开)。
package export

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
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
// knowledge 为知识点掌握度(可空);非空时追加第三张"知识点"表。
func ScoresXlsxZip(rep *score.Report, questionLabels []string, knowledge ...[]score.KnowledgeStat) ([]byte, error) {
	var kstats []score.KnowledgeStat
	if len(knowledge) > 0 {
		kstats = knowledge[0]
	}
	sheet1 := buildScoresSheet(rep)
	sheet2 := buildDetailSheet(rep, questionLabels)
	sheets := `<sheet name="成绩单" sheetId="1" r:id="rId1"/>
<sheet name="细目表" sheetId="2" r:id="rId2"/>`
	rels := `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>`
	ct := `<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`
	parts := [][2]string{}
	if len(kstats) > 0 {
		sheet3 := buildKnowledgeSheet(kstats)
		sheets += `
<sheet name="知识点" sheetId="3" r:id="rId3"/>`
		rels += `
<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet3.xml"/>`
		ct += `
<Override PartName="/xl/worksheets/sheet3.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`
		parts = append(parts, [2]string{"xl/worksheets/sheet3.xml", sheet3})
	}
	parts = append(parts, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
` + ct + `
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets>
` + sheets + `
</sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
` + rels + `
<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
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
	}...)
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

// buildKnowledgeSheet 知识点掌握度表:名称/覆盖题数/掌握度/涉及总分/作答样本。
func buildKnowledgeSheet(knowledge []score.KnowledgeStat) string {
	var rows []string
	rows = append(rows, rowXML(0,
		cell(0, 0, "知识点"), cell(1, 0, "覆盖题数"), cell(2, 0, "掌握度"),
		cell(3, 0, "涉及总分"), cell(4, 0, "实际得分"), cell(5, 0, "作答样本")))
	for i, k := range knowledge {
		rows = append(rows, rowXML(i+1,
			cell(0, i+1, k.Name),
			cell(1, i+1, k.Questions),
			cell(2, i+1, k.Rate),
			cell(3, i+1, k.Max),
			cell(4, i+1, k.Earned),
			cell(5, i+1, k.Answers)))
	}
	return sheetHeader + strings.Join(rows, "") + sheetFooter
}

func buildScoresSheet(rep *score.Report) string {
	var rows []string
	rows = append(rows, rowXML(0,
		cell(0, 0, "卷号"), cell(1, 0, "考号"), cell(2, 0, "姓名"),
		cell(3, 0, "考号有效"), cell(4, 0, "客观"), cell(5, 0, "主观"),
		cell(6, 0, "总分"), cell(7, 0, "满分"), cell(8, 0, "排名"),
		cell(9, 0, "待复核"), cell(10, 0, "缺考")))
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
			cell(8, i+1, s.Rank),
			cell(9, i+1, boolMark(s.Flagged)),
			cell(10, i+1, absentMark(s.Absent))))
	}
	return sheetHeader + strings.Join(rows, "") + sheetFooter
}

func boolMark(b bool) string {
	if b {
		return "⚑"
	}
	return ""
}

func absentMark(b bool) string {
	if b {
		return "缺考"
	}
	return ""
}

// buildDetailSheet 细目表:逐题满分/作答情况 + 难度系数 + 区分度 + 客观题选项人数分布,
// 拆分小问的题目附逐问子行(题号"17.1"),与网页端学情分析同源,便于线下留存与教研归档。
func buildDetailSheet(rep *score.Report, labels []string) string {
	var rows []string
	rows = append(rows, rowXML(0,
		cell(0, 0, "题号"), cell(1, 0, "题型"), cell(2, 0, "满分"),
		cell(3, 0, "答对/已阅"), cell(4, 0, "均分"), cell(5, 0, "得分率"),
		cell(6, 0, "难度系数"), cell(7, 0, "区分度"), cell(8, 0, "选项分布")))
	rowIdx := 0
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
			if q.Elective != "" { // 选做题:分母为实际选做人数(未被所选的不进统计)
				correct = fmt.Sprintf("%d/%d(选做)", q.Graded, q.Chosen)
			} else {
				correct = fmt.Sprintf("%d/%d", q.Graded, q.Count)
			}
		default:
			correct = fmt.Sprintf("%d/%d", q.Correct, q.Count)
		}
		rowIdx++
		mk := func(col int, v any) string { return cell(col, rowIdx, v) }
		rows = append(rows, rowXML(rowIdx,
			mk(0, q.Index),
			mk(1, label),
			mk(2, q.Max),
			mk(3, correct),
			mk(4, mean),
			mk(5, q.Rate),
			mk(6, q.Difficulty),
			mk(7, q.Discrimination),
			mk(8, optionsText(q.Options))))
		for _, p := range q.Parts { // 小问子行:题号 "17.1",带逐问均分与得分率
			rowIdx++
			mk := func(col int, v any) string { return cell(col, rowIdx, v) }
			rows = append(rows, rowXML(rowIdx,
				mk(0, fmt.Sprintf("%d.%d", q.Index, p.Index)),
				mk(1, p.Label),
				mk(2, p.Max),
				mk(3, fmt.Sprintf("%d/*", p.Graded)),
				mk(4, trimFloat(p.Mean)),
				mk(5, p.Rate),
				mk(6, p.Rate)))
		}
	}
	return sheetHeader + strings.Join(rows, "") + sheetFooter
}

// optionsText 把选项人数分布渲染为紧凑文本:"A:12 B:3 C:1 D:0 未答:2"。
func optionsText(opts map[string]int) string {
	if len(opts) == 0 {
		return ""
	}
	letters := make([]string, 0, len(opts))
	for k := range opts {
		letters = append(letters, k)
	}
	sort.Strings(letters)
	parts := make([]string, 0, len(letters))
	for _, k := range letters {
		parts = append(parts, fmt.Sprintf("%s:%d", k, opts[k]))
	}
	return strings.Join(parts, " ")
}

// ScoresCSV 输出 UTF-8 BOM 的 CSV(Excel 直接打开不乱码)。
func ScoresCSV(rep *score.Report) []byte {
	var b strings.Builder
	b.WriteString("\xEF\xBB\xBF") // BOM
	b.WriteString("卷号,考号,考号有效,客观,主观,总分,满分,排名,待复核,缺考\r\n")
	for _, s := range rep.Students {
		mark := ""
		if s.Flagged {
			mark = "1"
		}
		absent := ""
		if s.Absent {
			absent = "缺考"
		}
		fmt.Fprintf(&b, "%d,%s,%v,%.2f,%.2f,%.2f,%.2f,%d,%s,%s\r\n",
			s.Index, s.RegNumber, s.RegValid, s.Objective, s.Subjective, s.Score, s.Total, s.Rank, mark, absent)
	}
	return []byte(b.String())
}
