// 拆分表格引擎：把一张总表按字段取值或按行数拆成多个子表数据集。
// 按值拆分时拆分字段保留在子表里（可再合并回去）；空值行归入「空值」组，不丢数据。
package tabfill

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// SplitSpec 拆分规格
type SplitSpec struct {
	Headers []string
	Rows    [][]string
	Mode    string // "byValue"（按字段取值） / "byRows"（按行数）
	Field   int    // byValue：拆分字段下标
	Size    int    // byRows：每份行数（>=1）
}

// SplitGroup 一组拆分结果：组名（用作文件名一部分）＋ 该组数据行
type SplitGroup struct {
	Name string
	Rows [][]string
}

// Split 执行拆分，返回按稳定顺序排列的分组。
func Split(spec SplitSpec) ([]SplitGroup, error) {
	if spec.Mode != "byValue" && spec.Mode != "byRows" {
		return nil, fmt.Errorf("未知拆分方式: %q", spec.Mode)
	}
	if len(spec.Rows) == 0 {
		return nil, fmt.Errorf("总表没有数据行，无法拆分")
	}

	if spec.Mode == "byRows" {
		if spec.Size < 1 {
			return nil, fmt.Errorf("每份行数必须 ≥ 1")
		}
		var groups []SplitGroup
		for start := 0; start < len(spec.Rows); start += spec.Size {
			end := start + spec.Size
			if end > len(spec.Rows) {
				end = len(spec.Rows)
			}
			groups = append(groups, SplitGroup{
				Name: fmt.Sprintf("第%02d批", len(groups)+1),
				Rows: spec.Rows[start:end],
			})
		}
		return groups, nil
	}

	// byValue：按字段取值分组，保持首次出现顺序
	if spec.Field < 0 || spec.Field >= len(spec.Headers) {
		return nil, fmt.Errorf("拆分字段下标越界")
	}
	idx := map[string]int{} // 组名 → 组下标
	var groups []SplitGroup
	for _, rec := range spec.Rows {
		v := ""
		if spec.Field < len(rec) {
			v = strings.TrimSpace(rec[spec.Field])
		}
		name := v
		if name == "" {
			name = "空值"
		}
		gi, ok := idx[name]
		if !ok {
			gi = len(groups) // 新组下标；不修 gi 的话首行会错进第 0 组
			idx[name] = gi
			groups = append(groups, SplitGroup{Name: name})
		}
		groups[gi].Rows = append(groups[gi].Rows, rec)
	}
	return groups, nil
}

// SanitizeFileName 清理不能出现在 Windows 文件名里的字符
func SanitizeFileName(name string) string {
	r := strings.NewReplacer(
		`\`, "_", "/", "_", ":", "_", "*", "_", "?", "_",
		`"`, "_", "<", "_", ">", "_", "|", "_",
	)
	return strings.TrimSpace(r.Replace(name))
}

// ---------------------------------------------------------------------------
// 表格内拆分落盘：分组结果 → 一个 xlsx 里的多个工作表（Sheet）
// ---------------------------------------------------------------------------

// WriteXlsxMultiSheet 把分组结果写成一个 xlsx 文件：每组一个工作表，
// 每个 Sheet 首行为表头（加粗），数据行自动还原数字/日期类型。
// Sheet 名 = 组名（做合法性清理与防重）。
func WriteXlsxMultiSheet(headers []string, groups []SplitGroup, dst string) error {
	if len(groups) == 0 {
		return fmt.Errorf("没有可写的工作表")
	}
	f := excelize.NewFile()
	defer f.Close()

	boldID, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	used := map[string]bool{}
	for i, g := range groups {
		sheet := SanitizeSheetName(g.Name, i, used)
		used[sheet] = true
		if i == 0 {
			f.SetSheetName("Sheet1", sheet) // 新建文件自带 Sheet1，改名复用
		} else if _, err := f.NewSheet(sheet); err != nil {
			return fmt.Errorf("创建工作表 %q 失败: %w", sheet, err)
		}
		if err := writeSheetData(f, sheet, headers, g.Rows, boldID); err != nil {
			return err
		}
	}
	if err := f.SaveAs(dst); err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}
	return nil
}

// SanitizeSheetName 清理 Excel 工作表名：替换非法字符（: \ / ? * [ ] 等），
// 截断到 31 字符，并与 used 中已有名字防重（重名加序号）。
func SanitizeSheetName(name string, idx int, used map[string]bool) string {
	r := strings.NewReplacer(
		":", "_", `\`, "_", "/", "_", "?", "_", "*", "_",
		"[", "_", "]", "_", "'", "_",
	)
	s := strings.TrimSpace(r.Replace(name))
	if s == "" {
		s = fmt.Sprintf("Sheet%d", idx+1)
	}
	runes := []rune(s)
	if len(runes) > 31 {
		s = string(runes[:31])
	}
	if !used[s] {
		return s
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("%d", i)
		cand := s + suffix
		runes = []rune(cand)
		if len(runes) > 31 {
			cand = string([]rune(s)[:31-len(suffix)]) + suffix
		}
		if !used[cand] {
			return cand
		}
	}
}

// writeSheetData 向指定工作表写表头（加粗）＋数据行（类型自动还原）
func writeSheetData(f *excelize.File, sheet string, headers []string, rows [][]string, boldID int) error {
	for c, h := range headers {
		coord, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := f.SetCellValue(sheet, coord, h); err != nil {
			return fmt.Errorf("写表头失败: %w", err)
		}
		_ = f.SetCellStyle(sheet, coord, coord, boldID)
	}
	for r, rec := range rows {
		for c := 0; c < len(headers); c++ {
			v := ""
			if c < len(rec) {
				v = rec[c]
			}
			coord, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := f.SetCellValue(sheet, coord, typedValue(v)); err != nil {
				return fmt.Errorf("写入 %s 失败: %w", coord, err)
			}
		}
	}
	return nil
}
