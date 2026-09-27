//go:build windows

// Package windialog 封装 Windows 原生文件对话框（另存为 / 选择文件夹），
// 供表格工具箱实现"产物可选保存路径"。对话框由 exe 进程弹出，
// 内嵌窗口与浏览器回退两种模式下都能正常工作。
package windialog

import (
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

var (
	modComdlg32    = syscall.NewLazyDLL("comdlg32.dll")
	modShell32     = syscall.NewLazyDLL("shell32.dll")
	modOle32       = syscall.NewLazyDLL("ole32.dll")
	procGetSave    = modComdlg32.NewProc("GetSaveFileNameW")
	procBrowse     = modShell32.NewProc("SHBrowseForFolderW")
	procPathFromID = modShell32.NewProc("SHGetPathFromIDListW")
	procCoInit     = modOle32.NewProc("CoInitializeEx")
	procCoTaskFree = modOle32.NewProc("CoTaskMemFree")
)

const (
	ofnOverwritePrompt = 0x0002
	ofnPathMustExist   = 0x0800
	bifReturnOnlyFSDir = 0x0001
	bifNewDialogStyle  = 0x0040
	coinitApartment    = 0x2
)

// openFileNameW 与 winuser OPENFILENAMEW（64 位）布局一致
type openFileNameW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       uintptr
	LpstrCustomFilter uintptr
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         uintptr
	NMaxFile          uint32
	LpstrFileTitle    uintptr
	NMaxFileTitle     uint32
	LpstrInitialDir   uintptr
	LpstrTitle        uintptr
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       uintptr
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    uintptr
}

// browseInfoW 与 SHBrowseForFolder 的 BROWSEINFOW 布局一致
type browseInfoW struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName uintptr
	LpszTitle      uintptr
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

func ptr16(s string) uintptr {
	p, err := syscall.UTF16FromString(s)
	if err != nil {
		p = []uint16{0}
	}
	return uintptr(unsafe.Pointer(&p[0]))
}

// filterUTF16 把 "描述|模式|描述|模式" 形式的过滤器转成对话框要求的
// 双 NUL 结尾 UTF-16 序列（Go 字符串里不便放 \x00，故用 | 分隔）
func filterUTF16(filter string) []uint16 {
	var out []uint16
	for _, part := range splitPipe(filter) {
		out = append(out, utf16.Encode([]rune(part))...)
		out = append(out, 0)
	}
	out = append(out, 0)
	return out
}

func splitPipe(s string) []string {
	var parts []string
	cur := ""
	for _, r := range s {
		if r == '|' {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(parts, cur)
}

func fromUTF16(buf []uint16) string {
	for i, c := range buf {
		if c == 0 {
			// 截到首个 NUL，并处理可能的 surrogate 边界
			s := utf16.Decode(buf[:i])
			if len(s) == 0 {
				return ""
			}
			return string(s)
		}
	}
	return string(utf16.Decode(buf))
}

// SaveFile 弹出系统"另存为"对话框。
// defaultName 预填文件名；filter 形如 "Word 文档|*.docx|所有文件|*.*"。
// 返回用户选择的完整路径；取消返回空串。
func SaveFile(title, defaultName, filter string) string {
	buf := make([]uint16, 32768)
	if s, err := syscall.UTF16FromString(defaultName); err == nil && len(s) <= len(buf) {
		copy(buf, s)
	}
	f := filterUTF16(filter)
	ofn := openFileNameW{
		LStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		LpstrFilter: uintptr(unsafe.Pointer(&f[0])),
		LpstrFile:   uintptr(unsafe.Pointer(&buf[0])),
		NMaxFile:    uint32(len(buf)),
		LpstrTitle:  ptr16(title),
		Flags:       ofnOverwritePrompt | ofnPathMustExist,
	}
	r, _, _ := procGetSave.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return fromUTF16(buf)
}

// PickFolder 弹出"选择文件夹"对话框；取消返回空串。
func PickFolder(title string) string {
	_, _, _ = procCoInit.Call(0, coinitApartment) // BIF_NEWDIALOGSTYLE 需要 COM
	disp := make([]uint16, utf8.UTFMax*260)
	bi := browseInfoW{
		PszDisplayName: uintptr(unsafe.Pointer(&disp[0])),
		LpszTitle:      ptr16(title),
		UlFlags:        bifReturnOnlyFSDir | bifNewDialogStyle,
	}
	pidl, _, _ := procBrowse.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer func() { _, _, _ = procCoTaskFree.Call(pidl) }()
	buf := make([]uint16, 32768)
	r, _, _ := procPathFromID.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if r == 0 {
		return ""
	}
	return fromUTF16(buf)
}
