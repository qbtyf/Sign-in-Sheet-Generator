package tabfill

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSplitByValue(t *testing.T) {
	headers := []string{"姓名", "工号", "班组", "部门", "工种"}
	rows := [][]string{
		{"张三", "A001", "一班", "生产部", "焊工"},
		{"李四", "A002", "二班", "生产部", "电工"},
		{"王五", "A003", "一班", "设备部", "钳工"},
		{"赵六", "A004", "三班", "设备部", "焊工"},
		{"钱七", "A005", "二班", "生产部", "电工"},
		{"孙八", "A006", "三班", "安环部", "叉车工"},
	}
	groups, err := Split(SplitSpec{Headers: headers, Rows: rows, Mode: "byValue", Field: 2})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(groups, "", " ")
	fmt.Println(string(b))
	// 断言分组正确：每组的每行 rec[2] 必须等于组名
	for _, g := range groups {
		for _, r := range g.Rows {
			if r[2] != g.Name {
				t.Errorf("分组错误: 组 %q 收到了 %q 的行 %v", g.Name, r[2], r)
			}
		}
	}
	want := map[string]int{"一班": 2, "二班": 2, "三班": 2}
	if len(groups) != len(want) {
		t.Fatalf("组数错误: got %d want %d", len(groups), len(want))
	}
	for _, g := range groups {
		if len(g.Rows) != want[g.Name] {
			t.Errorf("组 %q 行数错误: got %d want %d", g.Name, len(g.Rows), want[g.Name])
		}
	}
}
