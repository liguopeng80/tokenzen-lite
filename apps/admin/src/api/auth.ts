import type { LoginRequest, User } from '@token-zen/shared';
import { apiGet, apiPost, apiPut } from '@token-zen/shared/api';
import { httpClient } from './client';

export const authApi = {
  login: (data: LoginRequest) =>
    apiPost<User>(httpClient, '/auth/login', data),

  logout: () => apiPost<void>(httpClient, '/auth/logout'),

  getSelf: () => apiGet<User>(httpClient, '/auth/me'),

  changePassword: (originalPassword: string, password: string) =>
    apiPut<void>(httpClient, '/auth/password', {
      original_password: originalPassword,
      password,
    }),
};
