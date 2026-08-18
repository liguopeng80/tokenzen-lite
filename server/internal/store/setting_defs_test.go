package store

import "testing"

// TestConcurrencySettingDefaults 固化并发准入设置项的默认值与类型，
// 防止与 docs/deployment.md 内存预算换算表脱钩的无意回改。
func TestConcurrencySettingDefaults(t *testing.T) {
	total := settingDef("max_concurrent_requests")
	if total == nil {
		t.Fatal("缺少设置项 max_concurrent_requests")
	}
	if total.Kind != SettingInt64 {
		t.Errorf("max_concurrent_requests Kind 应为 int64，实际 %s", total.Kind)
	}
	if total.Default != int64(40) {
		t.Errorf("max_concurrent_requests 默认值应为 40（512M 内存预算换算，见 docs/deployment.md），实际 %v", total.Default)
	}

	large := settingDef("max_concurrent_large_requests")
	if large == nil {
		t.Fatal("缺少设置项 max_concurrent_large_requests")
	}
	if large.Kind != SettingInt64 {
		t.Errorf("max_concurrent_large_requests Kind 应为 int64，实际 %s", large.Kind)
	}
	if large.Default != int64(2) {
		t.Errorf("max_concurrent_large_requests 默认值应为 2，实际 %v", large.Default)
	}
}

// TestConcurrencySettingValidate 并发准入设置项的取值校验：
// 负值会被 ConcurrencyGate 解释为"不限制"，等同静默关闭内存保护，必须在写入时拒绝；
// 0 保留"不限制"语义，上限 1000 防止误配出远超内存预算的量级。
func TestConcurrencySettingValidate(t *testing.T) {
	for _, key := range []string{"max_concurrent_requests", "max_concurrent_large_requests",
		"max_concurrent_requests_per_user", "max_keys_per_user"} {
		def := settingDef(key)
		if def == nil {
			t.Fatalf("缺少设置项 %s", key)
		}
		if def.Validate == nil {
			t.Fatalf("%s 必须定义 Validate：负值会关闭并发准入的内存保护", key)
		}
		for _, bad := range []int64{-1, -100, 1001} {
			if err := def.Validate(bad); err == nil {
				t.Errorf("%s Validate(%d) 应拒绝，实际通过", key, bad)
			}
		}
		for _, ok := range []int64{0, 1, 40, 1000} {
			if err := def.Validate(ok); err != nil {
				t.Errorf("%s Validate(%d) 应通过，实际拒绝: %v", key, ok, err)
			}
		}
	}
}

// TestRateLimitSettingValidate 限流设置项的取值校验：
// 负值会被限流器解释为"不限流"，等同静默关闭限流保护，必须在写入时拒绝；
// 0 保留"不限流"语义，上限 100000 防止误配出无意义的量级。
func TestRateLimitSettingValidate(t *testing.T) {
	for _, key := range []string{"rate_limit_per_key_rpm", "rate_limit_per_user_rpm"} {
		def := settingDef(key)
		if def == nil {
			t.Fatalf("缺少设置项 %s", key)
		}
		if def.Validate == nil {
			t.Fatalf("%s 必须定义 Validate：负值会静默关闭限流保护", key)
		}
		for _, bad := range []int64{-1, -100, 100001} {
			if err := def.Validate(bad); err == nil {
				t.Errorf("%s Validate(%d) 应拒绝，实际通过", key, bad)
			}
		}
		for _, ok := range []int64{0, 1, 120, 240, 100000} {
			if err := def.Validate(ok); err != nil {
				t.Errorf("%s Validate(%d) 应通过，实际拒绝: %v", key, ok, err)
			}
		}
	}
}

// TestServerAddressSettingValidate 对外 API 基址的取值校验：
// 该值直接展示在用户端接入指引里，写错会让用户按指引配置的客户端连不上；
// 空串保留"未配置"语义，由前端按当前站点地址推断。
func TestServerAddressSettingValidate(t *testing.T) {
	def := settingDef("server_address")
	if def == nil {
		t.Fatal("缺少设置项 server_address")
	}
	if def.Kind != SettingString {
		t.Errorf("server_address Kind 应为 string，实际 %s", def.Kind)
	}
	if def.Default != "" {
		t.Errorf("server_address 默认值应为空串（未配置），实际 %v", def.Default)
	}
	for _, ok := range []string{"", "   ", "https://api.example.com", "http://192.168.1.10:19030"} {
		if err := def.Validate(ok); err != nil {
			t.Errorf("server_address Validate(%q) 应通过，实际拒绝: %v", ok, err)
		}
	}
	for _, bad := range []string{"api.example.com", "ftp://api.example.com",
		"https://api.example.com/", "https://"} {
		if err := def.Validate(bad); err == nil {
			t.Errorf("server_address Validate(%q) 应拒绝，实际通过", bad)
		}
	}
}
