//go:build !windows

// Package windialog 非 Windows 平台的占位实现（表格工具箱实际只在 Windows 运行）
package windialog

// SaveFile 非 Windows 无原生对话框，返回空串
func SaveFile(title, defaultName, filter string) string { return "" }

// PickFolder 非 Windows 无原生对话框，返回空串
func PickFolder(title string) string { return "" }
