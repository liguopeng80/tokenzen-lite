import type { ModelInfo } from '@token-zen/shared';
import { apiGet } from '@token-zen/shared/api';
import { httpClient } from './client';

export const modelsApi = {
  /** 模型目录，含单价与时段倍率，仅返回已上架模型。需登录：上架清单是内部信息。 */
  list: () => apiGet<ModelInfo[]>(httpClient, '/me/models'),
};
