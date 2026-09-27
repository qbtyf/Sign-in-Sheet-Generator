// 拆分表格引擎：把一张总表按字段取值或按行数拆成多个子表数据集。
// 按值拆分时拆分字段保留在子表里（可再合并回去）；空值行归入「空值」组，不丢数据。
package tabfill

import (
	"fmt"
	"strings"
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
