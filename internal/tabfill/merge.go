// 多表合一引擎：把多张结构相近的来源表按字段映射拼接成一张总表。
// 映射方向：总表字段（基准表字段＋新增字段）→ 每来源表的字段下标（-1 填空）。
package tabfill

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// SrcTable 一张待合并的来源表（已按所选表头行读取）
type SrcTable struct {
	Name    string   // 来源显示名（文件名·工作表名，用于来源列）
	Headers []string // 来源表字段
	Rows    [][]string
}

// MergeSpec 合并规格
type MergeSpec struct {
	Base      []string // 总表字段清单（基准表字段＋新增字段）
	PerSource [][]int  // 与 srcs 一一对应；每个元素长度 = len(Base)，值 = 来源字段下标（-1 填空）
	SourceCol bool     // 总表末尾附加「来源」列
	Dedup     int      // 按总表字段下标判重（保留首次出现），-1 = 不判重
}

// MergeResult 合并结果（交由 WriteXlsxTable / WriteDocxTable 落盘）
type MergeResult struct {
	Headers []string
	Rows    [][]string
}

// Merge 执行合并：逐来源表逐行映射到总表字段，拼接、判重。
func Merge(spec MergeSpec, srcs []SrcTable) (*MergeResult, error) {
	if len(spec.Base) == 0 {
		return nil, fmt.Errorf("总表没有字段（基准表为空）")
	}
	width := len(spec.Base)
	headers := make([]string, width)
	copy(headers, spec.Base)
	if spec.SourceCol {
		headers = append(headers, "来源")
	}

	res := &MergeResult{Headers: headers}
	seen := map[string]bool{}
	for si, src := range srcs {
		if si >= len(spec.PerSource) {
			return nil, fmt.Errorf("来源表 %q 缺少映射", src.Name)
		}
		mp := spec.PerSource[si]
		if len(mp) != width {
			return nil, fmt.Errorf("来源表 %q 映射长度不匹配", src.Name)
		}
		for _, rec := range src.Rows {
			out := make([]string, len(headers))
			for c := 0; c < width; c++ {
				v := ""
				if j := mp[c]; j >= 0 && j < len(rec) {
					v = rec[j]
				}
				out[c] = v
			}
			if spec.SourceCol {
				out[width] = src.Name
			}
			if spec.Dedup >= 0 && spec.Dedup < width {
				key := strings.TrimSpace(out[spec.Dedup])
				if key != "" {
					if seen[key] {
						continue // 重复行丢弃，保留首次出现
					}
					seen[key] = true
				}
			}
			res.Rows = append(res.Rows, out)
		}
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// 通用落盘：表头＋数据行 → xlsx / docx（合并与拆分共用）
// ---------------------------------------------------------------------------

// WriteXlsxTable 把表头＋数据行写成 xlsx（Sheet 名"数据"）。
// 数值与日期字符串自动还原为真正的数字/日期类型；≥11 位的纯数字保持文本
//（手机号、身份证号防精度丢失与科学计数显示）。
func WriteXlsxTable(headers []string, rows [][]string, dst string) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "数据"
	f.SetSheetName("Sheet1", sheet)

	boldID, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
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
	if err := f.SaveAs(dst); err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}
	return nil
}

// typedValue 把字符串还原为合适的单元格类型：
// 日期时间 → time.Time；短数字 → float64；其余（含长数字）→ 原字符串。
func typedValue(s string) any {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	layouts := []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
		"2006/01/02 15:04:05", "2006/01/02", "2006/1/2", "2006年1月2日",
	}
	for _, layout := range layouts {
		if v, err := time.ParseInLocation(layout, t, time.Local); err == nil {
			return v
		}
	}
	if len(t) <= 10 {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			return v
		}
	}
	return s
}
