import type {
  CacheReportResponse,
  DailyStat,
  HeatmapResponse,
  LedgerEntry,
  MeUsageLog,
  MergedLedgerRow,
  MyBalance,
  PaginatedData,
  ServiceStatus,
  SummaryRow,
  TokenReportResponse,
} from '@token-zen/shared';
import { apiGet, apiPost } from '@token-zen/shared/api';
import { httpClient } from './client';

export const balanceApi = {
  get: () => apiGet<MyBalance>(httpClient, '/me/balance'),
};

export const redeemApi = {
  redeem: (code: string) =>
    apiPost<LedgerEntry>(httpClient, '/me/redeem', { code }),
};

export const ledgerApi = {
  /** 默认按调用合并：一次调用的预扣与结算差额合并为一条净额。 */
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<MergedLedgerRow>>(httpClient, '/me/ledger', {
      params,
    }),
};

export const serviceStatusApi = {
  get: () => apiGet<ServiceStatus>(httpClient, '/me/service-status'),
};

export const usageApi = {
  logs: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<MeUsageLog>>(httpClient, '/me/usage-logs', {
      params,
    }),

  /** 单请求追溯详情：强制 user_id = 当前登录用户，他人 request_id 返回 404。 */
  getDetail: (requestId: string) =>
    apiGet<MeUsageLog>(httpClient, '/me/usage-logs/detail', {
      params: { request_id: requestId },
    }),

  /**
   * 按当前筛选条件导出全部记录的下载地址。导出走浏览器直接下载而非 XHR：
   * 结果是 CSV 流，交给浏览器可避免把整份数据先读进内存。会话 cookie 随同源请求自动携带。
   */
  exportUrl: (params: Record<string, unknown>) => {
    const query = new URLSearchParams();
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        query.set(key, String(value));
      }
    });
    const suffix = query.toString();
    return `/api/me/usage-logs/export${suffix ? `?${suffix}` : ''}`;
  },

  summary: (params?: {
    group_by?: 'day' | 'model' | 'key' | 'project';
    days?: number;
    start_timestamp?: number;
    end_timestamp?: number;
  }) => apiGet<SummaryRow[]>(httpClient, '/me/usage-summary', { params }),

  daily: (params?: { days?: number }) =>
    apiGet<DailyStat[]>(httpClient, '/me/usage-daily', { params }),

  cacheReport: (params?: {
    group_by?: 'day' | 'model' | 'project';
    start_timestamp?: number;
    end_timestamp?: number;
    days?: number;
  }) => apiGet<CacheReportResponse>(httpClient, '/me/cache-report', { params }),

  tokenReport: (params?: {
    group_by?: 'day' | 'model' | 'project';
    start_timestamp?: number;
    end_timestamp?: number;
    days?: number;
  }) => apiGet<TokenReportResponse>(httpClient, '/me/token-report', { params }),

  heatmap: (params?: {
    start_timestamp?: number;
    end_timestamp?: number;
    days?: number;
    model?: string;
  }) => apiGet<HeatmapResponse>(httpClient, '/me/heatmap', { params }),
};
