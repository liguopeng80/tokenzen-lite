import type { SiteConfig } from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

export const siteApi = {
  getConfig: () => apiGet<SiteConfig>(httpClient, '/site/config'),
};
