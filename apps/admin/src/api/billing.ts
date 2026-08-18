import type {
  Redemption,
  RedemptionBatchResult,
  LedgerEntry,
  PaginatedData,
  RedemptionStoredStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface RedemptionBatchRequest {
  count: number;
  credits: number;
  name: string;
  expires_at?: string;
}

export const redemptionApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<Redemption>>(httpClient, '/admin/redemptions/', {
      params,
    }),

  createBatch: (data: RedemptionBatchRequest) =>
    apiPost<RedemptionBatchResult>(
      httpClient,
      '/admin/redemptions/batch',
      data,
    ),

  // 只接受存储态：expired 是后端推导的展示态，写回没有对应的落库字段。
  setStatus: (id: number, status: RedemptionStoredStatus) =>
    apiPut<void>(httpClient, `/admin/redemptions/${id}/status`, { status }),
};

export const ledgerApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<LedgerEntry>>(httpClient, '/admin/ledger', {
      params,
    }),
};
