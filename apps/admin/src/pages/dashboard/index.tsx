import { useEffect, useMemo, useState } from 'react';
import { Card, Col, Row, Statistic, Typography, Spin, Segmented, Empty, Table } from 'antd';
import { message } from '@token-zen/shared';
import {
  DollarOutlined,
  UserOutlined,
  ThunderboltOutlined,
  SyncOutlined,
  CheckCircleOutlined,
  WarningOutlined,
  RiseOutlined,
  FallOutlined,
  CalendarOutlined,
} from '@ant-design/icons';
import { DualAxes } from '@ant-design/charts';
import dayjs from 'dayjs';
import type { StatsOverview, DailyStat, ProfitRow } from '@token-zen/shared';
import { CalendarHeatmap } from '@token-zen/shared';
import { dashboardApi } from '@/api/dashboard';
import SetupGuide from './SetupGuide';
import { useMoney } from '@/stores/site';
import { semantic, primaryPalette, warmGray, brand } from '@token-zen/shared/theme';

const { Title } = Typography;

/** Colored circle icon wrapper for stat cards */
function StatIcon({ icon, bgColor }: { icon: React.ReactNode; bgColor: string }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 40,
        height: 40,
        borderRadius: 10,
        background: bgColor,
        fontSize: 20,
      }}
    >
      {icon}
    </span>
  );
}

type TimeRange = '7' | '30' | '90';
type CalendarWindow = '90' | '183' | '365';
type ProfitGroupBy = 'channel' | 'model';

function DashboardPage() {
  const money = useMoney();
  const [overview, setOverview] = useState<StatsOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [dailyStats, setDailyStats] = useState<DailyStat[]>([]);
  const [chartLoading, setChartLoading] = useState(true);
  const [timeRange, setTimeRange] = useState<TimeRange>('7');
  const [calendarDays, setCalendarDays] = useState<CalendarWindow>('365');
  const [calendarData, setCalendarData] = useState<DailyStat[]>([]);
  const [calendarLoading, setCalendarLoading] = useState(true);
  const [profitGroupBy, setProfitGroupBy] = useState<ProfitGroupBy>('channel');
  const [profitRows, setProfitRows] = useState<ProfitRow[]>([]);
  const [profitLoading, setProfitLoading] = useState(true);

  const fetchOverview = async () => {
    setLoading(true);
    try {
      const data = await dashboardApi.overview();
      setOverview(data);
    } catch {
      message.error('加载统计数据失败');
    } finally {
      setLoading(false);
    }
  };

  const fetchDailyStats = async (days: TimeRange) => {
    setChartLoading(true);
    try {
      const data = await dashboardApi.usageDaily(Number(days));
      // 兜底空数组：后端已把空列表归一为 []，这里防的是旧版本后端返回的 null
      setDailyStats(data ?? []);
    } catch {
      setDailyStats([]);
    } finally {
      setChartLoading(false);
    }
  };

  const fetchProfit = async (groupBy: ProfitGroupBy) => {
    setProfitLoading(true);
    try {
      const data = await dashboardApi.profit(groupBy);
      setProfitRows(data ?? []);
    } catch {
      setProfitRows([]);
    } finally {
      setProfitLoading(false);
    }
  };

  const fetchCalendar = async (days: CalendarWindow) => {
    setCalendarLoading(true);
    try {
      const data = await dashboardApi.calendar(Number(days));
      setCalendarData(data ?? []);
    } catch {
      message.error('加载活跃日历失败');
      setCalendarData([]);
    } finally {
      setCalendarLoading(false);
    }
  };

  useEffect(() => {
    fetchOverview();
  }, []);

  useEffect(() => {
    fetchDailyStats(timeRange);
  }, [timeRange]);

  useEffect(() => {
    fetchCalendar(calendarDays);
  }, [calendarDays]);

  useEffect(() => {
    fetchProfit(profitGroupBy);
  }, [profitGroupBy]);

  const chartData = useMemo(
    () =>
      dailyStats.map((d) => ({
        // DailyStat.day 是完整 ISO 8601 时间戳（含时区偏移），必须先归一化为纯日期
        // 字符串再入图，否则横轴标签会显示出时间与时区偏移，见 packages/shared 类型注释
        date: dayjs(d.day).format('YYYY-MM-DD'),
        credits_charged: d.credits_charged,
        requests: d.requests,
      })),
    [dailyStats],
  );

  const dualAxesConfig = {
    data: chartData,
    xField: 'date',
    axis: {
      x: {
        labelFormatter: (v: string) => {
          const parts = v.split('-');
          return parts.length === 3 ? `${parts[1]}-${parts[2]}` : v;
        },
      },
    },
    children: [
      {
        type: 'line' as const,
        yField: 'credits_charged',
        // 悬浮提示显式命名，否则显示的是后端字段名 credits_charged。
        tooltip: {
          items: [
            {
              channel: 'y' as const,
              name: '收入',
              valueFormatter: (v: number) => money.format(v),
            },
          ],
        },
        style: { lineWidth: 2, stroke: primaryPalette[500] },
        axis: {
          y: {
            title: '收入',
            position: 'left' as const,
            style: { titleFill: primaryPalette[500] },
            labelFormatter: (v: number) => money.format(v),
          },
        },
      },
      {
        type: 'line' as const,
        yField: 'requests',
        tooltip: { items: [{ channel: 'y' as const, name: '请求次数' }] },
        style: { lineWidth: 2, stroke: semantic.info },
        axis: {
          y: {
            title: '请求次数',
            position: 'right' as const,
            style: { titleFill: semantic.info },
          },
        },
      },
    ],
    height: 300,
  };

  const todayMargin = overview
    ? overview.credits_charged_today - overview.credits_cost_today
    : 0;

  const refreshAll = () => {
    fetchOverview();
    fetchDailyStats(timeRange);
    fetchCalendar(calendarDays);
    fetchProfit(profitGroupBy);
  };

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <Title level={4} style={{ marginTop: 0, marginBottom: 0 }}>
          仪表盘
        </Title>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <button
            type="button"
            onClick={refreshAll}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              border: 'none',
              background: 'transparent',
              color: warmGray[500],
              cursor: 'pointer',
              fontSize: 14,
            }}
          >
            <SyncOutlined spin={loading} /> 刷新
          </button>
        </div>
      </div>

      <SetupGuide />

      <Spin spinning={loading}>
        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="总用户数"
                value={overview?.total_users ?? 0}
                prefix={<StatIcon icon={<UserOutlined style={{ color: semantic.success }} />} bgColor={semantic.successBg} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="今日活跃用户"
                value={overview?.active_users_today ?? 0}
                prefix={<StatIcon icon={<UserOutlined style={{ color: semantic.info }} />} bgColor={semantic.infoBg} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="今日请求数"
                value={overview?.requests_today ?? 0}
                prefix={<StatIcon icon={<ThunderboltOutlined style={{ color: brand.tertiary }} />} bgColor={semantic.warningBg} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="今日收入"
                value={money.format(overview?.credits_charged_today ?? 0)}
                prefix={<StatIcon icon={<DollarOutlined style={{ color: brand.primary }} />} bgColor={primaryPalette[50]} />}
              />
            </Card>
          </Col>
        </Row>
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="今日成本"
                value={money.format(overview?.credits_cost_today ?? 0)}
                prefix={<StatIcon icon={<DollarOutlined style={{ color: semantic.warning }} />} bgColor={semantic.warningBg} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="今日毛利"
                value={money.format(todayMargin)}
                valueStyle={todayMargin < 0 ? { color: semantic.error } : { color: semantic.success }}
                prefix={
                  <StatIcon
                    icon={todayMargin < 0 ? <FallOutlined style={{ color: semantic.error }} /> : <RiseOutlined style={{ color: semantic.success }} />}
                    bgColor={todayMargin < 0 ? semantic.errorBg : semantic.successBg}
                  />
                }
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="启用渠道"
                value={overview?.channels_enabled ?? 0}
                prefix={<StatIcon icon={<CheckCircleOutlined style={{ color: semantic.success }} />} bgColor={semantic.successBg} />}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="禁用渠道"
                value={overview?.channels_disabled ?? 0}
                prefix={
                  <StatIcon
                    icon={<WarningOutlined style={{ color: (overview?.channels_disabled ?? 0) > 0 ? semantic.error : warmGray[300] }} />}
                    bgColor={(overview?.channels_disabled ?? 0) > 0 ? semantic.errorBg : warmGray[100]}
                  />
                }
                valueStyle={(overview?.channels_disabled ?? 0) > 0 ? { color: semantic.error } : undefined}
              />
            </Card>
          </Col>
        </Row>
      </Spin>

      <Card
        style={{ marginTop: 16 }}
        title={
          <span>
            <CalendarOutlined /> 活跃日历
          </span>
        }
        extra={
          <Segmented
            value={calendarDays}
            onChange={(v) => setCalendarDays(v as CalendarWindow)}
            options={[
              { label: '近 90 天', value: '90' },
              { label: '近半年', value: '183' },
              { label: '近一年', value: '365' },
            ]}
          />
        }
      >
        <Spin spinning={calendarLoading}>
          <CalendarHeatmap data={calendarData} formatMoney={money.format} />
        </Spin>
      </Card>

      <Card
        style={{ marginTop: 16 }}
        title={
          <span>
            <ThunderboltOutlined /> 收入与请求趋势
          </span>
        }
        extra={
          <Segmented
            value={timeRange}
            onChange={(v) => setTimeRange(v as TimeRange)}
            options={[
              { label: '近 7 天', value: '7' },
              { label: '近 30 天', value: '30' },
              { label: '近 90 天', value: '90' },
            ]}
          />
        }
      >
        <Spin spinning={chartLoading}>
          {/* 只有一天数据时折线退化成一个孤立数据点，占满高度却读不出任何趋势，
              明确写出「还看不出趋势」比画一条虚线更有信息量。 */}
          {chartData.length >= 2 ? (
            <DualAxes {...dualAxesConfig} />
          ) : (
            <Empty
              description={
                chartData.length === 1
                  ? '目前只有一天的数据，攒够两天才能看出趋势。'
                  : '所选时间范围内还没有调用记录。'
              }
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              style={{ height: 300, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
            />
          )}
        </Spin>
      </Card>

      <Card
        style={{ marginTop: 16 }}
        title={
          <span>
            <DollarOutlined /> 利润分析（近 30 天）
          </span>
        }
        extra={
          <Segmented
            value={profitGroupBy}
            onChange={(v) => setProfitGroupBy(v as ProfitGroupBy)}
            options={[
              { label: '按渠道', value: 'channel' },
              { label: '按模型', value: 'model' },
            ]}
          />
        }
      >
        <Spin spinning={profitLoading}>
          {profitRows.length > 0 ? (
            <Table
              dataSource={profitRows}
              rowKey="group_key"
              pagination={false}
              size="small"
              columns={[
                { title: profitGroupBy === 'channel' ? '渠道' : '模型', dataIndex: 'group_key' },
                {
                  title: '请求数',
                  dataIndex: 'requests',
                  align: 'right' as const,
                  sorter: (a: ProfitRow, b: ProfitRow) => a.requests - b.requests,
                },
                {
                  title: '收入',
                  dataIndex: 'credits_charged',
                  align: 'right' as const,
                  render: (v: number) => money.format(v),
                  sorter: (a: ProfitRow, b: ProfitRow) => a.credits_charged - b.credits_charged,
                },
                {
                  title: '成本',
                  dataIndex: 'credits_cost',
                  align: 'right' as const,
                  render: (v: number) => money.format(v),
                  sorter: (a: ProfitRow, b: ProfitRow) => a.credits_cost - b.credits_cost,
                },
                {
                  title: '毛利',
                  dataIndex: 'margin',
                  align: 'right' as const,
                  render: (v: number) => (
                    <span style={{ color: v < 0 ? semantic.error : semantic.success, fontWeight: 600 }}>
                      {money.format(v)}
                    </span>
                  ),
                  sorter: (a: ProfitRow, b: ProfitRow) => a.margin - b.margin,
                  defaultSortOrder: 'descend' as const,
                },
              ]}
            />
          ) : (
            <Empty description="暂无利润数据" />
          )}
        </Spin>
      </Card>
    </div>
  );
}

export default DashboardPage;
