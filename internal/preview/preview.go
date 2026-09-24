// Package preview 将生成后的签到表（docx/xlsx）渲染为 HTML 表格供界面预览
package preview

import (
	"fmt"
	"html"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

// Docx 把 docx 文件渲染为 HTML（标题段落 + 表格）
func Docx(path string) (string, error) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return "", err
	}
	return DocxFromXML(string(xmlBytes))
}

// DocxFromXML 把 docx 的 document.xml 内容直接渲染为 HTML（内嵌模板预览用，无磁盘文件）
func DocxFromXML(xmlStr string) (string, error) {
	var sb strings.Builder

	for _, line := range docx.TopParagraphs(xmlStr) {
		sb.WriteString(fmt.Sprintf(`<div class="pv-title">%s</div>`, html.EscapeString(line)))
	}
	tables := docx.ParseTables(xmlStr)
	if len(tables) == 0 {
		sb.WriteString(`<div class="pv-empty">（无表格）</div>`)
		return sb.String(), nil
	}
	// 渲染全部顶层表格（信息表＋签到网格等），表间留间距
	for ti, tbl := range tables {
		if ti > 0 {
			sb.WriteString(`<div style="height:14px"></div>`)
		}
		sb.WriteString(openTableTag(tbl.GridCols))
		for _, row := range tbl.Rows {
			sb.WriteString("<tr>")
			spanSum, hasVMerge := 0, false
			for _, c := range row.Cells {
				text := strings.TrimSpace(c.Text)
				// 纵向合并的延续格：跳过渲染（浏览器按上方 rowspan 对位）
				if c.VMerge && text == "" {
					hasVMerge = true
					continue
				}
				attr := ""
				if c.GridSpan > 1 {
					attr += fmt.Sprintf(` colspan="%d"`, c.GridSpan)
				}
				if c.VMerge {
					attr += ` rowspan="2"`
				}
				spanSum += c.GridSpan
				cls := ""
				if text == "姓名" || text == "序号" || text == "签名" || text == "班组" || text == "单位" || text == "部门" {
					cls = ` class="pv-head"`
				} else if text != "" && !isChineseName(text) {
					cls = ` class="pv-label"`
				}
				sb.WriteString(fmt.Sprintf(`<td%s%s>%s</td>`, attr, cls, html.EscapeString(text)))
			}
			// 防御：行内跨列合计不足网格列数（模板缺 gridSpan 声明的常见缺陷）
			// 时末尾补空格，避免右侧出现空洞；纵向合并行不补（rowspan 已占位）
			if !hasVMerge && len(tbl.GridCols) > 0 && spanSum < len(tbl.GridCols) {
				sb.WriteString(fmt.Sprintf(`<td colspan="%d"></td>`, len(tbl.GridCols)-spanSum))
			}
			sb.WriteString("</tr>")
		}
		sb.WriteString("</table>")
	}
	return sb.String(), nil
}

// Xlsx 把 xlsx 文件渲染为 HTML
func Xlsx(path string) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sheet := f.GetSheetList()[0]
	rows, err := f.GetRows(sheet)
	if err != nil {
		return "", err
	}

	// 合并单元格 → colspan
	covered := map[string]bool{}
	spanOf := map[string]int{}
	mm, _ := f.GetMergeCells(sheet)
	for _, m := range mm {
		c1, r1, _ := excelize.CellNameToCoordinates(m.GetStartAxis())
		c2, r2, _ := excelize.CellNameToCoordinates(m.GetEndAxis())
		if r1 == r2 && c2 > c1 {
			spanOf[m.GetStartAxis()] = c2 - c1 + 1
			for c := c1 + 1; c <= c2; c++ {
				cn, _ := excelize.CoordinatesToCellName(c, r1)
				covered[cn] = true
			}
		}
	}

	var sb strings.Builder
	// 依据 Excel 列宽生成 colgroup，还原真实版式
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	total := 0
	var cols strings.Builder
	for ci := 1; ci <= maxCols; ci++ {
		name, _ := excelize.ColumnNumberToName(ci)
		w, _ := f.GetColWidth(sheet, name)
		px := int(w*7) + 8
		if px < 28 {
			px = 28
		}
		total += px
		cols.WriteString(fmt.Sprintf(`<col style="width:%dpx">`, px))
	}
	if maxCols > 0 {
		sb.WriteString(fmt.Sprintf(`<table class="pv-table" style="table-layout:fixed;width:%dpx"><colgroup>%s</colgroup>`, total, cols.String()))
	} else {
		sb.WriteString(`<table class="pv-table">`)
	}
	for ri, row := range rows {
		sb.WriteString("<tr>")
		max := len(row)
		for ci := 0; ci < max; ci++ {
			cn, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
			if covered[cn] {
				continue
			}
			text := strings.TrimSpace(row[ci])
			attr := ""
			if sp := spanOf[cn]; sp > 1 {
				attr += fmt.Sprintf(` colspan="%d"`, sp)
			}
			cls := ""
			if text == "姓名" || text == "序号" || text == "签名" || text == "班组" || text == "单位" || text == "部门" {
				cls = ` class="pv-head"`
			}
			sb.WriteString(fmt.Sprintf(`<td%s%s>%s</td>`, attr, cls, html.EscapeString(text)))
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</table>")
	return sb.String(), nil
}

// openTableTag 依据 tblGrid 列宽（dxa）生成带 colgroup 的表格开标签，还原 Word 真实版式；
// 无列宽信息时退回自适应表格。换算：1 英寸=1440 dxa=96px → px = dxa×96/1440。
func openTableTag(gridCols []int) string {
	if len(gridCols) == 0 {
		return `<table class="pv-table">`
	}
	total := 0
	var cols strings.Builder
	for _, w := range gridCols {
		px := w * 96 / 1440
		if px < 28 {
			px = 28
		}
		total += px
		cols.WriteString(fmt.Sprintf(`<col style="width:%dpx">`, px))
	}
	return fmt.Sprintf(`<table class="pv-table" style="table-layout:fixed;width:%dpx"><colgroup>%s</colgroup>`, total, cols.String())
}

// isChineseName 粗略判断 2~4 个汉字（视为姓名，不做标签底色）
func isChineseName(s string) bool {
	r := []rune(s)
	if len(r) < 2 || len(r) > 4 {
		return false
	}
	for _, c := range r {
		if c < 0x4e00 || c > 0x9fff {
			return false
		}
	}
	return true
}
