package tabfill

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

var demoHeaders = []string{"姓名", "工号", "班组"}

func demoGroups() []SplitGroup {
	return []SplitGroup{
		{Name: "一班", Rows: [][]string{{"张三", "A001", "一班"}, {"王五", "A003", "一班"}}},
		{Name: "二班", Rows: [][]string{{"李四", "A002", "二班"}}},
		{Name: "空值", Rows: [][]string{{"赵六", "A004", ""}}},
		// 长名字 + 非法字符，验证 Sheet 名清理
		{Name: `超长班组名称测试超长班组名称测试超长班组名称测试超长班组/名称`, Rows: [][]string{{"钱七", "A005", "x"}}},
	}
}

func TestWriteXlsxMultiSheet(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "multi.xlsx")
	if err := WriteXlsxMultiSheet(demoHeaders, demoGroups(), dst); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) != 4 {
		t.Fatalf("应有 4 个工作表，实际 %d: %v", len(sheets), sheets)
	}
	// 逐 Sheet 校验：首行表头一致，行数 = 组行数 + 表头
	for i, g := range demoGroups() {
		got := sheets[i]
		rows, err := f.GetRows(got)
		if err != nil {
			t.Fatal(err)
		}
		want := len(g.Rows) + 1
		if len(rows) != want {
			t.Errorf("Sheet %q 应有 %d 行，实际 %d", got, want, len(rows))
		}
		if len(rows) > 0 && (rows[0][0] != "姓名" || rows[0][2] != "班组") {
			t.Errorf("Sheet %q 表头错误: %v", got, rows[0])
		}
	}
	// Sheet 名不应含非法字符且 ≤31 字符
	for _, s := range sheets {
		if strings.ContainsAny(s, `:/\?*[]`) {
			t.Errorf("Sheet 名含非法字符: %q", s)
		}
		if len([]rune(s)) > 31 {
			t.Errorf("Sheet 名超 31 字符: %q", s)
		}
	}
}

func TestSanitizeSheetNameDedup(t *testing.T) {
	used := map[string]bool{}
	a := SanitizeSheetName("班组", 0, used)
	used[a] = true
	b := SanitizeSheetName("班组", 1, used)
	used[b] = true
	c := SanitizeSheetName("班组", 2, used)
	if a == b || b == c || a == c {
		t.Errorf("重名未防重: %q %q %q", a, b, c)
	}
	if SanitizeSheetName("", 0, map[string]bool{}) == "" {
		t.Error("空名应有兜底")
	}
}

func TestAppendMergedSheet(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "原表.xlsx")
	// 原文件：两张已有工作表
	if err := WriteXlsxMultiSheet(demoHeaders,
		[]SplitGroup{{Name: "一月", Rows: [][]string{{"张三", "A001", "一"}}}, {Name: "二月", Rows: [][]string{{"李四", "A002", "二"}}}},
		src); err != nil {
		t.Fatal(err)
	}
	// 追加"合并总表"（含类型还原：长数字保持文本）
	headers4 := append(append([]string{}, demoHeaders...), "手机号/日期")
	rows := [][]string{{"张三", "A001", "一", "13800138000"}, {"李四", "A002", "二", "2026-09-27"}}
	dst := filepath.Join(dir, "原表.xlsx") // 同名另存 = 写回
	name, err := AppendMergedSheet(src, dst, "合并总表", headers4, rows)
	if err != nil {
		t.Fatal(err)
	}
	if name != "合并总表" {
		t.Fatalf("Sheet 名应为首选名，实际 %q", name)
	}
	f, err := excelize.OpenFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) != 3 || sheets[0] != "一月" || sheets[1] != "二月" || sheets[2] != "合并总表" {
		t.Fatalf("Sheet 列表错误: %v", sheets)
	}
	got, _ := f.GetRows("一月")
	if len(got) != 2 { // 表头 + 1 行
		t.Errorf("原 Sheet「一月」应原样保留 2 行，实际 %d", len(got))
	}
	got, _ = f.GetRows("合并总表")
	if len(got) != 3 || got[1][3] != "13800138000" {
		t.Errorf("合并总表内容错误: %v", got)
	}
	// 再追加同名 Sheet 应自动改叫 合并总表2
	name2, err := AppendMergedSheet(dst, dst, "合并总表", demoHeaders, rows)
	if err != nil || name2 != "合并总表2" {
		t.Fatalf("重名应自动加序号，实际 %q err=%v", name2, err)
	}
}

func TestAppendDocxTable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "原表.docx")
	if err := WriteDocxTable(demoHeaders, [][]string{{"张三", "A001", "一班"}}, "原始表格", src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "原表.docx")
	if err := AppendDocxTable(src, dst, "合并总表", demoHeaders, [][]string{{"李四", "A002", "二班"}}); err != nil {
		t.Fatal(err)
	}
	// 验证 docx 里有两个表格，且能读出追加的内容（解压读 word/document.xml）
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var docXML string
	for _, fe := range zr.File {
		if fe.Name == "word/document.xml" {
			rc, _ := fe.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			docXML = string(b)
		}
	}
	if docXML == "" {
		t.Fatal("找不到 word/document.xml")
	}
	s := docXML
	if n := strings.Count(s, "<w:tbl>"); n != 2 {
		t.Fatalf("docx 应有 2 张表格，实际 %d", n)
	}
	if !strings.Contains(s, "李四") || !strings.Contains(s, "合并总表") {
		t.Error("追加的表格内容缺失")
	}
	if !strings.Contains(s, "张三") {
		t.Error("原表格内容丢失")
	}
	// 追加内容必须位于 sectPr 之前
	if i := strings.LastIndex(s, "<w:sectPr"); i >= 0 {
		if !strings.Contains(s[:i], "李四") {
			t.Error("追加表格应位于 sectPr 之前")
		}
	}
}
