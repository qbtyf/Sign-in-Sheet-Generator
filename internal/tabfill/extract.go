package tabfill

// 提取表格引擎（V1.4）：从 Word(.docx) 文档中提取表格。
// 两种输出：
//   .docx —— 复制原 docx 压缩包全部内容（styles/theme/fonts 等原样保留），
//            仅重写 word/document.xml，把勾选的表格按原文完整搬运 → 100% 保真
//   .xlsx —— 每个表格一个工作表：横向合并(gridSpan)/纵向合并(vMerge)还原为
//            Excel 合并单元格，列宽按 tblGrid 换算，底纹/加粗/边框尽量还原，
//            长数字保文本（typedValue 同源）

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"

	"signsheet/internal/docx"
)

// TableInfo 提取表格功能的表格清单项
type TableInfo struct {
	Idx     int      `json:"idx"`     // 表格序号（0 基，按文档中出现顺序）
	Rows    int      `json:"rows"`    // 行数
	Cols    int      `json:"cols"`    // 网格列数（含被合并的虚拟列）
	Preview []string `json:"preview"` // 前 3 行文本预览（行内用「 | 」连接）
}

// ListDocxTables 列出 docx 中的全部顶层表格
func ListDocxTables(path string) ([]TableInfo, error) {
	data, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("不是有效的 docx 文档: %w", err)
	}
	tables := docx.ParseTables(string(data))
	if len(tables) == 0 {
		return nil, fmt.Errorf("文档里没有找到表格（请确认是 Word 中的真实表格，而不是空格/制表符模拟的伪表格）")
	}
	out := make([]TableInfo, len(tables))
	for i, t := range tables {
		info := TableInfo{Idx: i, Rows: len(t.Rows), Cols: gridColsOf(t)}
		for r, row := range t.Rows {
			if r >= 3 {
				break
			}
			parts := make([]string, 0, len(row.Cells))
			for _, c := range row.Cells {
				s := strings.TrimSpace(c.Text)
				if s == "" {
					continue
				}
				parts = append(parts, s)
			}
			if len(parts) > 0 {
				info.Preview = append(info.Preview, strings.Join(parts, " | "))
			}
		}
		out[i] = info
	}
	return out, nil
}

// gridColsOf 网格列数 = max(tblGrid 列数, 各行 gridSpan 之和的最大值)
func gridColsOf(t docx.Table) int {
	n := len(t.GridCols)
	for _, r := range t.Rows {
		s := 0
		for _, c := range r.Cells {
			if c.GridSpan > 1 {
				s += c.GridSpan
			} else {
				s++
			}
		}
		if s > n {
			n = s
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

// ExtractDocx 按勾选序号（idxs，0 基）提取表格，outFmt = ".docx" / ".xlsx"
func ExtractDocx(src, dst, outFmt string, idxs []int) error {
	if strings.EqualFold(outFmt, ".xlsx") {
		return extractDocxToXlsx(src, dst, idxs)
	}
	return extractDocxKeepFormat(src, dst, idxs)
}

// ---------------------------------------------------------------------------
// 输出一：docx 原样提取（100% 保真）
// ---------------------------------------------------------------------------

// extractDocxKeepFormat 复制原 docx 全包，仅重写 document.xml：
// 勾选的表格 XML 原文搬运，其余正文内容去掉，sectPr（页面设置）保留。
func extractDocxKeepFormat(src, dst string, idxs []int) error {
	data, err := docx.ReadFile(src, "word/document.xml")
	if err != nil {
		return fmt.Errorf("不是有效的 docx 文档: %w", err)
	}
	orig := string(data)
	tables := docx.ParseTables(orig)
	if len(tables) == 0 {
		return fmt.Errorf("文档里没有找到表格")
	}
	for _, idx := range idxs {
		if idx < 0 || idx >= len(tables) {
			return fmt.Errorf("表格序号 %d 超出范围（共 %d 张）", idx+1, len(tables))
		}
	}
	var body strings.Builder
	for n, idx := range idxs {
		if n > 0 {
			body.WriteString("<w:p/>") // 相邻表格之间必须有段落，否则 Word 视为一张表
		}
		// Inner 是从原文档逐字节切出的，加上外壳即与原文完全一致
		body.WriteString("<w:tbl>" + tables[idx].Inner + "</w:tbl>")
	}
	body.WriteString("<w:p/>") // 表格后必须跟一个段落（Word 规范）
	newDoc := rebuildDocument(orig, body.String())
	return docx.WriteDocx(src, map[string][]byte{"word/document.xml": []byte(newDoc)}, dst)
}

// rebuildDocument 用原文档的根元素开标签（保留全部 xmlns 声明）和末尾 sectPr
// 重建 document.xml
func rebuildDocument(orig, bodyContent string) string {
	i := strings.Index(orig, "<w:document")
	if i < 0 {
		// 理论上到不了：docx 包已确认能解析
		i = strings.Index(orig, "<w:body")
		rootOpen := orig[:i]
		return rootOpen + "<w:body>" + bodyContent + "</w:body></w:document>"
	}
	j := strings.IndexByte(orig[i:], '>')
	rootOpen := orig[i : i+j+1]
	var sect string
	if k := strings.LastIndex(orig, "<w:sectPr"); k >= 0 {
		if e := strings.Index(orig[k:], "</w:sectPr>"); e >= 0 {
			sect = orig[k : k+e+len("</w:sectPr>")]
		} else if e2 := strings.Index(orig[k:], "/>"); e2 >= 0 {
			sect = orig[k : k+e2+2]
		}
	}
	return rootOpen + "<w:body>" + bodyContent + sect + "</w:body></w:document>"
}

// ---------------------------------------------------------------------------
// 输出二：xlsx（结构还原）
// ---------------------------------------------------------------------------

// 纵向合并标记：<w:vMerge/>（延续）或 <w:vMerge w:val="restart"/>（开始）
var reVMergeVal = regexp.MustCompile(`<w:vMerge(?:[^>]*?w:val="([^"]*)")?[^>]*?/?>`)

// 单元格底纹填充色
var reShdFill = regexp.MustCompile(`<w:shd[^>]*?w:fill="([0-9A-Fa-f]{6})"`)

// extCell 网格中的一个坐标位
type extCell struct {
	text string
	bold bool
	fill string // "FFRRGGBB"，空=无底纹

	covered bool // 被横向合并覆盖 / 纵向合并延续位，不写值

	mergeR1 int // 合并矩形结束行（0 基，-1 = 不纵向合并）
	mergeC1 int // 合并矩形结束列（0 基，-1 = 不横向合并）
}

// extractDocxToXlsx 提取为 xlsx：每个表格一个工作表
func extractDocxToXlsx(src, dst string, idxs []int) error {
	data, err := docx.ReadFile(src, "word/document.xml")
	if err != nil {
		return fmt.Errorf("不是有效的 docx 文档: %w", err)
	}
	tables := docx.ParseTables(string(data))
	if len(tables) == 0 {
		return fmt.Errorf("文档里没有找到表格")
	}
	for _, idx := range idxs {
		if idx < 0 || idx >= len(tables) {
			return fmt.Errorf("表格序号 %d 超出范围（共 %d 张）", idx+1, len(tables))
		}
	}

	f := excelize.NewFile()
	defer f.Close()
	used := map[string]bool{}
	styleCache := map[string]int{}
	hasBorderAll := make([]bool, len(tables))
	for i, idx := range idxs {
		hasBorderAll[i] = tableHasBorders(tables[idx])
	}
	boldID, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})

	for n, idx := range idxs {
		t := tables[idx]
		sheet := SanitizeSheetName(fmt.Sprintf("表格%d", idx+1), n, used)
		if n == 0 {
			f.SetSheetName("Sheet1", sheet)
		} else if _, err := f.NewSheet(sheet); err != nil {
			return fmt.Errorf("创建工作表失败: %w", err)
		}
		grid, nCols := docxTableGrid(t)

		// 列宽（tblGrid 的 dxa 宽度 → Excel 字符宽）
		for c, dxa := range t.GridCols {
			if c >= nCols {
				break
			}
			w := float64(dxa)/15.0 // dxa → px（96dpi）
			w = (w - 5) / 7        // px → Excel 字符宽
			if w < 4 {
				w = 4
			}
			if w > 100 {
				w = 100
			}
			col, _ := excelize.ColumnNumberToName(c + 1)
			_ = f.SetColWidth(sheet, col, col, w)
		}

		// 写值与样式
		for r := range grid {
			for c := range grid[r] {
				cell := &grid[r][c]
				if cell.covered {
					continue
				}
				coord, _ := excelize.CoordinatesToCellName(c+1, r+1)
				if err := f.SetCellValue(sheet, coord, typedValue(cell.text)); err != nil {
					return fmt.Errorf("写入 %s 失败: %w", coord, err)
				}
				styleID := cellStyleID(f, cell, hasBorderAll[n], boldID, styleCache)
				if styleID > 0 {
					_ = f.SetCellStyle(sheet, coord, coord, styleID)
				}
			}
		}

		// 合并单元格
		for r := range grid {
			for c := range grid[r] {
				cell := &grid[r][c]
				if cell.covered || (cell.mergeR1 < 0 && cell.mergeC1 < 0) {
					continue
				}
				r1, c1 := r, c
				if cell.mergeR1 > r1 {
					r1 = cell.mergeR1
				}
				if cell.mergeC1 > c1 {
					c1 = cell.mergeC1
				}
				if r1 == r && c1 == c {
					continue
				}
				a, _ := excelize.CoordinatesToCellName(c+1, r+1)
				b, _ := excelize.CoordinatesToCellName(c1+1, r1+1)
				_ = f.MergeCell(sheet, a, b)
			}
		}
	}
	if err := f.SaveAs(dst); err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}
	return nil
}

// docxTableGrid 把 docx 表格展开成规则的网格模型（处理横/纵合并）
func docxTableGrid(t docx.Table) ([][]extCell, int) {
	nCols := len(t.GridCols)
	for _, r := range t.Rows {
		s := 0
		for _, c := range r.Cells {
			if c.GridSpan > 1 {
				s += c.GridSpan
			} else {
				s++
			}
		}
		if s > nCols {
			nCols = s
		}
	}
	if nCols == 0 {
		nCols = 1
	}

	grid := make([][]extCell, len(t.Rows))
	for r := range grid {
		grid[r] = make([]extCell, nCols)
		for c := range grid[r] {
			grid[r][c].mergeR1, grid[r][c].mergeC1 = -1, -1
		}
	}

	// 纵向合并的开启格：起始列 -> {起始行, 横向跨度}
	type vopen struct{ r0, hspan int }
	open := map[int]vopen{}

	for r, row := range t.Rows {
		c := 0
		for _, cell := range row.Cells {
			span := cell.GridSpan
			if span < 1 {
				span = 1
			}
			if c >= nCols {
				break // 脏数据防御
			}
			inner := cell.Inner
			isVM := strings.Contains(inner, "<w:vMerge")
			isRestart := isVM && func() bool {
				m := reVMergeVal.FindStringSubmatch(inner)
				return m != nil && m[1] == "restart"
			}()

			switch {
			case isVM && !isRestart:
				// 纵向合并的延续格：不写值，只延伸开启格的合并矩形
				if vo, ok := open[c]; ok {
					for cc := c; cc < c+span && cc < nCols; cc++ {
						grid[vo.r0][cc].mergeR1 = r
					}
				}
				for cc := c; cc < c+span && cc < nCols; cc++ {
					grid[r][cc].covered = true
				}
			default:
				fill := ""
				if m := reShdFill.FindStringSubmatch(inner); m != nil && !strings.EqualFold(m[1], "auto") {
					fill = "FF" + strings.ToUpper(m[1])
				}
				grid[r][c] = extCell{
					text:    cell.Text,
					bold:    strings.Contains(inner, "<w:b/>"),
					fill:    fill,
					mergeR1: -1,
					mergeC1: -1,
				}
				if isRestart {
					open[c] = vopen{r0: r, hspan: span}
				}
				if span > 1 {
					grid[r][c].mergeC1 = c + span - 1
					for cc := c + 1; cc < c+span && cc < nCols; cc++ {
						grid[r][cc].covered = true
					}
				}
			}
			c += span
		}
	}
	return grid, nCols
}

// tableHasBorders 表格是否带边框（tblBorders 或任一单元格 tcBorders）
func tableHasBorders(t docx.Table) bool {
	if strings.Contains(t.Inner, "<w:tblBorders") {
		return true
	}
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			if strings.Contains(c.Inner, "<w:tcBorders") {
				return true
			}
		}
	}
	return false
}

// cellStyleID 组合并缓存样式（加粗/底纹/边框三种要素）
func cellStyleID(f *excelize.File, cell *extCell, hasBorder bool, boldID int, cache map[string]int) int {
	if !cell.bold && cell.fill == "" && !hasBorder {
		return 0
	}
	key := fmt.Sprintf("%v|%s|%v", cell.bold, cell.fill, hasBorder)
	if id, ok := cache[key]; ok {
		return id
	}
	if cell.bold && cell.fill == "" && !hasBorder {
		cache[key] = boldID
		return boldID
	}
	st := &excelize.Style{}
	if cell.bold {
		st.Font = &excelize.Font{Bold: true}
	}
	if cell.fill != "" {
		// 传 6 位 RRGGBB，excelize 会自动补 FF alpha 写成合法的 8 位 aRGB；
		// 若直接传 8 位它反而再叠一层 FF 变成非法的 9 位
		st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{cell.fill[2:]}}
	}
	if hasBorder {
		// Border 必须显式给 Color，否则 excelize 写出非法的 rgb="FF"（openpyxl 拒开）
		st.Border = []excelize.Border{
			{Type: "left", Style: 1, Color: "000000"}, {Type: "right", Style: 1, Color: "000000"},
			{Type: "top", Style: 1, Color: "000000"}, {Type: "bottom", Style: 1, Color: "000000"},
		}
	}
	id, err := f.NewStyle(st)
	if err != nil {
		return 0
	}
	cache[key] = id
	return id
}

// ---------------------------------------------------------------------------
// 夹具（mkfixtures 复用）：最小 docx 壳
// ---------------------------------------------------------------------------

// MinimalDocxShell 生成最小可打开的 docx 压缩包（document.xml 由调用者提供）
func MinimalDocxShell(docXML string, dst string) error {
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
		if _, err := io.WriteString(fw, e.body); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}
