package server

import (
	"reflect"
	"testing"

	"signsheet/internal/generator"
	"signsheet/internal/tplengine"
)

// TestSanitizeMapping 方案A（2026-09-24 姓名重复修复）：
// 1. 姓名列映射必须被过滤；2. 多姓名格模板全部映射忽略；3. 单姓名列模板其他列保留。
func TestSanitizeMapping(t *testing.T) {
	multi := &tplengine.Analysis{NameCols: []int{0, 2, 4}} // 姓名/签名×3 版式
	single := &tplengine.Analysis{NameCols: []int{0}}      // 单姓名列版式
	// 多姓名格模板：姓名列被默认映射（修复前前端 bug 的真实输入）
	inMulti := []generator.ColMap{
		{ColIdx: 0, Field: "姓名"},
		{ColIdx: 2, Field: "姓名"},
		{ColIdx: 4, Field: "姓名"},
	}
	// 单姓名列模板：姓名列(0) + 其他列(1)
	inSingle := []generator.ColMap{
		{ColIdx: 0, Field: "姓名"},
		{ColIdx: 1, Field: "工号"},
	}

	// 多姓名格模板：全部忽略，强制走"每行多个姓名格"模式
	if got := sanitizeMapping(multi, inMulti); got != nil {
		t.Fatalf("多姓名格模板应忽略全部映射，得到 %v", got)
	}

	// 单姓名列模板：姓名列过滤，其他列保留
	got := sanitizeMapping(single, inSingle)
	want := []generator.ColMap{{ColIdx: 1, Field: "工号"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("单姓名列模板应保留非姓名列映射，得到 %v 期望 %v", got, want)
	}

	// 空入参 / nil 模板
	if sanitizeMapping(nil, inMulti) != nil || sanitizeMapping(single, nil) != nil {
		t.Fatal("空入参应返回 nil")
	}
}
