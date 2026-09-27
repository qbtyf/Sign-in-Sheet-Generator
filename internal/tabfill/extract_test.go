package tabfill

// 提取表格引擎单测：清单、docx 保真、xlsx 合并格还原

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

// docxDocumentXML 从 docx 字节里取出 word/document.xml 内容
func docxDocumentXML(data string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader([]byte(data)), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	}
	return "", fmt.Errorf("docx 内未找到 word/document.xml")
}

// parseTopTablesForTest 按出现顺序返回各表格完整 XML（<w:tbl>…</w:tbl>）
func parseTopTablesForTest(doc string) []string {
	tables := docx.ParseTables(doc)
	out := make([]string, len(tables))
	for i, t := range tables {
		out[i] = "<w:tbl>" + t.Inner + "</w:tbl>"
	}
	return out
}

// extFixtureDocx 生成与 mkfixtures -genext 同构的测试文档（两表：合并格 + 普通表）
func extFixtureDocx(t *testing.T, dir string) string {
	t.Helper()
	bold := "<w:rPr><w:b/></w:rPr>"
	shd := `<w:shd w:val="clear" w:color="auto" w:fill="D9E2F3"/>`
	border := `<w:tblBorders>` +
		`<w:top w:val="single" w:sz="4"/><w:left w:val="single" w:sz="4"/>` +
		`<w:bottom w:val="single" w:sz="4"/><w:right w:val="single" w:sz="4"/>` +
		`<w:insideH w:val="single" w:sz="4"/><w:insideV w:val="single" w:sz="4"/>` +
		`</w:tblBorders>`
	tc := func(text, extraPr, rPr string) string {
		run := ""
		if text != "" {
			run = "<w:r>" + rPr + "<w:t xml:space=\"preserve\">" + text + "</w:t></w:r>"
		}
		return "<w:tc><w:tcPr>" + extraPr + "</w:tcPr><w:p>" + run + "</w:p></w:tc>"
	}
	tbl0 := "<w:tbl><w:tblPr>" + border + "</w:tblPr>" +
		`<w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>` +
		"<w:tr>" +
		tc("姓名", `<w:tcW w:w="3000"/>`+shd, bold) +
		tc("工号", `<w:tcW w:w="3000"/>`+shd, bold) +
		tc("班组", `<w:tcW w:w="3000"/>`+shd, bold) +
		tc("部门", `<w:tcW w:w="3000"/>`+shd, bold) +
		"</w:tr>" +
		"<w:tr>" +
		tc("张三", `<w:tcW w:w="3000"/>`, "") +
		tc("A001", `<w:tcW w:w="3000"/>`, "") +
		tc("二班·生产一部", `<w:tcW w:w="6000"/><w:gridSpan w:val="2"/>`, "") +
		"</w:tr>" +
		"<w:tr>" +
		tc("李四", `<w:tcW w:w="3000"/>`, "") +
		tc("A002", `<w:tcW w:w="3000"/>`, "") +
		tc("二班", `<w:tcW w:w="3000"/>`, "") +
		tc("生产部", `<w:tcW w:w="3000"/><w:vMerge w:val="restart"/>`, "") +
		"</w:tr>" +
		"<w:tr>" +
		tc("王五", `<w:tcW w:w="3000"/>`, "") +
		tc("A003", `<w:tcW w:w="3000"/>`, "") +
		tc("二班", `<w:tcW w:w="3000"/>`, "") +
		tc("", `<w:tcW w:w="3000"/><w:vMerge/>`, "") +
		"</w:tr>" +
		"</w:tbl>"
	tbl1 := "<w:tbl><w:tblPr>" + border + "</w:tblPr>" +
		`<w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>` +
		"<w:tr>" + tc("物料", `<w:tcW w:w="3000"/>`, "") + tc("数量", `<w:tcW w:w="3000"/>`, "") + tc("单价", `<w:tcW w:w="3000"/>`, "") + "</w:tr>" +
		"<w:tr>" + tc("13800138000", `<w:tcW w:w="3000"/>`, "") + tc("100", `<w:tcW w:w="3000"/>`, "") + tc("0.5", `<w:tcW w:w="3000"/>`, "") + "</w:tr>" +
		"<w:tr>" + tc("螺母", `<w:tcW w:w="3000"/>`, "") + tc("80", `<w:tcW w:w="3000"/>`, "") + tc("0.3", `<w:tcW w:w="3000"/>`, "") + "</w:tr>" +
		"</w:tbl>"
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		tbl0 + "<w:p/>" + tbl1 + "<w:p/><w:sectPr/></w:body></w:document>"

	path := filepath.Join(dir, "ext.docx")
	if err := MinimalDocxShell(docXML, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListDocxTables(t *testing.T) {
	path := extFixtureDocx(t, t.TempDir())
	tables, err := ListDocxTables(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("应列出 2 张表格，实际 %d", len(tables))
	}
	if tables[0].Rows != 4 || tables[0].Cols != 4 {
		t.Errorf("表格0 应为 4行×4列，实际 %d行×%d列", tables[0].Rows, tables[0].Cols)
	}
	if tables[1].Rows != 3 || tables[1].Cols != 3 {
		t.Errorf("表格1 应为 3行×3列，实际 %d行×%d列", tables[1].Rows, tables[1].Cols)
	}
	if len(tables[0].Preview) == 0 || !strings.Contains(tables[0].Preview[0], "姓名") {
		t.Errorf("表格0 预览应含表头，实际 %v", tables[0].Preview)
	}
}

func TestExtractDocxKeepFormat(t *testing.T) {
	dir := t.TempDir()
	src := extFixtureDocx(t, dir)
	dst := filepath.Join(dir, "out.docx")

	// 只提取第 0 张
	if err := ExtractDocx(src, dst, ".docx", []int{0}); err != nil {
		t.Fatal(err)
	}
	srcData, _ := os.ReadFile(src)
	dstData, _ := os.ReadFile(dst)
	if len(dstData) == 0 {
		t.Fatal("输出文件为空")
	}

	// 输出文档应恰好 1 张表，且表格 XML 与原文档第 0 张逐字节一致
	srcDoc, err := docxDocumentXML(string(srcData))
	if err != nil {
		t.Fatal(err)
	}
	dstDoc, err := docxDocumentXML(string(dstData))
	if err != nil {
		t.Fatal(err)
	}
	srcTables := parseTopTablesForTest(srcDoc)
	dstTables := parseTopTablesForTest(dstDoc)
	if len(dstTables) != 1 {
		t.Fatalf("提取结果应只有 1 张表，实际 %d", len(dstTables))
	}
	if dstTables[0] != srcTables[0] {
		t.Error("提取的表格 XML 与原文档不一致（保真失败）")
	}
	// 提取两张 → 输出 2 张，内容仍逐字节一致
	dst2 := filepath.Join(dir, "out2.docx")
	if err := ExtractDocx(src, dst2, ".docx", []int{1, 0}); err != nil {
		t.Fatal(err)
	}
	dst2Data, _ := os.ReadFile(dst2)
	dst2Doc, _ := docxDocumentXML(string(dst2Data))
	dst2Tables := parseTopTablesForTest(dst2Doc)
	if len(dst2Tables) != 2 {
		t.Fatalf("提取结果应有 2 张表，实际 %d", len(dst2Tables))
	}
	if dst2Tables[0] != srcTables[1] || dst2Tables[1] != srcTables[0] {
		t.Error("两张表提取后顺序或内容不对")
	}
}

func TestExtractDocxToXlsx(t *testing.T) {
	dir := t.TempDir()
	src := extFixtureDocx(t, dir)
	dst := filepath.Join(dir, "out.xlsx")
	if err := ExtractDocx(src, dst, ".xlsx", []int{0, 1}); err != nil {
		t.Fatal(err)
	}

	f, err := excelize.OpenFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) != 2 {
		t.Fatalf("应有 2 个工作表，实际 %v", sheets)
	}
	// 表格1 工作表（第 0 张）
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("表格0 应 4 行，实际 %d", len(rows))
	}
	if rows[0][0] != "姓名" || rows[1][2] != "二班·生产一部" {
		t.Errorf("内容不符: 首行=%v 第2行=%v", rows[0], rows[1])
	}
	if got, _ := f.GetCellValue(sheets[0], "D3"); got != "生产部" {
		t.Errorf("纵向合并起始格 D3 应为「生产部」，实际 %q", got)
	}
	// 合并格校验：横向 C2:D2、纵向 D3:D4
	merges, _ := f.GetMergeCells(sheets[0])
	joined := fmt.Sprintf("%v", merges)
	if !strings.Contains(joined, "C2") || !strings.Contains(joined, "D2") {
		t.Errorf("缺少横向合并 C2:D2，实际 %s", joined)
	}
	if !strings.Contains(joined, "D3") || !strings.Contains(joined, "D4") {
		t.Errorf("缺少纵向合并 D3:D4，实际 %s", joined)
	}
	// 表格2 工作表：长数字保文本
	if got, _ := f.GetCellValue(sheets[1], "A2"); got != "13800138000" {
		t.Errorf("长数字应保文本，实际 %q", got)
	}
	// 列宽已设置（>默认值）
	col, _ := excelize.ColumnNumberToName(1)
	w, _ := f.GetColWidth(sheets[0], col)
	if w <= 4.01 {
		t.Errorf("列宽应按原表格换算（>4），实际 %v", w)
	}
}

// TestExtractXlsxStylesValid 产物 styles.xml 里所有 rgb 必须是合法 8 位 aRGB
//（回归防护：Border 无 Color 时 excelize 会写出 rgb="FF"，openpyxl 直接拒开）
func TestExtractXlsxStylesValid(t *testing.T) {
	dir := t.TempDir()
	src := extFixtureDocx(t, dir)
	dst := filepath.Join(dir, "out.xlsx")
	if err := ExtractDocx(src, dst, ".xlsx", []int{0, 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var styles string
	for _, f := range zr.File {
		if f.Name == "xl/styles.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			styles = string(b)
		}
	}
	if styles == "" {
		t.Fatal("styles.xml 缺失")
	}
	re := regexp.MustCompile(`rgb="([^"]*)"`)
	for _, m := range re.FindAllStringSubmatch(styles, -1) {
		if !regexp.MustCompile(`^[0-9A-Fa-f]{8}$`).MatchString(m[1]) {
			t.Errorf("非法 rgb 值 %q（必须是 8 位 aRGB）", m[1])
		}
	}
	// 底纹色应正确写入 8 位 aRGB
	if !strings.Contains(styles, `rgb="FFD9E2F3"`) {
		t.Errorf("底纹色未正确写入，styles.xml 片段: %s", styles[:min(len(styles), 400)])
	}
}
