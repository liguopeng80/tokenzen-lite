package config

import (
	"strings"
	"testing"
)

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TZL_DATABASE_URL", "postgres://tzl:pw@localhost:5433/tzl_test")
	t.Setenv("TZL_ENV", "dev")
	t.Setenv("TZL_PORT", "")
	t.Setenv("TZL_SESSION_SECRET", "")
	t.Setenv("TZL_LOG_LEVEL", "")
}

func TestLoadDefaults(t *testing.T) {
	setBaseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if cfg.Port != 19030 {
		t.Errorf("默认端口应为 19030，实际 %d", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("默认日志级别应为 info，实际 %q", cfg.LogLevel)
	}
	if cfg.Env != EnvDev {
		t.Errorf("默认环境应为 dev，实际 %q", cfg.Env)
	}
}

func TestLoadMissingDatabaseURL(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_DATABASE_URL", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TZL_DATABASE_URL") {
		t.Fatalf("缺少 TZL_DATABASE_URL 应报错，实际: %v", err)
	}
}

func TestLoadProdRequiresSessionSecret(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_ENV", "prod")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TZL_SESSION_SECRET") {
		t.Fatalf("生产环境缺 SessionSecret 应报错，实际: %v", err)
	}
	t.Setenv("TZL_SESSION_SECRET", strings.Repeat("x", 32))
	t.Setenv("TZL_ENCRYPT_KEY", strings.Repeat("k", 32))
	if _, err := Load(); err != nil {
		t.Fatalf("提供合规 SessionSecret 与 EncryptKey 后不应报错: %v", err)
	}
}

func TestLoadProdRequiresEncryptKey(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_ENV", "prod")
	t.Setenv("TZL_SESSION_SECRET", strings.Repeat("x", 32))
	t.Setenv("TZL_ENCRYPT_KEY", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TZL_ENCRYPT_KEY") {
		t.Fatalf("生产环境缺加密密钥应报错，实际: %v", err)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_ENV", "staging")
	t.Setenv("TZL_LOG_LEVEL", "verbose")
	_, err := Load()
	if err == nil {
		t.Fatal("非法枚举值应报错")
	}
	for _, want := range []string{"TZL_ENV", "TZL_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应包含 %s，实际: %v", want, err)
		}
	}
}

func TestLoadBindAddrDefaultLoopback(t *testing.T) {
	setBaseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if cfg.BindAddr != "127.0.0.1" {
		t.Errorf("默认绑定地址应为 127.0.0.1，实际 %q", cfg.BindAddr)
	}
	if cfg.TrustedProxies != nil {
		t.Errorf("默认可信代理列表应为空，实际 %v", cfg.TrustedProxies)
	}
}

func TestLoadBindAddrInvalid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_BIND_ADDR", "not-an-ip")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TZL_BIND_ADDR") {
		t.Fatalf("非法绑定地址应报错，实际: %v", err)
	}
}

func TestLoadBindAddrExplicitExposed(t *testing.T) {
	// 显式配置对外暴露地址（IPv4 通配与 IPv6 通配）应通过校验。
	for _, addr := range []string{"0.0.0.0", "::"} {
		t.Run(addr, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("TZL_BIND_ADDR", addr)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("显式 TZL_BIND_ADDR=%q 应通过校验，实际报错: %v", addr, err)
			}
			if cfg.BindAddr != addr {
				t.Errorf("BindAddr 应为 %q，实际 %q", addr, cfg.BindAddr)
			}
		})
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8 ,::1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	want := []string{"127.0.0.1", "10.0.0.0/8", "::1"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("可信代理列表应为 %v，实际 %v", want, cfg.TrustedProxies)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Errorf("第 %d 项应为 %q，实际 %q", i, want[i], cfg.TrustedProxies[i])
		}
	}
}

func TestLoadTrustedProxiesInvalid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_TRUSTED_PROXIES", "127.0.0.1,bogus")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TZL_TRUSTED_PROXIES") {
		t.Fatalf("非法可信代理条目应报错，实际: %v", err)
	}
}

// Secure cookie 的默认值必须跟随运行环境：开发环境为明文 HTTP，置 true 会让
// 浏览器拒绝保存会话 cookie。
func TestSessionCookieSecureDefaultsToEnv(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_SESSION_COOKIE_SECURE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if cfg.SessionCookieSecure {
		t.Errorf("dev 环境默认应关闭 Secure cookie")
	}

	t.Setenv("TZL_ENV", "prod")
	t.Setenv("TZL_SESSION_SECRET", strings.Repeat("s", 32))
	t.Setenv("TZL_ENCRYPT_KEY", strings.Repeat("k", 32))
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if !cfg.SessionCookieSecure {
		t.Errorf("prod 环境默认应启用 Secure cookie")
	}
}

// 以明文 HTTP 对内网提供服务的生产部署，须能显式关闭 Secure 属性，
// 否则用户登录后立即掉登录态。
func TestSessionCookieSecureExplicitOverride(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("TZL_ENV", "prod")
	t.Setenv("TZL_SESSION_SECRET", strings.Repeat("s", 32))
	t.Setenv("TZL_ENCRYPT_KEY", strings.Repeat("k", 32))

	for _, tc := range []struct {
		raw  string
		want bool
	}{{"false", false}, {"0", false}, {"off", false}, {"true", true}, {"1", true}} {
		t.Setenv("TZL_SESSION_COOKIE_SECURE", tc.raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() 失败: %v", err)
		}
		if cfg.SessionCookieSecure != tc.want {
			t.Errorf("TZL_SESSION_COOKIE_SECURE=%q 期望 %v，实际 %v",
				tc.raw, tc.want, cfg.SessionCookieSecure)
		}
	}
}
