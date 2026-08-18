import { useMemo } from 'react';
import { Empty, Spin, Tooltip, Typography } from 'antd';
import dayjs from 'dayjs';
import type { DailyStat } from '../types';

const { Text } = Typography;

/**
 * GitHub 贡献图风格的年度日历热力图。
 *
 * 自绘网格（不依赖 @ant-design/charts）：
 *   - 列 = 周（时间顺序，左旧右新），行 = 7 个星期（周一…周日，周首为周一）；
 *   - 渲染截至今天、向前 N 天的滚动窗口（N = data 覆盖天数，由父组件决定）；
 *   - 格子背景色按所选 metric（requests 或 credits）相对最大值的比例，
 *     在浅橙→主橙→深橙之间插值——与 Heatmap.tsx 共用同一组 primaryPalette 三段配色，
 *     保持全站视觉一致；
 *   - 鼠标悬停展示该日的日期、请求次数、Token 总量与消费（货币，由父组件注入）。
 *
 * 货币格式化经 formatMoney prop 由父组件注入（shared 包不能引各 app 的 store）；
 * 未提供时回落原始积分数。
 */
export interface CalendarHeatmapProps {
  data: DailyStat[];
  loading?: boolean;
  /** 决定配色强度字段：'requests'（默认）按请求次数，'credits' 按扣费积分 */
  metric?: 'requests' | 'credits';
  /** 货币格式化函数，由父组件传入 money.format；shared 包不能引 app store */
  formatMoney?: (credits: number) => string;
  emptyHint?: string;
}

/** 浅底色（无请求）与满强度色（最大请求）之间的线性插值。
 *  与 Heatmap.tsx 同组配色（primaryPalette 50→300→600），保持全站视觉一致。 */
export function intensityColor(ratio: number): string {
  if (ratio <= 0) return '#FFF7F0'; // primaryPalette[50]
  // 在 primaryPalette 50→600 之间取色，避免最深色对比过强。
  // 简化为两段插值：50→300（0..0.5）、300→600（0.5..1）。
  const stops = [
    [255, 247, 240], // 50
    [255, 188, 130], // 300
    [208, 101, 32], //  600
  ];
  const r = Math.min(1, Math.max(0, ratio));
  const idx = r < 0.5 ? 0 : 1;
  const t = r < 0.5 ? r * 2 : (r - 0.5) * 2;
  const a = stops[idx];
  const b = stops[idx + 1];
  const rr = Math.round(a[0] + (b[0] - a[0]) * t);
  const gg = Math.round(a[1] + (b[1] - a[1]) * t);
  const bb = Math.round(a[2] + (b[2] - a[2]) * t);
  return `rgb(${rr}, ${gg}, ${bb})`;
}

const WEEKDAY_LABELS = ['', '一', '', '三', '', '五', '']; // 行 0..6，只显示一/三/五
const MONTH_LABELS = ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'];

const DAY_KEY = 'YYYY-MM-DD';

interface WeekColumn {
  /** 该列每行（0=周一 … 6=周日）的日期，null 表示窗口外（不渲染） */
  days: (dayjs.Dayjs | null)[];
  /** 该列首日（周一）的月份，用于月份标签 */
  firstMonth: number;
}

/** 把 DailyStat[] 转为按 'YYYY-MM-DD' 索引的 Map。DailyStat.day 为完整 ISO 8601 时间戳，
 *  必须先 dayjs 归一化为纯日期 key，见 types/index.ts 的字段注释。 */
function indexByDay(data: DailyStat[]): Map<string, DailyStat> {
  const m = new Map<string, DailyStat>();
  for (const d of data) {
    const key = dayjs(d.day).format(DAY_KEY);
    // 同一日理论上只出现一次；若数据源异常出现重复，后值覆盖前值，保持确定性。
    m.set(key, d);
  }
  return m;
}

/** 由 data 推断要渲染的天数窗口：取最早与最晚日期之间的跨度（含两端）。
 *  没有数据时退化为 0，调用方据此进入空状态。 */
function inferWindowDays(data: DailyStat[]): number {
  if (data.length === 0) return 0;
  let min = Number.POSITIVE_INFINITY;
  let max = Number.NEGATIVE_INFINITY;
  for (const d of data) {
    const t = dayjs(d.day).valueOf();
    if (t < min) min = t;
    if (t > max) max = t;
  }
  return dayjs(max).diff(dayjs(min), 'day') + 1;
}

export function CalendarHeatmap({
  data,
  loading,
  metric = 'requests',
  formatMoney,
  emptyHint,
}: CalendarHeatmapProps) {
  const { byDay, maxRequests, maxCredits, totalRequests } = useMemo(() => {
    const m = indexByDay(data);
    let maxR = 0;
    let maxC = 0;
    let totalR = 0;
    for (const d of data) {
      if (d.requests > maxR) maxR = d.requests;
      if (d.credits_charged > maxC) maxC = d.credits_charged;
      totalR += d.requests;
    }
    return { byDay: m, maxRequests: maxR, maxCredits: maxC, totalRequests: totalR };
  }, [data]);

  // 周列布局：以 data 时间跨度推断窗口，缺省（数据为空时）退化为空状态。
  const { columns, cellSize } = useMemo(() => {
    const days = inferWindowDays(data);
    // 与 spec 一致：≥180 天用 12px，否则 16px；空数据按 16px 计算无意义，但给个默认避免 0。
    const size = days >= 180 ? 12 : 16;
    if (days <= 0) {
      return { columns: [] as WeekColumn[], cellSize: size };
    }
    const today = dayjs().startOf('day');
    const startDay = today.subtract(days - 1, 'day');
    // 把 startDay 倒推到本周周一：dayjs.day() 周日=0、周一=1… 周六=6，
    // 转「周一为 0」的偏移 = (day() + 6) % 7。
    const firstMonday = startDay.subtract((startDay.day() + 6) % 7, 'day');
    const cols: WeekColumn[] = [];
    let cursor = firstMonday;
    while (!cursor.isAfter(today)) {
      const daysArr: (dayjs.Dayjs | null)[] = [];
      for (let r = 0; r < 7; r++) {
        const d = cursor.add(r, 'day');
        // 早于 startDay 或晚于 today 的格子不渲染（窗口外）。
        daysArr.push(d.isBefore(startDay) || d.isAfter(today) ? null : d);
      }
      cols.push({ days: daysArr, firstMonth: cursor.month() });
      cursor = cursor.add(7, 'day');
    }
    return { columns: cols, cellSize: size };
  }, [data]);

  if (loading) {
    return (
      <div style={{ padding: 32, textAlign: 'center' }}>
        <Spin />
      </div>
    );
  }

  if (totalRequests === 0) {
    return (
      <Empty
        description={emptyHint ?? '所选区间内没有已结算的调用记录。'}
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        style={{ padding: 32 }}
      />
    );
  }

  const gap = 3;
  const labelWidth = 28;
  const monthLabelHeight = 18;
  const maxValue = metric === 'credits' ? maxCredits : maxRequests;
  // 月份标签：第一列始终显示；后续列只在月份相对上一标签列变化时显示，避免拥挤。
  let lastLabeledMonth = -1;

  const formatCreditsForDisplay = (credits: number): string =>
    formatMoney ? formatMoney(credits) : `${credits.toLocaleString()} 积分`;

  return (
    <div data-testid="calendar-heatmap" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'inline-block', minWidth: 'max-content' }}>
        {/* 月份标签行 */}
        <div style={{ display: 'flex', paddingLeft: labelWidth, height: monthLabelHeight }}>
          {columns.map((col, i) => {
            const show = i === 0 || col.firstMonth !== lastLabeledMonth;
            if (show) lastLabeledMonth = col.firstMonth;
            return (
              <div
                key={i}
                style={{
                  width: cellSize + gap,
                  fontSize: 11,
                  color: '#7A7A72',
                  lineHeight: '16px',
                  whiteSpace: 'nowrap',
                }}
              >
                {show ? MONTH_LABELS[col.firstMonth] : ''}
              </div>
            );
          })}
        </div>
        {/* 7 行星期 × N 列周 */}
        {WEEKDAY_LABELS.map((label, row) => (
          <div key={row} style={{ display: 'flex', alignItems: 'center', height: cellSize + gap }}>
            <div
              style={{
                width: labelWidth,
                fontSize: 11,
                color: '#5C5C56',
                paddingRight: 6,
                textAlign: 'right',
                lineHeight: `${cellSize + gap}px`,
              }}
            >
              {label}
            </div>
            {columns.map((col, ci) => {
              const d = col.days[row];
              if (d === null) {
                // 窗口外的格子：占位以保持列对齐，但不渲染交互元素。
                return (
                  <div
                    key={ci}
                    style={{ width: cellSize, height: cellSize, margin: gap / 2 }}
                  />
                );
              }
              const key = d.format(DAY_KEY);
              const stat = byDay.get(key);
              const value = metric === 'credits' ? stat?.credits_charged ?? 0 : stat?.requests ?? 0;
              const ratio = maxValue > 0 ? value / maxValue : 0;
              const testId = `calendar-cell-${key}`;
              return (
                <Tooltip
                  key={ci}
                  title={
                    <div>
                      <div>{key}</div>
                      <div>请求次数：{(stat?.requests ?? 0).toLocaleString()}</div>
                      <div>Token 总量：{(stat?.total_tokens ?? 0).toLocaleString()}</div>
                      <div>消费：{formatCreditsForDisplay(stat?.credits_charged ?? 0)}</div>
                    </div>
                  }
                >
                  <div
                    data-testid={testId}
                    style={{
                      width: cellSize,
                      height: cellSize,
                      margin: gap / 2,
                      borderRadius: 2,
                      backgroundColor: intensityColor(ratio),
                      border: '1px solid rgba(0,0,0,0.04)',
                      cursor: value > 0 ? 'pointer' : 'default',
                    }}
                  />
                </Tooltip>
              );
            })}
          </div>
        ))}
        {/* 图例 */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            marginTop: 12,
            paddingLeft: labelWidth,
            gap: 8,
          }}
        >
          <Text type="secondary" style={{ fontSize: 12 }}>
            少
          </Text>
          {[0, 0.25, 0.5, 0.75, 1].map((r) => (
            <div
              key={r}
              style={{
                width: 18,
                height: 12,
                borderRadius: 2,
                backgroundColor: intensityColor(r),
                border: '1px solid rgba(0,0,0,0.04)',
              }}
            />
          ))}
          <Text type="secondary" style={{ fontSize: 12 }}>
            {metric === 'credits'
              ? `多（峰值 ${formatCreditsForDisplay(maxValue)} / 天）`
              : `多（峰值 ${maxValue.toLocaleString()} 次/天）`}
          </Text>
        </div>
      </div>
    </div>
  );
}

export default CalendarHeatmap;
