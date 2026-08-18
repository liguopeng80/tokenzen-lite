import type { SettingItem } from '@token-zen/shared';
import { apiGet, apiPut } from '@token-zen/shared/api';
import { httpClient } from './client';

export const settingsApi = {
  list: () => apiGet<SettingItem[]>(httpClient, '/admin/settings'),

  update: (key: string, value: number | boolean | string) =>
    apiPut<void>(httpClient, '/admin/settings', { key, value }),
};
