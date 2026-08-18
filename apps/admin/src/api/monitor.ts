import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

/** 进程内运行指标快照：GET /admin/stats/runtime。
 * 由 obs 模块导出，运维大屏（M3-fe-2）消费；本任务先建好 client 与类型。 */

export interface GaugeValue {
  name: string;
  value: number;
}

export interface CounterValue {
  name: string;
  labels: Record<string, string>;
  value: number;
}

export interface HistogramValue {
  name: string;
  labels: Record<string, string>;
  count: number;
  sum: number;
  p50: number;
  p95: number;
  p99: number;
}

export interface RuntimeSnapshot {
  generated_at: string;
  gauges: GaugeValue[];
  counters: CounterValue[];
  histograms: HistogramValue[];
}

export const monitorApi = {
  runtime: () => apiGet<RuntimeSnapshot>(httpClient, '/admin/stats/runtime'),
};
