package obs

import (
	"context"
	"runtime/debug"
)

// RunSafe 执行 fn，捕获 panic 并以 ERROR 级别记录名称、panic 值与调用栈后返回——
// 不向上抛。用于后台常驻循环的每一轮：单项任务 panic 不能杀死整组维护。
func RunSafe(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			Logger(context.Background()).Error("后台任务 panic，已恢复",
				"task", name, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
}
