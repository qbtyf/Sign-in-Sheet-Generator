// Package tabfill 表格填充引擎：把原始表格（xlsx/docx）的整批数据，
// 按字段对应关系填入模板表格（xlsx/docx）的数据区。
// 与签到表"每行一份文档"不同，这里是"整表填充"：所有数据行填进同一份模板。
package tabfill

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
	"signsheet/internal/roster"
)

// Mapping 模板列 → 原始字段 的对应关系。
// 下标 = 模板表头行第 i 个单元格（0 基，物理格），值 = 原始表 Headers 下标；-1 = 不填充
type Mapping []int

// FillDocx 用 docx 模板生成填充结果。
// tableIdx：模板中第几张顶层表格（0 基）；headerRow：模板表头行（1 基）。
// 数据区 = 表头行之下所有行；行数不足按最后一行样式扩展，多余行删除。
func FillDocx(tplPath string, tableIdx, headerRow int, mp Mapping, src *roster.Table, dst string) error {
	if len(src.Rows) == 0 {
		return fmt.Errorf("原始表格没有数据行，无法填充")
	}
	docXMLBytes, err := docx.ReadFile(tplPath, "word/document.xml")
	if err != nil {
		return fmt.Errorf("读取模板失败: %w", err)
	}
	docXML := string(docXMLBytes)
	tables := docx.ParseTables(docXML)
	if tableIdx < 0 || tableIdx >= len(tables) {
		return fmt.Errorf("模板表格序号越界（共 %d 张）", len(tables))
	}
	tbl := tables[tableIdx]
	headerIdx := headerRow - 1
	if headerIdx < 0 || headerIdx >= len(tbl.Rows) {
		return fmt.Errorf("模板表头行号越界（共 %d 行）", len(tbl.Rows))
	}
	tplDataRows := tbl.Rows[headerIdx+1:]
	if len(tplDataRows) == 0 {
		return fmt.Errorf("模板表头行之下没有数据行，请确认表头行号")
	}

	// 按数据条数重组数据区行：不够克隆最后一行补，多余丢弃
	n := len(src.Rows)
	newRows := make([]string, 0, n)
	for i := 0; i < n; i++ {
		idx := i
		if idx >= len(tplDataRows) {
			idx = len(tplDataRows) - 1 // 克隆最后一行（保留其样式）
		}
		newRows = append(newRows, writeDocxRow(tplDataRows[idx].Inner, mp, src.Rows[i]))
	}

	// 重组表格内部 XML：表头及之前的部分 + 新数据区
	tblInner := tbl.Inner
	firstIdx := strings.Index(tblInner, tplDataRows[0].Inner)
	if firstIdx < 0 {
		return fmt.Errorf("模板表格结构异常，无法定位数据区")
	}
	lastEnd := strings.LastIndex(tblInner, tplDataRows[len(tplDataRows)-1].Inner)
	if lastEnd < 0 {
		return fmt.Errorf("模板表格结构异常，无法定位数据区末尾")
	}
	lastEnd += len(tplDataRows[len(tplDataRows)-1].Inner)
	var sb strings.Builder
	sb.WriteString(tblInner[:firstIdx])
	for _, r := range newRows {
		sb.WriteString(r)
	}
	sb.WriteString(tblInner[lastEnd:])
	newDoc := docx.ReplaceTableSpan(docXML, tableIdx, docx.RebuildTable(sb.String()))

	return docx.WriteDocx(tplPath, map[string][]byte{"word/document.xml": []byte(newDoc)}, dst)
}

// writeDocxRow 把一条数据记录按映射写入行 XML（克隆版式，只改文字）
func writeDocxRow(rowInner string, mp Mapping, rec []string) string {
	row := docx.ParseRow(rowInner)
	// 从后往前替换，避免替换后偏移/索引失效
	for c := len(mp) - 1; c >= 0; c-- {
		si := mp[c]
		if si < 0 || si >= len(rec) || c >= len(row.Cells) {
			continue
		}
		cell := row.Cells[c]
		newInner := docx.SetCellText(&cell, rec[si], false)
		rowInner = docx.ReplaceCellInRow(rowInner, c, newInner)
		// 重新解析，保证后续（更靠前的）单元格索引仍准确
		row = docx.ParseRow(rowInner)
	}
	return rowInner
}

// FillXlsx 用 xlsx 模板生成填充结果。
// sheet：模板工作表名；headerRow：模板表头行（1 基）。
// 数据区行数自动匹配：不足插入新行，多余删除；新单元格套用模板首行数据区样式。
func FillXlsx(tplPath, sheet string, headerRow int, mp Mapping, src *roster.Table, dst string) error {
	if len(src.Rows) == 0 {
		return fmt.Errorf("原始表格没有数据行，无法填充")
	}
	f, err := excelize.OpenFile(tplPath)
	if err != nil {
		return fmt.Errorf("打开模板失败: %w", err)
	}
	defer f.Close()

	rows, err := f.GetRows(sheet)
	if err != nil {
		return fmt.Errorf("读取模板工作表 %q 失败: %w", sheet, err)
	}
	headerIdx := headerRow - 1
	if headerIdx < 0 || headerIdx >= len(rows) {
		return fmt.Errorf("模板表头行号越界（共 %d 行）", len(rows))
	}

	// 列宽 = 表头行与所有数据行中的最大格数
	width := len(rows[headerIdx])
	for _, r := range rows[headerIdx+1:] {
		if len(r) > width {
			width = len(r)
		}
	}
	if width == 0 {
		return fmt.Errorf("模板表头行为空")
	}

	tplData := rows[headerIdx+1:]
	n := len(src.Rows)
	have := len(tplData)

	// 记录模板首条数据行每列的样式（供新行套用）；必须在增删行之前取
	styleIDs := make([]int, width)
	for c := 1; c <= width; c++ {
		if have > 0 {
			coord, _ := excelize.CoordinatesToCellName(c, headerRow+1)
			styleIDs[c-1], _ = f.GetCellStyle(sheet, coord)
		}
	}

	// 增删数据区行，使行数等于 n
	if n > have {
		// 在现有数据区末尾之后插入（headerRow+1+have 行之前 = 紧跟最后一条数据之后）
		if err := f.InsertRows(sheet, headerRow+1+have, n-have); err != nil {
			return fmt.Errorf("扩展数据区失败: %w", err)
		}
	} else if n < have {
		// 删除多余的尾部数据行；每次删一行，后面的行自动上移，故始终删同一位置
		for i := 0; i < have-n; i++ {
			if err := f.RemoveRow(sheet, headerRow+n+1); err != nil {
				return fmt.Errorf("删除多余数据行失败: %w", err)
			}
		}
	}

	// 逐行逐列写入
	for i, rec := range src.Rows {
		rowNum := headerRow + 1 + i
		for c := 0; c < len(mp) && c < width; c++ {
			si := mp[c]
			if si < 0 || si >= len(rec) {
				continue
			}
			coord, _ := excelize.CoordinatesToCellName(c+1, rowNum)
			if err := f.SetCellValue(sheet, coord, rec[si]); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", coord, err)
			}
			if styleIDs[c] > 0 {
				_ = f.SetCellStyle(sheet, coord, coord, styleIDs[c])
			}
		}
	}

	if err := f.SaveAs(dst); err != nil {
		return fmt.Errorf("保存结果失败: %w", err)
	}
	return nil
}

// AutoPair 同名自动预配对：返回与模板表头等长的 Mapping，
// 模板栏目与原始字段完全同名（去空格）时配对，否则 -1。
func AutoPair(tplHeaders, srcHeaders []string) Mapping {
	mp := make(Mapping, len(tplHeaders))
	for i, th := range tplHeaders {
		th = strings.TrimSpace(th)
		mp[i] = -1
		if th == "" {
			continue
		}
		for j, sh := range srcHeaders {
			if strings.TrimSpace(sh) == th {
				mp[i] = j
				break
			}
		}
	}
	return mp
}
