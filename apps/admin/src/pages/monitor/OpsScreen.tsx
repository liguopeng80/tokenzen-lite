import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Card, Col, Empty, Row, Spin, Statistic, Tag, Tooltip, Typography } from 'antd';
import { DualAxes } from '@ant-design/charts';
import dayjs from 'dayjs';
import type {
  Channel,
  ChannelStatus,
  HealthPoint,
  HealthTimelineResponse,
  StatsOverview,
  UsageLog,
} from '@token-zen/shared';
import { monitorApi, type RuntimeSnapshot } from '@/api/monitor';
import { analyticsApi } from '@/api/analytics';
import { channelApi } from '@/api/channels';
import { dashboardApi } from '@/api/dashboard';
import { usageLogApi } from '@/api/usageLogs';
import { MonitorLayout } from '@/layouts/MonitorLayout';
import { primaryPalette, semantic, warmGray } from '@token-zen/shared/theme';

const { Text } = Typography;

/** 大屏深色背景下的卡片样式：半透明深色表面，避免 antd 默认深色 Card 的纯灰观感。 */
const DARK_CARD_STYLE = { body: { background: 'rgba(255,255,255,0.04)' } };
/** 大屏图表轴文字颜色。 */
const AXIS_LABEL_FILL = 'rgba(255,255,255,0.55)';
const AXIS_LABEL_FILL_STRONG = 'rgba(255,255,255,0.85)';

/** 指标名常量（与后端 obs 模块对齐），避免裸字符串。 */
const METRIC_IN_FLIGHT = 'tzl_relay_in_flight';
const METRIC_USAGE_DROPPED = 'tzl_usage_log_dropped';
const METRIC_RELAY_REQUESTS = 'tzl_relay_requests_total';
const METRIC_CHANNEL_ATTEMPTS = 'tzl_channel_attempts_total';

/** MonitorLayout 主轮询周期（快速源）。慢源在 onRefresh 内节流到 ~60s。 */
const FAST_INTERVAL_MS = 10_000;
/** 慢源（渠道/失败日志）最小刷新间隔，实现 60s 节奏。 */
const SLOW_THROTTLE_MS = 55_000;

/** 运维大屏时间窗口：近 24 小时，按小时桶。 */
const HEALTH_WINDOW_SECONDS = 24 * 3600;
const FAILED_PAGE_SIZE = 20;
const CHANNEL_PAGE_SIZE = 1000;

/** 分位线主色：p50 品牌橙、p95 警示黄、p99 错误红；失败率用信息蓝，与延迟线区分。 */
const PERCENTILE_COLORS = {
  p50: primaryPalette[500],
  p95: semantic.warning,
  p99: semantic.error,
} as const;
const FAIL_RATE_COLOR = semantic.info;

/** 渠道状态在拓扑格子上的标识色：enabled 绿、manual_disabled 灰、auto_disabled 红。 */
const STATUS_COLOR: Record<ChannelStatus, string> = {
  enabled: semantic.success,
  manual_disabled: warmGray[400],
  auto_disabled: semantic.error,
};
const STATUS_LABEL: Record<ChannelStatus, string> = {
  enabled: '正常',
  manual_disabled: '手动停用',
  auto_disabled: '自动停用',
};

/** 相对时间格式化（中文），避免引入 dayjs 插件依赖。 */
function relativeTime(iso: string): string {
  const seconds = Math.floor((Date.now() - dayjs(iso).valueOf()) / 1000);
  if (seconds < 60) return `${seconds} 秒前`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

/** 由 runtime 快照汇总指定计数器的累计值，可选按 label 值过滤。 */
function sumCounter(
  snapshot: RuntimeSnapshot | null,
  name: string,
  labelFilter?: { label: string; value: string },
): number {
  if (!snapshot) return 0;
  return snapshot.counters
    .filter((c) => {
      if (c.name !== name) return false;
      if (labelFilter && c.labels[labelFilter.label] !== labelFilter.value) return false;
      return true;
    })
    .reduce((sum, c) => sum + c.value, 0);
}

/** 由 runtime 快照读取单个 gauge 值。 */
function readGauge(snapshot: RuntimeSnapshot | null, name: string): number {
  if (!snapshot) return 0;
  return snapshot.gauges.find((g) => g.name === name)?.value ?? 0;
}

/** 顶部全局状态条：在途、RPM、失败率、活跃/异常渠道、日志丢弃。 */
function GlobalStatusBar({
  inFlight,
  rpm,
  failRate,
  overview,
  dropped,
}: {
  inFlight: number;
  rpm: number | null;
  failRate: number | null;
  overview: StatsOverview | null;
  dropped: number;
}) {
  const failRateText = failRate === null ? '—' : `${failRate.toFixed(2)}%`;
  const failRateColor =
    failRate === null
      ? AXIS_LABEL_FILL_STRONG
      : failRate >= 5
        ? semantic.error
        : failRate >= 1
          ? semantic.warning
          : semantic.success;
  return (
    <Row gutter={[12, 12]} data-testid="stat-bar">
      <Col xs={12} md={8} xl={4}>
        <Card styles={DARK_CARD_STYLE} data-testid="stat-inflight">
          <Statistic
            title={<span style={{ color: AXIS_LABEL_FILL }}>在途请求</span>}
            value={inFlight}
            valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
          />
        </Card>
      </Col>
      <Col xs={12} md={8} xl={4}>
        <Card styles={DARK_CARD_STYLE} data-testid="stat-rpm">
          <Statistic
            title={<span style={{ color: AXIS_LABEL_FILL }}>全局 RPM</span>}
            value={rpm === null ? '—' : Math.round(rpm)}
            valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
          />
        </Card>
      </Col>
      <Col xs={12} md={8} xl={4}>
        <Card styles={DARK_CARD_STYLE} data-testid="stat-fail-rate">
          <Statistic
            title={<span style={{ color: AXIS_LABEL_FILL }}>失败率</span>}
            value={failRateText}
            valueStyle={{ color: failRateColor }}
          />
        </Card>
      </Col>
      <Col xs={12} md={8} xl={4}>
        <Card styles={DARK_CARD_STYLE} data-testid="stat-channels">
          <Statistic
            title={<span style={{ color: AXIS_LABEL_FILL }}>活跃 / 异常渠道</span>}
            value={overview ? `${overview.channels_enabled} / ${overview.channels_disabled}` : '—'}
            valueStyle={{ color: AXIS_LABEL_FILL_STRONG }}
          />
        </Card>
      </Col>
      <Col xs={12} md={8} xl={4}>
        <Card styles={DARK_CARD_STYLE} data-testid="stat-dropped">
          <Statistic
            title={<span style={{ color: AXIS_LABEL_FILL }}>日志丢弃</span>}
            value={dropped}
            valueStyle={{
              color: dropped > 0 ? semantic.error : AXIS_LABEL_FILL_STRONG,
            }}
          />
        </Card>
      </Col>
    </Row>
  );
}

/** 渠道状态格子：底色按状态，副标签显示成功率（来自 channel_attempts 计数器）。 */
function ChannelCell({
  channel,
  successRate,
}: {
  channel: Channel;
  successRate: number | null;
}) {
  const color = STATUS_COLOR[channel.status];
  const rateText = successRate === null ? null : `${(successRate * 100).toFixed(1)}%`;
  const tooltip = [
    channel.name,
    STATUS_LABEL[channel.status],
    channel.provider,
    rateText ? `成功率 ${rateText}` : '无样本',
  ].join(' / ');
  return (
    <Tooltip title={tooltip}>
      <div
        style={{
          display: 'inline-flex',
          flexDirection: 'column',
          padding: '4px 8px',
          borderRadius: 4,
          background: `${color}22`,
          border: `1px solid ${color}66`,
          minWidth: 72,
        }}
      >
        <span style={{ color: AXIS_LABEL_FILL_STRONG, fontSize: 12, lineHeight: '18px' }}>
          {channel.name}
        </span>
        <span style={{ color, fontSize: 11, lineHeight: '16px' }}>
          {STATUS_LABEL[channel.status]}
          {rateText ? ` · ${rateText}` : ''}
        </span>
      </div>
    </Tooltip>
  );
}

/** 模型 ↔ 渠道拓扑：按模型聚合，每个模型一行泳道，行内列出能服务它的渠道格子。 */
function TopologyView({
  channels,
  channelStats,
}: {
  channels: Channel[];
  channelStats: Map<string, { success: number; failure: number }>;
}) {
  // 按 model 聚合：model → 能服务它的渠道列表。
  const lanes = useMemo(() => {
    const map = new Map<string, Channel[]>();
    for (const ch of channels) {
      for (const model of ch.models) {
        const list = map.get(model);
        if (list) list.push(ch);
        else map.set(model, [ch]);
      }
    }
    // 稳定排序：渠道数多 → 模型名升序。
    return Array.from(map.entries())
      .sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]));
  }, [channels]);

  const successRateOf = (name: string): number | null => {
    const s = channelStats.get(name);
    if (!s) return null;
    const total = s.success + s.failure;
    return total === 0 ? null : s.success / total;
  };

  return (
    <Card
      styles={{ body: { background: 'rgba(255,255,255,0.04)', flex: 1, minHeight: 0, overflow: 'auto' } }}
      title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>模型 ↔ 渠道拓扑</span>}
      style={{ flex: 1, minHeight: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}
    >
      <div data-testid="topology-view" style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {lanes.length === 0 ? (
          <Empty
            description="暂无渠道数据"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            style={{ padding: 24 }}
          />
        ) : (
          lanes.map(([model, chs]) => (
            <div
              key={model}
              style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}
            >
              <div
                style={{
                  width: 180,
                  flexShrink: 0,
                  color: AXIS_LABEL_FILL_STRONG,
                  fontSize: 13,
                  fontWeight: 500,
                  wordBreak: 'break-all',
                }}
              >
                {model}
              </div>
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                {chs.map((ch) => (
                  <ChannelCell
                    key={ch.id}
                    channel={ch}
                    successRate={successRateOf(ch.name)}
                  />
                ))}
              </div>
            </div>
          ))
        )}
      </div>
    </Card>
  );
}

/** 近 24 小时延迟分位与失败率趋势（DualAxes：左轴延迟 ms，右轴失败率%）。 */
function LatencyPanel({ points }: { points: HealthPoint[] }) {
  const chartData = useMemo(
    () =>
      points.map((p) => ({
        time: dayjs(p.bucket_start).format('MM-DD HH:mm'),
        p50: p.p50_ms,
        p95: p.p95_ms,
        p99: p.p99_ms,
        fail_rate: +(p.fail_rate * 100).toFixed(3),
      })),
    [points],
  );

  const config = {
    data: chartData,
    xField: 'time',
    axis: {
      x: { labelFill: AXIS_LABEL_FILL },
    },
    legend: {
      color: {
        itemMarker: 'circle',
        labelFormatter: (v: string) =>
          ({
            p50: 'P50',
            p95: 'P95',
            p99: 'P99',
            fail_rate: '失败率',
          })[v] ?? v,
      },
    },
    children: [
      {
        type: 'line' as const,
        yField: 'p50',
        style: { lineWidth: 2, stroke: PERCENTILE_COLORS.p50 },
        tooltip: { items: [{ channel: 'y' as const, name: 'P50', valueFormatter: (v: number) => `${v} ms` }] },
        axis: {
          y: {
            position: 'left' as const,
            title: '延迟(ms)',
            labelFill: AXIS_LABEL_FILL,
            style: { titleFill: PERCENTILE_COLORS.p50 },
          },
        },
      },
      {
        type: 'line' as const,
        yField: 'p95',
        style: { lineWidth: 2, stroke: PERCENTILE_COLORS.p95 },
        tooltip: { items: [{ channel: 'y' as const, name: 'P95', valueFormatter: (v: number) => `${v} ms` }] },
      },
      {
        type: 'line' as const,
        yField: 'p99',
        style: { lineWidth: 2, stroke: PERCENTILE_COLORS.p99 },
        tooltip: { items: [{ channel: 'y' as const, name: 'P99', valueFormatter: (v: number) => `${v} ms` }] },
      },
      {
        type: 'line' as const,
        yField: 'fail_rate',
        style: { lineWidth: 2, stroke: FAIL_RATE_COLOR },
        tooltip: { items: [{ channel: 'y' as const, name: '失败率', valueFormatter: (v: number) => `${v.toFixed(3)}%` }] },
        axis: {
          y: {
            position: 'right' as const,
            title: '失败率(%)',
            labelFill: AXIS_LABEL_FILL,
            style: { titleFill: FAIL_RATE_COLOR },
          },
        },
      },
    ],
    height: 300,
  };

  return (
    <Card
      styles={DARK_CARD_STYLE}
      title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>近 24 小时延迟与失败率</span>}
      data-testid="latency-panel"
    >
      <div style={{ minHeight: 300 }}>
        {chartData.length >= 2 ? (
          <DualAxes {...config} />
        ) : (
          <Empty
            description="近 24 小时暂无足够样本"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            style={{ height: 300, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
          />
        )}
      </div>
    </Card>
  );
}

/** 近期失败滚动条：相对时间 + 模型 + 错误类型。 */
function ErrorTicker({ errors }: { errors: UsageLog[] }) {
  return (
    <Card
      styles={DARK_CARD_STYLE}
      title={<span style={{ color: AXIS_LABEL_FILL_STRONG }}>近期失败</span>}
      data-testid="error-ticker"
    >
      {errors.length === 0 ? (
        <Text style={{ color: AXIS_LABEL_FILL }}>近期无失败</Text>
      ) : (
        <div style={{ display: 'flex', gap: 8, overflowX: 'auto', paddingBottom: 4 }}>
          {errors.map((e) => (
            <div
              key={e.id}
              style={{
                flex: '0 0 auto',
                display: 'flex',
                flexDirection: 'column',
                gap: 2,
                padding: '6px 10px',
                borderRadius: 4,
                background: `${semantic.error}1A`,
                border: `1px solid ${semantic.error}55`,
                minWidth: 180,
              }}
            >
              <span style={{ color: AXIS_LABEL_FILL, fontSize: 11 }}>
                {relativeTime(e.created_at)}
              </span>
              <span style={{ color: AXIS_LABEL_FILL_STRONG, fontSize: 13 }}>{e.model_name || '—'}</span>
              <Tag color="red" style={{ margin: 0, alignSelf: 'flex-start' }}>
                {e.error_class || 'unknown'}
              </Tag>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

/**
 * 运维大屏（/monitor/operations）。
 *
 * 数据源全部来自既有端点：
 *   - GET /admin/stats/runtime：在途请求、RPM（两次采样差）、失败率、日志丢弃、渠道成功率
 *   - GET /admin/stats/health-timeline（hour 桶，近 24h）：延迟分位与失败率趋势
 *   - GET /admin/channels：模型↔渠道拓扑
 *   - GET /admin/stats/overview：活跃/异常渠道计数
 *   - GET /admin/usage-logs?status=failed：近期失败滚动条
 *
 * 轮询策略：MonitorLayout 在 autoRefresh=true 时自带 10s 主轮询调用 onRefresh。
 * 快速源（runtime/health）每拍都刷；慢源（channels/errors/overview）在 onRefresh 内以
 * 55s 节流实现 ~60s 节奏，避免双定时器冗余。autoRefresh 关闭时 MonitorLayout 停止 interval，
 * 轮询自然停止；卸载时 MonitorLayout 自清 interval，无泄漏。轮询错误静默处理（数据保持上一次
 * 快照），避免 10s 一次的 message.error 刷屏。
 */
function OpsScreen() {
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [runtime, setRuntime] = useState<RuntimeSnapshot | null>(null);
  const [health, setHealth] = useState<HealthPoint[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [overview, setOverview] = useState<StatsOverview | null>(null);
  const [errors, setErrors] = useState<UsageLog[]>([]);
  const [fastLoading, setFastLoading] = useState(false);
  const [slowLoading, setSlowLoading] = useState(false);

  // RPM 两次采样差：存 {ts, total}，首拍为 null 时显示「—」。
  const rpmPrevRef = useRef<{ ts: number; total: number } | null>(null);
  const [rpm, setRpm] = useState<number | null>(null);
  // 慢源节流：上次慢源成功拉取的时刻。
  const lastSlowAtRef = useRef<number>(0);

  const refreshRuntime = useCallback(async () => {
    setFastLoading(true);
    try {
      const snap = await monitorApi.runtime();
      setRuntime(snap);
      const total = sumCounter(snap, METRIC_RELAY_REQUESTS);
      const now = Date.now();
      const prev = rpmPrevRef.current;
      if (prev !== null) {
        const dt = (now - prev.ts) / 1000;
        if (dt > 0) {
          const diff = total - prev.total;
          // 计数器理论上单调递增；进程重启可能导致回退，此时丢弃本拍 RPM。
          setRpm(diff >= 0 ? (diff / dt) * 60 : null);
        }
      }
      rpmPrevRef.current = { ts: now, total };
    } catch {
      // 轮询错误静默：保留上一次快照，避免刷屏。
    } finally {
      setFastLoading(false);
    }
  }, []);

  const refreshHealth = useCallback(async () => {
    try {
      const endTimestamp = Math.floor(Date.now() / 1000);
      const startTimestamp = endTimestamp - HEALTH_WINDOW_SECONDS;
      const resp: HealthTimelineResponse = await analyticsApi.healthTimeline({
        start_timestamp: startTimestamp,
        end_timestamp: endTimestamp,
        bucket: 'hour',
      });
      setHealth(resp.points ?? []);
    } catch {
      // 静默
    }
  }, []);

  const refreshChannels = useCallback(async () => {
    setSlowLoading(true);
    try {
      const [chResp, ov] = await Promise.all([
        channelApi.list({ page: 1, page_size: CHANNEL_PAGE_SIZE }),
        dashboardApi.overview(),
      ]);
      setChannels(chResp.items ?? []);
      setOverview(ov);
    } catch {
      // 静默
    } finally {
      setSlowLoading(false);
    }
  }, []);

  const refreshErrors = useCallback(async () => {
    try {
      const resp = await usageLogApi.list({
        status: 'failed',
        page: 1,
        page_size: FAILED_PAGE_SIZE,
      });
      setErrors(resp.items ?? []);
    } catch {
      // 静默
    }
  }, []);

  const refreshFast = useCallback(() => {
    void refreshRuntime();
    void refreshHealth();
  }, [refreshRuntime, refreshHealth]);

  const refreshSlow = useCallback(() => {
    void refreshChannels();
    void refreshErrors();
  }, [refreshChannels, refreshErrors]);

  // MonitorLayout onRefresh：快速源每拍都刷；慢源按 55s 节流，实现 ~60s 节奏。
  const handleRefresh = useCallback(() => {
    refreshFast();
    if (Date.now() - lastSlowAtRef.current >= SLOW_THROTTLE_MS) {
      lastSlowAtRef.current = Date.now();
      refreshSlow();
    }
  }, [refreshFast, refreshSlow]);

  // 首次挂载：立即全量拉取一次（慢源强制，初始化 lastSlowAtRef）。
  useEffect(() => {
    lastSlowAtRef.current = Date.now();
    refreshFast();
    refreshSlow();
  }, [refreshFast, refreshSlow]);

  // ---- 派生展示数据 ----
  const inFlight = readGauge(runtime, METRIC_IN_FLIGHT);
  const dropped = readGauge(runtime, METRIC_USAGE_DROPPED);
  const totalRequests = sumCounter(runtime, METRIC_RELAY_REQUESTS);
  const failedRequests = sumCounter(runtime, METRIC_RELAY_REQUESTS, {
    label: 'status',
    value: 'failed',
  });
  const failRate = totalRequests > 0 ? (failedRequests / totalRequests) * 100 : null;

  // 渠道成功率：由 channel_attempts_total{channel,outcome} 聚合到 channel name。
  const channelStats = useMemo(() => {
    const map = new Map<string, { success: number; failure: number }>();
    if (!runtime) return map;
    for (const c of runtime.counters) {
      if (c.name !== METRIC_CHANNEL_ATTEMPTS) continue;
      const name = c.labels.channel;
      if (!name) continue;
      const entry = map.get(name) ?? { success: 0, failure: 0 };
      if (c.labels.outcome === 'success') entry.success += c.value;
      else if (c.labels.outcome === 'failure') entry.failure += c.value;
      map.set(name, entry);
    }
    return map;
  }, [runtime]);

  return (
    <MonitorLayout
      title="运维大屏"
      onRefresh={handleRefresh}
      refreshing={fastLoading || slowLoading}
      refreshIntervalMs={FAST_INTERVAL_MS}
      autoRefresh={autoRefresh}
      onAutoRefreshChange={setAutoRefresh}
    >
      <div
        data-testid="ops-screen"
        style={{ display: 'flex', flexDirection: 'column', gap: 12, height: '100%' }}
      >
        <GlobalStatusBar
          inFlight={inFlight}
          rpm={rpm}
          failRate={failRate}
          overview={overview}
          dropped={dropped}
        />
        <LatencyPanel points={health} />
        <div style={{ display: 'flex', gap: 12, flex: 1, minHeight: 0 }}>
          <div
            style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', position: 'relative' }}
            data-testid="ops-topology-wrap"
          >
            <TopologyView channels={channels} channelStats={channelStats} />
            {slowLoading && channels.length === 0 && (
              <div
                style={{
                  position: 'absolute',
                  inset: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <Spin />
              </div>
            )}
          </div>
        </div>
        <ErrorTicker errors={errors} />
      </div>
    </MonitorLayout>
  );
}

export default OpsScreen;
