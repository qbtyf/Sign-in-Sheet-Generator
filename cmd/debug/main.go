// 调试：输出指定 docx 每行每格文本
package main

import (
	"fmt"
	"os"
	"strings"

	"signsheet/internal/docx"
)

func main() {
	path := os.Args[1]
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		panic(err)
	}
	tables := docx.ParseTables(string(xmlBytes))
	for ti, tbl := range tables {
		fmt.Printf("=== 表格 %d ===\n", ti)
		maxRows := len(tbl.Rows)
		if maxRows > 6 {
			maxRows = 6
		}
		for ri := 0; ri < maxRows; ri++ {
			var texts []string
			for _, c := range tbl.Rows[ri].Cells {
				texts = append(texts, fmt.Sprintf("[%d gs%d]%q", len(texts), c.GridSpan, strings.TrimSpace(c.Text)))
			}
			fmt.Printf("r%d: %s\n", ri, strings.Join(texts, " "))
		}
	}
}
