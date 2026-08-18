import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
} from 'axios';
import { message } from '../feedback';
import type { ApiResponse } from '../types';

// 前端"已登录"标记（仅路由守卫用；真正的鉴权凭据是 session cookie）
const USER_ID_KEY = 'tzl_user_id';

function getUserId(): string | null {
  return localStorage.getItem(USER_ID_KEY);
}

export function setUserId(id: number): void {
  localStorage.setItem(USER_ID_KEY, String(id));
}

export function clearUserId(): void {
  localStorage.removeItem(USER_ID_KEY);
}

/**
 * 后端在 4xx/5xx 时把拒绝原因写在响应信封的 message 里（面向使用者的完整中文句子），
 * 而 axios 生成的 error.message 是 "Request failed with status code 400" 这类英文串。
 * 取出信封里的原因，取不到时保留 axios 原文。
 */
export function backendErrorMessage(error: unknown): string | null {
  const body = (error as { response?: { data?: unknown } })?.response?.data;
  if (!body || typeof body !== 'object') return null;
  const { message: msg } = body as { message?: unknown };
  return typeof msg === 'string' && msg.trim() !== '' ? msg : null;
}

export function createHttpClient(baseURL: string = '/api'): AxiosInstance {
  const client = axios.create({
    baseURL,
    timeout: 30000,
    withCredentials: true,
    headers: {
      'Content-Type': 'application/json',
    },
  });

  // 统一处理：把后端的拒绝原因提到 error.message；401 另清登录标记并跳转登录页
  client.interceptors.response.use(
    (response) => response,
    (error) => {
      const reason = backendErrorMessage(error);
      if (reason) {
        error.message = reason;
      }
      if (error.response?.status === 401) {
        const wasLoggedIn = !!getUserId();
        clearUserId();
        const currentPath = window.location.pathname;
        if (!currentPath.includes('/login')) {
          if (wasLoggedIn) {
            message.warning('登录已过期，请重新登录');
          }
          setTimeout(() => window.location.replace('/login'), 300);
        }
      }
      return Promise.reject(error);
    },
  );

  return client;
}

// 从统一信封提取 data；success=false 时抛出后端 message
export function extractData<T>(response: { data: ApiResponse<T> }): T {
  const { data: body } = response;
  if (!body.success) {
    throw new Error(body.message || '请求失败');
  }
  return body.data as T;
}

export async function apiGet<T>(
  client: AxiosInstance,
  url: string,
  config?: AxiosRequestConfig,
): Promise<T> {
  const response = await client.get<ApiResponse<T>>(url, config);
  return extractData(response);
}

export async function apiPost<T>(
  client: AxiosInstance,
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  const response = await client.post<ApiResponse<T>>(url, data, config);
  return extractData(response);
}

export async function apiPut<T>(
  client: AxiosInstance,
  url: string,
  data?: unknown,
  config?: AxiosRequestConfig,
): Promise<T> {
  const response = await client.put<ApiResponse<T>>(url, data, config);
  return extractData(response);
}

export async function apiDelete<T>(
  client: AxiosInstance,
  url: string,
  config?: AxiosRequestConfig,
): Promise<T> {
  const response = await client.delete<ApiResponse<T>>(url, config);
  return extractData(response);
}
