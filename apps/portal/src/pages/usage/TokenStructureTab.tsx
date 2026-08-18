import { useState } from 'react';
import { Card, Col, Empty, Row, Segmented, Space, Spin, Statistic, Table } from 'antd';
import { Pie } from '@ant-design/charts';
import type { ColumnsType } from 'antd/es/table';
import type { TokenReportGroup, TokenReportOverall, TokenReportResponse } from '@token-zen/shared';
import { formatNumber } from '@token-zen/shared';
import { useAsync } from '@token-zen/shared/hooks';
import { brand, primaryPalette, semantic } from '@token-zen/shared/theme';
import { usageApi } from '@/api/usage';
import { useMoney } from '@/stores/site';
import { rangeQueryParams, type UsageDateRange } from './UsageRangeBar';

type TokenGroupBy = 'day' | 'model' | 'project';

const GROUP_LABEL: Record<TokenGroupBy, string> = {
  day: '日期',
  model: '模型',
  project: '项目',
};

interface Props {
  dateRange: UsageDateRange;
}

interface Slice {
  type: string;
  value: number;
  color: string;
}

const SLICE_META: { key: keyof Omit<TokenReportOverall, 'total_tokens'>; label: string; color: string }[] = [
  { key: 'prompt_tokens', label: '输入（不含缓存）', color: primaryPalette[500] },
  { key: 'cache_read_tokens', label: '缓存命中读', color: semantic.info },
  { key: 'cache_write_tokens', label: '缓存写入', color: semantic.warning },
  { key: 'completion_tokens', label: '输出', color: brand.primary },
];

/** token 结构 Tab：输入/缓存命中读/缓存写入/输出四类 billed token 的占比与按维度明细。
 * 与缓存分析同走保留期安全的聚合路径，但聚焦消费结构而非缓存效率。 */
function TokenStructureTab({ dateRange }: Props) {
  const money = useMoney();
  const [groupBy, setGroupBy] = useState<TokenGroupBy>('model');
  const { data: report, loading } = useAsync<TokenReportResponse>(
    () => usageApi.tokenReport({ group_by: groupBy, ...rangeQueryParams(dateRange) }),
    [groupBy, dateRange],
  );

  const overall = report?.overall;
  const total = overall?.total_tokens ?? 0;
  const groups: TokenReportGroup[] = report?.groups ?? [];

  const slices: Slice[] =
    total > 0
      ? SLICE_META.map((m) => ({
          type: m.label,
          value: Number(overall?.[m.key] ?? 0),
          color: m.color,
        })).filter((s) => s.value > 0)
      : [];

  const pieConfig = {
    data: slices,
    angleField: 'value',
    colorField: 'type',
    color: slices.map((s) => s.color),
    height: 260,
    legend: { color: { position: 'right' as const } },
    label: {
      text: 'value',
      style: { fontSize: 11 },
      formatter: (_v: unknown, item: { value?: number }) =>
        total > 0 && item.value ? `${((item.value / total) * 100).toFixed(1)}%` : '',
    },
    tooltip: {
      items: [
        {
          channel: 'y' as const,
          name: 'token',
          valueFormatter: (v: number) => `${formatNumber(v)}（${pct(v, total)}）`,
        },
      ],
    },
  };

  const columns: ColumnsType<TokenReportGroup> = [
    { title: GROUP_LABEL[groupBy], dataIndex: 'group_key' },
    {
      title: '请求次数',
      dataIndex: 'requests',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.requests - b.requests,
    },
    {
      title: '输入',
      dataIndex: 'prompt_tokens',
      align: 'right',
      render: (n: number) => `${formatNumber(n)} (${pct(n, total)})`,
      sorter: (a, b) => a.prompt_tokens - b.prompt_tokens,
    },
    {
      title: '缓存命中读',
      dataIndex: 'cache_read_tokens',
      align: 'right',
      render: (n: number) => `${formatNumber(n)} (${pct(n, total)})`,
      sorter: (a, b) => a.cache_read_tokens - b.cache_read_tokens,
    },
    {
      title: '缓存写入',
      dataIndex: 'cache_write_tokens',
      align: 'right',
      render: (n: number) => `${formatNumber(n)} (${pct(n, total)})`,
      sorter: (a, b) => a.cache_write_tokens - b.cache_write_tokens,
    },
    {
      title: '输出',
      dataIndex: 'completion_tokens',
      align: 'right',
      render: (n: number) => `${formatNumber(n)} (${pct(n, total)})`,
      sorter: (a, b) => a.completion_tokens - b.completion_tokens,
    },
    {
      title: '合计',
      dataIndex: 'total_tokens',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.total_tokens - b.total_tokens,
      defaultSortOrder: 'descend' as const,
    },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      align: 'right',
      render: (v: number) => money.format(v),
      sorter: (a, b) => a.credits_charged - b.credits_charged,
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Segmented
          data-testid="token-group-by"
          value={groupBy}
          onChange={(v) => setGroupBy(v as TokenGroupBy)}
          options={[
            { label: '模型', value: 'model' },
            { label: '日期', value: 'day' },
            { label: '项目', value: 'project' },
          ]}
        />
      </Space>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {SLICE_META.map((m) => (
          <Col xs={24} sm={12} lg={6} key={m.key}>
            <Card loading={loading} styles={{ body: { padding: '20px 24px' } }}>
              <Statistic
                title={`${m.label}占比`}
                value={overall ? pct(Number(overall[m.key]), total) : '-'}
                valueStyle={{ color: m.color }}
              />
              <div style={{ marginTop: 4, color: 'rgba(0,0,0,0.45)', fontSize: 12 }}>
                {overall ? formatNumber(Number(overall[m.key])) : 0}
              </div>
            </Card>
          </Col>
        ))}
      </Row>

      <Card title="token 结构占比">
        <Spin spinning={loading}>
          {slices.length > 0 ? (
            <Pie {...pieConfig} />
          ) : (
            <Empty
              description="这段时间还没有调用记录。"
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              style={{ height: 260, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
            />
          )}
        </Spin>
      </Card>

      <Card title={`按${GROUP_LABEL[groupBy]}明细`} style={{ marginTop: 16 }}>
        <Table
          columns={columns}
          dataSource={groups}
          rowKey="group_key"
          loading={loading}
          pagination={false}
          size="middle"
        />
      </Card>
    </div>
  );
}

/** token 量占总量的百分比，无总量时返回 '-'. */
function pct(value: number, total: number): string {
  if (total <= 0) return '-';
  return `${((value / total) * 100).toFixed(1)}%`;
}

export default TokenStructureTab;
