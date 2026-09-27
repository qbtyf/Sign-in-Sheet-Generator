// pdfverify 开发期验证工具：对真实 PDF 跑网格重建，导出 xlsx 供对拍。
package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/coregx/gxpdf"

	"signsheet/internal/pdfext"
	"signsheet/internal/pdfgrid"
)

func main() {
	debug := len(os.Args) > 1 && os.Args[1] == "-debug"
	args := os.Args[1:]
	if debug {
		args = os.Args[2:]
	}
	for _, path := range args {
		if debug {
			debugLines(path)
			continue
		}
		fmt.Printf("\n================ %s ================\n", path)
		tables, err := pdfext.ListPDFTables(path)
		if err != nil {
			fmt.Println("清单失败:", err)
			continue
		}
		for _, t := range tables {
			fmt.Printf("第%d页: %d 行 x %d 列  预览: %v\n", t.Page, t.Rows, t.Cols, t.Preview)
		}
		out := os.TempDir() + "/go_extract_p1.xlsx"
		n, err := pdfext.ExtractPDFTables(path, []int{tables[0].Page}, ".xlsx", out)
		fmt.Printf("提取第%d页 -> %s: 表格数 %d, err=%v\n", tables[0].Page, out, n, err)
	}
}

func pdfextList(path string) ([]pdfext.PDFTableInfo, error) { return pdfext.ListPDFTables(path) }
func pdfextExtract(path string, page int, out string) (int, error) {
	return pdfext.ExtractPDFTables(path, []int{page}, ".xlsx", out)
}

func debugLines(path string) {
	raw, _ := os.ReadFile(path)
	if i := bytes.Index(raw, []byte("startxref\r")); i >= 0 {
		out := make([]byte, len(raw))
		copy(out, raw)
		copy(out[i:], []byte("startxref\n"))
		raw = out
	}
	doc, err := gxpdf.OpenFromBytes(raw)
	if err != nil {
		fmt.Println("打开失败:", err)
		return
	}
	defer doc.Close()
	paths, err := doc.GetVectorGraphicsForPage(0)
	if err != nil {
		fmt.Println("向量失败:", err)
		return
	}
	var hys, vxs, other []float64
	type vseg struct {
		x, h float64
	}
	var vsegs []vseg
	nSeg := 0
	for _, p := range paths {
		verbs := make([]int, len(p.Verbs))
		for i, v := range p.Verbs {
			verbs[i] = int(v)
		}
		openSegs, closedSegs := pdfgrid.CollectSegs(verbs, p.Coords)
		all := append(append([]pdfgrid.Seg{}, openSegs...), closedSegs...)
		for _, s := range all {
			nSeg++
			if math.Abs(s.Y1-s.Y2) < 0.01 {
				hys = append(hys, s.Y1)
			} else if math.Abs(s.X1-s.X2) < 0.01 {
				vxs = append(vxs, s.X1)
				vsegs = append(vsegs, vseg{s.X1, math.Abs(s.Y2 - s.Y1)})
			} else {
				other = append(other, s.Y1)
			}
		}
	}
	sort.Float64s(hys)
	sort.Float64s(vxs)
	fmt.Printf("线段总数: %d (横 %d 竖 %d 斜 %d)\n", nSeg, len(hys), len(vxs), len(other))
	fmt.Println("横线 y 值:", fround(hys))
	fmt.Println("竖线 x 值:", fround(vxs))
	for _, v := range vsegs {
		fmt.Printf("  竖线 x=%.1f 高=%.1f\n", v.x, v.h)
	}
}

func fround(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = math.Round(x*10) / 10
	}
	return out
}
