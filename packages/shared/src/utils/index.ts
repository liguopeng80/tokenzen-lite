import dayjs from 'dayjs';

/**
 * 金额展示。内部记账单位为"积分"（整数），对外统一以货币符号呈现；
 * 兑换率与符号由后端 /api/site/config 下发，禁止硬编码。详见 ./money。
 */
export * from './money';

/** 格式化积分数额（千分位）——仅供内部/调试或 CSV 原始整数列使用，不用于用户界面。 */
export function formatCredits(credits: number): string {
  return new Intl.NumberFormat('zh-CN').format(credits);
}

/**
 * 格式化时间。后端时间字段为 ISO 8601 字符串（GORM time.Time 序列化）。
 */
export function formatTime(
  value: string | null | undefined,
  format: string = 'YYYY-MM-DD HH:mm:ss',
): string {
  if (!value) return '-';
  const d = dayjs(value);
  if (!d.isValid() || d.unix() <= 0) return '-';
  return d.format(format);
}

/** 格式化 Unix 秒时间戳（仅少数聚合接口使用） */
export function formatTimestamp(
  timestamp: number,
  format: string = 'YYYY-MM-DD HH:mm:ss',
): string {
  if (!timestamp || timestamp <= 0) return '-';
  return dayjs.unix(timestamp).format(format);
}

/** 耗时展示 */
export function formatElapsedTime(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/** API Key 前缀展示（列表仅有 key_prefix，直接展示并加省略号） */
export function maskKey(keyPrefix: string): string {
  if (!keyPrefix) return '-';
  return `${keyPrefix}****`;
}

/** 大数缩写 */
export function formatNumber(num: number): string {
  if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`;
  if (num >= 1_000) return `${(num / 1_000).toFixed(1)}K`;
  return num.toString();
}

/** 复制到剪贴板 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const result = document.execCommand('copy');
    document.body.removeChild(textarea);
    return result;
  }
}

/** 导出 CSV */
export function exportToCSV(
  data: Record<string, unknown>[],
  filename: string,
  columns?: { key: string; title: string }[],
): void {
  if (data.length === 0) return;
  const keys = columns ? columns.map((c) => c.key) : Object.keys(data[0]);
  const headers = columns ? columns.map((c) => c.title) : keys;
  const csvRows = data.map((row) =>
    keys
      .map((key) => {
        const value = row[key];
        const str = value == null ? '' : String(value);
        if (str.includes(',') || str.includes('\n') || str.includes('"')) {
          return `"${str.replace(/"/g, '""')}"`;
        }
        return str;
      })
      .join(','),
  );
  const csvContent = [headers.join(','), ...csvRows].join('\n');
  const blob = new Blob(['﻿' + csvContent], {
    type: 'text/csv;charset=utf-8;',
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}
