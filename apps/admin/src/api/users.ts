import type {
  User,
  ApiKey,
  PaginatedData,
  LedgerEntry,
  Role,
  UserStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface AdminCreateUserRequest {
  username: string;
  /** 留空时由系统生成一次性初始密码，明文在响应的 initial_password 中返回一次。 */
  password?: string;
  display_name?: string;
  email?: string;
  role?: Role;
  department_id?: number | null;
  /** 建号即发放的积分，0 表示不发放。余额为零的账号即使密钥正确也会被拒绝调用。 */
  initial_credits?: number;
}

/** 建号响应。initial_password 仅在系统代为生成时出现，且只在本次响应中返回。 */
export type AdminCreateUserResponse = User & { initial_password?: string };

export interface AdminUpdateUserRequest {
  display_name?: string;
  email?: string;
  role?: Role;
}

export const userApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<User>>(httpClient, '/admin/users/', {
      params,
    }),

  getById: (id: number) => apiGet<User>(httpClient, `/admin/users/${id}`),

  create: (data: AdminCreateUserRequest) =>
    apiPost<AdminCreateUserResponse>(httpClient, '/admin/users/', data),

  update: (id: number, data: AdminUpdateUserRequest) =>
    apiPut<void>(httpClient, `/admin/users/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/admin/users/${id}`),

  setStatus: (id: number, status: UserStatus) =>
    apiPost<void>(httpClient, `/admin/users/${id}/status`, { status }),

  resetPassword: (id: number, password: string) =>
    apiPost<void>(httpClient, `/admin/users/${id}/reset-password`, {
      password,
    }),

  /** amount 正数为分配、负数为扣回；note 必填 */
  adjustCredits: (id: number, amount: number, note: string) =>
    apiPost<LedgerEntry>(httpClient, `/admin/users/${id}/credits`, {
      amount,
      note,
    }),

  listKeys: (id: number) =>
    apiGet<PaginatedData<ApiKey>>(httpClient, `/admin/users/${id}/keys`, {
      params: { page: 1, page_size: 100 },
    }),
};
