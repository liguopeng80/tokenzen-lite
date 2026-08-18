import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { SiteConfig } from '@token-zen/shared';
import { buildMoneyFormatters, makeMoneyContext } from '@token-zen/shared/utils';

/**
 * portal 站点配置 store 测试（报告 P2-8：兑换率兜底值放大十倍）。
 * 核心回归：/api/site/config 请求失败时，兜底兑换率必须为 0（而非旧值 100000），
 * 页面以 rate > 0 守卫隐藏折算金额。
 */

vi.mock('@/api/site', () => ({
  siteApi: {
    getConfig: vi.fn(),
  },
}));

import { siteApi } from '@/api/site';
import { useSiteStore } from './site';

const mockedGetConfig = vi.mocked(siteApi.getConfig);

/** 初始兜底状态（与 store 内 FALLBACK_SITE_CONFIG 一致） */
const INITIAL_FALLBACK: SiteConfig = {
  site_name: 'Token Zen',
  exchange_rate_credits_per_cny: 0,
  currency_symbol: '¥',
  register_enabled: false,
  server_address: '',
  low_balance_threshold_credits: 0,
  profile_display_name_editable: true,
  profile_email_editable: true,
};

const BACKEND_CONFIG: SiteConfig = {
  site_name: 'Token Zen Prod',
  exchange_rate_credits_per_cny: 1_000_000,
  currency_symbol: '¥',
  register_enabled: true,
  server_address: 'https://api.example.com',
  low_balance_threshold_credits: 100_000,
  profile_display_name_editable: true,
  profile_email_editable: true,
};

/** 与模型价格页/兑换页一致的守卫谓词 */
const showCnyGuard = (rate: number): boolean => rate > 0;

beforeEach(() => {
  // S5: zustand store 为模块级单例，每个用例前重置为初始兜底状态，
  // 保证用例可独立、乱序、重复运行。
  useSiteStore.setState({ config: INITIAL_FALLBACK, loaded: false });
  mockedGetConfig.mockReset();
});

describe('useSiteStore 初始状态', () => {
  it('S1: 兜底兑换率与预警阈值为 0，loaded 为 false，站点名保留兜底值', () => {
    const state = useSiteStore.getState();
    expect(state.config.exchange_rate_credits_per_cny).toBe(0);
    expect(state.loaded).toBe(false);
    expect(state.config.site_name).toBe('Token Zen');
    // 配置取不到时不展示无法确认的入口与提示：注册入口隐藏、余额预警关闭
    expect(state.config.register_enabled).toBe(false);
    expect(state.config.low_balance_threshold_credits).toBe(0);
  });
});

describe('useSiteStore.fetchConfig', () => {
  it('S2: API 成功后 config 被后端配置整体替换，loaded 为 true', async () => {
    mockedGetConfig.mockResolvedValueOnce(BACKEND_CONFIG);

    await useSiteStore.getState().fetchConfig();

    const state = useSiteStore.getState();
    expect(state.config).toEqual(BACKEND_CONFIG);
    expect(state.config.exchange_rate_credits_per_cny).toBe(1_000_000);
    expect(state.loaded).toBe(true);
  });

  it('S3: API 失败后不抛异常，loaded 为 true，兑换率仍为 0（核心回归）', async () => {
    mockedGetConfig.mockRejectedValueOnce(new Error('network error'));

    await expect(useSiteStore.getState().fetchConfig()).resolves.toBeUndefined();

    const state = useSiteStore.getState();
    expect(state.loaded).toBe(true);
    // 缺陷成因：旧兜底值 100000 使折算金额放大十倍；修复后必须为 0
    expect(state.config.exchange_rate_credits_per_cny).toBe(0);
    expect(state.config).toEqual(INITIAL_FALLBACK);
  });
});

describe('守卫谓词与折算的跨步集成', () => {
  it('S4a: fetchConfig 失败后守卫谓词为 false，折算金额不展示', async () => {
    mockedGetConfig.mockRejectedValueOnce(new Error('network error'));

    await useSiteStore.getState().fetchConfig();

    const rate = useSiteStore.getState().config.exchange_rate_credits_per_cny;
    expect(showCnyGuard(rate)).toBe(false);
  });

  it('S4b: fetchConfig 成功后守卫谓词为 true，展示值无十倍放大', async () => {
    mockedGetConfig.mockResolvedValueOnce(BACKEND_CONFIG);

    await useSiteStore.getState().fetchConfig();

    const rate = useSiteStore.getState().config.exchange_rate_credits_per_cny;
    expect(showCnyGuard(rate)).toBe(true);
    expect(buildMoneyFormatters(makeMoneyContext(rate, '¥')).format(3_000_000)).toBe('¥3.00');
  });
});
