import type { UsageLog, PaginatedData } from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

export const usageLogApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<UsageLog>>(httpClient, '/admin/usage-logs', {
      params,
    }),

  getDetail: (requestId: string) =>
    apiGet<UsageLog>(httpClient, '/admin/usage-logs/detail', {
      params: { request_id: requestId },
    }),

  /**
   * 按当前筛选条件导出全部记录的下载地址。导出走浏览器直接下载而非 XHR：
   * 结果是 CSV 流，交给浏览器可避免把整份数据先读进内存。
   */
  exportUrl: (params: Record<string, unknown>) => {
    const query = new URLSearchParams();
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        query.set(key, String(value));
      }
    });
    const suffix = query.toString();
    return `/api/admin/usage-logs/export${suffix ? `?${suffix}` : ''}`;
  },
};
