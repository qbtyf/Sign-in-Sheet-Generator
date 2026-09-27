package pdfgrid

import (
	"testing"
)

// TestBuildSimpleGrid 手工构造 2x3 网格（2 行边界、3 条竖线）+ 字符
func TestBuildSimpleGrid(t *testing.T) {
	// 横线 y=100（全宽）、y=80、y=60（全宽）——构成 2 行
	segs := []Seg{
		{X1: 10, Y1: 100, X2: 210, Y2: 100},
		{X1: 10, Y1: 80, X2: 210, Y2: 80},
		{X1: 10, Y1: 60, X2: 210, Y2: 60},
		{X1: 10, Y1: 60, X2: 10, Y2: 100},
		{X1: 110, Y1: 60, X2: 110, Y2: 100},
		{X1: 210, Y1: 60, X2: 210, Y2: 100},
	}
	// "姓名" 在顶行左格（y 高 = 视觉在上），"签名" 在底行右格
	spans := []CharSpan{
		{Text: "姓", X: 30, X2: 44, Y: 84, FontSize: 12},
		{Text: "名", X: 46, X2: 60, Y: 84, FontSize: 12},
		{Text: "签", X: 150, X2: 164, Y: 66, FontSize: 12},
		{Text: "名", X: 166, X2: 180, Y: 66, FontSize: 12},
	}
	g := Build(segs, spans, 1.5)
	if g.NR != 2 || g.NC != 2 {
		t.Fatalf("期望 2 行 2 列，得到 %d 行 %d 列", g.NR, g.NC)
	}
	// 视觉首行（y 高）在第 0 行
	if g.Rows[0][0] != "姓名" {
		t.Errorf("Rows[0][0] = %q，期望 \"姓名\"", g.Rows[0][0])
	}
	if g.Rows[1][1] != "签名" {
		t.Errorf("Rows[1][1] = %q，期望 \"签名\"", g.Rows[1][1])
	}
	// 其余格子应为空
	if g.Rows[0][1] != "" || g.Rows[1][0] != "" {
		t.Errorf("空格非空: %q / %q", g.Rows[0][1], g.Rows[1][0])
	}
}

// TestBuildRowSpanningText 短竖线只存在于部分行时，
// 不存在该边界的行格子横向贯通（合并语义）
func TestBuildRowSpanningText(t *testing.T) {
	// 横线 y=100, y=70, y=40；竖线 x=10（全贯通）, x=210（全贯通），
	// x=110 仅存在于下行（y 40..70）
	segs := []Seg{
		{X1: 10, Y1: 100, X2: 210, Y2: 100},
		{X1: 10, Y1: 70, X2: 210, Y2: 70},
		{X1: 10, Y1: 40, X2: 210, Y2: 40},
		{X1: 10, Y1: 40, X2: 10, Y2: 100},
		{X1: 210, Y1: 40, X2: 210, Y2: 100},
		{X1: 110, Y1: 40, X2: 110, Y2: 70},
	}
	// 顶行放跨列长文字（横贯 20..200，跨过 x=110 但顶行无此边界）
	var spans []CharSpan
	x := 20.0
	for _, ch := range "科室班组集中培训" {
		spans = append(spans, CharSpan{Text: string(ch), X: x, X2: x + 14, Y: 82, FontSize: 12})
		x += 14
	}
	// 底行左右两格各一个字
	spans = append(spans, CharSpan{Text: "左", X: 50, X2: 64, Y: 52, FontSize: 12})
	spans = append(spans, CharSpan{Text: "右", X: 150, X2: 164, Y: 52, FontSize: 12})

	g := Build(segs, spans, 1.5)
	if g.NR != 2 {
		t.Fatalf("期望 2 行，得到 %d", g.NR)
	}
	// 顶行贯通为 1 格完整文字；底行 2 格；规整化补齐后顶行末位补空
	if len(g.Rows[0]) < 1 || g.Rows[0][0] != "科室班组集中培训" {
		t.Errorf("顶行应贯通为 1 格完整文字，得到 %v", g.Rows[0])
	}
	if g.Rows[0][0] != "" && len(g.Rows[0]) > 1 && g.Rows[0][1] != "" {
		t.Errorf("顶行补位应为空，得到 %v", g.Rows[0])
	}
	if len(g.Rows[1]) != 2 || g.Rows[1][0] != "左" || g.Rows[1][1] != "右" {
		t.Errorf("底行应分为 2 格，得到 %v", g.Rows[1])
	}
}
