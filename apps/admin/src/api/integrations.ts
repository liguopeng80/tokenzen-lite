import type {
  CreatedServiceToken,
  Integration,
  IntegrationDetail,
  IntegrationStatus,
  PaginatedData,
  ServiceToken,
  ServiceTokenStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface CreateIntegrationPayload {
  name: string;
  /** 服务账号用户名由它拼成 svc:<slug>，创建后不可变 */
  slug: string;
}

export interface UpdateIntegrationPayload {
  name: string;
}

/**
 * 接入方与服务令牌的管理端接口（root 独占）。端点前缀 /api/admin/integrations。
 * 服务令牌明文以 tzs- 前缀，仅创建响应中返回一次，后端只存哈希。
 */
export const integrationApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<Integration>>(httpClient, '/admin/integrations/', {
      params,
    }),

  getById: (id: number) =>
    apiGet<IntegrationDetail>(httpClient, `/admin/integrations/${id}`),

  create: (data: CreateIntegrationPayload) =>
    apiPost<Integration>(httpClient, '/admin/integrations/', data),

  update: (id: number, data: UpdateIntegrationPayload) =>
    apiPut<void>(httpClient, `/admin/integrations/${id}`, data),

  /** 级联停用：接入方 + 全部令牌 + 全部用户。账务历史保留。 */
  disable: (id: number) =>
    apiPost<void>(httpClient, `/admin/integrations/${id}/disable`, {}),

  listServiceTokens: (integrationId: number) =>
    apiGet<ServiceToken[]>(
      httpClient,
      `/admin/integrations/${integrationId}/service-tokens`,
    ),

  /** 签发令牌。响应带一次性明文 token，关闭后无法再次取得。 */
  createServiceToken: (integrationId: number, name: string) =>
    apiPost<CreatedServiceToken>(
      httpClient,
      `/admin/integrations/${integrationId}/service-tokens`,
      { name },
    ),

  updateServiceTokenStatus: (
    integrationId: number,
    tokenId: number,
    status: ServiceTokenStatus,
  ) =>
    apiPut<void>(
      httpClient,
      `/admin/integrations/${integrationId}/service-tokens/${tokenId}/status`,
      { status },
    ),

  /** 软删除服务令牌，记录保留供事后追溯。 */
  deleteServiceToken: (integrationId: number, tokenId: number) =>
    apiDelete<void>(
      httpClient,
      `/admin/integrations/${integrationId}/service-tokens/${tokenId}`,
    ),
};

/** 供导入处按名引用状态枚举类型，避免到处重复字符串。 */
export type { IntegrationStatus, ServiceTokenStatus };
