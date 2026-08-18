package obs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// resolveIP 让 RealIPMiddleware 处理一次请求并返回其解析出的客户端 IP 与
// 处理后请求上残留的 X-Real-IP 头。
func resolveIP(t *testing.T, trustedProxies []string, remoteAddr, realIPHeader string) (ip, header string) {
	t.Helper()
	handler := RealIPMiddleware(trustedProxies)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			ip = ClientIP(r)
			header = r.Header.Get(HeaderRealIP)
		}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	if realIPHeader != "" {
		req.Header.Set(HeaderRealIP, realIPHeader)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return ip, header
}

func TestRealIPMiddlewareNoTrustedProxies(t *testing.T) {
	// 未配置可信代理：不采信头，且删除头防止后续环节误用。
	ip, header := resolveIP(t, nil, "203.0.113.7:52011", "198.51.100.9")
	if ip != "203.0.113.7" {
		t.Errorf("应使用连接远端地址 203.0.113.7，实际 %q", ip)
	}
	if header != "" {
		t.Errorf("未配置可信代理时应删除 X-Real-IP 头，实际残留 %q", header)
	}
}

func TestRealIPMiddlewareTrustedProxy(t *testing.T) {
	ip, _ := resolveIP(t, []string{"127.0.0.1"}, "127.0.0.1:40100", "198.51.100.9")
	if ip != "198.51.100.9" {
		t.Errorf("可信代理转发时应采信 X-Real-IP，实际 %q", ip)
	}
}

func TestRealIPMiddlewareTrustedCIDR(t *testing.T) {
	ip, _ := resolveIP(t, []string{"10.0.0.0/8"}, "10.1.2.3:40100", "198.51.100.9")
	if ip != "198.51.100.9" {
		t.Errorf("可信 CIDR 内的代理应被采信，实际 %q", ip)
	}
}

func TestRealIPMiddlewareTrustedProxyIPv6(t *testing.T) {
	// 部署文档推荐 TZL_TRUSTED_PROXIES=127.0.0.1,::1：IPv6 回环代理转发的
	// IPv6 客户端地址应被采信。
	ip, _ := resolveIP(t, []string{"::1"}, "[::1]:40100", "2001:db8::9")
	if ip != "2001:db8::9" {
		t.Errorf("IPv6 可信代理转发时应采信 X-Real-IP，实际 %q", ip)
	}
}

func TestRealIPMiddlewareUntrustedRemote(t *testing.T) {
	ip, header := resolveIP(t, []string{"127.0.0.1"}, "203.0.113.7:52011", "198.51.100.9")
	if ip != "203.0.113.7" {
		t.Errorf("非可信来源伪造 X-Real-IP 不应被采信，实际 %q", ip)
	}
	if header != "" {
		t.Errorf("非可信来源的 X-Real-IP 头应被删除，实际残留 %q", header)
	}
}

func TestRealIPMiddlewareTrustedProxyInvalidHeader(t *testing.T) {
	ip, header := resolveIP(t, []string{"127.0.0.1"}, "127.0.0.1:40100", "not-an-ip")
	if ip != "127.0.0.1" {
		t.Errorf("头非法时应回退连接远端地址，实际 %q", ip)
	}
	if header != "" {
		t.Errorf("非法 X-Real-IP 头应被删除，实际残留 %q", header)
	}
}

func TestRealIPMiddlewareTrustedProxyNoHeader(t *testing.T) {
	ip, _ := resolveIP(t, []string{"127.0.0.1"}, "127.0.0.1:40100", "")
	if ip != "127.0.0.1" {
		t.Errorf("无 X-Real-IP 头时应使用连接远端地址，实际 %q", ip)
	}
}
