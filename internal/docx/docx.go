// Package docx 提供对 .docx（OOXML）文件的底层读写能力：
// 解析表格（行/单元格/合并标记）、向单元格写入文本、替换占位符、重建文档。
// 所有操作基于标准库 archive/zip + 字符串级 XML 处理，不依赖第三方库。
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// ZIP 读写
// ---------------------------------------------------------------------------

// ReadFile 从 docx(zip) 中读取指定内部路径的文件内容
func ReadFile(path, inner string) ([]byte, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开 docx 失败: %w", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == inner {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("docx 内未找到 %s", inner)
}

// ReadBytes 从内存中的 docx(zip) 字节读取指定内部路径的文件内容（内嵌模板预览用）
func ReadBytes(data []byte, inner string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解析 docx 失败: %w", err)
	}
	for _, f := range r.File {
		if f.Name == inner {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("docx 内未找到 %s", inner)
}

// WriteDocx 将内部文件映射写入新的 docx（其余条目原样拷贝自 src）
func WriteDocx(src string, replaces map[string][]byte, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("打开源 docx 失败: %w", err)
	}
	defer r.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	w := zip.NewWriter(out)
	defer w.Close()

	for _, f := range r.File {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if rep, ok := replaces[f.Name]; ok {
			if _, err := fw.Write(rep); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, rc); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// XML 标签扫描
// ---------------------------------------------------------------------------

type tagKind int

const (
	tagOpen tagKind = iota
	tagClose
	tagSelfClose
)

type xmlTag struct {
	name  string
	kind  tagKind
	start int // '<' 位置
	end   int // '>' 位置（含）
}

// 扫描字符串中所有标签（要求属性值中不含 '>'，OOXML 实际满足）
func scanTags(s string) []xmlTag {
	var tags []xmlTag
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			break
		}
		start := i + lt
		gt := strings.IndexByte(s[start:], '>')
		if gt < 0 {
			break
		}
		end := start + gt
		raw := s[start : end+1]
		kind := tagOpen
		var name string
		switch {
		case strings.HasPrefix(raw, "</"):
			kind = tagClose
			name = raw[2 : len(raw)-1] // 去掉 "</" 和 ">"
		case strings.HasSuffix(raw, "/>"):
			kind = tagSelfClose
			name = raw[1 : len(raw)-2] // 去掉 "<" 和 "/>"
		default:
			name = raw[1 : len(raw)-1] // 去掉 "<" 和 ">"
		}
		if j := strings.IndexAny(name, " \t\r\n"); j >= 0 {
			name = name[:j]
		}
		tags = append(tags, xmlTag{name: name, kind: kind, start: start, end: end})
		i = end + 1
	}
	return tags
}

type span struct {
	start, end int // 含首含尾的字节区间
}

// topLevelSpans 返回字符串中顶层 <name>...</name> 的区间。
// 搜索 w:tr / w:tc 时会跳过嵌套表格内部（tblDepth 计数）；
// 搜索 w:tbl 自身时返回最外层表格区间。
func topLevelSpans(s string, name string) []span {
	tags := scanTags(s)
	var spans []span
	tblDepth := 0
	var openStart int
	open := false
	for _, t := range tags {
		isTbl := t.name == "w:tbl"
		switch {
		case isTbl && t.kind == tagOpen:
			if name == "w:tbl" && tblDepth == 0 && !open {
				open = true
				openStart = t.start
			}
			tblDepth++
		case isTbl && t.kind == tagSelfClose:
			if name == "w:tbl" && tblDepth == 0 && !open {
				spans = append(spans, span{t.start, t.end})
			}
		case isTbl && t.kind == tagClose:
			tblDepth--
			if name == "w:tbl" && tblDepth == 0 && open {
				spans = append(spans, span{openStart, t.end})
				open = false
			}
		case t.name == name && tblDepth == 0:
			switch t.kind {
			case tagOpen:
				if !open {
					open = true
					openStart = t.start
				}
			case tagSelfClose:
				if !open {
					spans = append(spans, span{t.start, t.end})
				}
			case tagClose:
				if open {
					spans = append(spans, span{openStart, t.end})
					open = false
				}
			}
		}
	}
	return spans
}

// ---------------------------------------------------------------------------
// 表格模型
// ---------------------------------------------------------------------------

// Cell 表格中的一个物理单元格
type Cell struct {
	Inner    string // <w:tc> 与 </w:tc> 之间的原始 XML
	GridSpan int    // 横向合并跨的网格数（默认 1）
	VMerge   bool   // 是否含纵向合并标记
	Text     string // 可见文本（所有 w:t 拼接）
}

// Row 表格中的一行
type Row struct {
	Inner string
	Cells []Cell
}

// Table 解析后的表格
// Table 表格
type Table struct {
	Inner    string
	Rows     []Row
	GridCols []int // w:tblGrid 声明的各列宽度（dxa 单位），预览还原版式用
}

var (
	reGridSpan = regexp.MustCompile(`<w:gridSpan[^>]*w:val="(\d+)"`)
	reWT       = regexp.MustCompile(`<w:t(?: [^>]*)?>`)
	reWTAll    = regexp.MustCompile(`<w:t(?: [^>]*)?>(?s:(.*?))</w:t>`)
	reTCPr     = regexp.MustCompile(`<w:tcPr(?s:[^>]*?)(/>|>.*?</w:tcPr>)`)
	reGridCol  = regexp.MustCompile(`<w:gridCol[^>]*w:w="(\d+)"`)
)

func xmlUnescape(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", "\"", "&apos;", "'", "&#x9;", "\t", "&#xA;", "\n", "&amp;", "&")
	return r.Replace(s)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func parseRow(rowInner string) Row {
	row := Row{Inner: rowInner}
	for _, sp := range topLevelSpans(rowInner, "w:tc") {
		inner := rowInner[sp.start : sp.end+1]
		inner = inner[strings.IndexByte(inner, '>')+1 : len(inner)-len("</w:tc>")]
		c := Cell{Inner: inner, GridSpan: 1}
		if m := reTCPr.FindStringSubmatch(inner); m != nil {
			tcpr := m[0]
			if g := reGridSpan.FindStringSubmatch(tcpr); g != nil {
				c.GridSpan, _ = strconv.Atoi(g[1])
			}
			c.VMerge = strings.Contains(tcpr, "<w:vMerge")
		}
		var sb strings.Builder
		for _, t := range reWTAll.FindAllStringSubmatch(inner, -1) {
			sb.WriteString(xmlUnescape(t[1]))
		}
		c.Text = sb.String()
		row.Cells = append(row.Cells, c)
	}
	return row
}

// ParseRow 解析行 XML（供外部基于最新行内容读取单元格）
func ParseRow(rowInner string) Row { return parseRow(rowInner) }

// ParseTables 提取 document.xml 中所有顶层表格
func ParseTables(docXML string) []Table {
	var tables []Table
	for _, sp := range topLevelSpans(docXML, "w:tbl") {
		inner := docXML[sp.start : sp.end+1]
		inner = inner[strings.IndexByte(inner, '>')+1 : len(inner)-len("</w:tbl>")]
		tbl := Table{Inner: inner}
		for _, g := range reGridCol.FindAllStringSubmatch(inner, -1) {
			n, _ := strconv.Atoi(g[1])
			tbl.GridCols = append(tbl.GridCols, n)
		}
		for _, rsp := range topLevelSpans(inner, "w:tr") {
			tbl.Rows = append(tbl.Rows, parseRow(inner[rsp.start:rsp.end+1]))
		}
		tables = append(tables, tbl)
	}
	return tables
}

// TopParagraphs 提取表格之外的顶层段落文本（用于标题/说明）
func TopParagraphs(docXML string) []string {
	var lines []string
	s := docXML
	// 逐个去掉顶层表格，避免其内部段落混入
	for {
		sp := topLevelSpans(s, "w:tbl")
		if len(sp) == 0 {
			break
		}
		s = s[:sp[0].start] + s[sp[0].end+1:]
	}
	for _, sp := range topLevelSpans(s, "w:p") {
		inner := s[sp.start : sp.end+1]
		var sb strings.Builder
		for _, t := range reWTAll.FindAllStringSubmatch(inner, -1) {
			sb.WriteString(xmlUnescape(t[1]))
		}
		if txt := strings.TrimSpace(sb.String()); txt != "" {
			lines = append(lines, txt)
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// 单元格写入
// ---------------------------------------------------------------------------

// SetCellText 将单元格文本设为 name（保留原格式：改写第一个 w:t，清空其余），并可设置居中
func SetCellText(cell *Cell, text string, center bool) string {
	inner := cell.Inner
	wts := reWTAll.FindAllStringSubmatchIndex(inner, -1)
	if len(wts) > 0 {
		// 从后往前替换，避免偏移失效
		for i := len(wts) - 1; i >= 0; i-- {
			m := wts[i]
			val := ""
			if i == 0 {
				val = xmlEscape(text)
			}
			// m[0]:m[1] 是整个 <w:t...>...</w:t> 元素；开标签止于元素内第一个 '>'
			gt := strings.IndexByte(inner[m[0]:m[1]], '>')
			openTag := inner[m[0] : m[0]+gt+1]
			var newTag string
			if strings.HasSuffix(openTag, "/>") {
				// 自闭合 <w:t/> —— 展开为带内容的形式
				newTag = openTag[:len(openTag)-2] + ">" + val + "</w:t>"
				inner = inner[:m[0]] + newTag + inner[m[1]:]
			} else {
				newTag = openTag + val + "</w:t>"
				inner = inner[:m[0]] + newTag + inner[m[1]:]
			}
		}
	} else {
		// 无 w:t：在第一个段落末尾注入 run
		run := fmt.Sprintf(`<w:r><w:t xml:space="preserve">%s</w:t></w:r>`, xmlEscape(text))
		idx := strings.Index(inner, "</w:p>")
		if idx < 0 {
			return inner
		}
		inner = inner[:idx] + run + inner[idx:]
	}
	if center {
		inner = centerParagraphs(inner)
	}
	return inner
}

// SetCellLines 将单元格多行内容按"行"写入：保留每行原有的"一、二、三、"编号前缀，
// 条目数超过现有行时按中文编号续行（克隆最后一行格式）。返回新 inner。
func SetCellLines(cell *Cell, items []string) string {
	inner := cell.Inner
	paraSpans := topLevelSpans(inner, "w:p")
	if len(paraSpans) == 0 {
		return SetCellText(cell, strings.Join(items, "；"), false)
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	// 模板行自带编号（一、二、三、）则沿用模板编号；全空则自动补中文编号
	autoNum := true
	for _, sp := range paraSpans {
		if linePrefix(paraText(inner[sp.start:sp.end+1])) != "" {
			autoNum = false
			break
		}
	}
	n := len(paraSpans)
	if len(items) > n {
		n = len(items)
	}
	for idx := 0; idx < n; idx++ {
		if idx >= len(items) {
			continue // 条目少于行数：保留原空编号行
		}
		prefix := ""
		if autoNum {
			prefix = CNNum(idx+1) + "、"
		}
		// 条目自带编号（用户输入"一、xxx"）则不再叠加前缀，避免"一、一、"
		if linePrefix(items[idx]) != "" {
			prefix = ""
		}
		if idx < len(paraSpans) {
			sp := paraSpans[idx]
			if !autoNum {
				prefix = linePrefix(paraText(inner[sp.start : sp.end+1]))
			}
			edits = append(edits, edit{sp.start, sp.end + 1,
				setParaText(inner[sp.start:sp.end+1], prefix+items[idx])})
			continue
		}
		// 续行：克隆最后一个段落，编号取"十一、十二、…"
		cloneOf := len(paraSpans) - 1
		sp := paraSpans[cloneOf]
		lastPara := inner[sp.start : sp.end+1]
		if !autoNum || linePrefix(items[idx]) != "" {
			prefix = ""
			if !autoNum && linePrefix(items[idx]) == "" {
				prefix = CNNum(idx+1) + "、"
			}
		}
		newPara := setParaText(lastPara, prefix+items[idx])
		edits = append(edits, edit{sp.end + 1, sp.end + 1, newPara})
	}
	// 从后往前应用编辑
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		inner = inner[:e.start] + e.text + inner[e.end:]
	}
	return inner
}

// paraText 提取段落可见文本
func paraText(paraInner string) string {
	var sb strings.Builder
	for _, t := range reWTAll.FindAllStringSubmatch(paraInner, -1) {
		sb.WriteString(xmlUnescape(t[1]))
	}
	return sb.String()
}

// setParaText 将段落文本设为 text（保留首个 w:t 属性，清空其余）
func setParaText(paraInner, text string) string {
	wts := reWTAll.FindAllStringSubmatchIndex(paraInner, -1)
	if len(wts) == 0 {
		run := fmt.Sprintf(`<w:r><w:t xml:space="preserve">%s</w:t></w:r>`, xmlEscape(text))
		idx := strings.Index(paraInner, "</w:p>")
		if idx < 0 {
			return paraInner
		}
		return paraInner[:idx] + run + paraInner[idx:]
	}
	for i := len(wts) - 1; i >= 0; i-- {
		m := wts[i]
		val := ""
		if i == 0 {
			val = xmlEscape(text)
		}
		gt := strings.IndexByte(paraInner[m[0]:m[1]], '>')
		openTag := paraInner[m[0] : m[0]+gt+1]
		var newTag string
		if strings.HasSuffix(openTag, "/>") {
			newTag = openTag[:len(openTag)-2] + ">" + val + "</w:t>"
		} else {
			newTag = openTag + val + "</w:t>"
		}
		paraInner = paraInner[:m[0]] + newTag + paraInner[m[1]:]
	}
	return paraInner
}

// linePrefix 提取"一、""（二）"式编号前缀；非编号行返回空
func linePrefix(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) < 2 {
		return ""
	}
	last := r[len(r)-1]
	if last != '、' && last != '．' && last != '.' && last != '）' {
		return ""
	}
	for _, c := range r[:len(r)-1] {
		if !strings.ContainsRune("一二三四五六七八九十（）()", c) {
			return ""
		}
	}
	return s
}

// CNNum 阿拉伯数字转中文序号（1→一，11→十一，21→二十一）
func CNNum(n int) string {
	nums := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九", "十"}
	if n <= 0 {
		return ""
	}
	if n <= 10 {
		return nums[n]
	}
	if n < 20 {
		return "十" + nums[n-10]
	}
	if n < 100 {
		tail := ""
		if n%10 != 0 {
			tail = nums[n%10]
		}
		return nums[n/10] + "十" + tail
	}
	return fmt.Sprintf("%d", n)
}

// centerParagraphs 确保每个 <w:p> 的 pPr 中含 <w:jc w:val="center"/>
func centerParagraphs(inner string) string {
	var out strings.Builder
	pos := 0
	for {
		pStart := strings.Index(inner[pos:], "<w:p>")
		pStart2 := strings.Index(inner[pos:], "<w:p ")
		start := -1
		openLen := 5
		if pStart >= 0 && (pStart2 < 0 || pStart < pStart2) {
			start = pos + pStart
		} else if pStart2 >= 0 {
			start = pos + pStart2
			openLen = 0
		}
		if start < 0 {
			out.WriteString(inner[pos:])
			break
		}
		gt := strings.Index(inner[start:], ">")
		if gt < 0 {
			out.WriteString(inner[pos:])
			break
		}
		openEnd := start + gt + 1
		pClose := strings.Index(inner[openEnd:], "</w:p>")
		if pClose < 0 {
			out.WriteString(inner[pos:])
			break
		}
		closeAbs := openEnd + pClose
		para := inner[start:closeAbs]
		para = ensureJC(para, openLen)
		out.WriteString(inner[pos:start])
		out.WriteString(para)
		pos = closeAbs
	}
	return out.String()
}

// ensureJC 在段落 XML（不含 </w:p> 的开段部分）中插入居中标记
func ensureJC(para string, openTagLen int) string {
	head := para[:openTagLen] // "<w:p>" 或 "<w:p ...>"
	rest := para[openTagLen:]
	if strings.Contains(rest, "<w:jc") {
		return para
	}
	jc := `<w:jc w:val="center"/>`
	pprIdx := strings.Index(rest, "<w:pPr>")
	if pprIdx < 0 {
		// 无 pPr：紧随段落后插入
		return head + "<w:pPr>" + jc + "</w:pPr>" + rest
	}
	pprEnd := strings.Index(rest[pprIdx:], "</w:pPr>")
	if pprEnd < 0 {
		return para
	}
	pprEndAbs := pprIdx + pprEnd
	pprContent := rest[pprIdx+len("<w:pPr>") : pprEndAbs]
	if strings.Contains(pprContent, "<w:jc") {
		return para
	}
	// 按 schema 顺序：jc 应在 rPr 之前
	rprIdx := strings.Index(pprContent, "<w:rPr")
	if rprIdx >= 0 {
		newContent := pprContent[:rprIdx] + jc + pprContent[rprIdx:]
		return head + rest[:pprIdx+len("<w:pPr>")] + newContent + rest[pprEndAbs:]
	}
	return head + rest[:pprEndAbs] + jc + rest[pprEndAbs:]
}

// ReplacePlaceholders 全文替换 {占位符}（值做 XML 转义）
func ReplacePlaceholders(docXML string, values map[string]string) string {
	for k, v := range values {
		docXML = strings.ReplaceAll(docXML, "{"+k+"}", xmlEscape(v))
	}
	return docXML
}

// ExtractPlaceholders 提取文档中所有 {xx} 占位符名称（去重、保序）
func ExtractPlaceholders(docXML string) []string {
	re := regexp.MustCompile(`\{([^{}]{1,30})\}`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(docXML, -1) {
		name := strings.TrimSpace(m[1])
		// 过滤疑似非占位符（含 xml 标签碎片）
		if name == "" || strings.ContainsAny(name, "<>/\"=") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ReplaceTableSpan 用新内容替换 docXML 中第 idx 个顶层表格（0 基）
func ReplaceTableSpan(docXML string, idx int, newInner string) string {
	spans := topLevelSpans(docXML, "w:tbl")
	if idx < 0 || idx >= len(spans) {
		return docXML
	}
	sp := spans[idx]
	var buf bytes.Buffer
	buf.WriteString(docXML[:sp.start])
	buf.WriteString(newInner)
	buf.WriteString(docXML[sp.end+1:])
	return buf.String()
}

// RebuildTable 由修改后的行内 XML 重建 <w:tbl> 字符串
func RebuildTable(tblInner string) string {
	return "<w:tbl>" + tblInner + "</w:tbl>"
}

// RowInner 返回行的完整 XML（含 <w:tr>）
func RowInner(r Row) string { return r.Inner }

// ReplaceCellInRow 将行内第 idx 个物理单元格替换为 newCellInner（不含 <w:tc> 外壳）
func ReplaceCellInRow(rowInner string, idx int, newCellInner string) string {
	spans := topLevelSpans(rowInner, "w:tc")
	if idx < 0 || idx >= len(spans) {
		return rowInner
	}
	sp := spans[idx]
	var buf bytes.Buffer
	buf.WriteString(rowInner[:sp.start])
	buf.WriteString("<w:tc>")
	buf.WriteString(newCellInner)
	buf.WriteString("</w:tc>")
	buf.WriteString(rowInner[sp.end+1:])
	return buf.String()
}

// CellSpans 返回行内物理单元格区间数
func CellCount(rowInner string) int {
	return len(topLevelSpans(rowInner, "w:tc"))
}

// ReplaceRowInTable 将表格内第 idx 个顶层行替换为 newRowInner（含 <w:tr> 外壳）
func ReplaceRowInTable(tblInner string, idx int, newRowInner string) string {
	spans := topLevelSpans(tblInner, "w:tr")
	if idx < 0 || idx >= len(spans) {
		return tblInner
	}
	sp := spans[idx]
	var buf bytes.Buffer
	buf.WriteString(tblInner[:sp.start])
	buf.WriteString(newRowInner)
	buf.WriteString(tblInner[sp.end+1:])
	return buf.String()
}
