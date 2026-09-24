// Package roster 解析人员名单（Excel xlsx / Word docx 两种格式），
// 提取工作表清单、表头与数据行，供前端选择字段与判重。
package roster

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

// SheetInfo 一个可选数据源（xlsx 工作表 或 docx 表格）的概要
type SheetInfo struct {
	Key            string   `json:"key"`            // 唯一标识，如 "xlsx:本工" / "docx:0"
	Label          string   `json:"label"`          // 界面显示名
	HeaderRow      int      `json:"headerRow"`      // 建议表头行（1 基）
	Headers        []string `json:"headers"`        // 表头内容
	DataRows       int      `json:"dataRows"`       // 表头之下的数据行数
	TotalRows      int      `json:"totalRows"`      // 工作表总行数
}

// Table 选定工作表后的完整数据
type Table struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"` // 数据行（等宽补齐）
}

// LoadXlsx 列出 xlsx 中所有工作表概要
func LoadXlsx(path string) ([]SheetInfo, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 失败: %w", err)
	}
	defer f.Close()

	var out []SheetInfo
	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name)
		if err != nil {
			continue
		}
		info := SheetInfo{
			Key:       "xlsx:" + name,
			Label:     name,
			TotalRows: len(rows),
		}
		hdr, data := detectHeader(rows)
		info.HeaderRow = hdr + 1 // 转 1 基
		if hdr >= 0 {
			info.Headers = pad(rows[hdr])
			info.DataRows = data
		}
		out = append(out, info)
	}
	return out, nil
}

// ReadXlsxSheet 读取 xlsx 指定工作表（headerRow 为 1 基）
func ReadXlsxSheet(path, sheet string, headerRow int) (*Table, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	return buildTable(rows, headerRow-1), nil
}

// LoadDocx 列出 docx 中所有顶层表格概要（每张表当一个工作表）
func LoadDocx(path string) ([]SheetInfo, error) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("读取 Word 失败: %w", err)
	}
	tables := docx.ParseTables(string(xmlBytes))
	var out []SheetInfo
	for i, tbl := range tables {
		var strRows [][]string
		for _, r := range tbl.Rows {
			cells := make([]string, len(r.Cells))
			for j, c := range r.Cells {
				cells[j] = strings.TrimSpace(c.Text)
			}
			strRows = append(strRows, cells)
		}
		info := SheetInfo{
			Key:       fmt.Sprintf("docx:%d", i),
			Label:     fmt.Sprintf("表格 %d", i+1),
			TotalRows: len(strRows),
		}
		hdr, data := detectHeader(strRows)
		info.HeaderRow = hdr + 1
		if hdr >= 0 {
			info.Headers = pad(strRows[hdr])
			info.DataRows = data
		}
		out = append(out, info)
	}
	return out, nil
}

// ReadDocxSheet 读取 docx 第 tableIdx 张表格（headerRow 为 1 基）
func ReadDocxSheet(path string, tableIdx, headerRow int) (*Table, error) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return nil, err
	}
	tables := docx.ParseTables(string(xmlBytes))
	if tableIdx < 0 || tableIdx >= len(tables) {
		return nil, fmt.Errorf("表格序号越界")
	}
	var strRows [][]string
	for _, r := range tables[tableIdx].Rows {
		cells := make([]string, len(r.Cells))
		for j, c := range r.Cells {
			cells[j] = strings.TrimSpace(c.Text)
		}
		strRows = append(strRows, cells)
	}
	return buildTable(strRows, headerRow-1), nil
}

// detectHeader 在前 10 行中找第一个非空格数 >=2 的行作为表头
func detectHeader(rows [][]string) (hdr int, dataCount int) {
	hdr = -1
	limit := len(rows)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		if nonEmpty(pad(rows[i])) >= 2 {
			hdr = i
			break
		}
	}
	if hdr < 0 {
		return -1, 0
	}
	for i := hdr + 1; i < len(rows); i++ {
		if nonEmpty(pad(rows[i])) > 0 {
			dataCount++
		}
	}
	return hdr, dataCount
}

func buildTable(rows [][]string, hdr int) *Table {
	if hdr < 0 || hdr >= len(rows) {
		return &Table{}
	}
	t := &Table{Headers: pad(rows[hdr])}
	width := len(t.Headers)
	for i := hdr + 1; i < len(rows); i++ {
		cells := pad(rows[i])
		if nonEmpty(cells) == 0 {
			continue
		}
		for len(cells) < width {
			cells = append(cells, "")
		}
		t.Rows = append(t.Rows, cells[:width])
	}
	return t
}

func pad(r []string) []string {
	out := make([]string, len(r))
	for i, v := range r {
		out[i] = strings.TrimSpace(strings.ReplaceAll(v, "\u00a0", " "))
	}
	return out
}

func nonEmpty(r []string) int {
	n := 0
	for _, v := range r {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}
