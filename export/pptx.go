package export

import (
	"archive/zip"
	"bytes"
	"fmt"
)

// ReviewImage 是讲评页里的一张切片图(典型满分/待改进作答)。
type ReviewImage struct {
	Data  []byte // PNG
	Label string // 图下说明
}

// ReviewSlide 是一页讲评内容:标题 + 文本行 + 可选并排图片。
type ReviewSlide struct {
	Title  string
	Lines  []string
	Images []ReviewImage // 0/1/2 张,两张时左右并排
}

const slideW = 12192000 // EMU,16:9
const slideH = 6858000

// ReviewPPTXZip 生成讲评课件 pptx(OPC zip 字节)。
// 首页为概览,其后每页一道低分率题:客观题给选项分布,主观题给典型切片。
func ReviewPPTXZip(title string, overview []string, slides []ReviewSlide) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	must := func(name, content string) {
		if err := zipWrite(zw, name, content); err != nil {
			panic(err) // zip.Writer 仅在底层写出错时失败;此处不可能发生
		}
	}

	// ---- 部件清单 ----
	var ct bytes.Buffer
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/>
<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>
<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>
<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>
<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`)
	allSlides := append([]ReviewSlide{{Title: title, Lines: overview}}, slides...)
	for i := range allSlides {
		fmt.Fprintf(&ct, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1)
	}
	ct.WriteString("</Types>")
	must("[Content_Types].xml", ct.String())

	must("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
</Relationships>`)

	// ---- presentation.xml 与其 rels ----
	var sldIds, presRels bytes.Buffer
	presRels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i := range allSlides {
		fmt.Fprintf(&sldIds, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, i+2)
		fmt.Fprintf(&presRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i+2, i+1)
	}
	presRels.WriteString("</Relationships>")
	must("ppt/_rels/presentation.xml.rels", presRels.String())
	must("ppt/presentation.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" saveSubsetFonts="1">
<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>
<p:sldIdLst>%s</p:sldIdLst>
<p:sldSz cx="%d" cy="%d"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`, sldIds.String(), slideW, slideH))

	// ---- 母版 / 版式 / 主题 ----
	masterTree := `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`
	must("ppt/slideMasters/slideMaster1.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree>%s</p:spTree></p:cSld>
<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>
<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>
<p:txStyles><titleStyle><a:lvl1pPr><a:defRPr sz="3200"/></a:defRPr></a:lvl1pPr></titleStyle>
<bodyStyle><a:lvl1pPr><a:defRPr sz="1800"/></a:defRPr></a:lvl1pPr></bodyStyle>
<otherStyle><a:lvl1pPr><a:defRPr sz="1600"/></a:defRPr></a:lvl1pPr></otherStyle></p:txStyles>
</p:sldMaster>`, masterTree))
	must("ppt/slideMasters/_rels/slideMaster1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>
</Relationships>`)
	must("ppt/slideLayouts/slideLayout1.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1">
<p:cSld name="空白"><p:spTree>%s</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`, masterTree))
	must("ppt/slideLayouts/_rels/slideLayout1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>
</Relationships>`)
	must("ppt/theme/theme1.xml", themeXML)

	// ---- 幻灯片 ----
	mediaN := 0
	for i, s := range allSlides {
		var shapes bytes.Buffer
		id := 2
		// 标题
		shapes.WriteString(textBox(id, 457200, 274638, 11277600, 800100, s.Title, 2800, true))
		id++
		// 正文行
		y := 1250000
		for _, line := range s.Lines {
			shapes.WriteString(textBox(id, 609600, y, 5800000, 420000, line, 1400, false))
			id++
			y += 480000
			if y > slideH-1200000 {
				break
			}
		}
		// 图片(1~2 张,左右并排)
		for k, img := range s.Images {
			if img.Data == nil {
				continue
			}
			mediaN++
			relID := fmt.Sprintf("rIdImg%d", k)
			w := 4800000
			x := 6700000
			if k == 0 {
				x = 800000
			}
			fmt.Fprintf(&shapes, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="图%d"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr>
<p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill>
<p:spPr><a:xfrm><a:off x="%d" y="1250000"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic>`, id, k+1, relID, x, w, w*3/4)
			id++
			var rels bytes.Buffer
			rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
			fmt.Fprintf(&rels, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/image%d.png"/>`, relID, mediaN)
			rels.WriteString("</Relationships>")
			must(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), rels.String())
			imgPart := fmt.Sprintf("ppt/media/image%d.png", mediaN)
			w2, err := zw.Create(imgPart)
			if err != nil {
				return nil, err
			}
			if _, err := w2.Write(img.Data); err != nil {
				return nil, err
			}
			// 图片说明
			shapes.WriteString(textBox(id, x, 1250000+w*3/4+60000, w, 360000, img.Label, 1200, false))
			id++
		}
		must(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>
%s</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`, shapes.String()))
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// textBox 生成一个绝对定位文本框。
func textBox(id int, x, y, cx, cy int, text string, size int, bold bool) string {
	bAttr := ""
	if bold {
		bAttr = ` b="1"`
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="文本%d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>
<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>
<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="%d"%s/><a:t>%s</a:t></a:r></a:p></p:txBody></p:sp>`,
		id, id, x, y, cx, cy, size, bAttr, xmlEscape(text))
}

// themeXML 是最小但结构完整的 OOXML 主题(PowerPoint/WPS/LibreOffice 兼容)。
const themeXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="OpenGrade">
<a:themeElements>
<a:clrScheme name="OpenGrade"><a:dk1><a:srgbClr val="1C2430"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1>
<a:dk2><a:srgbClr val="3A4250"/></a:dk2><a:lt2><a:srgbClr val="F5F7FA"/></a:lt2>
<a:accent1><a:srgbClr val="0B6E4F"/></a:accent1><a:accent2><a:srgbClr val="B45309"/></a:accent2>
<a:accent3><a:srgbClr val="2563EB"/></a:accent3><a:accent4><a:srgbClr val="7C3AED"/></a:accent4>
<a:accent5><a:srgbClr val="DB2777"/></a:accent5><a:accent6><a:srgbClr val="059669"/></a:accent6>
<a:hlink><a:srgbClr val="0B6E4F"/></a:hlink><a:folHlink><a:srgbClr val="66707D"/></a:folHlink></a:clrScheme>
<a:fontScheme name="OpenGrade"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>
<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>
<a:fmtScheme name="OpenGrade">
<a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>
<a:solidFill><a:schemeClr val="phClr"><a:tint val="60000"/></a:schemeClr></a:solidFill>
<a:solidFill><a:schemeClr val="phClr"><a:tint val="30000"/></a:schemeClr></a:solidFill></a:fillStyleLst>
<a:lnStyleLst><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>
<a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>
<a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>
<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle>
<a:effectStyle><a:effectLst/></a:effectStyle>
<a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>
<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>
<a:solidFill><a:schemeClr val="phClr"><a:tint val="60000"/></a:schemeClr></a:solidFill>
<a:solidFill><a:schemeClr val="phClr"><a:tint val="30000"/></a:schemeClr></a:solidFill></a:bgFillStyleLst>
</a:fmtScheme></a:themeElements></a:theme>`
