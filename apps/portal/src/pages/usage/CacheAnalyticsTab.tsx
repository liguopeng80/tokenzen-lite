import { useMemo, useState } from 'react';
import { Card, Col, Empty, Row, Segmented, Space, Spin, Statistic, Table } from 'antd';
import { Line } from '@ant-design/charts';
import type { ColumnsType } from 'antd/es/table';
import type { CacheReportGroup, CacheReportResponse } from '@token-zen/shared';
import { formatNumber } from '@token-zen/shared';
import { useAsync } from '@token-zen/shared/hooks';
import { brand, primaryPalette, semantic } from '@token-zen/shared/theme';
import { usageApi } from '@/api/usage';
import { useMoney } from '@/stores/site';
import { rangeQueryParams, type UsageDateRange } from './UsageRangeBar';

type CacheGroupBy = 'day' | 'model' | 'project';

const GROUP_LABEL: Record<CacheGroupBy, string> = {
  day: '日期',
  model: '模型',
  project: '项目',
};

/** 缓存命中率展示为百分比，保留一位小数。无输入时显示 0。 */
function hitRatePercent(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

interface Props {
  dateRange: UsageDateRange;
}

/** 缓存分析 Tab：整体缓存命中率与缓存 token 量，按日期或模型分组。
 * 复用 portal dashboard 的 @ant-design/charts 包装方式与主题色。 */
function CacheAnalyticsTab({ dateRange }: Props) {
  const money = useMoney();
  const [groupBy, setGroupBy] = useState<CacheGroupBy>('day');
  const { data: report, loading } = useAsync<CacheReportResponse>(
    () => usageApi.cacheReport({ group_by: groupBy, ...rangeQueryParams(dateRange) }),
    [groupBy, dateRange],
  );

  const overall = report?.overall;
  const groups = report?.groups ?? [];

  const columns: ColumnsType<CacheReportGroup> = [
    { title: GROUP_LABEL[groupBy], dataIndex: 'group_key' },
    {
      title: '请求次数',
      dataIndex: 'requests',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.requests - b.requests,
    },
    {
      title: '缓存命中率',
      dataIndex: 'cache_hit_rate',
      align: 'right',
      render: (r: number) => hitRatePercent(r),
      sorter: (a, b) => a.cache_hit_rate - b.cache_hit_rate,
    },
    {
      title: '缓存读 Token',
      dataIndex: 'cache_read_tokens',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.cache_read_tokens - b.cache_read_tokens,
    },
    {
      title: '缓存写 Token',
      dataIndex: 'cache_write_tokens',
      align: 'right',
      render: (n: number) => formatNumber(n),
      sorter: (a, b) => a.cache_write_tokens - b.cache_write_tokens,
    },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      align: 'right',
      render: (v: number) => money.format(v),
      sorter: (a, b) => a.credits_charged - b.credits_charged,
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
      <Space style={{ marginBottom: 12 }}>
        <Segmented
          data-testid="cache-group-by"
          value={groupBy}
          onChange={(v) => setGroupBy(v as CacheGroupBy)}
          options={[
            { label: '日期', value: 'day' },
            { label: '模型', value: 'model' },
            { label: '项目', value: 'project' },
          ]}
        />
      </Space>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="整体缓存命中率"
              value={overall ? hitRatePercent(overall.cache_hit_rate) : '-'}
              valueStyle={{ color: brand.primary }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="缓存读 Token"
              value={overall ? formatNumber(overall.cache_read_tokens) : 0}
              valueStyle={{ color: semantic.info }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="缓存写 Token"
              value={overall ? formatNumber(overall.cache_write_tokens) : 0}
              valueStyle={{ color: semantic.warning }}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="请求数"
              value={overall ? formatNumber(overall.requests) : 0}
            />
          </Card>
        </Col>
      </Row>

      <Card title="每日缓存命中率趋势" hidden={groupBy !== 'day'}>
        <Spin spinning={loading}>
          {timelineData.length >= 2 ? (
            <Line {...lineConfig} />
          ) : (
            <Empty
              description={
                timelineData.length === 1
                  ? '目前只有一天的数据，攒够两天才能看出趋势。'
                  : '这段时间还没有调用记录。'
              }
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              style={{ height: 240, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
            />
          )}
        </Spin>
      </Card>

      <Card title={`按${GROUP_LABEL[groupBy]}缓存明细`} hidden={groupBy === 'day'} style={{ marginTop: 16 }}>
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

export default CacheAnalyticsTab;
