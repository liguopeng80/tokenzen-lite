import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Card,
  Col,
  Row,
  Statistic,
  Typography,
  Spin,
  Segmented,
  DatePicker,
  Empty,
  Space,
  Tabs,
  Tag,
} from 'antd';
import { message } from '@token-zen/shared';
import { Line, DualAxes } from '@ant-design/charts';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import type { HealthPoint } from '@token-zen/shared';
import { analyticsApi } from '@/api/analytics';
import {
  semantic,
  primaryPalette,
  warmGray,
} from '@token-zen/shared/theme';
import OpsSummaryTab from './OpsSummaryTab';
import CallTypeTab from './CallTypeTab';
import CacheReportTab from './CacheReportTab';

const { Title } = Typography;
const { RangePicker } = DatePicker;

type WindowKey = '24h' | '72h' | '7d' | 'custom';

/** 各分位线的主色：p50 用品牌橙、p95 用警示黄、p99 用错误红，与「越右越严重」的直觉一致。 */
const PERCENTILE_COLORS: Record<string, string> = {
  p50: primaryPalette[500],
  p95: semantic.warning,
  p99: semantic.error,
};
const PERCENTILE_LABEL: Record<string, string> = {
  p50: 'P50',
  p95: 'P95',
  p99: 'P99',
};

/** 按桶维度格式化横轴标签：小时桶显示「MM-DD HH:00」，日桶显示「MM-DD」。 */
function bucketLabel(iso: string, bucket: 'hour' | 'day'): string {
  const d = dayjs(iso);
  return bucket === 'hour' ? d.format('MM-DD HH:00') : d.format('MM-DD');
}

/** 失败率展示为百分比，保留一位小数。 */
function failRatePercent(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

/**
 * 运维分析页：按小时/日分桶展示中继健康度——延迟分位（P50/P95/P99）、
 * 请求量与失败率的时间线。数据来自原始 usage_logs，适合近期窗口的 OPS 视角。
 */
function HealthTimelineTab() {
  const [windowKey, setWindowKey] = useState<WindowKey>('24h');
  const [customRange, setCustomRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const [points, setPoints] = useState<HealthPoint[]>([]);
  const [bucket, setBucket] = useState<'hour' | 'day'>('hour');
  const [loading, setLoading] = useState(false);

  // 由 windowKey 计算时间范围；custom 时取 RangePicker 的值。
  const range: [number, number] | null = useMemo(() => {
    if (windowKey === 'custom') {
      if (customRange && customRange[0] && customRange[1]) {
        return [
          customRange[0].startOf('hour').unix(),
          customRange[1].endOf('hour').unix(),
        ];
      }
      return null;
    }
    const now = dayjs();
    const map: Record<Exclude<WindowKey, 'custom'>, number> = {
      '24h': 24,
      '72h': 72,
      '7d': 7 * 24,
    };
    const hours = map[windowKey as Exclude<WindowKey, 'custom'>];
    return [now.subtract(hours, 'hour').unix(), now.unix()];
  }, [windowKey, customRange]);

  const load = useCallback(() => {
    if (!range) {
      setPoints([]);
      return;
    }
    setLoading(true);
    analyticsApi
      .healthTimeline({ start_timestamp: range[0], end_timestamp: range[1] })
      .then((data) => {
        setPoints(data.points ?? []);
        setBucket(data.bucket);
      })
      .catch(() => {
        message.error('加载健康度时间线失败');
        setPoints([]);
      })
      .finally(() => setLoading(false));
  }, [range]);

  useEffect(() => {
    load();
  }, [load]);

  // 窗口整体汇总：总请求、整体失败率、p95/p99 峰值。
  const summary = useMemo(() => {
    let totalReqs = 0;
    let totalFailed = 0;
    let p95Max = 0;
    let p99Max = 0;
    for (const p of points) {
      totalReqs += p.requests;
      totalFailed += p.failed;
      if (p.p95_ms > p95Max) p95Max = p.p95_ms;
      if (p.p99_ms > p99Max) p99Max = p.p99_ms;
    }
    return {
      totalReqs,
      overallFailRate: totalReqs > 0 ? totalFailed / totalReqs : 0,
      p95Max,
      p99Max,
    };
  }, [points]);

  // 延迟分位时间线：长格式 [{ time, metric, ms }]，colorField 自动分线。
  const latencyData = useMemo(
    () =>
      points.flatMap((p) =>
        (['p50', 'p95', 'p99'] as const).map((k) => ({
          time: bucketLabel(p.bucket_start, bucket),
          metric: k,
          ms: p[`${k}_ms`],
        })),
      ),
    [points, bucket],
  );

  // 请求量与失败率时间线。
  const volumeData = useMemo(
    () =>
      points.map((p) => ({
        time: bucketLabel(p.bucket_start, bucket),
        requests: p.requests,
        fail_rate: p.fail_rate * 100,
      })),
    [points, bucket],
  );

  const latencyConfig = {
    data: latencyData,
    xField: 'time',
    yField: 'ms',
    colorField: 'metric',
    scale: {
      color: {
        range: [PERCENTILE_COLORS.p50, PERCENTILE_COLORS.p95, PERCENTILE_COLORS.p99],
        domain: ['p50', 'p95', 'p99'],
      },
    },
    legend: {
      color: {
        itemMarker: 'circle',
        labelFormatter: (v: string) => PERCENTILE_LABEL[v] ?? v,
      },
    },
    axis: {
      y: { title: '延迟（ms）' },
    },
    height: 300,
  };

  const volumeConfig = {
    data: volumeData,
    xField: 'time',
    children: [
      {
        type: 'line' as const,
        yField: 'requests',
        tooltip: { items: [{ channel: 'y' as const, name: '请求数' }] },
        style: { lineWidth: 2, stroke: primaryPalette[500] },
        axis: {
          y: {
            title: '请求数',
            position: 'left' as const,
            style: { titleFill: primaryPalette[500] },
          },
        },
      },
      {
        type: 'line' as const,
        yField: 'fail_rate',
        tooltip: {
          items: [
            {
              channel: 'y' as const,
              name: '失败率',
              valueFormatter: (v: number) => `${v.toFixed(1)}%`,
            },
          ],
        },
        style: { lineWidth: 2, stroke: semantic.error },
        axis: {
          y: {
            title: '失败率（%）',
            position: 'right' as const,
            style: { titleFill: semantic.error },
            labelFormatter: (v: number) => `${v}%`,
          },
        },
      },
    ],
    height: 300,
  };

  const bucketNote =
    bucket === 'hour' ? '按服务器本地时区的小时分桶' : '按服务器本地时区的自然日分桶';

  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 20,
          flexWrap: 'wrap',
          gap: 12,
        }}
      >
        <Title level={4} style={{ marginTop: 0, marginBottom: 0 }}>
          运维分析
        </Title>
        <Space wrap data-testid="analytics-window-selector">
          <Segmented<WindowKey>
            value={windowKey}
            onChange={(v) => setWindowKey(v)}
            options={[
              { label: '近 24 小时', value: '24h' },
              { label: '近 72 小时', value: '72h' },
              { label: '近 7 天', value: '7d' },
              { label: '自定义', value: 'custom' },
            ]}
          />
          {windowKey === 'custom' && (
            <RangePicker
              showTime
              value={customRange as [Dayjs, Dayjs] | null}
              onChange={(v) => setCustomRange(v as [Dayjs | null, Dayjs | null] | null)}
              disabledDate={(current) => current && current.isAfter(dayjs())}
            />
          )}
        </Space>
      </div>

      <Spin spinning={loading}>
        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="窗口内总请求"
                value={summary.totalReqs}
                valueStyle={{ color: warmGray[900] }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="整体失败率"
                value={failRatePercent(summary.overallFailRate)}
                valueStyle={{
                  color:
                    summary.overallFailRate > 0.05
                      ? semantic.error
                      : summary.overallFailRate > 0.01
                        ? semantic.warning
                        : semantic.success,
                }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="P95 峰值延迟"
                value={summary.p95Max}
                suffix="ms"
                valueStyle={{ color: semantic.warning }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="P99 峰值延迟"
                value={summary.p99Max}
                suffix="ms"
                valueStyle={{ color: semantic.error }}
              />
            </Card>
          </Col>
        </Row>

        <Card
          style={{ marginTop: 16 }}
          title="延迟分位时间线（P50 / P95 / P99）"
          extra={<Tag color="default">{bucketNote}</Tag>}
        >
          <div data-testid="analytics-latency-chart" style={{ minHeight: 300 }}>
            {latencyData.length >= 2 ? (
              <Line {...latencyConfig} />
            ) : (
              <Empty
                description={
                  latencyData.length === 1
                    ? '目前只有一个时间桶，攒够两个才能看出趋势。'
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
        </Card>

        <Card style={{ marginTop: 16 }} title="请求量与失败率">
          <div data-testid="analytics-volume-chart" style={{ minHeight: 300 }}>
            {volumeData.length >= 1 ? (
              <DualAxes {...volumeConfig} />
            ) : (
              <Empty
                description="所选时间范围内还没有调用记录。"
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
        </Card>
      </Spin>
    </div>
  );
}

function AnalyticsPage() {
  return (
    <Tabs
      defaultActiveKey="health"
      items={[
        {
          key: 'health',
          label: '运维分析',
          children: <HealthTimelineTab />,
        },
        {
          key: 'calltype',
          label: '调用类型',
          children: <CallTypeTab />,
        },
        {
          key: 'ops',
          label: '经营分析',
          children: <OpsSummaryTab />,
        },
        {
          key: 'cache',
          label: '缓存分析',
          children: <CacheReportTab />,
        },
      ]}
    />
  );
}

export default AnalyticsPage;
