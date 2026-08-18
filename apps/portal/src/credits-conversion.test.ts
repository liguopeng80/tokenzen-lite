import { describe, it, expect } from 'vitest';
import {
  buildMoneyFormatters,
  makeMoneyContext,
  creditsToMoneyValue,
  moneyToCredits,
  detailDecimalsOf,
} from '@token-zen/shared/utils';

/**
 * 货币展示格式化测试。
 *
 * 精度规则：总额 2 位、明细 ceil(log10(rate)) 位（rate=1e6 → 6，无损）；尾部补零。
 * 兑换率未就绪（≤0）时返回占位符 "—"，杜绝 NaN 与历史回归（P2-8：兜底值放大十倍）。
 */

const cny = (rate: number, symbol = '¥') =>
  buildMoneyFormatters(makeMoneyContext(rate, symbol));

describe('creditsToMoneyValue', () => {
  it('兑换率与积分同量级时折算 1', () => {
    expect(creditsToMoneyValue(1_000_000, 1_000_000)).toBe(1);
  });

  it('零/负兑换率均返回 0（防御路径）', () => {
    expect(creditsToMoneyValue(1_000_000, 0)).toBe(0);
    expect(creditsToMoneyValue(1_000_000, -1)).toBe(0);
    expect(creditsToMoneyValue(0, 0)).toBe(0);
  });
});

describe('moneyToCredits', () => {
  it('货币转积分四舍五入', () => {
    expect(moneyToCredits(1, 1_000_000)).toBe(1_000_000);
    expect(moneyToCredits(0.0000005, 1_000_000)).toBe(1); // 0.5 积分 → 进位到 1
  });

  it('零/负兑换率返回 0', () => {
    expect(moneyToCredits(10, 0)).toBe(0);
  });
});

describe('detailDecimalsOf', () => {
  it('默认兑换率 1e6 → 6 位', () => {
    expect(detailDecimalsOf(1_000_000)).toBe(6);
  });

  it('非十的幂 1.5e6 → 7 位', () => {
    expect(detailDecimalsOf(1_500_000)).toBe(7);
  });
});

describe('MoneyFormatters.format（总额 2 位）', () => {
  it('兑换率未就绪输出占位符', () => {
    expect(cny(0).format(1_000_000)).toBe('—');
    expect(cny(0).format(123_456_789)).toBe('—');
  });

  it('常规金额输出两位小数并补零', () => {
    expect(cny(1_000_000).format(500_000)).toBe('¥0.50');
    expect(cny(1_000_000).format(3_000_000)).toBe('¥3.00');
  });

  it('非整数余额按两位小数舍入：12,345,678 积分 → ¥12.35', () => {
    expect(cny(1_000_000).format(12_345_678)).toBe('¥12.35');
  });

  it('符号随设置切换', () => {
    expect(cny(1_000_000, '$').format(3_000_000)).toBe('$3.00');
  });

  it('回归锚点——旧兜底值 100_000 会放大十倍（故兜底率必须为 0）', () => {
    expect(cny(100_000).format(1_000_000)).toBe('¥10.00');
    expect(cny(1_000_000).format(1_000_000)).toBe('¥1.00');
  });
});

describe('MoneyFormatters.formatDetail（明细 6 位）', () => {
  it('单积分无损 6 位', () => {
    expect(cny(1_000_000).formatDetail(1)).toBe('¥0.000001');
  });

  it('尾部补零', () => {
    expect(cny(1_000_000).formatDetail(500)).toBe('¥0.000500');
    expect(cny(1_000_000).formatDetail(100)).toBe('¥0.000100');
  });

  it('兑换率未就绪输出占位符', () => {
    expect(cny(0).formatDetail(500)).toBe('—');
  });
});

describe('MoneyFormatters.formatValue（图表裸数值）', () => {
  it('不带符号', () => {
    expect(cny(1_000_000).formatValue(3_000_000)).toBe(3);
    expect(cny(0).formatValue(3_000_000)).toBe(0);
  });
});

describe('MoneyFormatters.fromCredits（输入回显）', () => {
  it('积分转货币数值，与 toCredits 互逆', () => {
    const f = cny(1_000_000);
    expect(f.fromCredits(3_000_000)).toBe(3);
    expect(f.toCredits(f.fromCredits(3_000_000))).toBe(3_000_000);
  });
});
