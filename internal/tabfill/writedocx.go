package tabfill

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
)

// WriteDocxTable 把表头＋数据行写成 docx（含一张全边框表格的 Word 文档）。
// 从零生成最小 OOXML 结构：[Content_Types].xml + _rels/.rels + word/document.xml。
func WriteDocxTable(headers []string, rows [][]string, title string, dst string) error {
	var tbl strings.Builder
	tbl.WriteString(`<w:tbl><w:tblPr><w:tblBorders>` +
		`<w:top w:val="single" w:sz="4"/><w:left w:val="single" w:sz="4"/>` +
		`<w:bottom w:val="single" w:sz="4"/><w:right w:val="single" w:sz="4"/>` +
		`<w:insideH w:val="single" w:sz="4"/><w:insideV w:val="single" w:sz="4"/>` +
		`</w:tblBorders></w:tblPr><w:tblGrid>`)
	for range headers {
		tbl.WriteString(`<w:gridCol w:w="2600"/>`)
	}
	tbl.WriteString(`</w:tblGrid>`)

	cell := func(text string, bold bool) string {
		run := ""
		if text != "" {
			rpr := ""
			if bold {
				rpr = `<w:rPr><w:b/></w:rPr>`
			}
			run = `<w:r>` + rpr + `<w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r>`
		}
		return `<w:tc><w:tcPr><w:tcW w:w="2600"/></w:tcPr><w:p>` + run + `</w:p></w:tc>`
	}
	row := func(cells []string, bold bool) string {
		s := "<w:tr>"
		for _, c := range cells {
			s += cell(c, bold)
		}
		return s + "</w:tr>"
	}
	tbl.WriteString(row(headers, true))
	for _, r := range rows {
		rec := make([]string, len(headers))
		copy(rec, r)
		tbl.WriteString(row(rec, false))
	}
	tbl.WriteString(`</w:tbl>`)

	var body strings.Builder
	if title != "" {
		body.WriteString(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="32"/></w:rPr><w:t>` +
			xmlEscape(title) + `</w:t></w:r></w:p>`)
	}
	body.WriteString(tbl.String() + `<w:sectPr/>`)

	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() + `</w:body></w:document>`

	ct := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range []struct{ name, body string }{
		{"[Content_Types].xml", ct},
		{"_rels/.rels", rels},
		{"word/document.xml", docXML},
	} {
		fw, err := w.Create(e.name)
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(e.body)); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

// xmlEscape XML 文本转义
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
