package pdfext

import "testing"

// TestFixStartxref 修补保持文件长度不变（1 字节换 1 字节，偏移量不漂移）
func TestFixStartxref(t *testing.T) {
	in := []byte("startxref\r123\r\n%%EOF")
	out := FixStartxref(in)
	if len(out) != len(in) {
		t.Fatalf("修补后长度变化: %d -> %d", len(in), len(out))
	}
	if string(out[9]) != "\n" {
		t.Errorf("第 10 字节应为 \\n，得到 %q", out[9])
	}
	// 无需修补时内容不变
	same := []byte("startxref\n123")
	if string(FixStartxref(same)) != string(same) {
		t.Error("无需修补时不应改动")
	}
}
