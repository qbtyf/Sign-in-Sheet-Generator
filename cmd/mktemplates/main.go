// mktemplates 生成 9 个内置签到表模板（培训/会议/活动 × 3 种风格）到 builtins/ 目录。
// 仅开发期运行一次；产物通过 go:embed 打进 exe。
package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 风格定义
type style struct {
	suffix     string
	titleColor string
	titleSize  string // 半磅
	borderSize string
	labelShade string
	headShade  string
	labelFont  string
}

type kind struct {
	name     string
	title    string
	subtitle string
	rows     [][2]string // 标签 / 占位符
}

var styles = []style{
	{"简约蓝", "1F4E79", "36", "4", "DEEBF7", "DEEBF7", "黑体"},
	{"红头正式", "C00000", "44", "8", "F2F2F2", "F2F2F2", "黑体"},
	{"现代灰", "404040", "36", "4", "E7E6E6", "D9D9D9", "黑体"},
}

// 类型定义
var kinds = []kind{
	{"培训", "{培训名称}", "{副标题}", [][2]string{
		{"培训内容", "{培训内容}"}, {"讲师/主持人", "{讲师}"}, {"培训方式", "{培训方式}"},
		{"地点", "{地点}"}, {"日期", "{日期}"}, {"时间", "{时间}"},
		{"参加单位", "{参加单位}"}, {"培训人数", "{培训人数}"},
	}},
	{"会议", "{会议主题}", "{副标题}", [][2]string{
		{"议题", "{议题}"}, {"主持人", "{主持人}"}, {"会议形式", "{会议形式}"},
		{"地点", "{地点}"}, {"日期", "{日期}"}, {"时间", "{时间}"},
		{"参加单位", "{参加单位}"}, {"参会人数", "{参会人数}"},
	}},
	{"活动", "{活动名称}", "{副标题}", [][2]string{
		{"活动安排", "{活动安排}"}, {"负责人", "{负责人}"}, {"活动形式", "{活动形式}"},
		{"地点", "{地点}"}, {"日期", "{日期}"}, {"时间", "{时间}"},
		{"参加单位", "{参加单位}"}, {"参与人数", "{参与人数}"},
	}},
}

const (
	dataRows = 14
)

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// run 构造一个文本 run
func run(text, font, sizeHalf, color string, bold bool) string {
	var b strings.Builder
	b.WriteString("<w:r><w:rPr>")
	b.WriteString(fmt.Sprintf(`<w:rFonts w:ascii="%s" w:eastAsia="%s" w:hAnsi="%s"/>`, font, font, font))
	if bold {
		b.WriteString("<w:b/>")
	}
	if color != "" {
		b.WriteString(fmt.Sprintf(`<w:color w:val="%s"/>`, color))
	}
	b.WriteString(fmt.Sprintf(`<w:sz w:val="%s"/><w:szCs w:val="%s"/>`, sizeHalf, sizeHalf))
	b.WriteString("</w:rPr>")
	b.WriteString(fmt.Sprintf(`<w:t xml:space="preserve">%s</w:t>`, esc(text)))
	b.WriteString("</w:r>")
	return b.String()
}

func para(content, jc string) string {
	pr := ""
	if jc != "" {
		pr = fmt.Sprintf(`<w:pPr><w:jc w:val="%s"/></w:pPr>`, jc)
	}
	return "<w:p>" + pr + content + "</w:p>"
}

func cell(content, width, shade string, vAlign string) string {
	pr := fmt.Sprintf(`<w:tcPr><w:tcW w:w="%s" w:type="dxa"/>`, width)
	if shade != "" {
		pr += fmt.Sprintf(`<w:shd w:val="clear" w:color="auto" w:fill="%s"/>`, shade)
	}
	if vAlign != "" {
		pr += fmt.Sprintf(`<w:vAlign w:val="%s"/>`, vAlign)
	}
	pr += "</w:tcPr>"
	return "<w:tc>" + pr + content + "</w:tc>"
}

func borders(size, color string) string {
	b := fmt.Sprintf(`<w:top w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	l := fmt.Sprintf(`<w:left w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	bm := fmt.Sprintf(`<w:bottom w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	r := fmt.Sprintf(`<w:right w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	ih := fmt.Sprintf(`<w:insideH w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	iv := fmt.Sprintf(`<w:insideV w:val="single" w:sz="%s" w:color="%s"/>`, size, color)
	return fmt.Sprintf(`<w:tblBorders>%s%s%s%s%s%s</w:tblBorders>`, b, l, bm, r, ih, iv)
}

func buildDocumentXML(k kind, s style) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)

	// 标题
	b.WriteString(para(run(k.title, "黑体", s.titleSize, s.titleColor, true), "center"))
	// 副标题
	b.WriteString(para(run(k.subtitle, "宋体", "24", "595959", false), "center"))
	// 空行
	b.WriteString(para("", ""))

	// 信息表
	infoBorders := borders(s.borderSize, s.titleColor)
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/>` + infoBorders + `<w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid><w:gridCol w:w="2200"/><w:gridCol w:w="6800"/></w:tblGrid>`)
	for _, row := range k.rows {
		labelCell := cell(para(run(row[0], s.labelFont, "21", "", true), "center"), "2200", s.labelShade, "center")
		valueCell := cell(para(run(row[1], "宋体", "21", "", false), "left"), "6800", "", "center")
		b.WriteString("<w:tr>" + labelCell + valueCell + "</w:tr>")
	}
	b.WriteString(`</w:tbl>`)
	b.WriteString(para("", ""))

	// 签到表
	signBorders := borders(s.borderSize, s.titleColor)
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/>` + signBorders + `<w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid><w:gridCol w:w="900"/><w:gridCol w:w="2400"/><w:gridCol w:w="2400"/><w:gridCol w:w="3300"/></w:tblGrid>`)

	// 表头行
	head := "<w:tr>"
	for _, h := range []string{"序号", "姓名", "单位", "签名"} {
		head += cell(para(run(h, s.labelFont, "22", "", true), "center"), "", s.headShade, "center")
	}
	head += "</w:tr>"
	b.WriteString(head)

	// 数据行（姓名列留空待填）
	for i := 1; i <= dataRows; i++ {
		tr := `<w:tr><w:trPr><w:trHeight w:val="480" w:hRule="atLeast"/></w:trPr>`
		tr += cell(para(run(fmt.Sprintf("%d", i), "宋体", "21", "", false), "center"), "900", "", "center")
		tr += cell(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr></w:p>`, "2400", "", "center")
		tr += cell(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr></w:p>`, "2400", "", "center")
		tr += cell(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr></w:p>`, "3300", "", "center")
		tr += "</w:tr>"
		b.WriteString(tr)
	}
	b.WriteString(`</w:tbl>`)

	// 页脚说明
	b.WriteString(para(run("说明：请到场人员在本表签名确认。", "宋体", "18", "808080", false), "left"))

	// 页面设置 A4
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`

const rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

func writeDocx(path, docXML string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, item := range []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rels},
		{"word/document.xml", docXML},
	} {
		w, err := zw.Create(item.name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(item.body)); err != nil {
			return err
		}
	}
	return zw.Close()
}

// buildClassicXML 生成"经典表单"版式的签到表：
// 信息区为"标签格＋空白格/预填格"（无占位符，供留空字段识别），
// 签到区为 姓名|签名 ×3 三栏，页脚含培训效果评价。
func buildClassicXML(s style) string {
	blank := func() string {
		return `<w:p><w:pPr><w:jc w:val="center"/></w:pPr></w:p>`
	}
	// blankLines 生成 n 个左对齐空段落（多行大格留空用：视觉空白但保留多行结构，
	// 供 tplengine 识别为多行字段、生成时逐行填入）
	blankLines := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(`<w:p><w:pPr><w:jc w:val="left"/></w:pPr></w:p>`)
		}
		return sb.String()
	}
	labelCell := func(text, width string) string {
		return cell(para(run(text, s.labelFont, "21", "", true), "center"), width, s.labelShade, "center")
	}
	valueCell := func(content, width string) string {
		return cell(content, width, "", "center")
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	b.WriteString(para(run("专项培训签到表", "黑体", s.titleSize, s.titleColor, true), "center"))
	b.WriteString(para("", ""))

	cb := borders(s.borderSize, s.titleColor)
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/>` + cb + `<w:tblLayout w:type="fixed"/></w:tblPr>` +
		`<w:tblGrid>` + strings.Repeat(`<w:gridCol w:w="1500"/>`, 6) + `</w:tblGrid>`)

	// r0: 培训项目 | 空 | 培训方式 | 空（全部留空，由用户填写）
	b.WriteString("<w:tr>" +
		labelCell("培训项目", "1300") +
		valueCell(blank(), "1700") +
		labelCell("培训方式", "1300") +
		fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="4500" w:type="dxa"/><w:gridSpan w:val="3"/></w:tcPr>%s</w:tc>`,
			blank()) +
		"</w:tr>")
	// r1: 培训讲师 | 空 | 培训人数 | 空 | 培训时间 | 空
	b.WriteString("<w:tr>" +
		labelCell("培训讲师", "1300") + valueCell(blank(), "1700") +
		labelCell("培训人数", "1300") + valueCell(blank(), "1700") +
		labelCell("培训时间", "1300") + valueCell(blank(), "1700") +
		"</w:tr>")
	// r2: 培训内容 | 多行大格（3 个空段落，编号由生成时自动补）
	content := blankLines(3)
	b.WriteString("<w:tr>" +
		labelCell("培训内容", "1300") +
		fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="7700" w:type="dxa"/><w:gridSpan w:val="5"/></w:tcPr>%s</w:tc>`, content) +
		"</w:tr>")
	// r3: 参加培训人员（签到）合并行
	b.WriteString("<w:tr>" +
		fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="9000" w:type="dxa"/><w:gridSpan w:val="6"/></w:tcPr>%s</w:tc>`,
			para(run("参加培训人员（签到）", s.labelFont, "22", "", true), "center")) +
		"</w:tr>")
	// r4: 姓名|签名 ×3 表头
	head := "<w:tr>"
	for i := 0; i < 3; i++ {
		head += cell(para(run("姓名", s.labelFont, "22", "", true), "center"), "", s.headShade, "center")
		head += cell(para(run("签名", s.labelFont, "22", "", true), "center"), "", s.headShade, "center")
	}
	head += "</w:tr>"
	b.WriteString(head)
	// r5..: 数据行 12 行 × 6 空格
	for i := 0; i < 12; i++ {
		tr := "<w:tr>"
		for j := 0; j < 6; j++ {
			tr += cell(blank(), "1500", "", "center")
		}
		tr += "</w:tr>"
		b.WriteString(tr)
	}
	// 页脚：培训效果评价 + 评价人行
	b.WriteString("<w:tr>" +
		labelCell("培训效果评价", "1300") +
		fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="7700" w:type="dxa"/><w:gridSpan w:val="5"/></w:tcPr>%s</w:tc>`, blank()) +
		"</w:tr>")
	b.WriteString("<w:tr>" +
		labelCell("培训效果评价", "1300") +
		fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="7700" w:type="dxa"/><w:gridSpan w:val="5"/></w:tcPr>%s</w:tc>`,
			para(run("评价人：　　　　　　　　　年　　月　　日", "宋体", "21", "", false), "right")) +
		"</w:tr>")
	b.WriteString(`</w:tbl>`)
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

func main() {
	outDir := "builtins"
	for _, k := range kinds {
		for _, s := range styles {
			name := fmt.Sprintf("%s-%s.docx", k.name, s.suffix)
			docXML := buildDocumentXML(k, s)
			p := filepath.Join(outDir, name)
			if err := writeDocx(p, docXML); err != nil {
				fmt.Println("写入失败:", p, err)
				os.Exit(1)
			}
			fmt.Println("已生成:", p)
		}
	}
	// 经典表单版式（3 风格各一份）
	for _, s := range styles {
		name := fmt.Sprintf("培训-经典表单(%s).docx", s.suffix)
		p := filepath.Join(outDir, name)
		if err := writeDocx(p, buildClassicXML(s)); err != nil {
			fmt.Println("写入失败:", p, err)
			os.Exit(1)
		}
		fmt.Println("已生成:", p)
	}
}
