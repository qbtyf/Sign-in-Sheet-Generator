// e2e 端到端回归测试（V2）：
// 场景A 真实名单（花名册20260827）+ 真实模板（专项培训签到表模板.docx）——每行多姓名格模式
// 场景B 内置模板（占位符替换）
// 场景C 模板库保存
// 场景D 经典表单模板——留空字段识别与写回（标签格＋相邻格）
// 场景E 数据区列映射——名单字段逐人逐列填入（每行一人模式）
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"signsheet/internal/docx"
	"signsheet/internal/generator"
	"signsheet/internal/roster"
	"signsheet/internal/store"
	"signsheet/internal/tplengine"
)

var (
	workDir   = `D:/WorkBuddy培训签到表自动生成`
	rosterXls = workDir + `/花名册20260827.xlsx`
	tmplDocx  = workDir + `/专项培训签到表模板.docx`
	outDir    = workDir + `/signsheet/测试输出`
)

func must(err error, what string) {
	if err != nil {
		fmt.Println("❌ FAIL:", what, "->", err)
		os.Exit(1)
	}
}

func fail(what string) {
	fmt.Println("❌ FAIL:", what)
	os.Exit(1)
}

type sheetRole struct {
	label      string
	headerRow  int
	nameCol    int
	catCol     int
	excludeCol int
	excludeVal string
}

func main() {
	fmt.Println("===== e2e 回归测试（V2）开始 =====")
	os.RemoveAll(outDir)
	must(generator.EnsureDir(outDir), "建输出目录")

	// ---------- 场景A：真实名单 + 真实模板 ----------
	fmt.Println("\n--- 场景A：花名册20260827 + 专项培训签到表模板 ---")
	sheets, err := roster.LoadXlsx(rosterXls)
	must(err, "读取名单")

	var chosen []sheetRole
	for _, sh := range sheets {
		if sh.Label != "本工" && sh.Label != "协力工" {
			continue
		}
		tbl, err := roster.ReadXlsxSheet(rosterXls, sh.Label, sh.HeaderRow)
		must(err, "读工作表 "+sh.Label)
		r := sheetRole{label: sh.Label, headerRow: sh.HeaderRow, nameCol: -1, catCol: -1, excludeCol: -1}
		for i, h := range tbl.Headers {
			if r.nameCol < 0 && strings.Contains(h, "姓名") {
				r.nameCol = i
			}
			if r.catCol < 0 && strings.Contains(h, "班组") {
				r.catCol = i
			}
			if r.excludeCol < 0 && strings.Contains(h, "序号") && sh.Label == "本工" {
				r.excludeCol = i
				r.excludeVal = "外派"
			}
		}
		chosen = append(chosen, r)
		fmt.Printf("  [%s] 表头行=%d 姓名列=%d 分类列=%d 排除=%v 数据 %d 行\n", r.label, r.headerRow, r.nameCol, r.catCol, r.excludeVal, len(tbl.Rows))
	}
	if len(chosen) != 2 {
		fail("期望找到 本工+协力工 两个表")
	}

	merges := [][2]string{{"维修组", "维修班"}}
	groups := buildGroups(chosen, merges)
	total := 0
	for _, g := range groups {
		total += len(g.Persons)
	}
	fmt.Printf("  分组结果: %d 个班组, %d 人\n", len(groups), total)
	if len(groups) != 14 || total != 152 {
		fmt.Println("  期望 14 组 152 人，实际：")
		for _, g := range groups {
			fmt.Printf("    %s: %d\n", g.Name, len(g.Persons))
		}
		fail("分组数量不符")
	}

	a, err := tplengine.AnalyzeDocx(tmplDocx)
	must(err, "解析模板")
	fmt.Printf("  模板: 容量=%d (数据%d行×姓名列%d) 可映射列=%d 信息字段=%d 标题=%q\n",
		a.Capacity, a.DataRows, len(a.NameCols), len(a.DataCols), len(a.TplFields), a.Title)

	var allParts []generator.PartInfo
	for _, g := range groups {
		infos, err := generator.GenGroup(tmplDocx, outDir, a, g, 0, nil, nil)
		must(err, "生成 "+g.Name)
		allParts = append(allParts, infos...)
	}
	fmt.Printf("  生成文件: %d 张\n", len(allParts))

	sum := 0
	for _, p := range allParts {
		n, dup := verifyDocx(filepath.Join(outDir, p.File))
		if n != p.Count {
			fmt.Printf("  ❌ FAIL %s: 实填 %d, 期望 %d\n", p.File, n, p.Count)
			os.Exit(1)
		}
		if dup {
			fmt.Printf("  ❌ FAIL %s: 有重复姓名\n", p.File)
			os.Exit(1)
		}
		sum += n
	}
	fmt.Printf("  ✅ 场景A通过: %d 张文件, 实填合计 %d 人, 无重复\n", len(allParts), sum)
	verifyOrder(filepath.Join(outDir, "专项培训签到表-打磨班.docx"))

	// ---------- 场景B：内置模板 + 占位符填写 ----------
	fmt.Println("\n--- 场景B：内置模板(培训-简约蓝) + 字段填写 ---")
	tplPath := filepath.Join(workDir, "signsheet", "builtins", "培训-简约蓝.docx")
	b, err := tplengine.AnalyzeDocx(tplPath)
	must(err, "解析内置模板")
	fmt.Printf("  模板: 容量=%d 占位符=%v 可映射列=%d\n", b.Capacity, b.Placeholders, len(b.DataCols))
	found := map[string]bool{}
	for _, p := range b.Placeholders {
		found[p] = true
	}
	for _, need := range []string{"培训名称", "讲师", "培训人数", "日期"} {
		if !found[need] {
			fail("缺少占位符 " + need)
		}
	}
	fill := map[string]string{
		"培训名称": "测试培训", "讲师": "王工、李主任", "日期": "2026年9月24日",
		"培训人数": "3", "地点": "会议室", "培训方式": "线下集中",
	}
	gB := generator.Group{Name: "测试班", Persons: []generator.PersonRow{
		{Name: "张三"}, {Name: "李四"}, {Name: "王五"},
	}}
	infos, err := generator.GenGroup(tplPath, outDir, b, gB, 0, fill, nil)
	must(err, "生成内置模板签到表")
	n, dup := verifyDocx(filepath.Join(outDir, infos[0].File))
	if n != 3 || dup {
		fail(fmt.Sprintf("内置模板填名错误 填数=%d 重复=%v", n, dup))
	}
	if checkPlaceholder(filepath.Join(outDir, infos[0].File), "培训名称") {
		fail("占位符 {培训名称} 未被替换")
	}
	fmt.Println("  ✅ 场景B通过: 内置模板 3 人填入 + 字段替换完成")

	// ---------- 场景C：模板库 ----------
	fmt.Println("\n--- 场景C：模板库保存 ---")
	tplBytes, err := os.ReadFile(tplPath)
	must(err, "读模板字节")
	must(store.Save("测试模板", "docx", tplBytes, []byte(`[]`)), "保存模板库")
	entries, _ := store.List()
	if len(entries) == 0 {
		fail("模板库为空")
	}
	p1, _ := store.Paths("测试模板", "docx")
	if _, err := os.Stat(p1); err != nil {
		fail("模板文件不存在 " + p1)
	}
	fmt.Printf("  ✅ 场景C通过: 模板库 %d 条\n", len(entries))

	// ---------- 场景D：经典表单模板（留空字段识别 + 写回） ----------
	fmt.Println("\n--- 场景D：经典表单模板（留空字段识别与写回） ---")
	classicPath := filepath.Join(workDir, "signsheet", "builtins", "培训-经典表单(简约蓝).docx")
	c, err := tplengine.AnalyzeDocx(classicPath)
	must(err, "解析经典表单模板")
	fmt.Printf("  模板: 容量=%d 姓名列=%v 可映射列=%d 信息字段=%d\n", c.Capacity, c.NameCols, len(c.DataCols), len(c.TplFields))

	blankNames := map[string]tplengine.TplField{}
	for _, tf := range c.TplFields {
		if tf.Kind == "blank" {
			blankNames[tf.Name] = tf
			fmt.Printf("    留空字段: %s 默认=%q 多行=%v (r%d,c%d)\n", tf.Name, tf.Default, tf.Multi, tf.Row, tf.Col)
		}
	}
	for _, need := range []string{"培训项目", "培训方式", "培训讲师", "培训人数", "培训时间", "培训内容"} {
		if _, ok := blankNames[need]; !ok {
			fail("经典模板缺少留空字段 " + need)
		}
	}
	if blankNames["培训方式"].Default != "" {
		fail("培训方式应无预填默认值（模板已去固定内容）: " + blankNames["培训方式"].Default)
	}
	if !blankNames["培训内容"].Multi {
		fail("培训内容应识别为多行")
	}
	fillD := map[string]string{
		"培训项目": "有限空间作业培训", "培训方式": "车间集中培训", "培训讲师": "张工；李工",
		"培训人数": "5", "培训时间": "2026-09-24 09:00",
		"培训内容": "有限空间作业安全；气体检测仪使用；应急救援演练",
	}
	gD := generator.Group{Name: "", Persons: []generator.PersonRow{
		{Name: "赵一"}, {Name: "钱二"}, {Name: "孙三"}, {Name: "李四"}, {Name: "周五"},
	}}
	infosD, err := generator.GenGroup(classicPath, outDir, c, gD, 0, fillD, nil)
	must(err, "生成经典表单签到表")
	nD, dupD := verifyDocx(filepath.Join(outDir, infosD[0].File))
	if nD != 5 || dupD {
		fail(fmt.Sprintf("经典表单填名错误 填数=%d 重复=%v", nD, dupD))
	}
	outXML := readDocXML(filepath.Join(outDir, infosD[0].File))
	for _, need := range []string{"有限空间作业培训", "车间集中培训", "张工；李工", "一、有限空间作业安全", "二、气体检测仪使用", "三、应急救援演练"} {
		if !strings.Contains(outXML, need) {
			fail("经典表单未写入: " + need)
		}
	}
	if strings.Contains(outXML, "科室（班组）集中培训") {
		fail("培训方式预填文字未被替换")
	}
	fmt.Println("  ✅ 场景D通过: 6 个留空字段识别 + 写回 + 预填替换 + 多行编号全部正确")

	// ---------- 场景E：数据区列映射（每行一人逐列填入） ----------
	fmt.Println("\n--- 场景E：数据区列映射（名单字段逐人逐列填入） ---")
	var catCol = -1
	for _, dcol := range b.DataCols {
		if dcol.Label == "单位" {
			catCol = dcol.ColIdx
		}
	}
	if catCol < 0 {
		fail("内置模板未识别出 单位 数据列")
	}
	maps := []generator.ColMap{{ColIdx: catCol, Field: "单位"}}
	gE := generator.Group{Name: "", Persons: []generator.PersonRow{
		{Name: "张三", Vals: map[string]string{"单位": "甲班"}},
		{Name: "李四", Vals: map[string]string{"单位": "乙班"}},
		{Name: "王五", Vals: map[string]string{"单位": "丙班"}},
	}}
	infosE, err := generator.GenGroup(tplPath, outDir, b, gE, 0, fill, maps)
	must(err, "生成映射签到表")
	verifyMappedCells(filepath.Join(outDir, infosE[0].File), b, maps, []string{"张三", "李四", "王五"}, []string{"甲班", "乙班", "丙班"})
	fmt.Printf("  ✅ 场景E通过: %d 人逐行填入，姓名列+映射列(单位) 全部正确\n", len(gE.Persons))

	fmt.Println("\n===== 🎉 全部 e2e 测试通过（V2） =====")
}

func readDocXML(path string) string {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	must(err, "读输出文档")
	return string(xmlBytes)
}

func buildGroups(chosen []sheetRole, merges [][2]string) []generator.Group {
	var order []string
	acc := map[string][]generator.PersonRow{}
	teamOf := func(s string) string {
		for _, m := range merges {
			if s == m[0] {
				return m[1]
			}
		}
		return s
	}
	for _, r := range chosen {
		tbl, err := roster.ReadXlsxSheet(rosterXls, r.label, r.headerRow)
		if err != nil {
			continue
		}
		hdr := tbl.Headers
		for _, row := range tbl.Rows {
			name := ""
			if r.nameCol < len(row) {
				name = strings.TrimSpace(row[r.nameCol])
			}
			if name == "" {
				continue
			}
			if r.excludeCol >= 0 && r.excludeCol < len(row) &&
				strings.TrimSpace(row[r.excludeCol]) == r.excludeVal {
				continue
			}
			vals := map[string]string{}
			vals[hdr[r.nameCol]] = name
			team := ""
			if r.catCol >= 0 && r.catCol < len(row) {
				team = strings.TrimSpace(row[r.catCol])
			}
			team = teamOf(team)
			if team == "" {
				team = "未分组"
			}
			if _, ok := acc[team]; !ok {
				order = append(order, team)
			}
			acc[team] = append(acc[team], generator.PersonRow{Name: name, Cat: team, Vals: vals})
		}
	}
	var groups []generator.Group
	for _, t := range order {
		groups = append(groups, generator.Group{Name: t, Persons: acc[t]})
	}
	return groups
}

// verifyDocx 统计输出 docx 姓名区的填充数与重复
func verifyDocx(path string) (int, bool) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		fmt.Println("  verify 读取失败:", err)
		return -1, true
	}
	tables := docx.ParseTables(string(xmlBytes))
	for _, tbl := range tables {
		headerIdx := -1
		var nameCols []int
		for ri, row := range tbl.Rows {
			var nc []int
			for ci, c := range row.Cells {
				if strings.TrimSpace(c.Text) == "姓名" {
					nc = append(nc, ci)
				}
			}
			if len(nc) > 0 {
				headerIdx, nameCols = ri, nc
				break
			}
		}
		if headerIdx < 0 {
			continue
		}
		seen := map[string]bool{}
		count := 0
		for ri := headerIdx + 1; ri < len(tbl.Rows); ri++ {
			rowText := ""
			for _, c := range tbl.Rows[ri].Cells {
				rowText += c.Text
			}
			if strings.Contains(rowText, "培训效果评价") || strings.Contains(rowText, "说明：") {
				break
			}
			for _, nc := range nameCols {
				if nc >= len(tbl.Rows[ri].Cells) {
					continue
				}
				t := strings.TrimSpace(tbl.Rows[ri].Cells[nc].Text)
				if t == "" {
					continue
				}
				count++
				if seen[t] {
					return count, true
				}
				seen[t] = true
			}
		}
		return count, false
	}
	return -1, true
}

// verifyOrder 检查名单顺序：前6位应为打磨班本工，之后为协力工
func verifyOrder(path string) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	must(err, "读打磨班")
	tables := docx.ParseTables(string(xmlBytes))
	tbl := tables[0]
	headerIdx, nameCols := -1, []int{}
	for ri, row := range tbl.Rows {
		var nc []int
		for ci, c := range row.Cells {
			if strings.TrimSpace(c.Text) == "姓名" {
				nc = append(nc, ci)
			}
		}
		if len(nc) > 0 {
			headerIdx, nameCols = ri, nc
			break
		}
	}
	if headerIdx < 0 {
		fail("打磨班未找到姓名表头")
	}
	var got []string
	for ri := headerIdx + 1; ri < len(tbl.Rows); ri++ {
		rowText := ""
		for _, c := range tbl.Rows[ri].Cells {
			rowText += c.Text
		}
		if strings.Contains(rowText, "培训效果评价") {
			break
		}
		for _, nc := range nameCols {
			t := strings.TrimSpace(tbl.Rows[ri].Cells[nc].Text)
			if t != "" {
				got = append(got, t)
			}
		}
	}
	benSet := map[string]bool{}
	tbl2, _ := roster.ReadXlsxSheet(rosterXls, "本工", 2)
	for _, row := range tbl2.Rows {
		if len(row) > 11 && strings.TrimSpace(row[11]) == "打磨班" {
			benSet[strings.TrimSpace(row[2])] = true
		}
	}
	benCount := 0
	for _, name := range got {
		if benSet[name] {
			benCount++
		}
	}
	if benCount != 6 || len(got) != 19 {
		fail(fmt.Sprintf("打磨班 got=%d人 本工=%d人 (期望19人/本工6人)", len(got), benCount))
	}
	for i, name := range got {
		if i < 6 && !benSet[name] {
			fail(fmt.Sprintf("打磨班第 %d 位 %s 不在本工名单", i+1, name))
		}
		if i >= 6 && benSet[name] {
			fail(fmt.Sprintf("打磨班第 %d 位 %s 是本工却出现在协力工区", i+1, name))
		}
	}
	fmt.Printf("  ✅ 顺序验证: 打磨班 %d 人，前 6 位均为本工、后 13 位均为协力工\n", len(got))
}

// checkPlaceholder 检查文档中是否残留指定占位符
func checkPlaceholder(path, name string) bool {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	if err != nil {
		return true
	}
	return strings.Contains(string(xmlBytes), "{"+name+"}")
}

// verifyMappedCells 验证映射模式下：第 i 人 → 第 i 数据行，姓名列与映射列的值
func verifyMappedCells(path string, a *tplengine.Analysis, maps []generator.ColMap, names, catVals []string) {
	xmlBytes, err := docx.ReadFile(path, "word/document.xml")
	must(err, "读映射输出")
	tables := docx.ParseTables(string(xmlBytes))
	if len(tables) == 0 {
		fail("映射输出无表格")
	}
	// 内置模板：签到表是第二张表
	var tbl *docx.Table
	for i := range tables {
		for _, row := range tables[i].Rows {
			for _, c := range row.Cells {
				if strings.TrimSpace(c.Text) == "姓名" {
					tbl = &tables[i]
					break
				}
			}
			if tbl != nil {
				break
			}
		}
		if tbl != nil {
			break
		}
	}
	if tbl == nil {
		fail("映射输出未找到签到表")
	}
	for i, name := range names {
		ri := a.DataStartIdx + i
		if ri >= len(tbl.Rows) {
			fail("数据行越界")
		}
		gotName := strings.TrimSpace(tbl.Rows[ri].Cells[a.NameCols[0]].Text)
		if gotName != name {
			fail(fmt.Sprintf("第%d行姓名=%q 期望=%q", ri, gotName, name))
		}
		for _, m := range maps {
			gotVal := strings.TrimSpace(tbl.Rows[ri].Cells[m.ColIdx].Text)
			if gotVal != catVals[i] {
				fail(fmt.Sprintf("第%d行映射列%d=%q 期望=%q", ri, m.ColIdx, gotVal, catVals[i]))
			}
		}
	}
}
