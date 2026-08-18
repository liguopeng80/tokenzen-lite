import { useMemo } from 'react';
import { Empty, Spin, Tooltip, Typography } from 'antd';
import type { HeatmapCell } from '../types';

const { Text } = Typography;

/**
 * 7×24 周×时活跃时段热力图。
 *
 * 自绘网格（不依赖 @ant-design/charts 的 Heatmap）：
 *   - 行 = 星期（0=周日 .. 6=周六），列 = 小时（0..23）；
 *   - 单元格背景色按 requests 相对最大值的比例在浅橙→主橙之间插值；
 *   - 鼠标悬停展示该格的星期、时段、请求次数与扣费积分。
 *
 * 后端只返回产生数据的格子，组件内部补零，使无数据时段也以最浅色显示。
 */
export interface HeatmapProps {
  cells: HeatmapCell[];
  loading?: boolean;
  /** 渲染数值时是否显示人民币折算提示等附加信息；默认只显示请求次数与积分 */
  emptyHint?: string;
}

const WEEKDAY_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

/** 浅底色（无请求）与满强度色（最大请求）之间的线性插值。 */
function intensityColor(ratio: number): string {
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

export function Heatmap({ cells, loading, emptyHint }: HeatmapProps) {
  // 把 cells 填进 [7][24] 矩阵；同时记录最大请求数用于强度归一化。
  const { grid, maxRequests, totalRequests } = useMemo(() => {
    const g: HeatmapCell[][] = Array.from({ length: 7 }, () =>
      Array.from({ length: 24 }, () => ({ day_of_week: 0, hour: 0, requests: 0, credits_charged: 0 })),
    );
    let max = 0;
    let total = 0;
    for (const c of cells) {
      const dow = c.day_of_week;
      const hour = c.hour;
      if (dow < 0 || dow > 6 || hour < 0 || hour > 23) continue;
      g[dow][hour] = c;
      if (c.requests > max) max = c.requests;
      total += c.requests;
    }
    return { grid: g, maxRequests: max, totalRequests: total };
  }, [cells]);

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

  const cellSize = 30;
  const labelWidth = 48;
  // 构造表头：0..23
  const hours = Array.from({ length: 24 }, (_, i) => i);

  return (
    <div data-testid="heatmap-grid" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'inline-block', minWidth: labelWidth + 24 * cellSize + 16 }}>
        {/* 小时表头 */}
        <div style={{ display: 'flex', paddingLeft: labelWidth }}>
          {hours.map((h) => (
            <div
              key={h}
              style={{
                width: cellSize,
                textAlign: 'center',
                fontSize: 11,
                color: '#7A7A72',
                lineHeight: '20px',
              }}
            >
              {h % 3 === 0 ? h : ''}
            </div>
          ))}
        </div>
        {/* 7 行星期 */}
        {grid.map((row, dow) => (
          <div key={dow} style={{ display: 'flex', alignItems: 'center', height: cellSize + 2 }}>
            <div
              style={{
                width: labelWidth,
                fontSize: 12,
                color: '#5C5C56',
                paddingRight: 8,
                textAlign: 'right',
              }}
            >
              {WEEKDAY_LABELS[dow]}
            </div>
            {row.map((cell, hour) => {
              const ratio = maxRequests > 0 ? cell.requests / maxRequests : 0;
              return (
                <Tooltip
                  key={hour}
                  title={
                    <div>
                      <div>
                        {WEEKDAY_LABELS[dow]} {String(hour).padStart(2, '0')}:00–
                        {String(hour + 1).padStart(2, '0')}:00
                      </div>
                      <div>请求次数：{cell.requests.toLocaleString()}</div>
                      <div>扣费积分：{cell.credits_charged.toLocaleString()}</div>
                    </div>
                  }
                >
                  <div
                    data-testid={`heatmap-cell-${dow}-${hour}`}
                    style={{
                      width: cellSize,
                      height: cellSize,
                      margin: 1,
                      borderRadius: 3,
                      backgroundColor: intensityColor(ratio),
                      border: '1px solid rgba(0,0,0,0.04)',
                      cursor: cell.requests > 0 ? 'pointer' : 'default',
                    }}
                  />
                </Tooltip>
              );
            })}
          </div>
        ))}
        {/* 图例 */}
        <div style={{ display: 'flex', alignItems: 'center', marginTop: 12, paddingLeft: labelWidth, gap: 8 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            少
          </Text>
          {[0, 0.25, 0.5, 0.75, 1].map((r) => (
            <div
              key={r}
              style={{
                width: 20,
                height: 14,
                borderRadius: 2,
                backgroundColor: intensityColor(r),
                border: '1px solid rgba(0,0,0,0.04)',
              }}
            />
          ))}
          <Text type="secondary" style={{ fontSize: 12 }}>
            多（峰值 {maxRequests.toLocaleString()} 次/格）
          </Text>
        </div>
      </div>
    </div>
  );
}

export default Heatmap;
