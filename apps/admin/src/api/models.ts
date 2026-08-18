import type {
  ModelInfo,
  ModelPrice,
  ChannelCost,
  PaginatedData,
  Modality,
  BillingMode,
  ModelStatus,
} from '@token-zen/shared';
import { apiGet, apiPost, apiPut, apiDelete } from '@token-zen/shared/api';
import { httpClient } from './client';

export interface ModelPayload {
  name: string;
  display_name: string;
  description: string;
  modality: Modality;
  billing_mode: BillingMode;
  status: ModelStatus;
  tags: string;
  /** 所属厂商（受控枚举值，权威定义见 docs/glossary.md 的 Provider 节） */
  provider: string;
  /** 上下文窗口（token 数）；0 表示未知，≥1,000,000 视为 1M */
  context_window: number;
  /** 最大输出（token 数）；0 表示未知 */
  max_output: number;
  /** 能力标签（取值 vision/video/audio/reasoning，权威定义见 docs/glossary.md） */
  capabilities: string[];
  /** 模型级全局唯一对外短名，如 opus */
  alias: string;
}

export interface ModelPricePayload {
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
  audio_input_price: number;
  audio_output_price: number;
  per_call_price: number;
}

export interface PeakRulePayload {
  timezone: string;
  start_minute: number;
  end_minute: number;
  days_of_week: number[];
  multiplier_percent: number;
  enabled: boolean;
}

/** 批量导入的一条记录：模型信息 + 定价（定价必填，否则模型上架即可被零扣费调用）。 */
export interface ModelImportItem extends Partial<ModelPayload> {
  name: string;
  price: Partial<ModelPricePayload>;
}

/** 单条导入记录的处理结果，取值与后端 domain.ImportAction 一致。 */
export type ImportAction = 'created' | 'updated' | 'skipped' | 'failed';

export interface ModelImportResult {
  name: string;
  action: ImportAction;
  message: string;
}

export interface ModelImportSummary {
  created: number;
  updated: number;
  skipped: number;
  failed: number;
  results: ModelImportResult[];
}

/** 预置价目条目：厂商美元官价（微美元）+ 服务端按当前汇率折算出的积分单价。 */
export interface PresetModel {
  name: string;
  display_name: string;
  description: string;
  modality: Modality;
  billing_mode: BillingMode;
  /** 所属厂商（受控枚举值，权威定义见 docs/glossary.md） */
  provider: string;
  /** 上下文窗口（token 数）；0 表示未知 */
  context_window: number;
  /** 最大输出（token 数）；0 表示未知 */
  max_output: number;
  /** 能力标签（取值 vision/video/audio/reasoning） */
  capabilities: string[];
  /** 模型级全局唯一对外短名，如 opus；空串表示无别名 */
  alias: string;
  input_usd: number;
  output_usd: number;
  cache_read_usd: number;
  cache_write_usd: number;
  per_call_usd: number;
  price: ModelPricePayload;
}

export interface PresetProvider {
  id: string;
  name: string;
  pricing_url: string;
  models: PresetModel[];
}

export interface PresetCatalog {
  priced_at: string;
  note: string;
  markup_percent: number;
  usd_cny_rate_milli: number;
  exchange_rate_credits_per_cny: number;
  providers: PresetProvider[];
}

export const modelApi = {
  list: (params?: Record<string, unknown>) =>
    apiGet<PaginatedData<ModelInfo>>(httpClient, '/admin/models/', {
      params,
    }),

  getById: (id: number) => apiGet<ModelInfo>(httpClient, `/admin/models/${id}`),

  create: (data: ModelPayload) =>
    apiPost<ModelInfo>(httpClient, '/admin/models/', data),

  update: (id: number, data: ModelPayload) =>
    apiPut<void>(httpClient, `/admin/models/${id}`, data),

  delete: (id: number) => apiDelete<void>(httpClient, `/admin/models/${id}`),

  setPrice: (id: number, data: ModelPricePayload) =>
    apiPut<ModelPrice>(httpClient, `/admin/models/${id}/price`, data),

  setPeakRules: (id: number, rules: PeakRulePayload[]) =>
    apiPut<void>(httpClient, `/admin/models/${id}/peak-rules`, { rules }),

  getChannelCosts: (id: number) =>
    apiGet<ChannelCost[]>(httpClient, `/admin/models/${id}/channel-costs`),

  /** 批量导入模型与定价；overwrite 为 true 时覆盖已存在的同名模型。 */
  importModels: (items: ModelImportItem[], overwrite: boolean) =>
    apiPost<ModelImportSummary>(httpClient, '/admin/models/import', { items, overwrite }),

  /** 从远端 URL 拉取预置价目并导入（后端代理拉取，复用导入逻辑）。 */
  importFromUrl: (source_url: string, markup_percent: number, overwrite: boolean) =>
    apiPost<ModelImportSummary>(httpClient, '/admin/models/import-remote', {
      source_url,
      markup_percent,
      overwrite,
    }),

  /** 内置预置价目，积分单价按当前系统汇率与给定加价百分数在服务端折算。 */
  getPricingPresets: (markupPercent: number) =>
    apiGet<PresetCatalog>(httpClient, '/admin/models/pricing-presets', {
      params: { markup_percent: markupPercent },
    }),
};
