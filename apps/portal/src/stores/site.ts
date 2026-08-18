import { useMemo } from 'react';
import { create } from 'zustand';
import {
  buildMoneyFormatters,
  makeMoneyContext,
  type MoneyFormatters,
  type SiteConfig,
} from '@token-zen/shared';
import { siteApi } from '@/api/site';

/**
 * /api/site/config 请求失败时的兜底展示值，避免登录页在网络异常时白屏。
 * 兑换率兜底为 0：页面以 rate > 0 作为显示折算金额的守卫（与管理端一致），
 * 兜底时隐藏折算金额而非展示可能错误的数值。
 * 注册开关与余额预警阈值同理兜底为关闭：配置取不到时不展示无法确认的入口与提示。
 */
const FALLBACK_SITE_CONFIG: SiteConfig = {
  site_name: 'Token Zen',
  exchange_rate_credits_per_cny: 0,
  currency_symbol: '¥',
  register_enabled: false,
  server_address: '',
  low_balance_threshold_credits: 0,
  // 兜底取 true：配置不可达时不误锁，用户仍可尝试提交，由后端做权威判定。
  profile_display_name_editable: true,
  profile_email_editable: true,
};

interface SiteState {
  config: SiteConfig;
  loaded: boolean;
  fetchConfig: () => Promise<void>;
}

export const useSiteStore = create<SiteState>((set) => ({
  config: FALLBACK_SITE_CONFIG,
  loaded: false,

  fetchConfig: async () => {
    try {
      const config = await siteApi.getConfig();
      set({ config, loaded: true });
    } catch {
      set({ config: FALLBACK_SITE_CONFIG, loaded: true });
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
        makeMoneyContext(config.exchange_rate_credits_per_cny, config.currency_symbol),
      ),
    [config],
  );
}

/** 货币格式化器（非响应式快照）：供模块级列定义、图表 valueFormatter 等非组件上下文使用。 */
export function getMoney(): MoneyFormatters {
  const config = useSiteStore.getState().config;
  return buildMoneyFormatters(
    makeMoneyContext(config.exchange_rate_credits_per_cny, config.currency_symbol),
  );
}
