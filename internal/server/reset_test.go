package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"signsheet/internal/generator"
)

// TestHandleResetAlive 线上事故回归测试（2026-09-27）：
// 旧实现 `*ss = Session{...}` 整体覆盖含 sync.Mutex 的 Session 结构体，
// 把"已锁上的锁"原地替换为零值锁，defer Unlock 解到没锁过的锁 →
// 运行时致命错误 sync: unlock of unlocked mutex → 整个进程无声退出。
// 用户实际表现：生成完成后点「重做 → 全新一轮」程序窗口直接消失。
// 修复：handleReset 改为逐字段清空。本测试在带脏数据的会话上调用 handleReset，
// 若修复回退，进程会在本测试中直接死亡，go test 报 fatal error。
func TestHandleResetAlive(t *testing.T) {
	// 第一步：创建会话（通过 sessions.get 拿到真实 cookie）
	r0 := httptest.NewRequest("POST", "/api/reset", nil)
	w0 := httptest.NewRecorder()
	ss := sessions.get(w0, r0)

	// 第二步：往会话里塞脏数据，模拟"完整生成一轮后"的状态
	ss.mu.Lock()
	ss.Chosen = []string{"Sheet1"}
	ss.TplPath = "D:\\fake\\模板.docx"
	ss.TplSource = "builtin"
	ss.BiLang1, ss.BiLang2 = "zh-CN", "en"
	ss.OutDir = "D:\\fake\\输出"
	ss.Outputs = []generator.PartInfo{{File: "签到表-甲班.docx", Count: 3}}
	ss.mu.Unlock()

	// 第三步：带同一 cookie 调 handleReset（修复前此处进程直接死亡）
	cookie := w0.Result().Cookies()[0] // sid
	r1 := httptest.NewRequest("POST", "/api/reset", nil)
	r1.AddCookie(cookie)
	w1 := httptest.NewRecorder()
	handleReset(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("reset 应返回 200，实际 %d", w1.Code)
	}

	// 第四步：校验脏数据清空、关键字段保留（与旧行为语义一致）
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.Chosen != nil || ss.TplPath != "" || ss.TplSource != "" ||
		ss.BiLang1 != "" || ss.BiLang2 != "" || ss.OutDir != "" || ss.Outputs != nil {
		t.Fatalf("会话脏数据未清空: TplPath=%q TplSource=%q Outputs=%v", ss.TplPath, ss.TplSource, ss.Outputs)
	}
	if ss.ID == "" || ss.Dir == "" {
		t.Fatal("ID 与工作目录 Dir 应保留，不应被清空")
	}
	if ss.HeaderRows == nil || ss.Role == nil || ss.Removed == nil {
		t.Fatal("HeaderRows/Role/Removed 应重建为空 map（不应为 nil，防止后续写入 panic）")
	}
}
