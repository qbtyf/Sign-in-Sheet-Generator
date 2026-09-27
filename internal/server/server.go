// Package server 提供本地 Web 服务：页面、上传解析、判重、模板、生成、下载。
package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"signsheet/internal/docx"
	"signsheet/internal/generator"
	"signsheet/internal/preview"
	"signsheet/internal/roster"
	"signsheet/internal/store"
	"signsheet/internal/tplengine"
)

// ---------------------------------------------------------------------------
// 会话
// ---------------------------------------------------------------------------

type Role struct {
	NameCol    int    `json:"nameCol"`    // 姓名列（取人员姓名）
	FillCols   []int  `json:"fillCols"`   // 自动填入签到表的名单列
	CatCol     int    `json:"catCol"`     // 分类基础列（-1 = 不按分类拆分）
	ExcludeCol int    `json:"excludeCol"` // -1 表示不排除；某行该列=ExcludeVal 时跳过
	ExcludeVal string `json:"excludeVal"`
}

type FieldDesign struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`              // placeholder | blank
	Mode        string   `json:"mode"`              // fill | auto | roster | skip
	Control     string   `json:"control"`           // text|list|persons|select|location|date|time|auto|longtext
	Options     []string `json:"options,omitempty"` // select 控件的候选项
	Default     string   `json:"default,omitempty"` // 模板预填文字
	Multi       bool     `json:"multi,omitempty"`   // 预填为多行
	RosterField string   `json:"rosterField,omitempty"` // mode=roster：来源名单字段
}

type Session struct {
	mu       sync.Mutex
	ID       string
	Dir      string
	NoRoster bool

	RosterPath string
	RosterKind string // xlsx | docx
	Sheets     []roster.SheetInfo
	Chosen     []string
	HeaderRows map[string]int
	Tables     map[string]*roster.Table
	Role       map[string]Role
	MergeRules [][2]string
	Removed    map[string]map[int]bool // sheetKey -> 行号(0基,数据行) -> 删除

	TplPath string
	Tpl     *tplengine.Analysis
	Design  []FieldDesign
	Mapping []generator.ColMap // 数据区列 ← 名单字段

	OutDir   string
	Outputs  []generator.PartInfo
}

type sessionStore struct {
	mu sync.Mutex
	m  map[string]*Session
}

var sessions = &sessionStore{m: map[string]*Session{}}

func (s *sessionStore) get(w http.ResponseWriter, r *http.Request) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := r.Cookie("sid")
	if err == nil {
		if ss, ok := s.m[c.Value]; ok {
			return ss
		}
	}
	id := fmt.Sprintf("s%d", time.Now().UnixNano())
	dir, _ := os.MkdirTemp("", "signsheet-"+id)
	ss := &Session{
		ID: id, Dir: dir,
		HeaderRows: map[string]int{},
		Role:       map[string]Role{},
		Removed:    map[string]map[int]bool{},
	}
	s.m[id] = ss
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: id, Path: "/"})
	return ss
}

// ---------------------------------------------------------------------------
// 内置模板
// ---------------------------------------------------------------------------

var builtinFS fs.FS

// SetBuiltinFS 注入内嵌模板文件系统（根目录即 builtins/）
func SetBuiltinFS(f fs.FS) { builtinFS = f }

// ListBuiltins 列出内置模板
func ListBuiltins() []string {
	if builtinFS == nil {
		return nil
	}
	var out []string
	fs.WalkDir(builtinFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".docx") {
			out = append(out, strings.TrimPrefix(p, "builtins/"))
		}
		return nil
	})
	return out
}

// ---------------------------------------------------------------------------
// 字段控件推断
// ---------------------------------------------------------------------------

func suggestField(name string) FieldDesign {
	d := FieldDesign{Name: name, Mode: "fill", Control: "text"}
	c := func(keys ...string) bool {
		for _, k := range keys {
			if strings.Contains(name, k) {
				return true
			}
		}
		return false
	}
	switch {
	case c("人数"):
		d.Mode, d.Control = "auto", "auto"
	case c("内容", "议题", "议程", "安排", "事项"):
		d.Control = "list"
	case c("讲师", "主持", "主讲", "出席", "负责人"):
		d.Control = "persons"
	case c("方式", "形式"):
		d.Control = "select"
		d.Options = []string{"线下集中", "线上进行", "线上线下结合"}
	case c("地点", "场所"):
		d.Control = "location"
	case c("日期"):
		d.Control = "date"
	case c("时间"):
		d.Control = "time"
	case c("单位", "部门"):
		d.Control = "longtext"
	}
	return d
}

func fieldsFromAnalysis(a *tplengine.Analysis) []FieldDesign {
	out := []FieldDesign{}
	for _, tf := range a.TplFields {
		d := suggestField(tf.Name)
		d.Kind = tf.Kind
		d.Default = tf.Default
		d.Multi = tf.Multi
		if tf.Multi {
			d.Control = "list"
		}
		if tf.Kind == "blank" && tf.Default != "" && d.Control == "select" {
			d.Options = append([]string{tf.Default}, d.Options...)
		}
		out = append(out, d)
	}
	return out
}

// ---------------------------------------------------------------------------
// 判重
// ---------------------------------------------------------------------------

type dupItem struct {
	Sheet  string   `json:"sheet"`
	SheetL string   `json:"sheetLabel"`
	Row    int      `json:"row"`
	Cells  []string `json:"cells"`
	Remove bool     `json:"remove"`
}

type dupGroup struct {
	Key   string    `json:"key"`
	Items []dupItem `json:"items"`
}

// dedupKeyColsFromHeaders 按表头自动识别判重键列：姓名+班组(部门/单位)+身份证+电话/手机
func dedupKeyColsFromHeaders(headers []string) []int {
	var cols []int
	seen := map[int]bool{}
	pick := func(match func(string) bool) {
		for i, h := range headers {
			if !seen[i] && match(h) {
				cols = append(cols, i)
				seen[i] = true
				return
			}
		}
	}
	pick(func(h string) bool { return strings.Contains(h, "姓名") })
	pick(func(h string) bool { return strings.Contains(h, "班组") || strings.Contains(h, "部门") || strings.Contains(h, "单位") })
	pick(func(h string) bool { return strings.Contains(h, "身份证") })
	pick(func(h string) bool { return strings.Contains(h, "电话") || strings.Contains(h, "手机") })
	if len(cols) == 0 { // 兜底：无任何匹配列时按整行比对
		for i := range headers {
			cols = append(cols, i)
		}
	}
	return cols
}

func computeDupGroups(ss *Session) []dupGroup {
	type keyInfo struct{ cols []int }
	keys := map[string]keyInfo{}
	var order []string
	acc := map[string][]dupItem{}

	for _, sk := range ss.Chosen {
		tbl := ss.Tables[sk]
		if tbl == nil {
			continue
		}
		cols := dedupKeyColsFromHeaders(tbl.Headers)
		keys[sk] = keyInfo{cols}
		for ri, row := range tbl.Rows {
			if ss.Removed[sk][ri] {
				continue
			}
			var parts []string
			for _, c := range cols {
				if c < len(row) {
					parts = append(parts, strings.TrimSpace(row[c]))
				}
			}
			k := strings.Join(parts, "\x1f")
			if _, ok := acc[k]; !ok {
				order = append(order, k)
			}
			acc[k] = append(acc[k], dupItem{
				Sheet: sk, Row: ri,
				Cells: padCells(row, 8),
			})
		}
	}

	var groups []dupGroup
	for _, k := range order {
		items := acc[k]
		if len(items) < 2 {
			continue
		}
		for i := range items {
			items[i].Remove = i > 0 // 默认保留首条
			for _, sk := range ss.Chosen {
				if sk == items[i].Sheet {
					items[i].SheetL = sheetLabel(sk)
				}
			}
		}
		groups = append(groups, dupGroup{Key: k, Items: items})
	}
	return groups
}

func padCells(row []string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		v := ""
		if i < len(row) {
			v = row[i]
		}
		out = append(out, v)
	}
	return out
}

func sheetLabel(key string) string {
	if i := strings.Index(key, ":"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// ---------------------------------------------------------------------------
// 分组与生成
// ---------------------------------------------------------------------------

type nameRow struct {
	name, team string
}

func buildGroups(ss *Session) []generator.Group {
	// 有序分组
	var order []string
	acc := map[string][]generator.PersonRow{}
	for _, sk := range ss.Chosen {
		tbl := ss.Tables[sk]
		if tbl == nil {
			continue
		}
		role := ss.Role[sk]
		hdr := tbl.Headers
		for ri, row := range tbl.Rows {
			if ss.Removed[sk][ri] {
				continue
			}
			if role.ExcludeCol >= 0 && role.ExcludeCol < len(row) &&
				strings.TrimSpace(row[role.ExcludeCol]) == role.ExcludeVal {
				continue // 排除规则命中（如 外派员工）
			}
			name := ""
			if role.NameCol < len(row) {
				name = strings.TrimSpace(row[role.NameCol])
			}
			if name == "" {
				continue
			}
			// 收集自动填入字段值（键=表头字段名）
			vals := map[string]string{}
			for _, fc := range role.FillCols {
				if fc < len(hdr) && fc < len(row) {
					vals[hdr[fc]] = strings.TrimSpace(row[fc])
				}
			}
			cat := ""
			if role.CatCol >= 0 {
				cat = ""
				if role.CatCol < len(row) {
					cat = strings.TrimSpace(row[role.CatCol])
				}
				cat = applyMerges(cat, ss.MergeRules)
				if cat == "" {
					cat = "未分组"
				}
			}
			// CatCol<0：不按分类拆分 → 全部归入同一个无分类组（文件名不带分类后缀）
			if _, ok := acc[cat]; !ok {
				order = append(order, cat)
			}
			acc[cat] = append(acc[cat], generator.PersonRow{Name: name, Cat: cat, Vals: vals})
		}
	}
	var groups []generator.Group
	for _, t := range order {
		groups = append(groups, generator.Group{Name: t, Persons: acc[t]})
	}
	return groups
}

func applyMerges(team string, rules [][2]string) string {
	for _, r := range rules {
		if team == r[0] {
			return r[1]
		}
	}
	return team
}

func computeFill(base map[string]string, design []FieldDesign, total int, first generator.PersonRow) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for _, d := range design {
		switch d.Mode {
		case "auto":
			if strings.Contains(d.Name, "人数") {
				out[d.Name] = fmt.Sprintf("%d", total)
			} else if strings.Contains(d.Name, "日期") {
				out[d.Name] = time.Now().Format("2006年1月2日")
			}
		case "roster":
			// 由名单提供：取该组第一条记录的对应字段值
			if first.Vals != nil {
				if v := strings.TrimSpace(first.Vals[d.RosterField]); v != "" {
					out[d.Name] = v
				}
			}
		}
	}
	// 空值不填（没有则不填）
	for k, v := range out {
		if strings.TrimSpace(v) == "" {
			delete(out, k)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// HTTP 工具
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	ec, detail := matchErr(msg)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": ec, "detail": detail})
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

var reFilenameUnsafe = regexp.MustCompile(`[\\/:*?"<>|]`)

func safeFile(s string) string {
	return filepath.Base(reFilenameUnsafe.ReplaceAllString(s, "_"))
}

// ---------------------------------------------------------------------------
// Handler：名单
// ---------------------------------------------------------------------------

func handleUploadRoster(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "未收到文件")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	var kind string
	switch ext {
	case ".xlsx":
		kind = "xlsx"
	case ".docx":
		kind = "docx"
	case ".doc", ".xls":
		writeErr(w, 400, "暂不支持旧版 .doc/.xls 格式，请先用 Office/WPS 另存为 .docx/.xlsx 再上传")
		return
	default:
		writeErr(w, 400, "不支持的文件格式："+ext+"（支持 .xlsx 与 .docx）")
		return
	}
	path := filepath.Join(ss.Dir, "名单"+kind)
	if err := saveMultipartFile(file, path); err != nil {
		writeErr(w, 500, "保存文件失败: "+err.Error())
		return
	}
	var sheets []roster.SheetInfo
	if kind == "xlsx" {
		sheets, err = roster.LoadXlsx(path)
	} else {
		sheets, err = roster.LoadDocx(path)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ss.RosterPath, ss.RosterKind, ss.Sheets = path, kind, sheets
	ss.Chosen, ss.Tables, ss.Removed = nil, map[string]*roster.Table{}, map[string]map[int]bool{}
	writeJSON(w, map[string]any{"kind": kind, "sheets": sheets})
}

func saveMultipartFile(f multipart.File, path string) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, f)
	return err
}

func handleRosterConfig(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Sheets []struct {
			Key       string `json:"key"`
			HeaderRow int    `json:"headerRow"`
		} `json:"sheets"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.Chosen = nil
	ss.Tables = map[string]*roster.Table{}
	ss.Removed = map[string]map[int]bool{}
	type colInfo struct {
		Key     string   `json:"key"`
		Label   string   `json:"label"`
		Headers []string `json:"headers"`
		Count   int      `json:"count"`
	}
	var cols []colInfo
	for _, sc := range req.Sheets {
		var (
			tbl *roster.Table
			err error
		)
		if ss.RosterKind == "xlsx" {
			tbl, err = roster.ReadXlsxSheet(ss.RosterPath, sheetLabel(sc.Key), sc.HeaderRow)
		} else {
			idx := 0
			fmt.Sscanf(sheetLabel(sc.Key), "%d", &idx)
			tbl, err = roster.ReadDocxSheet(ss.RosterPath, idx, sc.HeaderRow)
		}
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		ss.Chosen = append(ss.Chosen, sc.Key)
		ss.HeaderRows[sc.Key] = sc.HeaderRow
		ss.Tables[sc.Key] = tbl
		cols = append(cols, colInfo{Key: sc.Key, Label: sheetLabel(sc.Key), Headers: tbl.Headers, Count: len(tbl.Rows)})
	}
	// 默认角色：姓名列=表头含"姓名"的首列并自动填入；分类基础=表头含"班组/部门/单位"的首列
	ss.Role = map[string]Role{}
	for _, c := range cols {
		role := Role{NameCol: -1, CatCol: -1, ExcludeCol: -1}
		for i, h := range c.Headers {
			if role.NameCol < 0 && strings.Contains(h, "姓名") {
				role.NameCol = i
			}
			if role.CatCol < 0 && (strings.Contains(h, "班组") || strings.Contains(h, "部门") || strings.Contains(h, "单位")) {
				role.CatCol = i
			}
		}
		if role.NameCol >= 0 {
			role.FillCols = []int{role.NameCol}
		}
		ss.Role[c.Key] = role
	}
	writeJSON(w, map[string]any{"columns": cols, "roles": ss.Role})
}

func handleRosterRoles(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Roles map[string]Role `json:"roles"`
		Merge [][2]string     `json:"merges"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for k, role := range req.Roles {
		ss.Role[k] = role
	}
	ss.MergeRules = req.Merge
	writeJSON(w, map[string]any{"ok": true})
}

// sanitizeMapping 过滤列映射（方案A，2026-09-24）：
//  1. 姓名列绝不参与映射——姓名由生成器自动依次填入不同人员，
//     若映射到姓名字段会把同一个人的名字复制到一行内多个姓名格；
//  2. 每行多个姓名格的模板（姓名/签名×N 版式），其他列映射也一并忽略，
//     强制走"每行多个姓名格"模式（签名等列留空手写）。
func sanitizeMapping(a *tplengine.Analysis, maps []generator.ColMap) []generator.ColMap {
	if a == nil || len(maps) == 0 {
		return nil
	}
	if len(a.NameCols) > 1 {
		return nil
	}
	nameCol := map[int]bool{}
	for _, nc := range a.NameCols {
		nameCol[nc] = true
	}
	var out []generator.ColMap
	for _, m := range maps {
		if !nameCol[m.ColIdx] {
			out = append(out, m)
		}
	}
	return out
}

// handleMapping 保存第④步"数据区列 ← 名单字段"映射
func handleMapping(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Mapping []generator.ColMap `json:"mapping"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	ss.Mapping = sanitizeMapping(ss.Tpl, req.Mapping)
	ss.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleDedupPrepare(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	groups := computeDupGroups(ss)
	writeJSON(w, map[string]any{"groups": groups})
}

func handleDedupApply(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Remove []dupItem `json:"remove"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.Removed = map[string]map[int]bool{}
	for _, it := range req.Remove {
		if ss.Removed[it.Sheet] == nil {
			ss.Removed[it.Sheet] = map[int]bool{}
		}
		ss.Removed[it.Sheet][it.Row] = true
	}
	type cnt struct {
		Sheet string `json:"sheet"`
		Count int    `json:"count"`
	}
	var counts []cnt
	total := 0
	for _, sk := range ss.Chosen {
		tbl := ss.Tables[sk]
		remain := 0
		if tbl != nil {
			for ri := range tbl.Rows {
				if !ss.Removed[sk][ri] {
					remain++
				}
			}
		}
		counts = append(counts, cnt{Sheet: sheetLabel(sk), Count: remain})
		total += remain
	}
	writeJSON(w, map[string]any{"counts": counts, "total": total})
}

// ---------------------------------------------------------------------------
// Handler：模板
// ---------------------------------------------------------------------------

func analyzeTemplate(path, kind string) (*tplengine.Analysis, []FieldDesign, error) {
	var (
		a   *tplengine.Analysis
		err error
	)
	if kind == "xlsx" {
		a, err = tplengine.AnalyzeXlsx(path)
	} else {
		a, err = tplengine.AnalyzeDocx(path)
	}
	if err != nil {
		return nil, nil, err
	}
	return a, fieldsFromAnalysis(a), nil
}

func handleTemplateUpload(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "未收到文件")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(hdr.Filename))
	var kind string
	switch ext {
	case ".xlsx":
		kind = "xlsx"
	case ".docx":
		kind = "docx"
	case ".doc", ".xls":
		writeErr(w, 400, "暂不支持旧版 .doc/.xls，请先另存为 .docx/.xlsx")
		return
	default:
		writeErr(w, 400, "不支持的模板格式："+ext)
		return
	}
	path := filepath.Join(ss.Dir, "模板."+kind)
	if err := saveMultipartFile(file, path); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a, fields, err := analyzeTemplate(path, kind)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ss.TplPath, ss.Tpl, ss.Design, ss.Outputs = path, a, fields, nil
	writeJSON(w, map[string]any{"analysis": a, "fields": fields})
}

func handleTemplateBuiltin(w http.ResponseWriter, r *http.Request) {	ss := sessions.get(w, r)
	var req struct {
		ID string `json:"id"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	data, err := fs.ReadFile(builtinFS, "builtins/"+req.ID)
	if err != nil {
		writeErr(w, 404, "内置模板不存在")
		return
	}
	path := filepath.Join(ss.Dir, "模板.docx")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a, fields, err := analyzeTemplate(path, "docx")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ss.TplPath, ss.Tpl, ss.Design, ss.Outputs = path, a, fields, nil
	writeJSON(w, map[string]any{"analysis": a, "fields": fields})
}

// reTplPlaceholder 模板占位符：{培训名称}、{副标题} 等（预览时替换为空白，模拟最终输出留白状态）
var reTplPlaceholder = regexp.MustCompile(`\{[^<>{}]*\}`)

// handleTemplatePreview 模板预览：?id=<内置模板文件名> 预览内置模板；无 id 则预览当前已选模板
// 预览为"最终输出状态"模拟：占位符清空、数据区保持空白，完整显示信息表＋签到网格
func handleTemplatePreview(w http.ResponseWriter, r *http.Request) {
	var (
		htmlStr string
		err     error
	)
	if id := r.URL.Query().Get("id"); id != "" {
		if strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
			writeErr(w, 400, "非法模板名")
			return
		}
		data, err := fs.ReadFile(builtinFS, "builtins/"+id)
		if err != nil {
			writeErr(w, 404, "内置模板不存在")
			return
		}
		b, err := docx.ReadBytes(data, "word/document.xml")
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		htmlStr, err = preview.DocxFromXML(reTplPlaceholder.ReplaceAllString(string(b), ""))
	} else {
		ss := sessions.get(w, r)
		ss.mu.Lock()
		path := ss.TplPath
		ss.mu.Unlock()
		if path == "" {
			writeErr(w, 400, "尚未选择模板")
			return
		}
		if strings.HasSuffix(path, ".xlsx") {
			htmlStr, err = preview.Xlsx(path)
		} else {
			b, e := docx.ReadFile(path, "word/document.xml")
			if e != nil {
				writeErr(w, 500, e.Error())
				return
			}
			htmlStr, err = preview.DocxFromXML(reTplPlaceholder.ReplaceAllString(string(b), ""))
		}
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, htmlStr)
}

func handleTemplateLibraryList(w http.ResponseWriter, r *http.Request) {	entries, _ := store.List()
	builtins := ListBuiltins()
	writeJSON(w, map[string]any{"library": entries, "builtins": builtins})
}

func handleTemplateLibraryLoad(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	tplPath, _ := store.Paths(req.Name, req.Kind)
	if _, err := os.Stat(tplPath); err != nil {
		writeErr(w, 404, "模板文件不存在")
		return
	}
	a, fields, err := analyzeTemplate(tplPath, req.Kind)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ss.TplPath, ss.Tpl, ss.Outputs = tplPath, a, nil
	if design, err := store.LoadDesign(req.Name, req.Kind); err == nil {
		json.Unmarshal(design, &fields)
	}
	ss.Design = fields
	writeJSON(w, map[string]any{"analysis": a, "fields": fields})
}

func handleTemplateDesign(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Design []FieldDesign `json:"design"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	ss.Design = req.Design
	ss.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleTemplateSave(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, 400, "请填写模板名称")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	data, err := os.ReadFile(ss.TplPath)
	if err != nil {
		writeErr(w, 400, "当前没有已加载的模板")
		return
	}
	designJSON, _ := json.Marshal(ss.Design)
	if err := store.Save(req.Name, ss.Tpl.Kind, data, designJSON); err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "dir": store.LibDir()})
}

// ---------------------------------------------------------------------------
// Handler：生成与下载
// ---------------------------------------------------------------------------

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Fill     map[string]string `json:"fill"`
		Capacity int               `json:"capacity"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.Tpl == nil {
		writeErr(w, 400, "请先选择模板")
		return
	}

	var groups []generator.Group
	hasCat := false
	if !ss.NoRoster {
		groups = buildGroups(ss)
		if len(groups) == 0 {
			writeErr(w, 400, "名单为空：请检查名单列映射或去重设置")
			return
		}
		for _, g := range groups {
			if g.Name != "" {
				hasCat = true
			}
		}
	} else {
		groups = []generator.Group{{Name: "", Persons: nil}}
	}

	// 无名单模式下人数字段不自动填（留空手写）
	design := ss.Design
	if ss.NoRoster {
		var d2 []FieldDesign
		for _, d := range design {
			if !strings.Contains(d.Name, "人数") {
				d2 = append(d2, d)
			}
		}
		design = d2
	}
	maps := sanitizeMapping(ss.Tpl, ss.Mapping) // 双保险：生成前再过滤一次（防旧会话残留）
	if ss.NoRoster {
		maps = nil
	}

	outDir := filepath.Join(ss.Dir, "输出")
	os.RemoveAll(outDir)
	if err := generator.EnsureDir(outDir); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	var outputs []generator.PartInfo
	for _, g := range groups {
		first := generator.PersonRow{}
		if len(g.Persons) > 0 {
			first = g.Persons[0]
		}
		fill := computeFill(req.Fill, design, len(g.Persons), first)
		infos, err := generator.GenGroup(ss.TplPath, outDir, ss.Tpl, g, req.Capacity, fill, maps)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		outputs = append(outputs, infos...)
	}
	ss.OutDir, ss.Outputs = outDir, outputs

	total := 0
	for _, o := range outputs {
		total += o.Count
	}
	writeJSON(w, map[string]any{
		"outputs": outputs,
		"groups":  len(groups),
		"total":   total,
		"hasCat":  hasCat,
	})
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	name := safeFile(r.URL.Query().Get("file"))
	path := filepath.Join(ss.OutDir, name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, 404, "文件不存在")
		return
	}
	var (
		htmlStr string
		err     error
	)
	if strings.HasSuffix(name, ".xlsx") {
		htmlStr, err = preview.Xlsx(path)
	} else {
		htmlStr, err = preview.Docx(path)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, htmlStr)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	name := safeFile(r.URL.Query().Get("file"))
	path := filepath.Join(ss.OutDir, name)
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 404, "文件不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	io.Copy(w, f)
}

func handleDownloadAll(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	entries, err := os.ReadDir(ss.OutDir)
	if err != nil || len(entries) == 0 {
		writeErr(w, 404, "暂无生成文件")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="签到表.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ss.OutDir, e.Name()))
		if err != nil {
			continue
		}
		fw, err := zw.Create(e.Name())
		if err != nil {
			continue
		}
		fw.Write(data)
	}
}

// handleDownloadGroup 打包下载某一分类的全部文件（⑧分类输出）
func handleDownloadGroup(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	group := r.URL.Query().Get("group")
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var files []string
	for _, o := range ss.Outputs {
		if o.Group == group {
			files = append(files, o.File)
		}
	}
	if len(files) == 0 {
		writeErr(w, 404, "该分类暂无生成文件")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeFile(group)+`.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(ss.OutDir, name))
		if err != nil {
			continue
		}
		fw, err := zw.Create(name)
		if err != nil {
			continue
		}
		fw.Write(data)
	}
}

func handleNoRoster(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	var req struct {
		Value bool `json:"value"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ss.mu.Lock()
	ss.NoRoster = req.Value
	ss.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Handler：重置
// ---------------------------------------------------------------------------

func handleReset(w http.ResponseWriter, r *http.Request) {
	ss := sessions.get(w, r)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	keepDir := ss.Dir
	*ss = Session{ID: ss.ID, Dir: keepDir}
	ss.HeaderRows = map[string]int{}
	ss.Role = map[string]Role{}
	ss.Removed = map[string]map[int]bool{}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// 路由注册
// ---------------------------------------------------------------------------

// Register 注册全部 API 路由
func Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/upload/roster", handleUploadRoster)
	mux.HandleFunc("POST /api/roster/config", handleRosterConfig)
	mux.HandleFunc("POST /api/roster/roles", handleRosterRoles)
	mux.HandleFunc("POST /api/mapping", handleMapping)
	mux.HandleFunc("POST /api/dedup/prepare", handleDedupPrepare)
	mux.HandleFunc("POST /api/dedup/apply", handleDedupApply)
	mux.HandleFunc("POST /api/mode/noroster", handleNoRoster)
	mux.HandleFunc("POST /api/template/upload", handleTemplateUpload)
	mux.HandleFunc("POST /api/template/builtin", handleTemplateBuiltin)
	mux.HandleFunc("POST /api/template/library-load", handleTemplateLibraryLoad)
	mux.HandleFunc("POST /api/template/design", handleTemplateDesign)
	mux.HandleFunc("POST /api/template/save", handleTemplateSave)
	mux.HandleFunc("GET /api/template/library", handleTemplateLibraryList)
	mux.HandleFunc("GET /api/template/preview", handleTemplatePreview)
	mux.HandleFunc("POST /api/generate", handleGenerate)
	mux.HandleFunc("GET /api/preview", handlePreview)
	mux.HandleFunc("GET /api/download", handleDownload)
	mux.HandleFunc("GET /api/download-all", handleDownloadAll)
	mux.HandleFunc("GET /api/download-group", handleDownloadGroup)
	mux.HandleFunc("POST /api/reset", handleReset)
}
