import { useMemo } from 'react';
import { create } from 'zustand';
import {
  buildMoneyFormatters,
  makeMoneyContext,
  type MoneyFormatters,
  type SiteConfig,
} from '@token-zen/shared';
import { siteApi } from '@/api/site';

interface SiteState {
  config: SiteConfig | null;
  loaded: boolean;
  fetchConfig: () => Promise<void>;
}

/** 站点配置（含积分/货币兑换率与展示符号），App 启动时拉取一次。 */
export const useSiteStore = create<SiteState>((set) => ({
  config: null,
  loaded: false,

  fetchConfig: async () => {
    try {
      const config = await siteApi.getConfig();
      set({ config, loaded: true });
    } catch {
      set({ loaded: true });
    }
  },
}));

/**
 * 货币格式化器（响应式）：订阅 site config，加载后组件自动重渲染。
 * 用户界面一律使用 format（总额 2 位）/ formatDetail（明细 6 位），不再直接展示积分。
 */
export function useMoney(): MoneyFormatters {
  const config = useSiteStore((s) => s.config);
  return useMemo(
    () =>
      buildMoneyFormatters(
        config
          ? makeMoneyContext(config.exchange_rate_credits_per_cny, config.currency_symbol)
          : null,
      ),
    [config],
  );
}

/** 货币格式化器（非响应式快照）：供模块级列定义、图表 valueFormatter 等非组件上下文使用。 */
export function getMoney(): MoneyFormatters {
  const config = useSiteStore.getState().config;
  return buildMoneyFormatters(
    config
      ? makeMoneyContext(config.exchange_rate_credits_per_cny, config.currency_symbol)
      : null,
  );
}
