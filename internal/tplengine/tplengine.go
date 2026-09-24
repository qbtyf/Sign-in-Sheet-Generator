// Package tplengine 解析签到表模板（docx / xlsx）：
// 定位"姓名"表头行与独立姓名列、检测单张容量、
// 识别数据区可映射列（供名单字段逐人填入）、
// 提取信息字段（{占位符} 与 "标签格＋相邻格" 留空字段）。
package tplengine

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

// Analysis 模板解析结果
type Analysis struct {
	Kind          string      `json:"kind"`          // docx | xlsx
	Sheet         string      `json:"sheet"`         // xlsx: 工作表名
	Title         string      `json:"title"`         // 模板标题（用于输出文件命名）
	TableIdx      int         `json:"tableIdx"`      // docx: 匹配到的顶层表格序号
	HeaderRowIdx  int         `json:"headerRowIdx"`  // 姓名表头行（0 基）
	NameCols      []int       `json:"nameCols"`      // 独立姓名列（0 基物理列号）
	DataStartIdx  int         `json:"dataStartIdx"`  // 数据区起始行（0 基）
	DataRows      int         `json:"dataRows"`      // 可用数据行数
	Capacity      int         `json:"capacity"`      // 单张容量 = 数据行 × 姓名列数
	Placeholders  []string    `json:"placeholders"`  // 文档中的 {占位符}
	HeaderPreview []string    `json:"headerPreview"` // 表头行各格文本
	DataCols      []DataCol   `json:"dataCols"`      // 数据区可映射列
	TplFields     []TplField  `json:"tplFields"`     // 信息字段（占位符 + 留空字段）
}

// DataCol 数据区可映射列（姓名列之外的空白列也可映射名单字段逐人填入）
type DataCol struct {
	ColIdx int    `json:"colIdx"` // 数据行内物理列号（0 基）
	Label  string `json:"label"`  // 表头行该列的文字（如 姓名/工号/签名）
	IsName bool   `json:"isName"` // 是否姓名列
}

// TplField 模板信息字段：占位符（{xx}）或留空字段（标签格＋相邻空格/预填格）
type TplField struct {
	Name    string `json:"name"`            // 字段名（标签/占位符名）
	Kind    string `json:"kind"`            // placeholder | blank
	Default string `json:"default,omitempty"` // 预填文字（如"科室（班组）集中培训"）
	Multi   bool   `json:"multi,omitempty"`   // 预填为多行（如"一、二、三、"）
	Row     int    `json:"row,omitempty"`     // blank: 表格内行号（0 基）
	Col     int    `json:"col,omitempty"`     // blank: 行内物理列号（0 基）
}

// footerKeywords 数据区终止关键词（页脚行特征）
var footerKeywords = []string{"培训效果评价", "评价人", "审核意见", "签到情况汇总"}

// AnalyzeDocx 解析 docx 模板
func AnalyzeDocx(path string) (*Analysis, error) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("读取模板失败: %w", err)
	}
	xmlStr := string(xmlBytes)
	tables := docx.ParseTables(xmlStr)

	for ti, tbl := range tables {
		for ri, row := range tbl.Rows {
			var nameCols []int
			for ci, c := range row.Cells {
				if strings.TrimSpace(c.Text) == "姓名" {
					nameCols = append(nameCols, ci)
				}
			}
			if len(nameCols) == 0 {
				continue
			}
			// 数据区：表头之下、姓名列全为空的连续行
			dataRows := 0
			for r := ri + 1; r < len(tbl.Rows); r++ {
				empty := true
				for _, nc := range nameCols {
					if nc < len(tbl.Rows[r].Cells) && strings.TrimSpace(tbl.Rows[r].Cells[nc].Text) != "" {
						empty = false
						break
					}
				}
				rowText := ""
				for _, c := range tbl.Rows[r].Cells {
					rowText += c.Text
				}
				if empty && containsAny(rowText, footerKeywords) {
					break
				}
				if !empty {
					break
				}
				dataRows++
			}
			a := &Analysis{
				Kind:         "docx",
				TableIdx:     ti,
				HeaderRowIdx: ri,
				NameCols:     nameCols,
				DataStartIdx: ri + 1,
				DataRows:     dataRows,
				Capacity:     dataRows * len(nameCols),
			}
			// 表头行预览
			for _, c := range row.Cells {
				a.HeaderPreview = append(a.HeaderPreview, strings.TrimSpace(c.Text))
			}
			// 标题：优先取表格外段落的首行；否则取表格前几行的第一个非空格文本
			paras := docx.TopParagraphs(xmlStr)
			if len(paras) > 0 {
				a.Title = paras[0]
			}
			if a.Title == "" {
				for r := 0; r < ri && r < len(tbl.Rows); r++ {
					for _, c := range tbl.Rows[r].Cells {
						t := strings.TrimSpace(c.Text)
						if t != "" {
							a.Title = t
							break
						}
					}
					if a.Title != "" {
						break
					}
				}
			}
			a.Placeholders = docx.ExtractPlaceholders(xmlStr)
			a.DataCols = docxDataCols(&tbl, ri, ri+1+dataRows, nameCols)
			a.TplFields = collectDocxFields(&tbl, ri, nameCols, a.Placeholders)
			return a, nil
		}
	}
	return nil, fmt.Errorf("模板中未找到含「姓名」表头行的签到表结构")
}

// docxDataCols 识别数据区的可映射列：表头行有文字、其下数据区该列全空
func docxDataCols(tbl *docx.Table, headerIdx, dataEnd int, nameCols []int) []DataCol {
	isName := map[int]bool{}
	for _, nc := range nameCols {
		isName[nc] = true
	}
	var out []DataCol
	for ci, c := range tbl.Rows[headerIdx].Cells {
		label := strings.TrimSpace(c.Text)
		if label == "" {
			continue
		}
		empty := true
		for r := headerIdx + 1; r < dataEnd && r < len(tbl.Rows); r++ {
			if ci < len(tbl.Rows[r].Cells) && strings.TrimSpace(tbl.Rows[r].Cells[ci].Text) != "" {
				empty = false
				break
			}
		}
		if !empty {
			continue
		}
		out = append(out, DataCol{ColIdx: ci, Label: label, IsName: isName[ci]})
	}
	return out
}

// collectDocxFields 收集信息字段：占位符 + 表头行之上"标签格＋相邻格"留空字段
func collectDocxFields(tbl *docx.Table, headerIdx int, nameCols []int, placeholders []string) []TplField {
	seen := map[string]bool{}
	var out []TplField
	for _, p := range placeholders {
		out = append(out, TplField{Name: p, Kind: "placeholder"})
		seen[p] = true
	}
	for r := 0; r < headerIdx; r++ {
		cells := tbl.Rows[r].Cells
		for i := 0; i < len(cells); i++ {
			label := cleanLabel(cells[i].Text)
			if !labelLike(label) {
				continue
			}
			if i+1 >= len(cells) {
				continue
			}
			vc := cells[i+1]
			vt := strings.TrimSpace(vc.Text)
			if containsAny(vt, footerKeywords) {
				continue
			}
			if _, dup := seen[label]; dup {
				i++ // 已有同名字段，仍跳过值格
				continue
			}
			f := TplField{Name: label, Kind: "blank", Default: vt, Row: r, Col: i + 1}
			// 多行检测：值格内段落数 >1（"<w:p>" 与 "<w:p " 两种开标签）
			pCount := strings.Count(vc.Inner, "<w:p>") + strings.Count(vc.Inner, "<w:p ")
			f.Multi = pCount > 1
			out = append(out, f)
			seen[label] = true
			i++ // 跳过值格
		}
	}
	return out
}

// cleanLabel 去掉标签尾部的冒号与空白
func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "：")
	s = strings.TrimSuffix(s, ":")
	return strings.TrimSpace(s)
}

// labelLike 判断文字是否像一个字段标签
func labelLike(s string) bool {
	if s == "" || s == "姓名" {
		return false
	}
	if n := len([]rune(s)); n > 12 {
		return false
	}
	if strings.ContainsAny(s, "{}<>") {
		return false
	}
	if containsAny(s, footerKeywords) {
		return false
	}
	return true
}

// AnalyzeXlsx 解析 xlsx 模板
func AnalyzeXlsx(path string) (*Analysis, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 模板失败: %w", err)
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			var nameCols []int
			for ci, v := range row {
				if strings.TrimSpace(v) == "姓名" {
					nameCols = append(nameCols, ci)
				}
			}
			if len(nameCols) == 0 {
				continue
			}
			dataRows := 0
			for r := ri + 1; r < len(rows); r++ {
				empty := true
				for _, nc := range nameCols {
					if nc < len(rows[r]) && strings.TrimSpace(rows[r][nc]) != "" {
						empty = false
						break
					}
				}
				rowText := strings.Join(rows[r], "")
				if empty && containsAny(rowText, footerKeywords) {
					break
				}
				if !empty {
					break
				}
				dataRows++
			}
			a := &Analysis{
				Kind:         "xlsx",
				Sheet:        sheet,
				HeaderRowIdx: ri,
				NameCols:     nameCols,
				DataStartIdx: ri + 1,
				DataRows:     dataRows,
				Capacity:     dataRows * len(nameCols),
			}
			for _, c := range rows[ri] {
				a.HeaderPreview = append(a.HeaderPreview, strings.TrimSpace(c))
			}
			if len(rows) > 0 && len(rows[0]) > 0 {
				a.Title = strings.TrimSpace(rows[0][0])
			}
			a.Placeholders = extractXlsxPlaceholders(f, sheet)
			a.DataCols = xlsxDataCols(rows, ri, ri+1+dataRows, nameCols)
			a.TplFields = collectXlsxFields(rows, ri, nameCols, a.Placeholders)
			return a, nil
		}
	}
	return nil, fmt.Errorf("Excel 模板中未找到含「姓名」的表头行")
}

// xlsxDataCols 识别 xlsx 数据区可映射列
func xlsxDataCols(rows [][]string, headerIdx, dataEnd int, nameCols []int) []DataCol {
	isName := map[int]bool{}
	for _, nc := range nameCols {
		isName[nc] = true
	}
	var out []DataCol
	for ci := range rows[headerIdx] {
		label := strings.TrimSpace(rows[headerIdx][ci])
		if label == "" {
			continue
		}
		empty := true
		for r := headerIdx + 1; r < dataEnd && r < len(rows); r++ {
			if ci < len(rows[r]) && strings.TrimSpace(rows[r][ci]) != "" {
				empty = false
				break
			}
		}
		if !empty {
			continue
		}
		out = append(out, DataCol{ColIdx: ci, Label: label, IsName: isName[ci]})
	}
	return out
}

// collectXlsxFields 收集 xlsx 信息字段
func collectXlsxFields(rows [][]string, headerIdx int, nameCols []int, placeholders []string) []TplField {
	seen := map[string]bool{}
	var out []TplField
	for _, p := range placeholders {
		out = append(out, TplField{Name: p, Kind: "placeholder"})
		seen[p] = true
	}
	for r := 0; r < headerIdx; r++ {
		cells := rows[r]
		for i := 0; i < len(cells); i++ {
			label := cleanLabel(cells[i])
			if !labelLike(label) {
				continue
			}
			if i+1 >= len(cells) {
				continue
			}
			vt := strings.TrimSpace(cells[i+1])
			if containsAny(vt, footerKeywords) {
				continue
			}
			if _, dup := seen[label]; dup {
				i++
				continue
			}
			f := TplField{Name: label, Kind: "blank", Default: vt, Row: r, Col: i + 1}
			f.Multi = strings.Contains(cells[i+1], "\n")
			out = append(out, f)
			seen[label] = true
			i++
		}
	}
	return out
}

// extractXlsxPlaceholders 扫描工作表所有单元格中的 {xx}
func extractXlsxPlaceholders(f *excelize.File, sheet string) []string {
	re := regexp.MustCompile(`\{([^{}]{1,30})\}`)
	seen := map[string]bool{}
	var out []string
	rows, _ := f.GetRows(sheet)
	for _, row := range rows {
		for _, v := range row {
			for _, m := range re.FindAllStringSubmatch(v, -1) {
				name := strings.TrimSpace(m[1])
				if name == "" || strings.ContainsAny(name, "<>/\"=") || seen[name] {
					continue
				}
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
