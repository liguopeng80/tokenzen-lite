import type { StatsOverview, DailyStat, ProfitRow, SetupStatus } from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

export const dashboardApi = {
  overview: () => apiGet<StatsOverview>(httpClient, '/admin/stats/overview'),

  setupStatus: () => apiGet<SetupStatus>(httpClient, '/admin/setup-status'),

  usageDaily: (days: number = 30) =>
    apiGet<DailyStat[]>(httpClient, '/admin/stats/usage-daily', {
      params: { days },
    }),

  /** 日历热力图（M1）：rollup 化的按日用量，支持 365 天回看。 */
  calendar: (days: number = 365) =>
    apiGet<DailyStat[]>(httpClient, '/admin/stats/calendar', {
      params: { days },
    }),

  profit: (groupBy: 'channel' | 'model', from?: number, to?: number) =>
    apiGet<ProfitRow[]>(httpClient, '/admin/stats/profit', {
      params: { group_by: groupBy, from, to },
    }),
};
