import { useCallback, useEffect, useMemo, useState } from 'react';
import { Card, Col, Empty, Row, Segmented, Spin, Statistic, Typography } from 'antd';
import {
  ArrowUpOutlined,
  ArrowDownOutlined,
} from '@ant-design/icons';
import { Bar, DualAxes } from '@ant-design/charts';
import { message } from '@token-zen/shared';
import dayjs from 'dayjs';
import type { DailyStat, OpsRankRow, OpsSummary, StatsOverview } from '@token-zen/shared';
import { dashboardApi } from '@/api/dashboard';
import { analyticsApi } from '@/api/analytics';
import { useMoney } from '@/stores/site';
import { MonitorLayout } from '@/layouts/MonitorLayout';
import { primaryPalette, semantic } from '@token-zen/shared/theme';

const { Text } = Typography;

/** 大屏深色背景下的卡片样式：半透明深色表面，避免 antd 默认深色 Card 的纯灰观感。 */
const DARK_CARD_STYLE = { body: { background: 'rgba(255,255,255,0.04)' } };
/** 大屏图表轴文字颜色。 */
const AXIS_LABEL_FILL = 'rgba(255,255,255,0.55)';
const AXIS_LABEL_FILL_STRONG = 'rgba(255,255,255,0.85)';

type TrendRange = '7' | '30' | '90';

/** 环比百分比格式化：null（上月分母为 0）显示「—」，正数前加「+」并保留 1 位。 */
function formatMom(pct: number | null | undefined): string {
  if (pct === null || pct === undefined) return '—';
  const sign = pct > 0 ? '+' : '';
  return `${sign}${pct.toFixed(1)}%`;
}

/** 由本月与上月同字段计算自定义环比（用于 margin 这种后端 mom 未覆盖的字段）。 */
function computePct(curr: number, prev: number): number | null {
  if (prev === 0) return null;
  return ((curr - prev) / prev) * 100;
}

/** 环比指示子组件：方向箭头 + 百分比；null 时显示「—」。 */
function MomIndicator({ pct }: { pct: number | null | undefined }) {
  if (pct === null || pct === undefined) {
    return <Text style={{ color: AXIS_LABEL_FILL, fontSize: 12 }}>环比 —</Text>;
  }
  const up = pct > 0;
  const flat = pct === 0;
  const color = flat ? AXIS_LABEL_FILL : up ? semantic.success : semantic.error;
  return (
    <Text style={{ color, fontSize: 12 }}>
      环比{' '}
      {!flat && (up ? <ArrowUpOutlined /> : <ArrowDownOutlined />)}
      {formatMom(pct)}
    </Text>
  );
}

/**
 * 运营分析大屏（/monitor/analytics）。
 *
 * 全部数据来自既有端点，无新增后端依赖：
 *   - 今日 KPI：GET /admin/stats/overview
 *   - 本月经营 + 排行：GET /admin/stats/ops-summary
 *   - 收入/请求趋势：GET /admin/stats/usage-daily
 *
 * 数据为日/月级粒度，挂载时拉取一次，手动刷新按钮重拉；不开启自动轮询。
 */
function AnalyticsScreen() {
  const money = useMoney();
  const [overview, setOverview] = useState<StatsOverview | null>(null);
  const [summary, setSummary] = useState<OpsSummary | null>(null);
  const [daily, setDaily] = useState<DailyStat[]>([]);
  const [trendRange, setTrendRange] = useState<TrendRange>('30');
  const [loading, setLoading] = useState(false);
  const [trendLoading, setTrendLoading] = useState(false);

  const loadHeader = useCallback(async () => {
    setLoading(true);
    try {
      const [ov, sm] = await Promise.all([dashboardApi.overview(), analyticsApi.opsSummary()]);
      setOverview(ov);
      setSummary(sm);
    } catch {
      message.error('加载运营数据失败');
    } finally {
      setLoading(false);
    }
  }, []);

  const loadTrend = useCallback(async (range: TrendRange) => {
    setTrendLoading(true);
    try {
      const data = await dashboardApi.usageDaily(Number(range));
      setDaily(data ?? []);
    } catch {
      setDaily([]);
    } finally {
      setTrendLoading(false);
    }
  }, []);

  useEffect(() => {
    loadHeader();
  }, [loadHeader]);

  useEffect(() => {
    loadTrend(trendRange);
  }, [loadTrend, trendRange]);

  const refresh = useCallback(() => {
    loadHeader();
    loadTrend(trendRange);
  }, [loadHeader, loadTrend, trendRange]);

  // ---- 派生展示数据 ----
  const todayMargin = overview ? overview.credits_charged_today - overview.credits_cost_today : 0;
  const tm = summary?.this_month;
  const pm = summary?.prev_month;
  const mom = summary?.mom;
  const monthMargin = tm ? tm.margin : 0;
  const prevMonthMargin = pm ? pm.margin : 0;
  const marginMom = tm && pm ? computePct(monthMargin, prevMonthMargin) : null;

  const trendData = useMemo(
    () =>
      daily.map((d) => ({
        // DailyStat.day 是完整 ISO 8601 时间戳（含时区偏移），归一化为纯日期再入图，
        // 否则横轴会显示出时间与时区（见 packages/shared 类型注释）。
        date: dayjs(d.day).format('YYYY-MM-DD'),
        credits_charged: d.credits_charged,
        requests: d.requests,
      })),
    [daily],
  );

  const trendConfig = {
    data: trendData,
    xField: 'date',
    axis: {
      x: {
        labelFormatter: (v: string) => {
          const parts = v.split('-');
          return parts.length === 3 ? `${parts[1]}-${parts[2]}` : v;
        },
        labelFill: AXIS_LABEL_FILL,
      },
    },
    children: [
      {
        type: 'line' as const,
        yField: 'credits_charged',
        tooltip: {
          items: [
            {
              channel: 'y' as const,
              name: '消费',
              valueFormatter: (v: number) => money.format(v),
            },
          ],
        },
        style: { lineWidth: 2, stroke: primaryPalette[500] },
        axis: {
          y: {
            title: '消费',
            position: 'left' as const,
            style: { titleFill: primaryPalette[500] },
            labelFormatter: (v: number) => money.format(v),
            labelFill: AXIS_LABEL_FILL,
          },
        },
      },
      {
        type: 'line' as const,
        yField: 'requests',
        tooltip: { items: [{ channel: 'y' as const, name: '请求数' }] },
        style: { lineWidth: 2, stroke: semantic.info },
        axis: {
          y: {
            title: '请求数',
            position: 'right' as const,
            style: { titleFill: semantic.info },
            labelFill: AXIS_LABEL_FILL,
          },
        },
      },
    ],
    height: 300,
  };

  const buildRankChart = (rows: OpsRankRow[] | undefined) => {
    const data = (rows ?? []).slice(0, 10).map((r) => ({
      label: r.group_key || `#${r.group_id}`,
      credits_charged: r.credits_charged,
      credits_cost: r.credits_cost,
    }));
    return {
      data,
      xField: 'credits_charged',
      yField: 'label',
      color: primaryPalette[500],
      axis: {
        x: {
          labelFormatter: (v: number) => money.format(v),
          labelFill: AXIS_LABEL_FILL,
        },
        y: { labelFill: AXIS_LABEL_FILL_STRONG },
      },
      tooltip: {
        items: [
          {
            channel: 'x' as const,
            name: '扣费',
            valueFormatter: (v: number) => money.format(v),
          },
        ],
      },
      scale: { x: { nice: true } },
      height: 320,
    };
  };

  const modelChartConfig = useMemo(() => buildRankChart(summary?.top_models), [summary]);
  const userChartConfig = useMemo(() => buildRankChart(summary?.top_users), [summary]);

  return (
    <MonitorLayout title="运营分析大屏" onRefresh={refresh} refreshing={loading || trendLoading}>
      <div
        data-testid="analytics-screen"
        style={{ display: 'flex', flexDirection: 'column', minHeight: '100%' }}
      >
      <Spin spinning={loading}>
        {/* 今日 KPI */}
        <Row gutter={[12, 12]}>
          <Col xs={24} sm={8}>
            <Card styles={DARK_CARD_STYLE} data-testid="kpi-today-charged">
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>今日消费</span>}
                value={money.format(overview?.credits_charged_today ?? 0)}
                valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={8}>
            <Card styles={DARK_CARD_STYLE} data-testid="kpi-today-cost">
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>今日成本</span>}
                value={money.format(overview?.credits_cost_today ?? 0)}
                valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={8}>
            <Card styles={DARK_CARD_STYLE} data-testid="kpi-today-margin">
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>今日毛利</span>}
                value={money.format(todayMargin)}
                valueStyle={{
                  color: todayMargin < 0 ? semantic.error : semantic.success,
                }}
              />
            </Card>
          </Col>
        </Row>

        {/* 本月经营 KPI：每项环比对比上月 */}
        <Row gutter={[12, 12]} style={{ marginTop: 12 }}>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={DARK_CARD_STYLE}>
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>本月扣费</span>}
                value={tm ? money.format(tm.credits_charged) : '—'}
                valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
              />
              <div style={{ marginTop: 6 }}>
                <MomIndicator pct={mom?.charged_pct} />
              </div>
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={DARK_CARD_STYLE}>
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>本月成本</span>}
                value={tm ? money.format(tm.credits_cost) : '—'}
                valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
              />
              <div style={{ marginTop: 6 }}>
                <MomIndicator pct={mom?.cost_pct} />
              </div>
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={DARK_CARD_STYLE}>
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>本月毛利</span>}
                value={tm ? money.format(monthMargin) : '—'}
                valueStyle={{
                  color: monthMargin < 0 ? semantic.error : AXIS_LABEL_FILL_STRONG,
                }}
              />
              <div style={{ marginTop: 6 }}>
                <MomIndicator pct={marginMom} />
              </div>
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={DARK_CARD_STYLE}>
              <Statistic
                title={<span style={{ color: AXIS_LABEL_FILL }}>本月充值</span>}
                value={tm ? money.format(tm.topup_credits) : '—'}
                valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
              />
              <div style={{ marginTop: 6 }}>
                <MomIndicator pct={mom?.topup_pct} />
              </div>
            </Card>
          </Col>
        </Row>
      </Spin>

      {/* 收入与请求趋势 */}
      <Card
        styles={DARK_CARD_STYLE}
        style={{ marginTop: 12 }}
        title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>消费与请求趋势</span>}
        extra={
          <Segmented<TrendRange>
            value={trendRange}
            onChange={(v) => setTrendRange(v)}
            options={[
              { label: '近 7 天', value: '7' },
              { label: '近 30 天', value: '30' },
              { label: '近 90 天', value: '90' },
            ]}
          />
        }
        data-testid="revenue-trend"
      >
        <Spin spinning={trendLoading}>
          <div style={{ minHeight: 300 }}>
            {trendData.length >= 2 ? (
              <DualAxes {...trendConfig} />
            ) : (
              <Empty
                description={
                  trendData.length === 1
                    ? '目前只有一天的数据，攒够两天才能看出趋势。'
                    : '所选时间范围内还没有调用记录。'
                }
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                style={{
                  height: 300,
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'center',
                }}
              />
            )}
          </div>
        </Spin>
      </Card>

      {/* 模型成本排行 / 用户消费排行 */}
      <Row gutter={[12, 12]} style={{ marginTop: 12 }}>
        <Col xs={24} lg={12}>
          <Card
            styles={DARK_CARD_STYLE}
            title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>模型成本 Top 10（本月扣费）</span>}
            data-testid="model-ranking"
          >
            <div style={{ minHeight: 320 }}>
              {(summary?.top_models ?? []).length > 0 ? (
                <Bar {...modelChartConfig} />
              ) : (
                <Empty
                  description="本月暂无消费记录"
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  style={{
                    height: 320,
                    display: 'flex',
                    flexDirection: 'column',
                    justifyContent: 'center',
                  }}
                />
              )}
            </div>
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card
            styles={DARK_CARD_STYLE}
            title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>用户消费 Top 10（本月扣费）</span>}
            data-testid="user-ranking"
          >
            <div style={{ minHeight: 320 }}>
              {(summary?.top_users ?? []).length > 0 ? (
                <Bar {...userChartConfig} />
              ) : (
                <Empty
                  description="本月暂无消费记录"
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  style={{
                    height: 320,
                    display: 'flex',
                    flexDirection: 'column',
                    justifyContent: 'center',
                  }}
                />
              )}
            </div>
          </Card>
        </Col>
      </Row>
      </div>
    </MonitorLayout>
  );
}

export default AnalyticsScreen;
