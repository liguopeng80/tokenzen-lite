package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// captureLogger 临时把 slog 默认 logger 切到内存 buffer，返回还原函数与读取缓冲的闭包。
// 测试期间用 t.Cleanup 还原，避免污染同进程其他测试。
func captureLogger(t *testing.T) (lines func() []map[string]any) {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("解析日志行失败: %v\n原始: %s", err, line)
			}
			out = append(out, m)
		}
		return out
	}
}

// RunSafe 必须吞掉 panic，不向上抛出——否则后台循环的 recover 一旦失效，
// 单项任务 panic 仍会杀死整组维护。
func TestRunSafeSwallowsPanic(t *testing.T) {
	lines := captureLogger(t)

	didRun := false
	// 直接调用，不应 panic；若 RunSafe 把 panic 透传，本测试本身会失败。
	RunSafe("test.swallow", func() {
		didRun = true
		panic("boom")
	})

	if !didRun {
		t.Fatal("fn 应当被实际调用")
	}

	// 调用方在 RunSafe 返回后继续执行，证明 panic 被吞掉。
	finished := false
	RunSafe("test.after", func() { finished = true })
	if !finished {
		t.Fatal("RunSafe 返回后调用方应能继续执行")
	}

	logs := lines()
	if len(logs) == 0 {
		t.Fatal("panic 应当被记成 ERROR 日志")
	}
	last := logs[len(logs)-1]
	if last["level"] != "ERROR" {
		t.Errorf("期望 level=ERROR，实际 %v", last["level"])
	}
	if last["task"] != "test.swallow" {
		t.Errorf("期望 task=test.swallow，实际 %v", last["task"])
	}
	if last["panic"] != "boom" {
		t.Errorf("期望 panic=boom，实际 %v", last["panic"])
	}
	stack, _ := last["stack"].(string)
	if !strings.Contains(stack, "panic") && !strings.Contains(stack, "goroutine") {
		t.Errorf("日志应包含调用栈，实际 stack=%q", stack)
	}
}

// 正常（不 panic）路径下 RunSafe 不应输出任何 ERROR 日志。
func TestRunSafeNoPanicNoErrorLog(t *testing.T) {
	lines := captureLogger(t)

	got := 0
	RunSafe("test.ok", func() { got = 42 })

	if got != 42 {
		t.Fatalf("fn 副作用应生效，got=%d", got)
	}
	for _, m := range lines() {
		if m["level"] == "ERROR" {
			t.Errorf("无 panic 时不应记 ERROR，捕获到: %v", m)
		}
	}
}

// RunSafe 在连续两轮中第一轮 panic 不影响第二轮执行——
// 这是「循环可继续」的最小化形：每一轮独立恢复，上一轮 panic 不污染下一轮。
func TestRunSafeLoopContinuesAfterPanic(t *testing.T) {
	lines := captureLogger(t)

	results := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		i := i
		RunSafe("test.loop", func() {
			if i == 1 {
				panic("middle panic")
			}
			results = append(results, i)
		})
	}

	if len(results) != 2 {
		t.Errorf("期望第 0、2 轮执行成功（结果 [0 2]），实际 results=%v", results)
	}
	// 至少有一条 ERROR 日志记录第 1 轮的 panic。
	sawPanic := false
	for _, m := range lines() {
		if m["level"] == "ERROR" && m["panic"] == "middle panic" {
			sawPanic = true
		}
	}
	if !sawPanic {
		t.Error("第 1 轮的 panic 应被记为 ERROR 日志")
	}
}

// 用 time.After 节奏的后台循环，包裹 RunSafe 后不会因单轮 panic 永久退出。
// 这是本次修复的端到端断言：协程仍存活，下一轮仍能运行。
func TestRunSafeMimicsSchedulerLoopSelfHeals(t *testing.T) {
	lines := captureLogger(t)

	ticks := make(chan int, 5)
	done := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				i++
				RunSafe("test.scheduler", func() {
					ticks <- i
					if i == 2 {
						panic("scheduled boom")
					}
				})
			}
		}
	}()

	// 收集前 4 轮：第 2 轮 panic 后循环必须继续到第 4 轮。
	collected := make([]int, 0, 4)
	for v := range ticks {
		collected = append(collected, v)
		if len(collected) == 4 {
			break
		}
	}
	close(done)

	if len(collected) != 4 {
		t.Fatalf("期望收集 4 轮（panic 后继续），实际 %d 轮: %v", len(collected), collected)
	}
	// 第 1、3、4 轮成功到达，第 2 轮 panic 仍记日志。
	sawPanic := false
	for _, m := range lines() {
		if m["level"] == "ERROR" && m["panic"] == "scheduled boom" {
			sawPanic = true
		}
	}
	if !sawPanic {
		t.Error("第 2 轮 panic 应被记为 ERROR 日志")
	}
}
