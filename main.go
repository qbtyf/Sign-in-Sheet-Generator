// 通用签到表生成器（signsheet）入口：
// 单体 exe，启动本地服务并用内嵌 WebView2 窗口显示操作界面。
// 窗口关闭即程序退出；WebView2 运行时缺失时自动退回系统浏览器模式。
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"runtime"
	"strings"
	"time"

	"github.com/jchv/go-webview2"

	"signsheet/internal/server"
)

//go:embed web
var webFS embed.FS

//go:embed builtins
var builtinFS embed.FS

// curWebView 保存当前窗口引用，供退出接口优雅销毁（避免 crashpad 报 CrashSender 错误）
var curWebView webview2.WebView

// appMode 当前运行模式：webview=内嵌窗口（关窗口即退出，页面隐藏退出按钮）；
// browser=浏览器回退/无头测试（关标签页不结束进程，页面显示退出按钮）。
// 通过 index.html 中的 __APP_MODE__ 占位符注入前端
var appMode = "browser"

func main() {
	diagFile := initDiagLog()
	if diagFile != nil {
		defer diagFile.Close()
	}
	log.Printf("==== 启动 ====")

	// 主流程崩溃兜底：panic 记入诊断日志后再按原样崩溃（运行时致命错误无法拦截，属已知限制）
	defer func() {
		if r := recover(); r != nil {
			log.Printf("主流程崩溃: %v\n%s", r, debug.Stack())
			panic(r)
		}
	}()

	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	server.SetBuiltinFS(builtinFS)
	localesSub, err := fs.Sub(webFS, "web/locales")
	if err != nil {
		panic(err)
	}
	server.SetLocaleFS(localesSub)

	mux := http.NewServeMux()
	mux.Handle("GET /", staticHandler(webSub))
	server.Register(mux)
	// 退出接口：浏览器回退模式下网页右上角「退出程序」是关闭入口；
	// WebView 模式下直接关窗口即可。两种模式都先销毁窗口再退出，避免崩溃报告弹窗
	mux.HandleFunc("GET /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><html lang=\"zh\"><head><meta charset=\"UTF-8\"><title>已退出</title></head><body style=\"font-family:sans-serif;text-align:center;padding-top:80px;color:#444\"><h2>通用签到表生成器已退出。</h2><p>本页面已失效，可以直接关闭。</p></body></html>")
		log.Printf("收到退出请求（/api/shutdown）")
		go func() {
			time.Sleep(500 * time.Millisecond)
			gracefulExit()
		}()
	})

	ln := listen()
	url := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	// headless 测试模式：只起服务不开窗口（SIGNHEADLESS=1）
	if os.Getenv("SIGNHEADLESS") == "1" {
		fmt.Println("headless 服务已启动:", url)
		if err := http.Serve(ln, mux); err != nil {
			panic(err)
		}
		return
	}

	// 首选：内嵌 WebView2 窗口（关窗口即退出，无需另开浏览器）
	if w, ok := newWebViewSafe(); ok {
		curWebView = w
		appMode = "webview"
		log.Printf("运行模式: webview 内嵌窗口, 地址: %s", url)
		defer w.Destroy()
		w.SetTitle("通用签到表生成器")
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
		w.Run() // 阻塞至窗口关闭 → main 返回 → 进程退出 → 服务随之结束
		log.Printf("窗口已关闭，程序退出")
		return
	}

	// 回退：WebView2 运行时缺失时用系统浏览器打开（此时靠页面右上角「退出程序」结束）
	fmt.Println("WebView2 不可用，退回浏览器模式")
	go func() {
		time.Sleep(300 * time.Millisecond)
		openBrowser(url)
	}()
	if err := http.Serve(ln, mux); err != nil {
		panic(err)
	}
}

// staticHandler 服务内嵌网页；index.html 单独处理，把 __APP_MODE__ 占位符
// 替换为当前运行模式（webview/browser），前端据此决定「退出程序」按钮是否显示
func staticHandler(webSub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webSub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			if data, err := fs.ReadFile(webSub, "index.html"); err == nil {
				html := strings.ReplaceAll(string(data), "__APP_MODE__", appMode)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(html))
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// gracefulExit 先销毁窗口再结束进程：直接 os.Exit 会绕过 WebView2 清理，
// 其崩溃报告器（crashpad）可能弹出 "Error launching CrashSender.exe" 错误框
func gracefulExit() {
	if curWebView != nil {
		func() {
			defer func() { recover() }() // 销毁过程中的任何异常都无关紧要，反正即将退出
			curWebView.Destroy()
		}()
	}
	os.Exit(0)
}

// initDiagLog 打开诊断日志（exe 同目录 诊断日志.txt，追加写入）。
// 背景：exe 以 windowsgui 静默启动，没有控制台，panic/异常信息无处可看；
// 该日志收集 Go 标准 log 输出（含 net/http 自动恢复的 handler panic 堆栈）
// 与关键事件（启动/退出/服务异常），出问题时有据可查。
// 超过 2MB 自动把旧文件改名为 诊断日志-旧.txt 重新开始，避免无限膨胀。
// 注意：打开失败时静默降级（不影响正常使用）；Go 运行时致命错误（如数据竞争）
// 直写系统 stderr，无法被本文件拦截，属已知限制。
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

// newWebViewSafe 创建 WebView2 窗口；运行时缺失导致内部 panic 时恢复并返回 false。
// 关键：DataPath 必须显式指定为「本实例独有」的 ASCII 路径——
// 库默认用 %AppData%\<exe名>（含中文），导致 crashpad 启动失败弹 CrashSender 错误框；
// 且所有实例共用同一文件夹，开第二个实例时文件夹被锁 → 白窗口
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

// webviewDataPath 返回本实例专用的 WebView2 数据文件夹：
// %LOCALAPPDATA%\SignInSheetGen\WebView2-<进程PID>（纯 ASCII，实例间互不冲突）
func webviewDataPath() string {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "SignInSheetGen")
	dir := filepath.Join(base, fmt.Sprintf("WebView2-%d", os.Getpid()))
	_ = os.MkdirAll(dir, 0o755)
	cleanStaleWebViewData(base)
	return dir
}

// cleanStaleWebViewData 清理 7 天前残留的旧实例数据文件夹（尽力而为，失败忽略）
func cleanStaleWebViewData(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "WebView2-") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < 7*24*time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, e.Name()))
	}
}

// listen 监听本地端口，17877 被占用时依次后移
func listen() net.Listener {
	var ln net.Listener
	var err error
	base := 17877
	for port := base; port < base+10; port++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln
		}
	}
	panic("无法监听本地端口 17877~17886")
}

// waitReady 轮询等待服务可连接（最多约 3 秒），避免 WebView 先于服务导航被拒
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
	cmd.Start()
}
