/**
 * 货币展示格式化。
 *
 * 系统以整数"积分"为内部无损计费单位，对外统一以货币符号呈现（默认 ¥）。
 * 兑换率 exchange_rate_credits_per_cny 与符号 currency_symbol 均由后端
 * /api/site/config 下发，禁止硬编码。积分本身不出现在用户界面。
 *
 * 精度规则（尾部一律补零）：
 * - 总额类（余额、消费合计、预算、兑换面值）：2 位小数。
 * - 明细类（单次扣费、单条流水、单价）：ceil(log10(rate)) 位小数，rate=1e6 → 6，无损。
 * - rate 未加载（≤0）时返回占位符 "—"，避免出现 NaN/Infinity。
 */

/** 货币展示上下文：由各 app 的 site store 从 SiteConfig 构造。 */
export interface MoneyContext {
  /** 每 1 货币单位对应的积分数（exchange_rate_credits_per_cny）。 */
  rate: number;
  /** 货币符号（currency_symbol）。 */
  symbol: string;
  /** 明细类展示的小数位（由 rate 推导）。 */
  detailDecimals: number;
}

/** 货币格式化器：组件经各 app 的 useMoney() 取得绑定到当前上下文的实例。 */
export interface MoneyFormatters {
  /** 总额类（2 位小数，补零）。 */
  format: (credits: number) => string;
  /** 明细类（detailDecimals 位小数，补零）。 */
  formatDetail: (credits: number) => string;
  /** 裸数值（图表轴用），不附带符号；rate 未就绪返回 0。 */
  formatValue: (credits: number) => number;
  /** 积分转货币数值（输入表单回显已有额度用）；rate 未就绪返回 0。 */
  fromCredits: (credits: number) => number;
  /** 货币金额转积分（输入表单提交用）。 */
  toCredits: (money: number) => number;
  /** 当前符号（供表头/标签使用）。 */
  symbol: string;
  /** 当前兑换率（只读，供原始运算/CSV 裸数字使用）。 */
  rate: number;
  /** 当前明细小数位（只读）。 */
  detailDecimals: number;
  /** 当前上下文是否就绪（rate > 0）。 */
  ready: boolean;
}

/** 总额类固定 2 位小数。 */
export const MONEY_TOTAL_DECIMALS = 2;

/** 占位符（rate 未加载或不可用）。 */
export const MONEY_PLACEHOLDER = '—';

/** 默认符号（上下文缺失时的兜底）。 */
export const DEFAULT_CURRENCY_SYMBOL = '¥';

/** 由兑换率推导明细小数位：使单个积分在末位非零可见的最小位数。rate=1e6 → 6。 */
export function detailDecimalsOf(rate: number): number {
  if (!rate || rate <= 1) return 0;
  let d = 0;
  for (let p = 1; p < rate; p *= 10) d += 1;
  return d;
}

/** 由 SiteConfig 字段构造货币上下文。 */
export function makeMoneyContext(rate: number, symbol: string): MoneyContext {
  return {
    rate,
    symbol: symbol || DEFAULT_CURRENCY_SYMBOL,
    detailDecimals: detailDecimalsOf(rate),
  };
}

/** 上下文未就绪时的占位格式化器。 */
const NULL_MONEY: MoneyFormatters = {
  format: () => MONEY_PLACEHOLDER,
  formatDetail: () => MONEY_PLACEHOLDER,
  formatValue: () => 0,
  fromCredits: () => 0,
  toCredits: () => 0,
  symbol: DEFAULT_CURRENCY_SYMBOL,
  rate: 0,
  detailDecimals: 0,
  ready: false,
};

/** 根据上下文构造格式化器；ctx 为 null 或 rate≤0 时返回占位器。 */
export function buildMoneyFormatters(ctx: MoneyContext | null): MoneyFormatters {
  if (!ctx || !ctx.rate || ctx.rate <= 0) return NULL_MONEY;
  const { rate, symbol, detailDecimals } = ctx;
  const money = (credits: number, decimals: number): string =>
    `${symbol}${(credits / rate).toFixed(decimals)}`;
  return {
    format: (c) => money(c, MONEY_TOTAL_DECIMALS),
    formatDetail: (c) => money(c, detailDecimals),
    formatValue: (c) => c / rate,
    fromCredits: (c) => c / rate,
    toCredits: (m) => Math.round(m * rate),
    symbol,
    rate,
    detailDecimals,
    ready: true,
  };
}

// ---- 低层纯函数（供非组件上下文或单元测试直接使用） ----

/** 积分折算货币数值，rate≤0 返回 0。 */
export function creditsToMoneyValue(credits: number, rate: number): number {
  if (!rate || rate <= 0) return 0;
  return credits / rate;
}

/** 货币金额换算积分（输入表单用），四舍五入到整数积分。rate≤0 返回 0。 */
export function moneyToCredits(money: number, rate: number): number {
  if (!rate || rate <= 0) return 0;
  return Math.round(money * rate);
}
