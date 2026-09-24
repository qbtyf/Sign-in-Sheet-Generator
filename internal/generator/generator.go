// Package generator 按模板生成签到表：
// docx 模板 → 直接操作 OOXML（处理横向合并格、居中、拆分、
// 名单字段逐人多列填入、留空字段写回）；
// xlsx 模板 → 通过 excelize 填写。
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
	"signsheet/internal/tplengine"
)

// PersonRow 一个人：姓名 + 分类值 + 各字段值（键=名单表头字段名）
type PersonRow struct {
	Name string
	Cat  string
	Vals map[string]string
}

// ColMap 模板数据区列 ← 名单字段 的映射
type ColMap struct {
	ColIdx int    `json:"colIdx"` // 模板数据区物理列号（0 基）
	Field  string `json:"field"`  // 名单表头字段名
}

// Group 一个分类的人员（已按名单顺序排序）
type Group struct {
	Name    string      // 分类名（空 = 未按分类拆分）
	Persons []PersonRow
}

// PartOpts 一张签到表的填写选项
type PartOpts struct {
	Fill    map[string]string // 字段值（键=字段名；同时覆盖占位符与留空字段）
	Persons []PersonRow       // 本张人员
	Maps    []ColMap          // 数据区列映射（非空 = 每行一人模式）
}

// PartInfo 一张输出文件的信息
type PartInfo struct {
	File  string `json:"file"`
	Group string `json:"group"`
	Part  int    `json:"part"`
	Parts int    `json:"parts"`
	Count int    `json:"count"`
}

// SplitPersons 按单张容量拆分人员序列
func SplitPersons(persons []PersonRow, capacity int) [][]PersonRow {
	if capacity <= 0 {
		capacity = 30
	}
	var parts [][]PersonRow
	for len(persons) > 0 {
		n := capacity
		if n > len(persons) {
			n = len(persons)
		}
		parts = append(parts, persons[:n])
		persons = persons[n:]
	}
	if len(parts) == 0 {
		parts = [][]PersonRow{{}}
	}
	return parts
}

// SanitizeName 去除文件名非法字符
func SanitizeName(s string) string {
	repl := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return strings.TrimSpace(repl.Replace(s))
}

// OutputPath 生成输出文件名：标题-分类(序号).docx/xlsx；分类为空时仅用标题
func OutputPath(dir, title, group string, part, parts int, ext string) string {
	if strings.TrimSpace(title) == "" {
		title = "签到表"
	}
	name := SanitizeName(title)
	if group != "" {
		name = SanitizeName(title + "-" + group)
	}
	if parts > 1 {
		name = fmt.Sprintf("%s(%d)", name, part)
	}
	return filepath.Join(dir, name+"."+ext)
}

// GenDocxPart 生成一张 docx 签到表，返回本张填写人数
func GenDocxPart(tplPath, outPath string, a *tplengine.Analysis, o PartOpts) (int, error) {
	xmlBytes, err := docx.ReadFile(tplPath, "word/document.xml")
	if err != nil {
		return 0, err
	}
	xmlStr := string(xmlBytes)

	// 1. 替换 {占位符}（留空字段名不会出现在占位符里，无副作用）
	xmlStr = docx.ReplacePlaceholders(xmlStr, o.Fill)

	// 2. 重新解析，定位目标表格
	tables := docx.ParseTables(xmlStr)
	if a.TableIdx < 0 || a.TableIdx >= len(tables) {
		return 0, fmt.Errorf("模板表格定位失败")
	}
	tbl := tables[a.TableIdx]
	tblInner := tbl.Inner

	// 行内容缓存：同一行多次改格时基于最新内容
	rowInners := map[int]string{}
	getRow := func(ri int) string {
		if s, ok := rowInners[ri]; ok {
			return s
		}
		return tbl.Rows[ri].Inner
	}
	setRow := func(ri int, inner string) {
		rowInners[ri] = inner
		tblInner = docx.ReplaceRowInTable(tblInner, ri, inner)
	}

	// 3. 留空字段写回（数据区之外的标签格＋相邻格）
	for _, tf := range a.TplFields {
		if tf.Kind != "blank" {
			continue
		}
		v := strings.TrimSpace(o.Fill[tf.Name])
		if v == "" {
			continue
		}
		if tf.Row >= len(tbl.Rows) {
			continue
		}
		rowInner := getRow(tf.Row)
		cellCount := docx.CellCount(rowInner)
		if tf.Col >= cellCount {
			continue
		}
		// 重新取该行该格（可能已被同格其他字段改过）
		curRow := rowInner
		cell := cellAt(curRow, tf.Col)
		if cell == nil {
			continue
		}
		var newInner string
		if tf.Multi {
			items := strings.Split(v, "；")
			newInner = docx.SetCellLines(cell, items)
		} else {
			newInner = docx.SetCellText(cell, v, false)
		}
		setRow(tf.Row, docx.ReplaceCellInRow(rowInner, tf.Col, newInner))
	}

	// 4. 填人员
	filled := 0
	persons := o.Persons
	if len(o.Maps) == 0 {
		// 每行多个姓名格模式（如 姓名/签名×3 的表）
		slot := 0
		for i := 0; i < a.DataRows && slot < len(persons); i++ {
			rowIdx := a.DataStartIdx + i
			if rowIdx >= len(tbl.Rows) {
				break
			}
			rowInner := getRow(rowIdx)
			changed := false
			for _, nc := range a.NameCols {
				if slot >= len(persons) {
					break
				}
				name := persons[slot].Name
				slot++
				if name == "" {
					continue
				}
				if nc >= docx.CellCount(rowInner) {
					continue
				}
				cell := cellAt(rowInner, nc)
				if cell == nil {
					continue
				}
				newInner := docx.SetCellText(cell, name, true)
				rowInner = docx.ReplaceCellInRow(rowInner, nc, newInner)
				changed = true
				filled++
			}
			if changed {
				setRow(rowIdx, rowInner)
			}
		}
	} else {
		// 每行一人模式：姓名→第一个姓名列，映射字段→同行对应列
		for i, p := range persons {
			if i >= a.DataRows {
				break
			}
			rowIdx := a.DataStartIdx + i
			if rowIdx >= len(tbl.Rows) {
				break
			}
			rowInner := getRow(rowIdx)
			if p.Name != "" && a.NameCols[0] < docx.CellCount(rowInner) {
				cell := cellAt(rowInner, a.NameCols[0])
				if cell != nil {
					rowInner = docx.ReplaceCellInRow(rowInner, a.NameCols[0], docx.SetCellText(cell, p.Name, true))
					filled++
				}
			}
			for _, m := range o.Maps {
				v := strings.TrimSpace(p.Vals[m.Field])
				if v == "" || m.ColIdx == a.NameCols[0] {
					continue
				}
				if m.ColIdx >= docx.CellCount(rowInner) {
					continue
				}
				cell := cellAt(rowInner, m.ColIdx)
				if cell == nil {
					continue
				}
				rowInner = docx.ReplaceCellInRow(rowInner, m.ColIdx, docx.SetCellText(cell, v, true))
			}
			setRow(rowIdx, rowInner)
		}
	}

	// 5. 写盘
	xmlStr = docx.ReplaceTableSpan(xmlStr, a.TableIdx, docx.RebuildTable(tblInner))
	replaces := map[string][]byte{"word/document.xml": []byte(xmlStr)}
	if err := docx.WriteDocx(tplPath, replaces, outPath); err != nil {
		return 0, err
	}
	return filled, nil
}

// cellAt 从行 XML 中解析出第 idx 个物理单元格（curRow 需为该行最新内容）
func cellAt(rowInner string, idx int) *docx.Cell {
	rows := docx.ParseRow(rowInner)
	if idx < 0 || idx >= len(rows.Cells) {
		return nil
	}
	return &rows.Cells[idx]
}

// GenXlsxPart 生成一张 xlsx 签到表，返回本张填写人数
func GenXlsxPart(tplPath, outPath string, a *tplengine.Analysis, o PartOpts) (int, error) {
	f, err := excelize.OpenFile(tplPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sheet := a.Sheet

	// 1. 替换占位符 / 留空字段单元格
	rows, _ := f.GetRows(sheet)
	fieldAt := map[[2]int]tplengine.TplField{}
	for _, tf := range a.TplFields {
		if tf.Kind == "blank" {
			fieldAt[[2]int{tf.Row, tf.Col}] = tf
		}
	}
	for ri, row := range rows {
		for ci, v := range row {
			nv := v
			for k, val := range o.Fill {
				nv = strings.ReplaceAll(nv, "{"+k+"}", val)
			}
			if tf, ok := fieldAt[[2]int{ri, ci}]; ok {
				fv := strings.TrimSpace(o.Fill[tf.Name])
				if fv != "" {
					if tf.Multi {
						fv = strings.ReplaceAll(fv, "；", "\n")
					}
					nv = fv
				}
			}
			if nv != v {
				cellName, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
				f.SetCellValue(sheet, cellName, nv)
			}
		}
	}

	// 2. 填人员（居中样式）
	style, err := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err != nil {
		style = -1
	}
	setCell := func(col, row int, val string) {
		cellName, _ := excelize.CoordinatesToCellName(col+1, row+1)
		f.SetCellValue(sheet, cellName, val)
		if style >= 0 {
			f.SetCellStyle(sheet, cellName, cellName, style)
		}
	}
	filled := 0
	persons := o.Persons
	if len(o.Maps) == 0 {
		slot := 0
		for i := 0; i < a.DataRows && slot < len(persons); i++ {
			for _, nc := range a.NameCols {
				if slot >= len(persons) {
					break
				}
				p := persons[slot]
				slot++
				if p.Name == "" {
					continue
				}
				setCell(nc, a.DataStartIdx+i, p.Name)
				filled++
			}
		}
	} else {
		for i, p := range persons {
			if i >= a.DataRows {
				break
			}
			if p.Name != "" {
				setCell(a.NameCols[0], a.DataStartIdx+i, p.Name)
				filled++
			}
			for _, m := range o.Maps {
				v := strings.TrimSpace(p.Vals[m.Field])
				if v == "" || m.ColIdx == a.NameCols[0] {
					continue
				}
				setCell(m.ColIdx, a.DataStartIdx+i, v)
			}
		}
	}

	if err := f.SaveAs(outPath); err != nil {
		return 0, err
	}
	return filled, nil
}

// GenGroup 生成一个分类的全部签到表（自动拆分），返回每张文件信息
func GenGroup(tplPath, outDir string, a *tplengine.Analysis, g Group, capacity int, fill map[string]string, maps []ColMap) ([]PartInfo, error) {
	if capacity <= 0 || capacity > a.Capacity {
		capacity = a.Capacity
	}
	if len(maps) > 0 && capacity > a.DataRows {
		capacity = a.DataRows // 每行一人模式：单张容量 = 数据行数
	}
	// 文件名标题用替换占位符后的值（如 {培训名称}→实际名称）
	title := a.Title
	for k, v := range fill {
		title = strings.ReplaceAll(title, "{"+k+"}", v)
	}
	title = strings.ReplaceAll(strings.ReplaceAll(title, "{", ""), "}", "")
	parts := SplitPersons(g.Persons, capacity)
	var infos []PartInfo
	for pi, ps := range parts {
		out := OutputPath(outDir, title, g.Name, pi+1, len(parts), a.Kind)
		opts := PartOpts{Fill: fill, Persons: ps, Maps: maps}
		var (
			n   int
			err error
		)
		if a.Kind == "xlsx" {
			n, err = GenXlsxPart(tplPath, out, a, opts)
		} else {
			n, err = GenDocxPart(tplPath, out, a, opts)
		}
		if err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", filepath.Base(out), err)
		}
		infos = append(infos, PartInfo{
			File:  filepath.Base(out),
			Group: g.Name,
			Part:  pi + 1,
			Parts: len(parts),
			Count: n,
		})
	}
	return infos, nil
}

// EnsureDir 确保目录存在
func EnsureDir(dir string) error { return os.MkdirAll(dir, 0o755) }
