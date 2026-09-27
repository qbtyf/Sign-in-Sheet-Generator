// 辅助工具：生成 e2e 测试夹具（-gen）与校验填充结果（-verify）。
// 用法：
//   mkfixtures -gen <输出目录>     生成 src.xlsx / tpl.xlsx / tpl.docx
//   mkfixtures -verify <文件路径>  打印 xlsx 各工作表内容或 docx 表格内容
package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

func main() {
	mode := os.Args[1]
	switch mode {
	case "-gen":
		dir := os.Args[2]
		if err := genAll(dir); err != nil {
			fmt.Println("生成失败:", err)
			os.Exit(1)
		}
		fmt.Println("夹具已生成到", dir)
	case "-genmin":
		dir := os.Args[2]
		if err := genSrcMin(dir + "/src_min.xlsx"); err != nil {
			fmt.Println("生成失败:", err)
			os.Exit(1)
		}
		fmt.Println("已生成", dir+"/src_min.xlsx")
	case "-gendocxsrc":
		dir := os.Args[2]
		if err := genSrcDocx(dir + "/src.docx"); err != nil {
			fmt.Println("生成失败:", err)
			os.Exit(1)
		}
		fmt.Println("已生成", dir+"/src.docx")
	case "-genmulti":
		dir := os.Args[2]
		if err := genMulti(dir); err != nil {
			fmt.Println("生成失败:", err)
			os.Exit(1)
		}
		fmt.Println("已生成", dir+"/multi.xlsx", "和", dir+"/multidoc.docx")
	case "-verify":
		path := os.Args[2]
		if strings.HasSuffix(strings.ToLower(path), ".xlsx") {
			verifyXlsx(path)
		} else {
			verifyDocx(path)
		}
	default:
		fmt.Println("未知模式:", mode)
		os.Exit(2)
	}
}

func genAll(dir string) error {
	if err := genSrc(dir + "/src.xlsx"); err != nil {
		return err
	}
	if err := genTplXlsx(dir + "/tpl.xlsx"); err != nil {
		return err
	}
	return genTplDocx(dir + "/tpl.docx")
}

// genSrc 原始表格：两张工作表（正式名单 + 历史杂表），
// 字段顺序与模板不同（姓名在工号前），且模板里对应"班组"的字段叫"部门"
func genSrc(path string) error {
	f := excelize.NewFile()
	rows := [][]any{
		{"姓名", "工号", "班组", "部门", "工种"},
		{"张三", "A001", "一班", "生产部", "焊工"},
		{"李四", "A002", "二班", "生产部", "电工"},
		{"王五", "A003", "一班", "设备部", "钳工"},
		{"赵六", "A004", "三班", "设备部", "焊工"},
		{"钱七", "A005", "二班", "生产部", "电工"},
		{"孙八", "A006", "三班", "安环部", "叉车工"},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			return err
		}
	}
	if _, err := f.NewSheet("历史杂表"); err != nil {
		return err
	}
	if err := f.SaveAs(path); err != nil {
		return err
	}
	return f.Close()
}

// genTplXlsx xlsx 模板：表头 [工号 姓名 工种 部门]，
// 数据区只有 2 行（少于原始 6 行 → 应自动扩展），字段顺序与原始不同
func genTplXlsx(path string) error {
	f := excelize.NewFile()
	rows := [][]any{
		{"工号", "姓名", "工种", "部门"},
		{"", "", "", ""},
		{"", "", "", ""},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			return err
		}
	}
	if err := f.SaveAs(path); err != nil {
		return err
	}
	return f.Close()
}

// genTplDocx docx 模板：单表，表头 [姓名 工号 班组]，数据区 2 行
func genTplDocx(path string) error {
	var sb strings.Builder
	sb.WriteString(`<w:tbl><w:tblPr><w:tblBorders>` +
		`<w:top w:val="single" w:sz="4"/><w:left w:val="single" w:sz="4"/>` +
		`<w:bottom w:val="single" w:sz="4"/><w:right w:val="single" w:sz="4"/>` +
		`<w:insideH w:val="single" w:sz="4"/><w:insideV w:val="single" w:sz="4"/>` +
		`</w:tblBorders></w:tblPr><w:tblGrid>` +
		`<w:gridCol w:w="3000"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/>` +
		`</w:tblGrid>`)
	cell := (func(text string) string {
		if text == "" {
			return `<w:tc><w:tcPr><w:tcW w:w="3000"/></w:tcPr><w:p><w:pPr><w:jc w:val="center"/></w:pPr></w:p></w:tc>`
		}
		return `<w:tc><w:tcPr><w:tcW w:w="3000"/></w:tcPr><w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p></w:tc>`
	})
	row := (func(cells ...string) string {
		s := "<w:tr>"
		for _, c := range cells {
			s += cell(c)
		}
		return s + "</w:tr>"
	})
	sb.WriteString(row("姓名", "工号", "班组"))
	sb.WriteString(row("", "", ""))
	sb.WriteString(row("", "", ""))
	sb.WriteString(`</w:tbl>`)

	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>测试模板</w:t></w:r></w:p>` + sb.String() +
		`<w:sectPr/></w:body></w:document>`

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
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// genSrcMin 只有 1 行数据的原始表格（测"多余行删除"场景）
func genSrcMin(path string) error {
	f := excelize.NewFile()
	rows := [][]any{
		{"姓名", "工号", "班组", "部门", "工种"},
		{"张三", "A001", "一班", "生产部", "焊工"},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			return err
		}
	}
	if err := f.SaveAs(path); err != nil {
		return err
	}
	return f.Close()
}

// genSrcDocx docx 原始表格：表头 [姓名 工号 班组] + 3 行数据（测 docx 作数据源）
func genSrcDocx(path string) error {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:tbl><w:tblPr/><w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>
<w:tr><w:tc><w:p><w:r><w:t>姓名</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>工号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>班组</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>周九</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B001</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>一班</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>吴十</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B002</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>二班</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>郑一</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B003</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>三班</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl><w:sectPr/></w:body></w:document>`

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
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// genMulti 生成「表格内合并」测试夹具：
// multi.xlsx = 3 张工作表（Sheet1 三行数据、二班 两行数据且隐藏、空表），表头同构
// multidoc.docx = 一个文档里两个表格（表头同构，各两行数据）
func genMulti(dir string) error {
	f := excelize.NewFile()
	mk := func(sheet string, rows [][]any) error {
		if _, err := f.NewSheet(sheet); err != nil {
			return err
		}
		for i, r := range rows {
			cell, _ := excelize.CoordinatesToCellName(1, i+1)
			if err := f.SetSheetRow(sheet, cell, &r); err != nil {
				return err
			}
		}
		return nil
	}
	if err := mk("一班", [][]any{
		{"姓名", "工号", "班组", "部门", "工种"},
		{"张三", "A001", "一班", "生产部", "焊工"},
		{"李四", "A002", "一班", "生产部", "电工"},
		{"王五", "A003", "一班", "设备部", "钳工"},
	}); err != nil {
		return err
	}
	if err := mk("二班", [][]any{
		{"姓名", "工号", "班组", "部门", "工种"},
		{"赵六", "A004", "二班", "设备部", "焊工"},
		{"钱七", "A005", "二班", "生产部", "电工"},
	}); err != nil {
		return err
	}
	if err := mk("空表", [][]any{{"姓名", "工号", "班组", "部门", "工种"}}); err != nil {
		return err
	}
	f.DeleteSheet("Sheet1")
	if err := f.SaveAs(dir + "/multi.xlsx"); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := hideSheetInFile(dir+"/multi.xlsx", "二班"); err != nil {
		return err
	}

	// multidoc.docx：两个同构表格
	tbl := func(rows ...[]string) string {
		s := "<w:tbl><w:tblPr/><w:tblGrid><w:gridCol w:w=\"3000\"/><w:gridCol w:w=\"3000\"/><w:gridCol w:w=\"3000\"/></w:tblGrid>"
		for _, r := range rows {
			s += "<w:tr>"
			for _, c := range r {
				s += "<w:tc><w:p><w:r><w:t>" + c + "</w:t></w:r></w:p></w:tc>"
			}
			s += "</w:tr>"
		}
		return s + "</w:tbl>"
	}
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		tbl([]string{"姓名", "工号", "班组"}, []string{"周九", "B001", "一班"}, []string{"吴十", "B002", "二班"}) +
		tbl([]string{"姓名", "工号", "班组"}, []string{"郑一", "B003", "三班"}, []string{"王二", "B004", "一班"}) +
		`<w:sectPr/></w:body></w:document>`

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
	return os.WriteFile(dir+"/multidoc.docx", buf.Bytes(), 0o644)
}

// hideSheetInFile 把 xlsx 内指定工作表标为 hidden（excelize v2.8.1 无此 API，直接改 workbook.xml）
func hideSheetInFile(path, sheet string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range r.File {
		fw, err := w.Create(f.Name)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if f.Name == "xl/workbook.xml" {
			var sb strings.Builder
			tmp := make([]byte, 4096)
			for {
				n, e := rc.Read(tmp)
				sb.Write(tmp[:n])
				if e != nil {
					break
				}
			}
			xml := sb.String()
			xml = strings.Replace(xml,
				`<sheet name="`+sheet+`"`,
				`<sheet name="`+sheet+`" state="hidden"`, 1)
			_, _ = fw.Write([]byte(xml))
		} else {
			if _, err := io.Copy(fw, rc); err != nil {
				rc.Close()
				return err
			}
		}
		rc.Close()
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func verifyXlsx(path string) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		fmt.Println("打开失败:", err)
		os.Exit(1)
	}
	defer f.Close()
	for _, sheet := range f.GetSheetList() {
		fmt.Println("== 工作表:", sheet)
		rows, _ := f.GetRows(sheet)
		for i, r := range rows {
			fmt.Printf("%2d | %s\n", i+1, strings.Join(padRow(r, lenOf(rows)), " | "))
		}
	}
}

func lenOf(rows [][]string) (w int) {
	for _, r := range rows {
		if len(r) > w {
			w = len(r)
		}
	}
	return
}

func padRow(r []string, w int) []string {
	for len(r) < w {
		r = append(r, "")
	}
	return r
}

func verifyDocx(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("打开失败:", err)
		os.Exit(1)
	}
	// 从 zip 中取 document.xml
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fmt.Println("zip 解析失败:", err)
		os.Exit(1)
	}
	var docXML string
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var sb strings.Builder
			tmp := make([]byte, 4096)
			for {
				n, e := rc.Read(tmp)
				sb.Write(tmp[:n])
				if e != nil {
					break
				}
			}
			rc.Close()
			docXML = sb.String()
		}
	}
	tables := docx.ParseTables(docXML)
	for ti, tbl := range tables {
		fmt.Printf("== 表格 %d（%d 行）==\n", ti+1, len(tbl.Rows))
		for _, row := range tbl.Rows {
			var cells []string
			for _, c := range row.Cells {
				cells = append(cells, c.Text)
			}
			fmt.Println("  " + strings.Join(cells, " | "))
		}
	}
}
