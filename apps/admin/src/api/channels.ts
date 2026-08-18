import type {
  Channel,
  ChannelCost,
  PaginatedData,
  Provider,
  ChannelProtocol,
  ChannelStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface ChannelPayload {
  name: string;
  provider: Provider;
  protocol: ChannelProtocol;
  base_url: string;
  /** 明文；编辑时留空 = 不更换 */
  api_key?: string;
  models: string[];
  model_mapping: Record<string, string>;
  priority?: number;
  weight?: number;
  param_override?: Record<string, unknown>;
  header_override?: Record<string, string>;
  test_model?: string;
}

export interface ChannelTestResult {
  ok: boolean;
  latency_ms: number;
  message: string;
}

export const channelApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<Channel>>(httpClient, '/admin/channels/', {
      params,
    }),

  getById: (id: number) => apiGet<Channel>(httpClient, `/admin/channels/${id}`),

  create: (data: ChannelPayload) =>
    apiPost<Channel>(httpClient, '/admin/channels/', data),

  update: (id: number, data: ChannelPayload) =>
    apiPut<void>(httpClient, `/admin/channels/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/admin/channels/${id}`),

  setStatus: (id: number, status: ChannelStatus) =>
    apiPut<void>(httpClient, `/admin/channels/${id}/status`, { status }),

  test: (id: number, model?: string) =>
    apiPost<ChannelTestResult>(
      httpClient,
      `/admin/channels/${id}/test${model ? `?model=${encodeURIComponent(model)}` : ''}`,
    ),

  getCosts: (id: number) =>
    apiGet<ChannelCost[]>(httpClient, `/admin/channels/${id}/costs`),

  setCosts: (id: number, costs: Omit<ChannelCost, 'id' | 'channel_id' | 'updated_at'>[]) =>
    apiPut<void>(httpClient, `/admin/channels/${id}/costs`, { costs }),
};
