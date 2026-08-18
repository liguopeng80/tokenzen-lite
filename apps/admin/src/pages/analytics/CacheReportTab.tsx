import { useEffect, useMemo, useState } from 'react';
import {
  Card,
  Col,
  DatePicker,
  Empty,
  InputNumber,
  Row,
  Segmented,
  Select,
  Space,
  Spin,
  Statistic,
  Table,
  Typography,
} from 'antd';
import { Line } from '@ant-design/charts';
import type { ColumnsType } from 'antd/es/table';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import type {
  CacheReportGroup,
  CacheReportResponse,
  Channel,
  DepartmentWithStats,
  PaginatedData,
  ProjectWithStats,
} from '@token-zen/shared';
import { formatNumber, message } from '@token-zen/shared';
import { brand, primaryPalette, semantic } from '@token-zen/shared/theme';
import { analyticsApi } from '@/api/analytics';
import { channelApi } from '@/api/channels';
import { departmentApi, projectApi } from '@/api/organization';
import { errorMessageOf } from '@/api/error';
import { useMoney } from '@/stores/site';

const { Title } = Typography;
const { RangePicker } = DatePicker;

type CacheGroupBy = 'day' | 'model' | 'project' | 'channel';

const GROUP_LABEL: Record<CacheGroupBy, string> = {
  day: '日期',
  model: '模型',
  project: '项目',
  channel: '渠道',
};

const DEFAULT_DAYS = 30;

/** 缓存命中率展示为百分比，保留一位小数。无输入时显示 0。 */
function hitRatePercent(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

/**
 * 管理端缓存分析 Tab：镜像 portal 的 CacheAnalyticsTab，叠加 scope 筛选
 * （部门/项目/用户/渠道）与成本列。命中率口径与 /me/cache-report 一致；
 * 分组行额外暴露 credits_cost 供运营视角的毛利对比。
 */
function CacheReportTab() {
  const money = useMoney();
  const [groupBy, setGroupBy] = useState<CacheGroupBy>('day');
  const [range, setRange] = useState<[Dayjs, Dayjs]>([
    dayjs().subtract(DEFAULT_DAYS, 'day').startOf('day'),
    dayjs().endOf('day'),
  ]);
  const [departmentId, setDepartmentId] = useState<number | undefined>();
  const [projectId, setProjectId] = useState<number | undefined>();
  const [userId, setUserId] = useState<number | undefined>();
  const [channelId, setChannelId] = useState<number | undefined>();
  const [departments, setDepartments] = useState<DepartmentWithStats[]>([]);
  const [projects, setProjects] = useState<ProjectWithStats[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [report, setReport] = useState<CacheReportResponse | undefined>();
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    departmentApi
      .options()
      .then((data) => setDepartments(data.items ?? []))
      .catch(() => setDepartments([]));
    projectApi
      .options()
      .then((data) => setProjects(data.items ?? []))
      .catch(() => setProjects([]));
    channelApi
      .list({ page: 1, page_size: 100 })
      .then((data: PaginatedData<Channel>) => setChannels(data.items ?? []))
      .catch(() => setChannels([]));
  }, []);

  const load = () => {
    setLoading(true);
    analyticsApi
      .cacheReport({
        group_by: groupBy,
        start_timestamp: range[0].unix(),
        end_timestamp: range[1].unix(),
        ...(departmentId !== undefined ? { department_id: departmentId } : {}),
        ...(projectId !== undefined ? { project_id: projectId } : {}),
        ...(userId !== undefined ? { user_id: userId } : {}),
        ...(channelId !== undefined ? { channel_id: channelId } : {}),
      })
      .then((data) => setReport(data))
      .catch((err) => {
        message.error(errorMessageOf(err, '加载缓存分析失败'));
        setReport(undefined);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groupBy, range, departmentId, projectId, userId, channelId]);

  const overall = report?.overall;
  const groups = report?.groups ?? [];

  const columns: ColumnsType<CacheReportGroup> = [
    { title: GROUP_LABEL[groupBy], dataIndex: 'group_key', key: 'group_key' },
    {
      title: '请求次数',
      dataIndex: 'requests',
      key: 'requests',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.requests - b.requests,
    },
    {
      title: '缓存命中率',
      dataIndex: 'cache_hit_rate',
      key: 'cache_hit_rate',
      align: 'right',
      render: (r: number) => hitRatePercent(r),
      sorter: (a, b) => a.cache_hit_rate - b.cache_hit_rate,
    },
    {
      title: '缓存读 Token',
      dataIndex: 'cache_read_tokens',
      key: 'cache_read_tokens',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.cache_read_tokens - b.cache_read_tokens,
    },
    {
      title: '缓存写 Token',
      dataIndex: 'cache_write_tokens',
      key: 'cache_write_tokens',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.cache_write_tokens - b.cache_write_tokens,
    },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      key: 'credits_charged',
      align: 'right',
      render: (v: number) => money.format(v),
      sorter: (a, b) => a.credits_charged - b.credits_charged,
    },
    {
      title: '成本',
      dataIndex: 'credits_cost',
      key: 'credits_cost',
      align: 'right',
      // 后端已旁置 credits_cost_money 货币串；优先用串避免前端再做汇率换算。
      render: (_v: number | undefined, row: CacheReportGroup) =>
        row.credits_cost_money ?? (row.credits_cost !== undefined ? money.format(row.credits_cost) : '-'),
      sorter: (a, b) => (a.credits_cost ?? 0) - (b.credits_cost ?? 0),
    },
  ];

  // 按日维度的命中率时间轴：后端已按日期升序返回。
  const timelineData = useMemo(
    () => groups.map((g) => ({ day: g.group_key, hit_rate: g.cache_hit_rate * 100 })),
    [groups],
  );
  const lineConfig = {
    data: timelineData,
    xField: 'day',
    yField: 'hit_rate',
    height: 240,
    axis: {
      x: {
        title: false as const,
        labelFormatter: (v: string) => {
          const parts = v.split('-');
          return parts.length === 3 ? `${parts[1]}-${parts[2]}` : v;
        },
      },
      y: {
        title: '缓存命中率（%）',
        labelFormatter: (v: number) => `${v}%`,
        min: 0,
        max: 100,
      },
    },
    style: { lineWidth: 2, stroke: primaryPalette[500] },
    tooltip: {
      items: [{ channel: 'y' as const, name: '缓存命中率', valueFormatter: (v: number) => `${v.toFixed(1)}%` }],
    },
  };

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
          缓存分析
        </Title>
        <Space wrap data-testid="cache-scope-bar">
          <Segmented<CacheGroupBy>
            data-testid="cache-group-by"
            value={groupBy}
            onChange={(v) => setGroupBy(v)}
            options={[
              { label: '日期', value: 'day' },
              { label: '模型', value: 'model' },
              { label: '项目', value: 'project' },
              { label: '渠道', value: 'channel' },
            ]}
          />
          <RangePicker
            data-testid="cache-date-range"
            value={range}
            allowClear={false}
            onChange={(values) => {
              if (values && values[0] && values[1]) {
                setRange([values[0].startOf('day'), values[1].endOf('day')]);
              }
            }}
            disabledDate={(current) => current && current.isAfter(dayjs().endOf('day'))}
          />
          <Select
            data-testid="cache-department"
            placeholder="全部部门"
            allowClear
            style={{ width: 160 }}
            value={departmentId}
            onChange={(v) => setDepartmentId(v)}
            options={[
              { label: '未分配部门', value: 0 },
              ...departments.map((d) => ({ label: d.name, value: d.id })),
            ]}
          />
          <Select
            data-testid="cache-project"
            placeholder="全部项目"
            allowClear
            style={{ width: 160 }}
            value={projectId}
            onChange={(v) => setProjectId(v)}
            options={[
              { label: '未归属项目', value: 0 },
              ...projects.map((p) => ({ label: p.name, value: p.id })),
            ]}
          />
          <Select
            data-testid="cache-channel"
            placeholder="全部渠道"
            allowClear
            style={{ width: 160 }}
            value={channelId}
            onChange={(v) => setChannelId(v)}
            options={channels.map((c) => ({ label: c.name, value: c.id }))}
          />
          <InputNumber
            data-testid="cache-user-id"
            placeholder="用户 ID（可选）"
            style={{ width: 150 }}
            min={0}
            value={userId}
            onChange={(v) => setUserId(v === null ? undefined : (v as number))}
          />
        </Space>
      </div>

      <Spin spinning={loading}>
        <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="整体缓存命中率"
                value={overall ? hitRatePercent(overall.cache_hit_rate) : '-'}
                valueStyle={{ color: brand.primary }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="缓存读 Token"
                value={overall ? formatNumber(overall.cache_read_tokens) : 0}
                valueStyle={{ color: semantic.info }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title="缓存写 Token"
                value={overall ? formatNumber(overall.cache_write_tokens) : 0}
                valueStyle={{ color: semantic.warning }}
              />
            </Card>
          </Col>
          <Col xs={24} sm={12} lg={6}>
            <Card styles={{ body: { padding: '20px 24px' } }}>
              <Statistic title="请求数" value={overall ? formatNumber(overall.requests) : 0} />
            </Card>
          </Col>
        </Row>

        <Card title="每日缓存命中率趋势" hidden={groupBy !== 'day'}>
          {timelineData.length >= 2 ? (
            <Line {...lineConfig} />
          ) : (
            <Empty
              description={
                timelineData.length === 1
                  ? '目前只有一天的数据，攒够两天才能看出趋势。'
                  : '所选时间范围内还没有调用记录。'
              }
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              style={{ height: 240, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
            />
          )}
        </Card>

        <Card title={`按${GROUP_LABEL[groupBy]}缓存明细`} hidden={groupBy === 'day'} style={{ marginTop: 16 }}>
          <Table<CacheReportGroup>
            columns={columns}
            dataSource={groups}
            rowKey="group_key"
            pagination={false}
            size="middle"
            locale={{
              emptyText: (
                <Empty
                  description="所选时间范围内还没有调用记录。"
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                />
              ),
            }}
          />
        </Card>
      </Spin>
    </div>
  );
}

export default CacheReportTab;
