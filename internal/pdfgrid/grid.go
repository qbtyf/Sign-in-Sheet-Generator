// Package pdfgrid 从 PDF 的向量线条与逐字符文字坐标重建表格网格。
//
// 算法（pdfplumber lattice 思路的 Go 实现，经真实中文 PDF 对拍验证）：
//  1. 线条收集：路径动词 MoveTo/LineTo/Close → 横线段（同 y）与竖线段（同 x）
//  2. 边界聚类：坐标相近（容差内）的线合并，得到行边界 ys 与列边界 xs
//  3. 文字归属：字符中心点 (cx, cy) 落入哪个格子就归属哪个格子
//  4. 同格文字按 y 从上到下、x 从左到右拼接
//
// 文字坐标来自 gopdf 的 TextSpan（逐字符精确 X..EndX），线条来自
// GxPDF 的向量路径，两者均为标准 PDF 用户空间坐标（原点左下，y 向上）。
package pdfgrid

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Seg 一条线段
type Seg struct {
	X1, Y1, X2, Y2 float64
}

// CharSpan 一个字符的精确位置（X 左缘，X2 右缘，Y 基线）
type CharSpan struct {
	Text     string
	X, X2, Y float64
	FontSize float64
}

// BuildResult 重建出的网格
type BuildResult struct {
	Rows [][]string // 行×列 二维数组
	NR   int        // 行数
	NC   int        // 列数
}

// CollectSegs 从路径动词流提取正交线段，区分开放路径与闭合路径。
// 闭合路径（矩形框，如书写下划线框）与开放线段分开返回：
// 网格构建优先用开放线段（pdfplumber 同款策略：rects 不参与 lattice 检测）。
// 动词常量取值与 GxPDF 的 PathVerb 一致（iota：MoveTo=0, LineTo=1, Close=4）。
// 返回 (开放路径线段, 闭合路径线段)。
func CollectSegs(verbs []int, coords []float64) (open, closed []Seg) {
	var segs []Seg
	var wasClosed bool
	cx, cy := 0.0, 0.0
	sx, sy := 0.0, 0.0 // 子路径起点（Close 用）
	i := 0
	const (
		vMoveTo = iota
		vLineTo
		vCubicTo
		vQuadTo
		vClose
	)
	flush := func() {
		if wasClosed {
			closed = append(closed, segs...)
		} else {
			open = append(open, segs...)
		}
		segs = nil
		wasClosed = false
	}
	for _, v := range verbs {
		switch v {
		case vMoveTo:
			flush()
			cx, cy = coords[i], coords[i+1]
			sx, sy = cx, cy
			i += 2
		case vLineTo:
			nx, ny := coords[i], coords[i+1]
			i += 2
			if math.Abs(ny-cy) < 0.01 || math.Abs(nx-cx) < 0.01 { // 仅正交线
				segs = append(segs, Seg{cx, cy, nx, ny})
			}
			cx, cy = nx, ny
		case vCubicTo:
			cx, cy = coords[i+4], coords[i+5]
			i += 6
		case vQuadTo:
			cx, cy = coords[i+2], coords[i+3]
			i += 4
		case vClose:
			if math.Abs(sy-cy) < 0.01 || math.Abs(sx-cx) < 0.01 {
				segs = append(segs, Seg{cx, cy, sx, sy})
			}
			cx, cy = sx, sy
			wasClosed = true
		}
	}
	flush()
	return open, closed
}

// Build 由线段与逐字符坐标重建网格。tol 为线条聚类容差（PDF 点）。
// 线段应只传开放路径线段（矩形框不参与网格检测，见 CollectSegs）；
// 所有正交线段都保留——短列线（如"姓名|签名"表头分界）同样是真边界。
func Build(segs []Seg, spans []CharSpan, tol float64) BuildResult {
	var hs, vs []Seg // 横线、竖线
	for _, s := range segs {
		if math.Abs(s.Y1-s.Y2) <= tol && math.Abs(s.X1-s.X2) > tol {
			x1, x2 := s.X1, s.X2
			if x1 > x2 {
				x1, x2 = x2, x1
			}
			hs = append(hs, Seg{X1: x1, X2: x2, Y1: s.Y1})
		} else if math.Abs(s.X1-s.X2) <= tol && math.Abs(s.Y1-s.Y2) > tol {
			y1, y2 := s.Y1, s.Y2
			if y1 > y2 {
				y1, y2 = y2, y1
			}
			vs = append(vs, Seg{X1: s.X1, Y1: y1, Y2: y2})
		}
	}
	if len(hs) < 2 || len(vs) < 2 {
		return BuildResult{}
	}

	ys := cluster(positions(hs, false), tol) // 行边界
	if len(ys) < 2 {
		return BuildResult{}
	}

	// 逐行归属：每行只用与该行区间相交的竖线作为列边界——
	// 短竖线（如"姓名|签名"分界）只在部分行存在，不存在的边界处
	// 格子自然横向贯通（等价 pdfplumber 的格子四边围合语义）。
	nr := len(ys) - 1
	rows := make([][]string, nr)
	for r := 0; r < nr; r++ {
		lo, hi := ys[r], ys[r+1]
		var rowBounds []float64
		for _, v := range vs {
			if v.Y1 < hi-tol && v.Y2 > lo+tol { // 竖线与行区间相交
				rowBounds = append(rowBounds, v.X1)
			}
		}
		xr := cluster(rowBounds, tol)
		if len(xr) < 2 {
			rows[r] = []string{}
			continue
		}
		nc := len(xr) - 1
		cells := make([][]cellChar, nc)
		for _, sp := range spans {
			cy := sp.Y + sp.FontSize*0.35 // 基线略上移作视觉中心
			if cy < lo-tol || cy >= hi-tol {
				continue
			}
			midx := (sp.X + sp.X2) / 2
			c := binIndex(xr, midx, tol)
			if c >= 0 && c < nc {
				cells[c] = append(cells[c], cellChar{y: sp.Y, x: midx, s: sp.Text})
			}
		}
		row := make([]string, nc)
		for c := 0; c < nc; c++ {
			row[c] = joinCell(cells[c])
		}
		rows[r] = row
	}

	// 行序反转：PDF y 向上（边界数组升序=页底到页顶），
	// 表格视觉第一行在页顶，输出按视觉顺序（第一行在前）
	for i, j := 0, nr-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	nc := 0
	for _, row := range rows {
		if len(row) > nc {
			nc = len(row)
		}
	}
	// 规整化：所有行补齐到最大列数（逐行列边界导致列数可能不同）
	for i, row := range rows {
		if len(row) < nc {
			padded := make([]string, nc)
			copy(padded, row)
			rows[i] = padded
		}
	}
	return BuildResult{Rows: rows, NR: nr, NC: nc}
}

// cellChar 归属到格子的单字符（y 用于格内分行，x 用于组内排序）
type cellChar struct {
	y, x float64
	s    string
}

// joinCell 拼接格内文字：按 y 分组，组内按 x 排序，组间用换行符连接。
func joinCell(chars []cellChar) string {
	if len(chars) == 0 {
		return ""
	}
	sort.Slice(chars, func(i, j int) bool {
		if math.Abs(chars[i].y-chars[j].y) > 0.5 {
			return chars[i].y > chars[j].y // PDF y 向上，大者视觉在上
		}
		return chars[i].x < chars[j].x
	})
	var b strings.Builder
	prevY := chars[0].y
	for i, ch := range chars {
		if i > 0 && prevY-ch.y > 3 { // 换行阈值：3pt 约半行高
			b.WriteString("\n")
		}
		b.WriteString(ch.s)
		prevY = ch.y
	}
	return strings.TrimSpace(b.String())
}

// positions 提取聚类轴上的坐标（竖线取 x，横线取 y）
func positions(segs []Seg, vertical bool) []float64 {
	out := make([]float64, 0, len(segs))
	for _, s := range segs {
		if vertical {
			out = append(out, s.X1)
		} else {
			out = append(out, s.Y1)
		}
	}
	return out
}

// cluster 排序并按容差合并相近坐标
func cluster(vals []float64, tol float64) []float64 {
	if len(vals) == 0 {
		return nil
	}
	sort.Float64s(vals)
	out := []float64{vals[0]}
	for _, v := range vals[1:] {
		if v-out[len(out)-1] > tol {
			out = append(out, v)
		}
	}
	return out
}

// binIndex 返回 v 落入递增边界数组 bounds 的区间下标，区间外返回 -1。
func binIndex(bounds []float64, v float64, tol float64) int {
	lo, hi := 0, len(bounds)-1
	if v < bounds[lo]-tol || v > bounds[hi]+tol {
		return -1
	}
	for i := lo; i < hi; i++ {
		if v >= bounds[i]-tol && v < bounds[i+1]-tol*0.5 {
			return i
		}
	}
	return hi - 1
}

// isWide 判断是否全角字符（保留供未来宽度估算扩展用）
func isWide(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3000 && r <= 0x303F) ||
		(r >= 0xFF00 && r <= 0xFFEF)
}
