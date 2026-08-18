import { useSiteStore } from '@/stores/site';

/**
 * 解析接入指引展示的 API Base URL。
 *
 * 取值优先级：
 * 1. 管理员在系统设置里配置的对外 API 基址（`server_address`，随 /api/site/config 下发）。
 *    生产环境的 /v1 通常挂在独立域名（如 api.example.com），与用户端站点不同源，
 *    只有管理员配置的地址是准确的。
 * 2. 构建期注入的 VITE_API_BASE_URL。
 * 3. 浏览器当前站点地址；本机开发时端口改写为后端端口。
 */
export function resolveBaseUrl(
  serverAddress: string,
  envBaseUrl: string | undefined,
  origin: string,
): string {
  const configured = serverAddress.trim();
  if (configured) return configured.replace(/\/+$/, '');
  if (envBaseUrl) return envBaseUrl.replace(/\/+$/, '');
  if (origin.includes('localhost') || origin.includes('127.0.0.1')) {
    return origin.replace(/:\d+$/, ':19030');
  }
  return origin;
}

/**
 * 判断展示的 Base URL 是否来自浏览器当前站点地址的推断。
 *
 * 推断值在容器部署或反向代理部署下通常不是真实的 API 入口：门户与 /v1 可能挂在
 * 不同端口或不同域名。此时指引必须明说地址靠不住，否则用户逐字照抄仍然连不通，
 * 且失败点在用户侧，会直接转为管理员的支持负担。
 */
export function isBaseUrlInferred(serverAddress: string, envBaseUrl: string | undefined): boolean {
  return serverAddress.trim() === '' && !envBaseUrl;
}

/**
 * 接入指引页使用的 API Base URL 及其来源。
 *
 * 站点配置由 App 启动时统一拉取，本 hook 只读 store，不再单独发请求。
 */
export function useBaseUrlInfo(): { baseUrl: string; inferred: boolean } {
  const serverAddress = useSiteStore((s) => s.config.server_address);
  const envBaseUrl = import.meta.env.VITE_API_BASE_URL;
  return {
    baseUrl: resolveBaseUrl(serverAddress, envBaseUrl, window.location.origin),
    inferred: isBaseUrlInferred(serverAddress, envBaseUrl),
  };
}

export function useBaseUrl(): string {
  return useBaseUrlInfo().baseUrl;
}
