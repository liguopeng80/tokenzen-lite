import type { ApiKey, CreatedApiKey, KeyStatus, PaginatedData } from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface CreateApiKeyRequest {
  name: string;
  credit_limit?: number;
  expires_at?: string;
  allowed_models?: string[];
  allowed_ips?: string[];
  /** 该 Key 单自然日累计扣费上限（积分，0 = 不限） */
  daily_spend_limit?: number;
  /** 归属项目 ID，null/省略表示不归属项目 */
  project_id?: number | null;
}

export interface UpdateApiKeyRequest {
  name?: string;
  status?: KeyStatus;
  credit_limit?: number;
  clear_limit?: boolean;
  expires_at?: string;
  clear_expires?: boolean;
  allowed_models?: string[];
  allowed_ips?: string[];
  daily_spend_limit?: number;
  /** 清除 Key 每日花费上限（置为 0 = 不限） */
  clear_daily_limit?: boolean;
  /** 归属项目 ID，非空时改挂到该项目 */
  project_id?: number;
  /** 清除项目归属（置为未归属） */
  clear_project?: boolean;
}

export const keysApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<ApiKey>>(httpClient, '/me/keys/', {
      params,
    }),

  getById: (id: number) => apiGet<ApiKey>(httpClient, `/me/keys/${id}`),

  create: (data: CreateApiKeyRequest) =>
    apiPost<CreatedApiKey>(httpClient, '/me/keys/', data),

  update: (id: number, data: UpdateApiKeyRequest) =>
    apiPut<void>(httpClient, `/me/keys/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/me/keys/${id}`),
};
