import type { LoginRequest, RegisterRequest, User } from '@token-zen/shared';
import { apiGet, apiPost, apiPut } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface ChangePasswordRequest {
  original_password: string;
  password: string;
}

export interface UpdateProfileRequest {
  display_name?: string;
  email?: string;
}

export const authApi = {
  login: (data: LoginRequest) =>
    apiPost<User>(httpClient, '/auth/login', data),

  logout: () => apiPost<void>(httpClient, '/auth/logout'),

  register: (data: RegisterRequest) =>
    apiPost<void>(httpClient, '/auth/register', data),

  getMe: () => apiGet<User>(httpClient, '/auth/me'),

  changePassword: (data: ChangePasswordRequest) =>
    apiPut<void>(httpClient, '/auth/password', data),

  updateProfile: (data: UpdateProfileRequest) =>
    apiPut<void>(httpClient, '/auth/profile', data),
};
