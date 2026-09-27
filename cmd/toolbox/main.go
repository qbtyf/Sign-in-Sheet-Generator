// 表格工具箱（toolbox）入口：
// 单体 exe，功能卡片式首页，点卡片进入对应工具。首个工具：表格填充。
// 复用签到表生成器的成熟模式：内嵌 WebView2、静默启动、诊断日志、优雅退出。
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jchv/go-webview2"

	"signsheet/internal/roster"
	"signsheet/internal/tabfill"
	"signsheet/internal/windialog"
	"signsheet/webbox"
)

// curWebView 当前窗口引用，供退出时优雅销毁（避免 crashpad 弹 CrashSender 错误框）
var curWebView webview2.WebView

// appMode 运行模式：webview=内嵌窗口 / browser=浏览器回退与无头测试
var appMode = "browser"

// workDir 本次运行的工作目录（上传文件 + 输出结果），进程结束可整体清理
var workDir string

// 输出结果子目录名（工作目录之下）
const outSub = "输出"

func main() {
	diagFile := initDiagLog()
	if diagFile != nil {
		defer diagFile.Close()
	}
	log.Printf("==== 表格工具箱启动 ====")
	defer func() {
		if r := recover(); r != nil {
			log.Printf("主流程崩溃: %v\n%s", r, debug.Stack())
			panic(r)
		}
	}()

	setupWorkDir()
	webSub, err := fs.Sub(webbox.FS, ".")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", staticHandler(webSub))
	registerAPI(mux)
	mux.HandleFunc("GET /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><html lang=\"zh\"><head><meta charset=\"UTF-8\"><title>已退出</title></head><body style=\"font-family:sans-serif;text-align:center;padding-top:80px;color:#444\"><h2>表格工具箱已退出。</h2><p>本页面已失效，可以直接关闭。</p></body></html>")
		log.Printf("收到退出请求（/api/shutdown）")
		go func() {
			time.Sleep(500 * time.Millisecond)
			gracefulExit()
		}()
	})

	ln := listen()
	url := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	// headless 测试模式（SIGNHEADLESS=1）：只起服务不开窗口
	if os.Getenv("SIGNHEADLESS") == "1" {
		fmt.Println("headless 服务已启动:", url)
		if err := http.Serve(ln, mux); err != nil {
			panic(err)
		}
		return
	}

	// 首选：内嵌 WebView2 窗口
	if w, ok := newWebViewSafe(); ok {
		curWebView = w
		appMode = "webview"
		log.Printf("运行模式: webview 内嵌窗口, 地址: %s", url)
		defer w.Destroy()
		w.SetTitle("表格工具箱")
		w.SetSize(1180, 800, webview2.HintNone)
		go func() {
			if err := http.Serve(ln, mux); err != nil {
				log.Printf("本地服务异常退出: %v", err)
				w.Dispatch(func() { w.Terminate() })
			}
		}()
		waitReady(url)
		w.Navigate(url)
		log.Printf("消息循环启动")
		w.Run()
		log.Printf("窗口已关闭，程序退出")
		return
	}

	// 回退：系统浏览器模式
	fmt.Println("WebView2 不可用，退回浏览器模式")
	go func() {
		time.Sleep(300 * time.Millisecond)
		openBrowser(url)
	}()
	if err := http.Serve(ln, mux); err != nil {
		panic(err)
	}
}

// ---------------------------------------------------------------------------
// 工作目录
// ---------------------------------------------------------------------------

// setupWorkDir 创建本次运行的工作目录：%LOCALAPPDATA%\TableToolbox\Work-<PID>
//（纯 ASCII 路径，避免中文路径引发的各类问题），并清理 7 天前的残留目录
func setupWorkDir() {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "TableToolbox")
	workDir = filepath.Join(base, fmt.Sprintf("Work-%d", os.Getpid()))
	if err := os.MkdirAll(filepath.Join(workDir, outSub), 0o755); err != nil {
		panic("创建工作目录失败: " + err.Error())
	}
	cleanStale(base, "Work-")
}

// cleanStale 清理 7 天前残留的旧实例目录（尽力而为，失败忽略）
func cleanStale(base, prefix string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < 7*24*time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, e.Name()))
	}
}

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

func registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/upload", handleUpload)
	mux.HandleFunc("POST /api/read", handleRead)
	mux.HandleFunc("POST /api/fill", handleFill)
	mux.HandleFunc("POST /api/merge", handleMerge)
	mux.HandleFunc("POST /api/split", handleSplit)
	mux.HandleFunc("POST /api/extract/list", handleExtractList)
	mux.HandleFunc("POST /api/extract", handleExtract)
	mux.HandleFunc("POST /api/saveas", handleSaveAs)
	mux.HandleFunc("POST /api/saveall", handleSaveAll)
	mux.HandleFunc("POST /api/pickdir", handlePickDir)
	mux.HandleFunc("GET /api/zip", handleZip)
	mux.HandleFunc("GET /api/download", handleDownload)
}

// writeErr 统一错误响应
func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// writeJSON 统一成功响应
func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeErr(w, 500, "内部序列化失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

// handleUpload 接收上传文件，返回 {path, name, sheets:[SheetInfo]}。
// form 字段：file（文件）、kind（src=原始表格 / tpl=模板表格）
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, 400, "上传数据无效: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "未找到上传文件")
		return
	}
	defer file.Close()

	name := hdr.Filename
	switch {
	case strings.HasSuffix(strings.ToLower(name), ".doc"):
		writeErr(w, 400, "暂不支持 .doc 老格式，请先用 Word/WPS 另存为 .docx 后再上传")
		return
	case strings.HasSuffix(strings.ToLower(name), ".xls"):
		writeErr(w, 400, "暂不支持 .xls 老格式，请先用 Excel/WPS 另存为 .xlsx 后再上传")
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".xlsx" && ext != ".docx" {
		writeErr(w, 400, "仅支持 .xlsx / .docx 文件（.doc/.xls 请先另存为新格式）")
		return
	}

	kind := r.FormValue("kind")
	if kind != "src" && kind != "tpl" {
		kind = "src"
	}
	saveName := fmt.Sprintf("%s-%d-%s", kind, time.Now().UnixNano(), filepath.Base(name))
	savePath := filepath.Join(workDir, saveName)
	out, err := os.Create(savePath)
	if err != nil {
		writeErr(w, 500, "保存上传文件失败: "+err.Error())
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeErr(w, 500, "保存上传文件失败: "+err.Error())
		return
	}
	out.Close()

	sheets, err := listSheets(savePath)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("上传成功: %s (%d 个数据源)", saveName, len(sheets))
	writeJSON(w, map[string]any{"path": saveName, "name": name, "sheets": sheets})
}

// listSheets 列出文件的数据源概要（xlsx=工作表 / docx=顶层表格）
func listSheets(path string) ([]roster.SheetInfo, error) {
	if strings.HasSuffix(strings.ToLower(path), ".xlsx") {
		return roster.LoadXlsx(path)
	}
	return roster.LoadDocx(path)
}

// tplReadRequest /api/read 请求体
type readRequest struct {
	Path      string `json:"path"`      // 上传时返回的文件名（工作目录内）
	Key       string `json:"key"`       // "xlsx:工作表名" 或 "docx:序号"
	HeaderRow int    `json:"headerRow"` // 表头行，1 基
}

// handleRead 读取指定数据源：返回表头字段、数据行数与前 5 行预览
func handleRead(w http.ResponseWriter, r *http.Request) {
	var req readRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	path, key, err := resolve(req.Path, req.Key)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.HeaderRow < 1 {
		req.HeaderRow = 1
	}
	var tbl *roster.Table
	if strings.HasPrefix(key, "xlsx:") {
		tbl, err = roster.ReadXlsxSheet(path, strings.TrimPrefix(key, "xlsx:"), req.HeaderRow)
	} else {
		idx := 0
		fmt.Sscanf(strings.TrimPrefix(key, "docx:"), "%d", &idx)
		tbl, err = roster.ReadDocxSheet(path, idx, req.HeaderRow)
	}
	if err != nil {
		writeErr(w, 400, "读取失败: "+err.Error())
		return
	}
	preview := tbl.Rows
	if len(preview) > 5 {
		preview = preview[:5]
	}
	writeJSON(w, map[string]any{
		"headers":  tbl.Headers,
		"rowCount": len(tbl.Rows),
		"preview":  preview,
	})
}

// readSource 按文件名＋数据源 key 读取一张表（xlsx 工作表 / docx 表格）
func readSource(path, key string, headerRow int) (*roster.Table, error) {
	if strings.HasPrefix(key, "xlsx:") {
		return roster.ReadXlsxSheet(path, strings.TrimPrefix(key, "xlsx:"), headerRow)
	}
	idx := 0
	fmt.Sscanf(strings.TrimPrefix(key, "docx:"), "%d", &idx)
	return roster.ReadDocxSheet(path, idx, headerRow)
}

// fillRequest /api/fill 请求体
type fillRequest struct {
	TplPath      string `json:"tplPath"`
	TplKey       string `json:"tplKey"`
	TplHeaderRow int    `json:"tplHeaderRow"`
	SrcPath      string `json:"srcPath"`
	SrcKey       string `json:"srcKey"`
	SrcHeaderRow int    `json:"srcHeaderRow"`
	Mapping      []int  `json:"mapping"` // 模板列 → 原始字段下标，-1=不填
}

// handleFill 执行填充，输出到 输出/ 目录，返回 {file}
func handleFill(w http.ResponseWriter, r *http.Request) {
	var req fillRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.TplHeaderRow < 1 {
		req.TplHeaderRow = 1
	}
	if req.SrcHeaderRow < 1 {
		req.SrcHeaderRow = 1
	}
	tplPath, tplKey, err := resolve(req.TplPath, req.TplKey)
	if err != nil {
		writeErr(w, 400, "模板: "+err.Error())
		return
	}
	srcPath, srcKey, err := resolve(req.SrcPath, req.SrcKey)
	if err != nil {
		writeErr(w, 400, "原始表格: "+err.Error())
		return
	}

	// 读原始数据
	var src *roster.Table
	if strings.HasPrefix(srcKey, "xlsx:") {
		src, err = roster.ReadXlsxSheet(srcPath, strings.TrimPrefix(srcKey, "xlsx:"), req.SrcHeaderRow)
	} else {
		idx := 0
		fmt.Sscanf(strings.TrimPrefix(srcKey, "docx:"), "%d", &idx)
		src, err = roster.ReadDocxSheet(srcPath, idx, req.SrcHeaderRow)
	}
	if err != nil {
		writeErr(w, 400, "读取原始表格失败: "+err.Error())
		return
	}
	if len(src.Rows) == 0 {
		writeErr(w, 400, "原始表格选中的数据源没有数据行")
		return
	}
	mp := tabfill.Mapping(req.Mapping)

	// 按模板格式选择填充器，输出格式跟随模板
	outName := fmt.Sprintf("表格填充结果-%s%s", time.Now().Format("20060102-150405"), strings.ToLower(filepath.Ext(tplPath)))
	outPath := filepath.Join(workDir, outSub, outName)
	if strings.HasSuffix(strings.ToLower(tplPath), ".docx") {
		tableIdx := 0
		fmt.Sscanf(strings.TrimPrefix(tplKey, "docx:"), "%d", &tableIdx)
		err = tabfill.FillDocx(tplPath, tableIdx, req.TplHeaderRow, mp, src, outPath)
	} else {
		err = tabfill.FillXlsx(tplPath, strings.TrimPrefix(tplKey, "xlsx:"), req.TplHeaderRow, mp, src, outPath)
	}
	if err != nil {
		log.Printf("填充失败: %v", err)
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("填充完成: %s（%d 行数据）", outName, len(src.Rows))
	writeJSON(w, map[string]any{"file": outName, "rows": len(src.Rows)})
}

// mergeSourceSpec 一个合并来源（前端传）
type mergeSourceSpec struct {
	Path      string `json:"path"`
	Key       string `json:"key"`
	HeaderRow int    `json:"headerRow"`
	Mapping   []int  `json:"mapping"` // 总表字段下标 → 来源字段下标（-1 填空）
	Name      string `json:"name"`    // 可选：来源显示名（表格内合并时=工作表名，用于来源列）
}

// mergeRequest /api/merge 请求体
type mergeRequest struct {
	Base      []string          `json:"base"`      // 总表字段（基准表字段＋新增字段）
	Sources   []mergeSourceSpec `json:"sources"`   // 顺序 = 合并顺序
	SourceCol bool              `json:"sourceCol"` // 末尾加「来源」列
	Dedup     int               `json:"dedup"`     // 按总表字段下标判重，-1 不判重
	BasePath  string            `json:"basePath"`  // 基准表文件（决定输出格式 xlsx/docx）
	OutFmt    string            `json:"outFmt"`    // 可选：".xlsx"/".docx"，空 = 跟随基准表
	InFile    bool              `json:"inFile"`    // true = 表格内合并：结果写回原文件（xlsx 加 Sheet / docx 文末加表格）
}

// handleMerge 多表合一：读各来源 → 引擎合并 → 按基准表格式输出
func handleMerge(w http.ResponseWriter, r *http.Request) {
	var req mergeRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.Base) == 0 {
		writeErr(w, 400, "总表没有字段，请先完成字段映射")
		return
	}
	if len(req.Sources) < 1 {
		writeErr(w, 400, "至少需要一个来源表")
		return
	}
	if req.Dedup < -1 {
		req.Dedup = -1
	}

	srcs := make([]tabfill.SrcTable, len(req.Sources))
	for i, s := range req.Sources {
		if s.HeaderRow < 1 {
			s.HeaderRow = 1
		}
		path, key, err := resolve(s.Path, s.Key)
		if err != nil {
			writeErr(w, 400, fmt.Sprintf("来源表 %d: %v", i+1, err))
			return
		}
		tbl, err := readSource(path, key, s.HeaderRow)
		if err != nil {
			writeErr(w, 400, fmt.Sprintf("读取来源表 %d 失败: %v", i+1, err))
			return
		}
		name := s.Name // 前端显式给了显示名（表格内合并=工作表名）则优先
		if name == "" {
			// 去掉上传时加的 "src-<纳秒>-" 前缀，保留用户可读的文件名
			name = s.Path
			if parts := strings.SplitN(name, "-", 3); len(parts) == 3 {
				name = parts[2]
			}
		}
		srcs[i] = tabfill.SrcTable{Name: name, Headers: tbl.Headers, Rows: tbl.Rows}
	}

	spec := tabfill.MergeSpec{Base: req.Base, SourceCol: req.SourceCol, Dedup: req.Dedup}
	for _, s := range req.Sources {
		if len(s.Mapping) != len(req.Base) {
			writeErr(w, 400, "字段映射长度与总表字段数不一致，请刷新重试")
			return
		}
		spec.PerSource = append(spec.PerSource, s.Mapping)
	}
	res, err := tabfill.Merge(spec, srcs)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(res.Rows) == 0 {
		writeErr(w, 400, "合并结果为 0 行（所有来源表都没有数据）")
		return
	}

	// 表格内合并：结果写回原文件（完整副本 + 追加合并结果），格式跟随原文件
	if req.InFile {
		baseName := filepath.Base(req.BasePath)
		srcPath := filepath.Join(workDir, baseName)
		if _, err := os.Stat(srcPath); err != nil {
			writeErr(w, 400, "找不到原文件: "+baseName)
			return
		}
		stem := baseName
		if parts := strings.SplitN(stem, "-", 3); len(parts) == 3 {
			stem = parts[2]
		}
		outName := stem
		outPath := filepath.Join(workDir, outSub, outName)
		if _, err := os.Stat(outPath); err == nil { // 输出目录重名则加时间戳
			ext2 := filepath.Ext(stem)
			outName = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(stem, ext2), time.Now().Format("150405"), ext2)
			outPath = filepath.Join(workDir, outSub, outName)
		}
		var sheetName string
		if strings.EqualFold(filepath.Ext(srcPath), ".docx") {
			err = tabfill.AppendDocxTable(srcPath, outPath, "合并总表", res.Headers, res.Rows)
			sheetName = "合并总表（文末表格）"
		} else {
			sheetName, err = tabfill.AppendMergedSheet(srcPath, outPath, "合并总表", res.Headers, res.Rows)
		}
		if err != nil {
			log.Printf("写回原文件失败: %v", err)
			writeErr(w, 400, err.Error())
			return
		}
		log.Printf("表格内合并完成: %s（%d 个来源，%d 行）", outName, len(srcs), len(res.Rows))
		writeJSON(w, map[string]any{"file": outName, "rows": len(res.Rows), "inFile": true, "sheetName": sheetName})
		return
	}

	ext := outExt(req.BasePath)
	if req.OutFmt == ".xlsx" || req.OutFmt == ".docx" {
		ext = req.OutFmt // 前端显式指定输出格式时优先
	}
	outName := fmt.Sprintf("合并总表-%s%s", time.Now().Format("20060102-150405"), ext)
	outPath := filepath.Join(workDir, outSub, outName)
	if strings.EqualFold(filepath.Ext(outName), ".docx") {
		err = tabfill.WriteDocxTable(res.Headers, res.Rows, "", outPath)
	} else {
		err = tabfill.WriteXlsxTable(res.Headers, res.Rows, outPath)
	}
	if err != nil {
		log.Printf("合并失败: %v", err)
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("合并完成: %s（%d 个来源，%d 行）", outName, len(srcs), len(res.Rows))
	writeJSON(w, map[string]any{"file": outName, "rows": len(res.Rows)})
}

// outExt 依据基准表文件扩展名决定输出扩展名（默认 .xlsx）
func outExt(basePath string) string {
	if strings.EqualFold(filepath.Ext(basePath), ".docx") {
		return ".docx"
	}
	return ".xlsx"
}
// splitRequest /api/split 请求体
type splitRequest struct {
	Path      string `json:"path"`
	Key       string `json:"key"`
	HeaderRow int    `json:"headerRow"`
	Mode      string `json:"mode"`   // byValue / byRows
	Field     int    `json:"field"`  // byValue：拆分字段下标
	Size      int    `json:"size"`   // byRows：每份行数
	OutFmt    string `json:"outFmt"` // 输出格式 ".xlsx" / ".docx"，空 = 跟随输入
	InFile    bool   `json:"inFile"` // true = 拆成单文件多工作表（Sheet），输出固定 xlsx
}

// handleSplit 拆分表格：读总表 → 引擎分组 → 逐份落盘，返回文件清单
func handleSplit(w http.ResponseWriter, r *http.Request) {
	var req splitRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.HeaderRow < 1 {
		req.HeaderRow = 1
	}
	path, key, err := resolve(req.Path, req.Key)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tbl, err := readSource(path, key, req.HeaderRow)
	if err != nil {
		writeErr(w, 400, "读取总表失败: "+err.Error())
		return
	}

	spec := tabfill.SplitSpec{Headers: tbl.Headers, Rows: tbl.Rows, Mode: req.Mode, Field: req.Field, Size: req.Size}
	groups, err := tabfill.Split(spec)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	// 表格内拆分：所有组写进同一个 xlsx 的多个工作表（Sheet）
	if req.InFile {
		stem := strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
		if parts := strings.SplitN(stem, "-", 3); len(parts) == 3 {
			stem = parts[2]
		}
		outName := tabfill.SanitizeFileName(stem) + "-拆分.xlsx"
		outPath := filepath.Join(workDir, outSub, outName)
		if err := tabfill.WriteXlsxMultiSheet(tbl.Headers, groups, outPath); err != nil {
			log.Printf("表格内拆分失败: %v", err)
			writeErr(w, 400, err.Error())
			return
		}
		type shInfo struct {
			Name string `json:"name"`
			Rows int    `json:"rows"`
		}
		sheets := make([]shInfo, 0, len(groups))
		for _, g := range groups {
			sheets = append(sheets, shInfo{Name: g.Name, Rows: len(g.Rows)})
		}
		log.Printf("表格内拆分完成: %s → %d 个工作表", stem, len(sheets))
		writeJSON(w, map[string]any{"file": outName, "sheets": sheets, "rows": len(tbl.Rows)})
		return
	}

	ext := req.OutFmt
	if ext != ".docx" && ext != ".xlsx" {
		ext = strings.ToLower(filepath.Ext(req.Path)) // 跟随输入
		if ext != ".docx" {
			ext = ".xlsx"
		}
	}
	stem := strings.TrimSuffix(filepath.Base(req.Path), filepath.Ext(req.Path))
	// 去掉上传时加的 "src-<纳秒>-" 前缀
	if parts := strings.SplitN(stem, "-", 3); len(parts) == 3 {
		stem = parts[2]
	}

	type outFile struct {
		File string `json:"file"`
		Rows int    `json:"rows"`
	}
	var files []outFile
	for _, g := range groups {
		outName := fmt.Sprintf("%s-%s%s", tabfill.SanitizeFileName(stem), tabfill.SanitizeFileName(g.Name), ext)
		outPath := filepath.Join(workDir, outSub, outName)
		if ext == ".docx" {
			err = tabfill.WriteDocxTable(tbl.Headers, g.Rows, g.Name, outPath)
		} else {
			err = tabfill.WriteXlsxTable(tbl.Headers, g.Rows, outPath)
		}
		if err != nil {
			log.Printf("拆分失败: %v", err)
			writeErr(w, 400, err.Error())
			return
		}
		files = append(files, outFile{File: outName, Rows: len(g.Rows)})
	}
	log.Printf("拆分完成: %s → %d 份（%s）", stem, len(files), req.Mode)
	writeJSON(w, map[string]any{"files": files, "base": stem})
}

// ---------------------------------------------------------------------------
// 提取表格（V1.4）
// ---------------------------------------------------------------------------

// extractListRequest /api/extract/list 请求体
type extractListRequest struct {
	Path string `json:"path"`
}

// handleExtractList 列出 docx 里的全部表格（提取表格第①步）
func handleExtractList(w http.ResponseWriter, r *http.Request) {
	var req extractListRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	path, _, err := resolve(req.Path, "docx:0")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".docx") {
		writeErr(w, 400, "提取表格目前支持 Word（.docx）文件；PDF 提取将在 V1.5 提供")
		return
	}
	tables, err := tabfill.ListDocxTables(path)
	if err != nil {
		log.Printf("提取表格清单失败: %v", err)
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"tables": tables})
}

// extractRequest /api/extract 请求体
type extractRequest struct {
	Path   string `json:"path"`
	Tables []int  `json:"tables"` // 勾选的表格序号（0 基）
	OutFmt string `json:"outFmt"` // ".docx" / ".xlsx"
}

// handleExtract 执行提取，输出到 输出/ 目录，返回 {file, tables}
func handleExtract(w http.ResponseWriter, r *http.Request) {
	var req extractRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.Tables) == 0 {
		writeErr(w, 400, "请至少勾选一个要提取的表格")
		return
	}
	if req.OutFmt != ".docx" && req.OutFmt != ".xlsx" {
		req.OutFmt = ".docx"
	}
	path, _, err := resolve(req.Path, "docx:0")
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".docx") {
		writeErr(w, 400, "提取表格目前支持 Word（.docx）文件；PDF 提取将在 V1.5 提供")
		return
	}

	outName := tabfill.SanitizeFileName(userStem(req.Path)) + "-提取" + req.OutFmt
	outPath := filepath.Join(workDir, outSub, outName)
	if _, err := os.Stat(outPath); err == nil { // 输出目录重名则加时间戳
		outName = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(outName, req.OutFmt), time.Now().Format("150405"), req.OutFmt)
		outPath = filepath.Join(workDir, outSub, outName)
	}
	if err := tabfill.ExtractDocx(path, outPath, req.OutFmt, req.Tables); err != nil {
		log.Printf("提取失败: %v", err)
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("提取完成: %s（%d 个表格 → %s）", outName, len(req.Tables), req.OutFmt)
	writeJSON(w, map[string]any{"file": outName, "tables": len(req.Tables)})
}

// ---------------------------------------------------------------------------
// 保存路径（V1.4）：另存到… / 输出目录
// ---------------------------------------------------------------------------

// saveAsRequest /api/saveas 请求体
type saveAsRequest struct {
	File string `json:"file"` // 输出目录里的文件名（裸文件名）
	Dir  string `json:"dir"`  // 可选：直接存到该目录（不弹对话框）
}

// handleSaveAs 把输出文件保存到用户选择的位置。
// Dir 为空时弹 Windows 原生"另存为"对话框（预填文件名）；用户取消返回 {cancelled:true}。
func handleSaveAs(w http.ResponseWriter, r *http.Request) {
	var req saveAsRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name := filepath.Base(req.File)
	src := filepath.Join(workDir, outSub, name)
	if _, err := os.Stat(src); err != nil {
		writeErr(w, 404, "文件不存在或已过期，请重新生成")
		return
	}

	var dst string
	if req.Dir != "" {
		info, err := os.Stat(req.Dir)
		if err != nil || !info.IsDir() {
			writeErr(w, 400, "保存目录无效: "+req.Dir)
			return
		}
		dst = uniqueDst(filepath.Join(req.Dir, name))
	} else {
		dst = windialog.SaveFile("另存为", name, filterForFile(name))
		if dst == "" {
			writeJSON(w, map[string]any{"cancelled": true})
			return
		}
	}
	if err := copyFile(src, dst); err != nil {
		log.Printf("另存失败: %v", err)
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	log.Printf("已保存: %s → %s", name, dst)
	writeJSON(w, map[string]any{"savedTo": dst})
}

// saveAllRequest /api/saveall 请求体
type saveAllRequest struct {
	Files []string `json:"files"` // 输出目录里的文件名列表
	Dir   string   `json:"dir"`   // 目标目录
}

// handleSaveAll 把多个输出文件一次性保存到指定目录（重名自动改名）
func handleSaveAll(w http.ResponseWriter, r *http.Request) {
	var req saveAllRequest
	if err := readBody(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if info, err := os.Stat(req.Dir); err != nil || !info.IsDir() {
		// 目录不存在时尝试自动创建（正常路径来自系统"选择文件夹"对话框，必存在）
		if mkErr := os.MkdirAll(req.Dir, 0o755); mkErr != nil {
			writeErr(w, 400, "输出目录无效，请先选择")
			return
		}
	}
	type savedItem struct {
		File string `json:"file"`
		To   string `json:"to"`
	}
	saved := make([]savedItem, 0, len(req.Files))
	for _, f := range req.Files {
		name := filepath.Base(f)
		src := filepath.Join(workDir, outSub, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // 单个缺失不阻断
		}
		dst := uniqueDst(filepath.Join(req.Dir, name))
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			continue
		}
		saved = append(saved, savedItem{File: name, To: dst})
	}
	if len(saved) == 0 {
		writeErr(w, 400, "没有可保存的文件")
		return
	}
	log.Printf("批量保存: %d 个文件 → %s", len(saved), req.Dir)
	writeJSON(w, map[string]any{"saved": saved})
}

// handlePickDir 弹出 Windows 原生"选择文件夹"对话框
func handlePickDir(w http.ResponseWriter, r *http.Request) {
	dir := windialog.PickFolder("选择输出目录")
	if dir == "" {
		writeJSON(w, map[string]any{"cancelled": true})
		return
	}
	writeJSON(w, map[string]any{"dir": dir})
}

// ---------------------------------------------------------------------------
// 保存路径公共小件
// ---------------------------------------------------------------------------

// userStem 去掉上传时加的 "src-<纳秒>-" 前缀和扩展名，得到用户可读的文件主干
func userStem(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if parts := strings.SplitN(stem, "-", 3); len(parts) == 3 {
		stem = parts[2]
	}
	return stem
}

// uniqueDst 目标已存在时自动加序号，绝不覆盖用户已有文件
func uniqueDst(dst string) string {
	if _, err := os.Stat(dst); err != nil {
		return dst
	}
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s(%d)%s", base, i, ext)
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
}

// filterForFile 按扩展名生成另存为对话框的过滤器（| 分隔，windialog 内转换）
func filterForFile(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return "Word 文档|*.docx|所有文件|*.*"
	case ".xlsx":
		return "Excel 工作簿|*.xlsx|所有文件|*.*"
	case ".zip":
		return "压缩文件|*.zip|所有文件|*.*"
	}
	return "所有文件|*.*"
}

// copyFile 复制文件（覆盖前由调用方保证 uniqueDst / 对话框确认）
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// handleZip 把指定的输出文件打包成一个 zip 供下载
func handleZip(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var names []string
	for _, n := range strings.Split(q.Get("names"), ",") {
		if n = filepath.Base(strings.TrimSpace(n)); n != "" && n != "." && n != ".." {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		writeErr(w, 400, "没有要打包的文件")
		return
	}
	zipName := fmt.Sprintf("拆分结果-%s.zip", time.Now().Format("20060102-150405"))
	zipPath := filepath.Join(workDir, outSub, zipName)
	zf, err := os.Create(zipPath)
	if err != nil {
		writeErr(w, 500, "创建 zip 失败: "+err.Error())
		return
	}
	zw := zip.NewWriter(zf)
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(workDir, outSub, n))
		if err != nil {
			continue // 单个缺失不阻断
		}
		fe, err := zw.Create(n)
		if err == nil {
			_, _ = fe.Write(data)
		}
	}
	_ = zw.Close()
	_ = zf.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(zipName)))
	data, _ := os.ReadFile(zipPath)
	_, _ = w.Write(data)
}

// handleDownload 提供输出文件下载
func handleDownload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.URL.Query().Get("name"))
	path := filepath.Join(workDir, outSub, name)
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 404, "文件不存在或已过期")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
	_, _ = io.Copy(w, f)
}

// ---------------------------------------------------------------------------
// 公共小件
// ---------------------------------------------------------------------------

// resolve 校验并拼出工作目录内的绝对路径，返回 (绝对路径, 规范化 key, 错误)
func resolve(name, key string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("缺少文件")
	}
	if filepath.Base(name) != name || strings.Contains(name, "..") {
		return "", "", fmt.Errorf("非法文件名")
	}
	if key == "" {
		return "", "", fmt.Errorf("未选择数据源")
	}
	return filepath.Join(workDir, name), key, nil
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// staticHandler 服务内嵌网页；index.html 注入 __APP_MODE__
func staticHandler(webSub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webSub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			if data, err := fs.ReadFile(webSub, "index.html"); err == nil {
				page := strings.ReplaceAll(string(data), "__APP_MODE__", appMode)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(page))
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func gracefulExit() {
	if curWebView != nil {
		func() {
			defer func() { recover() }()
			curWebView.Destroy()
		}()
	}
	os.Exit(0)
}

func initDiagLog() *os.File {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	path := filepath.Join(filepath.Dir(exe), "诊断日志.txt")
	if info, err := os.Stat(path); err == nil && info.Size() > 2*1024*1024 {
		_ = os.Rename(path, filepath.Join(filepath.Dir(exe), "诊断日志-旧.txt"))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	return f
}

func newWebViewSafe() (wv webview2.WebView, ok bool) {
	defer func() {
		if recover() != nil || wv == nil {
			wv, ok = nil, false
		}
	}()
	wv = webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: webviewDataPath(),
	})
	return wv, true
}

// webviewDataPath 本实例专用的 WebView2 数据文件夹（纯 ASCII，实例间互不冲突）
func webviewDataPath() string {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "TableToolbox")
	dir := filepath.Join(base, fmt.Sprintf("WebView2-%d", os.Getpid()))
	_ = os.MkdirAll(dir, 0o755)
	cleanStale(base, "WebView2-")
	return dir
}

func listen() net.Listener {
	var ln net.Listener
	var err error
	for port := 17878; port < 17888; port++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln
		}
	}
	panic("无法监听本地端口 17878~17887")
}

func waitReady(url string) {
	for i := 0; i < 30; i++ {
		conn, err := net.Dial("tcp", url[len("http://"):])
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", url)
	} else {
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
