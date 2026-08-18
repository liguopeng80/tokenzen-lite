package main

// serve 的端到端冒烟集成测试（需 TZL_TEST_DATABASE_URL）。
//
// 零改动覆盖：用子进程方式启动真实 tzl 二进制（serve 子命令），
// 断言完整装配链路：迁移 → 首次建号 → 依赖装配 → 后台任务 → HTTP 服务 →
// /healthz 200 → SIGTERM 优雅停机（在途请求 + 用量日志刷盘）→ 退出码 0。
//
// 覆盖三类故障形态：
//   - 「服务起不来」：迁移或装配失败时进程在启动期退出，healthz 轮询拿不到 200
//   - 「某组件空指针」：装配漏字段时 NewRouter 或首个请求 panic，进程崩溃
//   - 「装配失败」：buildDeps 漏字段导致 SIGTERM 前已 panic
//
// 测试策略：
//   1. 重置 schema（DROP/CREATE public）确保走「首次建号」路径
//   2. 取临时端口、构建 tzl 二进制到 t.TempDir()
//   3. 子进程方式启动 serve，env 指向测试库 + 临时端口
//   4. 轮询 /healthz 直到 200（或进程提前退出 → fail）
//   5. SIGTERM，等待退出，断言退出码 0 与停机时长上限
//   6. 第二轮：在已建号库上再起一次，验证「root 已存在则跳过」路径
//
// 此测试由主会话串行运行（-p 1，共库 tzl_test）。

import (
	"bytes"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

const (
	// smokeHealthzDeadline 等待子进程 /healthz 就绪的上限。
	// 含迁移执行 + 建号 + 装配 + HTTP 监听的时间。
	smokeHealthzDeadline = 60 * time.Second
	// smokeShutdownDeadline 等待子进程优雅退出的上限。
	// 含 srv.Shutdown 等待 + 用量日志刷盘的时间，应明显大于子进程的 ShutdownTimeoutSec。
	smokeShutdownDeadline = 30 * time.Second
	// smokePollInterval healthz 轮询间隔。
	smokePollInterval = 200 * time.Millisecond
)

// smokeTestDatabaseURL 取测试库 URL，未设置则跳过。
func smokeTestDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TZL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 TZL_TEST_DATABASE_URL，跳过 serve 冒烟测试")
	}
	return url
}

// resetSchema 删除并重建 public schema，确保子进程走「迁移 + 首次建号」路径。
// 共享测试库可能残留上一轮测试的数据与 schema_migrations，重置后起点干净。
func resetSchema(t *testing.T, dbURL string) {
	t.Helper()
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("重置 schema 失败: %v", err)
	}
}

// buildTzlBinary 构建当前 cmd/tzl 包到 t.TempDir() 下的二进制，返回路径。
// build.Dir 显式设为测试进程的工作目录（go test 把它设为被测包源码目录），
// 避免依赖调用方的 cwd。
func buildTzlBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tzl-smoke")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = cwd
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建 tzl 二进制失败: %v\n%s", err, out)
	}
	return bin
}

// freePort 取一个临时空闲端口（竞争窗口可接受：测试串行运行）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取临时端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// waitForHealthy 轮询 /healthz 直到 200；子进程在就绪前退出则 fail。
func waitForHealthy(t *testing.T, port int, cmd *exec.Cmd) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(smokeHealthzDeadline)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		// 子进程可能在启动期就退出（迁移失败、装配 panic 等）
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("子进程在就绪前已退出，退出码 %d", cmd.ProcessState.ExitCode())
		}
		time.Sleep(smokePollInterval)
	}
	t.Fatalf("子进程在 %s 内未就绪（/healthz 未返回 200）", smokeHealthzDeadline)
}

// runServeSubprocess 启动一个 tzl serve 子进程，等待 /healthz 就绪后返回。
// 调用方负责发 SIGTERM 并等待退出。返回用于捕获输出的 buffer 与进程句柄。
func runServeSubprocess(t *testing.T, bin string, dbURL string, port int) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	envs := append(os.Environ(),
		"TZL_DATABASE_URL="+dbURL,
		"TZL_BIND_ADDR=127.0.0.1",
		fmt.Sprintf("TZL_PORT=%d", port),
		"TZL_ENV=dev",
		"TZL_LOG_LEVEL=info",
		"TZL_SHUTDOWN_TIMEOUT_SEC=10",
		"TZL_ENCRYPT_KEY=tzl-smoke-test-key",
		"TZL_ROOT_USERNAME=root",
		"TZL_ROOT_PASSWORD=smoke-root-password",
	)
	var stderr bytes.Buffer
	cmd := exec.Command(bin, "serve")
	cmd.Env = envs
	cmd.Stderr = &stderr
	// 失败时把子进程日志带出来便于定位
	t.Cleanup(func() {
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("子进程 stderr:\n%s", stderr.String())
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动子进程失败: %v", err)
	}
	// 兜底清理：测试结束时若子进程仍存活，强杀避免泄漏
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	waitForHealthy(t, port, cmd)
	return cmd, &stderr
}

// stopAndWait 发 SIGTERM，等待子进程退出，断言退出码 0 与停机时长上限。
func stopAndWait(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("发送 SIGTERM 失败: %v", err)
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("子进程应退出码 0，实际: %v", err)
		}
	case <-time.After(smokeShutdownDeadline):
		t.Fatalf("子进程在 %s 内未退出", smokeShutdownDeadline)
	}
}

// TestServeSmoke_FreshBootstrap 验证空库启动的完整链路：
// 迁移 → 首次建号（root 不存在则建）→ 装配 → 后台 → HTTP → /healthz 200 → SIGTERM → 退出码 0。
func TestServeSmoke_FreshBootstrap(t *testing.T) {
	dbURL := smokeTestDatabaseURL(t)
	resetSchema(t, dbURL)
	bin := buildTzlBinary(t)
	port := freePort(t)

	cmd, _ := runServeSubprocess(t, bin, dbURL, port)
	stopAndWait(t, cmd)
}

// TestServeSmoke_ExistingRootSkip 验证已建号库启动：root 已存在则跳过建号，
// 其余链路（装配 → HTTP → 停机）同样通过。与 FreshBootstrap 共用同一测试库，
// 串行运行：FreshBootstrap 跑完后库内已有 root，本用例验证 skip 路径。
func TestServeSmoke_ExistingRootSkip(t *testing.T) {
	dbURL := smokeTestDatabaseURL(t)
	// 不重置 schema：沿用上轮的迁移结果与 root 账号，走「已存在则跳过」路径
	bin := buildTzlBinary(t)
	port := freePort(t)

	cmd, _ := runServeSubprocess(t, bin, dbURL, port)
	stopAndWait(t, cmd)
}
