// Package pdfext 从 PDF 提取表格并输出 xlsx / docx / pdf。
//
// 管线：WPS 兼容修补 → GxPDF 解析（线条+文字 run）→ pdfgrid 网格重建
// → 三种输出（xlsx/docx 复用 tabfill 写引擎，pdf 用 pdfcpu 页提取）。
package pdfext

import (
	"bytes"
	"fmt"
	"os"

	gopdf "github.com/razvandimescu/gopdf/pdf"
	"github.com/coregx/gxpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"

	"signsheet/internal/pdfgrid"
	"signsheet/internal/tabfill"
)

// PDFTableInfo 一页上重建出的表格信息
type PDFTableInfo struct {
	Page    int      `json:"page"`   // 页码（1 起）
	Rows    int      `json:"rows"`   // 行数（含表头行）
	Cols    int      `json:"cols"`   // 列数
	Preview []string `json:"preview"` // 首行每格截断文本
}

// FixStartxref 把 WPS 风格的 "startxref\r<数字>" 无损修补为 "startxref\n<数字>"。
// 1 字节换 1 字节，文件内所有对象偏移量保持不变。
func FixStartxref(data []byte) []byte {
	if i := bytes.Index(data, []byte("startxref\r")); i >= 0 {
		out := make([]byte, len(data))
		copy(out, data)
		copy(out[i:], []byte("startxref\n"))
		return out
	}
	return data
}

// openDocs 同时打开 GxPDF（线条）与 gopdf（逐字符文字）文档。
// 两者都经 WPS 兼容修补（gopdf 本身宽容，修补对它无害）。
func openDocs(path string) (*gxpdf.Document, *gopdf.Document, func(), error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	fixed := FixStartxref(raw)
	doc, err := gxpdf.OpenFromBytes(fixed)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("打开 PDF 失败: %w", err)
	}
	gdoc, err := gopdf.OpenBytes(fixed)
	if err != nil {
		doc.Close()
		return nil, nil, nil, fmt.Errorf("打开 PDF 失败(文字层): %w", err)
	}
	cleanup := func() {
		doc.Close()
	}
	return doc, gdoc, cleanup, nil
}

// ListPDFTables 打开 PDF 并重建每页表格，返回表格清单。
func ListPDFTables(path string) ([]PDFTableInfo, error) {
	doc, gdoc, cleanup, err := openDocs(path)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var out []PDFTableInfo
	for p := 1; p <= len(doc.Pages()); p++ {
		g, err := buildPageGrid(doc, gdoc, p)
		if err != nil {
			continue
		}
		if g.NR < 2 || g.NC < 2 {
			continue // 至少 2 行 2 列才算表格
		}
		info := PDFTableInfo{Page: p, Rows: g.NR, Cols: g.NC}
		first := g.Rows[0]
		info.Preview = make([]string, len(first))
		for c := 0; c < len(first); c++ {
			s := first[c]
			if len(s) > 12 {
				s = s[:12] + "…"
			}
			info.Preview[c] = s
		}
		out = append(out, info)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未检测到有线框的表格（扫描件或图片型 PDF 不支持）")
	}
	return out, nil
}

// ExtractPDFTables 把选定页码（1 起）的表格输出为指定格式。
// outFmt: ".xlsx"（每表一个 Sheet）/ ".docx"（按页序拼接）/ ".pdf"（页原样提取）。
// 返回处理的表格数。
func ExtractPDFTables(path string, pages []int, outFmt, dst string) (int, error) {
	if len(pages) == 0 {
		return 0, fmt.Errorf("未选择要提取的表格")
	}

	if outFmt == ".pdf" {
		// 页原样提取（pdfcpu 直接支持 WPS 格式，无需修补）
		ps := make([]string, len(pages))
		for i, p := range pages {
			ps[i] = fmt.Sprintf("%d", p)
		}
		if err := api.TrimFile(path, dst, ps, nil); err != nil {
			return 0, fmt.Errorf("页提取失败: %w", err)
		}
		return len(pages), nil
	}

	doc, gdoc, cleanup, err := openDocs(path)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	n := 0
	for _, p := range pages {
		g, err := buildPageGrid(doc, gdoc, p)
		if err != nil || g.NR < 2 || g.NC < 2 {
			continue
		}
		headers := g.Rows[0]
		body := g.Rows[1:]
		title := fmt.Sprintf("第%d页表格", p)
		switch outFmt {
		case ".xlsx":
			if n == 0 {
				if err := tabfill.WriteXlsxTable(headers, body, dst); err != nil {
					return n, err
				}
			} else {
				sheet := fmt.Sprintf("第%d页", p)
				if _, err := tabfill.AppendMergedSheet(dst, dst, sheet, headers, body); err != nil {
					return n, err
				}
			}
		case ".docx":
			if n == 0 {
				if err := tabfill.WriteDocxTable(headers, body, title, dst); err != nil {
					return n, err
				}
			} else {
				if err := tabfill.AppendDocxTable(dst, dst, title, headers, body); err != nil {
					return n, err
				}
			}
		default:
			return n, fmt.Errorf("不支持的输出格式: %s", outFmt)
		}
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("选定页均未检测到有线框的表格")
	}
	return n, nil
}

// buildPageGrid 重建某一页（1 起）的表格网格。
// 文字坐标：gopdf（逐字符精确 X..EndX）；线条：GxPDF 向量路径。
// 两库坐标系一致（标准 PDF 用户空间，原点左下，y 向上），已实测确认。
// 注意页码差异：GxPDF 向量 API 0 起，gopdf Page() 0 起。
func buildPageGrid(doc *gxpdf.Document, gdoc *gopdf.Document, page int) (pdfgrid.BuildResult, error) {
	paths, err := doc.GetVectorGraphicsForPage(page - 1)
	if err != nil {
		return pdfgrid.BuildResult{}, err
	}
	var segs []pdfgrid.Seg
	var closedSegs []pdfgrid.Seg
	for _, p := range paths {
		verbs := make([]int, len(p.Verbs))
		for i, v := range p.Verbs {
			verbs[i] = int(v)
		}
		openSegs, closed := pdfgrid.CollectSegs(verbs, p.Coords)
		segs = append(segs, openSegs...)
		closedSegs = append(closedSegs, closed...)
	}
	// 开放线段优先（pdfplumber 策略：矩形框不参与网格检测）；
	// 开放线不足 4 条时才并入闭合路径（纯矩形绘制的表格兜底）
	if len(segs) < 4 {
		segs = append(segs, closedSegs...)
	}

	gpage := gdoc.Page(page - 1)
	lines, err := gpage.TextLines()
	if err != nil {
		return pdfgrid.BuildResult{}, fmt.Errorf("文字提取失败: %w", err)
	}
	var spans []pdfgrid.CharSpan
	for _, l := range lines {
		for _, s := range l.Spans {
			spans = append(spans, pdfgrid.CharSpan{
				Text: s.Text, X: s.X, X2: s.EndX, Y: s.Y, FontSize: s.FontSize,
			})
		}
	}
	g := pdfgrid.Build(segs, spans, 1.5)
	return g, nil
}

