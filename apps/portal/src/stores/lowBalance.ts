import { useSiteStore } from './site';

/**
 * 余额预警阈值（积分）来自 /api/site/config 的 low_balance_threshold_credits，
 * 与管理员侧的低余额告警使用同一取值，禁止在页面内硬编码。
 * 返回 0 表示管理员已关闭预警。
 */
export function useLowBalanceThreshold(): number {
  return useSiteStore((s) => s.config.low_balance_threshold_credits);
}

/**
 * 判断余额是否达到预警条件。阈值为 0（关闭预警）或余额未知时一律返回 false，
 * 避免在阈值缺失时把正常余额标成预警。
 */
export function isLowBalance(balance: number | null, threshold: number): boolean {
  if (balance === null || threshold <= 0) return false;
  return balance < threshold;
}
