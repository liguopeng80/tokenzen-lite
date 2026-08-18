import type {
  CallType,
  CacheReportResponse,
  CostByCallTypeResponse,
  HealthTimelineResponse,
  OpsSummary,
} from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface HealthTimelineParams {
  start_timestamp?: number;
  end_timestamp?: number;
  /** 显式覆盖；不传时后端按窗口长度自动选（≤7 天 hour，超过 day） */
  bucket?: 'hour' | 'day';
  model?: string;
  channel_id?: number;
}

export interface CostByCallTypeParams {
  start_timestamp?: number;
  end_timestamp?: number;
}

export interface CacheReportParams {
  group_by?: 'day' | 'model' | 'project' | 'channel';
  start_timestamp?: number;
  end_timestamp?: number;
  user_id?: number;
  department_id?: number;
  project_id?: number;
  channel_id?: number;
}

export const analyticsApi = {
  /** 运维分析：按小时/日分桶的延迟分位与失败率时间线。 */
  healthTimeline: (params: HealthTimelineParams = {}) =>
    apiGet<HealthTimelineResponse>(httpClient, '/admin/stats/health-timeline', {
      params,
    }),
  /** 经营分析：本月与上月对比、模型/用户 Top N。month 为 YYYY-MM，缺省当前月。 */
  opsSummary: (month?: string) =>
    apiGet<OpsSummary>(httpClient, '/admin/stats/ops-summary', {
      params: month ? { month } : {},
    }),
  /** 运维分析：按派生调用类型（向量嵌入/图像/流式/非流式/其他）的扣费分布。 */
  costByCallType: (params: CostByCallTypeParams = {}) =>
    apiGet<CostByCallTypeResponse>(httpClient, '/admin/stats/cost-by-calltype', {
      params,
    }),
  /** 缓存分析：整体缓存命中率与缓存 token 量，按日期/模型/项目/渠道分组。
   * 管理端分组行额外暴露 credits_cost；overall 不含成本，命中率口径与 /me/cache-report 一致。 */
  cacheReport: (params: CacheReportParams = {}) =>
    apiGet<CacheReportResponse>(httpClient, '/admin/stats/cache-report', {
      params,
    }),
};

/** 调用类型在 UI 上的固定展示顺序（与扣费降序无关，便于稳定对比）。 */
export const CALL_TYPE_ORDER: CallType[] = [
  'stream',
  'non_stream',
  'embedding',
  'image',
  'other',
];
