import { useState, useCallback, useEffect } from 'react';
import { Card, Table, DatePicker, Select, Typography, Button, Tabs, Space, Tag, Tooltip, Drawer, Descriptions, message } from 'antd';
import { ReloadOutlined, DownloadOutlined, ProfileOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { MeUsageLog, UsageStatus, SummaryRow } from '@token-zen/shared';
import { UsageStatusLabel, ErrorClassLabel } from '@token-zen/shared';
import {
  formatTime,
  formatElapsedTime,
  formatNumber,
  exportToCSV,
} from '@token-zen/shared';
import { useAsync, useTable } from '@token-zen/shared/hooks';
import { usageApi } from '@/api/usage';
import { getMoney, useMoney } from '@/stores/site';
import { modelsApi } from '@/api/models';
import CacheAnalyticsTab from './CacheAnalyticsTab';
import HeatmapTab from './HeatmapTab';
import TokenStructureTab from './TokenStructureTab';
import UsageRangeBar, { rangeQueryParams, type UsageDateRange } from './UsageRangeBar';
import dayjs from 'dayjs';

const { Title } = Typography;
const { RangePicker } = DatePicker;

const STATUS_COLOR: Record<UsageStatus, string> = {
  settled: 'green',
  refunded: 'blue',
  failed: 'red',
};

const SUMMARY_GROUP_LABEL: Record<'day' | 'model' | 'key' | 'project', string> = {
  day: '日期',
  model: '模型',
  key: 'API 密钥',
  project: '项目',
};

function formatCacheTokens(read: number, write: number): string {
  if (!read && !write) return '-';
  return `读 ${formatNumber(read)} / 写 ${formatNumber(write)}`;
}

function UsagePage() {
  const money = useMoney();
  const [modelName, setModelName] = useState<string | undefined>();
  const [status, setStatus] = useState<UsageStatus | undefined>();
  const [dateRange, setDateRange] = useState<UsageDateRange>(null);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [summaryGroupBy, setSummaryGroupBy] = useState<'day' | 'model' | 'key' | 'project'>('day');
  const [summaryRange, setSummaryRange] = useState<UsageDateRange>(null);
  const [cacheRange, setCacheRange] = useState<UsageDateRange>(null);
  const [heatmapRange, setHeatmapRange] = useState<UsageDateRange>(null);
  const [tokenRange, setTokenRange] = useState<UsageDateRange>(null);
  const [detailLog, setDetailLog] = useState<MeUsageLog | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  useEffect(() => {
    modelsApi.list().then((models) => {
      setModelOptions((models ?? []).map((m) => m.name).sort());
    }).catch(() => {});
  }, []);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) => {
      const filters: Record<string, unknown> = { ...params };
      if (modelName) filters.model = modelName;
      if (status) filters.status = status;
      if (dateRange?.[0]) filters.start_timestamp = dateRange[0].unix();
      if (dateRange?.[1]) filters.end_timestamp = dateRange[1].unix();
      return usageApi.logs(filters);
    },
    [modelName, status, dateRange],
  );

  const { dataSource, loading, pagination, refresh } = useTable<MeUsageLog>({
    fetchFn,
    defaultPageSize: 20,
    deps: [modelName, status, dateRange],
  });

  const { data: summaryRows, loading: summaryLoading } = useAsync<SummaryRow[]>(
    () => usageApi.summary({ group_by: summaryGroupBy, ...rangeQueryParams(summaryRange) }),
    [summaryGroupBy, summaryRange],
  );
  const summaryData = summaryRows ?? [];

  // 当前筛选条件的查询参数（用于服务端导出 URL）。
  const exportFilters: Record<string, unknown> = {};
  if (modelName) exportFilters.model = modelName;
  if (status) exportFilters.status = status;
  if (dateRange?.[0]) exportFilters.start_timestamp = dateRange[0].unix();
  if (dateRange?.[1]) exportFilters.end_timestamp = dateRange[1].unix();

  const handleViewDetail = async (record: MeUsageLog) => {
    setDetailLoading(true);
    setDetailLog(record);
    try {
      const detail = await usageApi.getDetail(record.request_id);
      setDetailLog(detail);
    } catch {
      message.error('加载详情失败');
    } finally {
      setDetailLoading(false);
    }
  };

  // 服务端导出：浏览器直接下载 CSV 流，覆盖全部匹配记录（受 20 万行上限截断）。
  const handleExportAll = () => {
    window.location.href = usageApi.exportUrl(exportFilters);
  };

  // 当前页客户端导出（仅可见行，快捷存档）。
  const handleExportPage = () => {
    const m = getMoney();
    exportToCSV(
      dataSource.map((row) => ({
        time: formatTime(row.created_at),
        model: row.model_name,
        prompt_tokens: row.prompt_tokens,
        completion_tokens: row.completion_tokens,
        cache_read_tokens: row.cache_read_tokens,
        cache_write_tokens: row.cache_write_tokens,
        credits_charged: row.credits_charged,
        money_charged: m.formatValue(row.credits_charged).toFixed(m.detailDecimals),
        status: UsageStatusLabel[row.status] ?? row.status,
        latency_ms: row.latency_ms,
        is_stream: row.is_stream ? '是' : '否',
      })),
      `usage-${dayjs().format('YYYYMMDD')}.csv`,
      [
        { key: 'time', title: '时间' },
        { key: 'model', title: '模型' },
        { key: 'prompt_tokens', title: '输入 Tokens（含缓存）' },
        { key: 'completion_tokens', title: '输出 Tokens' },
        { key: 'cache_read_tokens', title: '缓存读 Tokens' },
        { key: 'cache_write_tokens', title: '缓存写 Tokens' },
        { key: 'credits_charged', title: '扣费积分' },
        { key: 'money_charged', title: '扣费金额' },
        { key: 'status', title: '状态' },
        { key: 'latency_ms', title: '耗时(ms)' },
        { key: 'is_stream', title: '流式' },
      ],
    );
  };

  const columns: ColumnsType<MeUsageLog> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      sorter: (a, b) => a.created_at.localeCompare(b.created_at),
      render: (t: string) => formatTime(t),
      width: 170,
    },
    { title: '模型', dataIndex: 'model_name' },
    {
      title: '输入 Tokens（含缓存）',
      dataIndex: 'prompt_tokens',
      sorter: (a, b) => a.prompt_tokens - b.prompt_tokens,
      render: (n: number) => formatNumber(n),
      align: 'right',
      width: 140,
      responsive: ['md'] as const,
    },
    {
      title: '输出 Tokens',
      dataIndex: 'completion_tokens',
      sorter: (a, b) => a.completion_tokens - b.completion_tokens,
      render: (n: number) => formatNumber(n),
      align: 'right',
      width: 110,
      responsive: ['md'] as const,
    },
    {
      title: '缓存 Tokens',
      key: 'cache_tokens',
      align: 'right',
      width: 150,
      responsive: ['lg'] as const,
      render: (_: unknown, record: MeUsageLog) =>
        formatCacheTokens(record.cache_read_tokens, record.cache_write_tokens),
    },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      sorter: (a, b) => a.credits_charged - b.credits_charged,
      render: (v: number) => money.formatDetail(v),
      align: 'right',
      width: 100,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (s: UsageStatus, record: MeUsageLog) => {
        const tag = <Tag color={STATUS_COLOR[s] ?? 'default'}>{UsageStatusLabel[s] ?? s}</Tag>;
        if (s !== 'failed' || !record.error_class) return tag;
        return (
          <Tooltip title={`${ErrorClassLabel[record.error_class] ?? record.error_class}：${record.error_message || '无详情'}`}>
            {tag}
          </Tooltip>
        );
      },
    },
    {
      title: '耗时',
      dataIndex: 'latency_ms',
      sorter: (a, b) => a.latency_ms - b.latency_ms,
      render: (t: number) => formatElapsedTime(t),
      align: 'right',
      width: 80,
      responsive: ['lg'] as const,
    },
    {
      title: '流式',
      dataIndex: 'is_stream',
      width: 70,
      render: (v: boolean) => (v ? <Tag>流式</Tag> : <Tag color="default">非流式</Tag>),
      responsive: ['lg'] as const,
    },
    {
      title: '操作',
      key: 'action',
      width: 80,
      fixed: 'right' as const,
      render: (_: unknown, record: MeUsageLog) => (
        <Button
          type="link"
          size="small"
          icon={<ProfileOutlined />}
          onClick={() => handleViewDetail(record)}
          data-testid={`usage-row-detail-${record.id}`}
        >
          详情
        </Button>
      ),
    },
  ];

  const summaryColumns: ColumnsType<SummaryRow> = [
    { title: SUMMARY_GROUP_LABEL[summaryGroupBy], dataIndex: 'group_key' },
    {
      title: '请求次数',
      dataIndex: 'requests',
      render: (n: number) => formatNumber(n),
      align: 'right',
      sorter: (a: SummaryRow, b: SummaryRow) => a.requests - b.requests,
    },
    {
      title: 'Token 消耗',
      dataIndex: 'total_tokens',
      render: (n: number) => formatNumber(n),
      align: 'right',
      sorter: (a: SummaryRow, b: SummaryRow) => a.total_tokens - b.total_tokens,
    },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      render: (v: number) => money.format(v),
      align: 'right',
      sorter: (a: SummaryRow, b: SummaryRow) => a.credits_charged - b.credits_charged,
      defaultSortOrder: 'descend',
    },
  ];

  const filterBar = (
    <Card style={{ marginBottom: 16 }}>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'center' }}>
        <RangePicker
          showTime
          onChange={(dates) => setDateRange(dates as UsageDateRange)}
        />
        <Select
          placeholder="模型"
          style={{ width: 200 }}
          allowClear
          showSearch
          value={modelName}
          onChange={setModelName}
          options={modelOptions.map((n) => ({ label: n, value: n }))}
          filterOption={(input, option) =>
            (option?.label as string)?.toLowerCase().includes(input.toLowerCase())
          }
        />
        <Select
          placeholder="状态"
          style={{ width: 140 }}
          allowClear
          value={status}
          onChange={setStatus}
          options={(Object.keys(UsageStatusLabel) as UsageStatus[]).map((s) => ({
            label: UsageStatusLabel[s],
            value: s,
          }))}
        />
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            icon={<DownloadOutlined />}
            onClick={handleExportAll}
            data-testid="usage-export-csv"
          >
            导出 CSV
          </Button>
          <Tooltip title="仅导出当前页数据">
            <Button
              icon={<DownloadOutlined />}
              onClick={handleExportPage}
              disabled={dataSource.length === 0}
              data-testid="usage-export-page"
            >
              导出当前页
            </Button>
          </Tooltip>
        </Space>
      </div>
    </Card>
  );

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        用量明细
      </Title>
      {filterBar}
      <Card>
        <Tabs
          items={[
            {
              key: 'detail',
              label: '明细',
              children: (
                <Table
                  columns={columns}
                  dataSource={dataSource}
                  rowKey="id"
                  loading={loading}
                  pagination={pagination}
                  scroll={{ x: 1000 }}
                />
              ),
            },
            {
              key: 'summary',
              label: '汇总',
              children: (
                <>
                  <Space style={{ marginBottom: 12 }}>
                    <span>按</span>
                    <Select
                      value={summaryGroupBy}
                      onChange={setSummaryGroupBy}
                      style={{ width: 120 }}
                      options={[
                        { label: '日期', value: 'day' },
                        { label: '模型', value: 'model' },
                        { label: 'API 密钥', value: 'key' },
                        { label: '项目', value: 'project' },
                      ]}
                    />
                    <span>汇总</span>
                    <UsageRangeBar
                      testId="summary-date-range"
                      value={summaryRange}
                      onChange={setSummaryRange}
                    />
                  </Space>
                  <Table
                    columns={summaryColumns}
                    dataSource={summaryData}
                    rowKey="group_key"
                    loading={summaryLoading}
                    pagination={false}
                    size="middle"
                  />
                </>
              ),
            },
            {
              key: 'cache',
              label: '缓存分析',
              children: (
                <>
                  <UsageRangeBar
                    testId="cache-date-range"
                    value={cacheRange}
                    onChange={setCacheRange}
                  />
                  <CacheAnalyticsTab dateRange={cacheRange} />
                </>
              ),
            },
            {
              key: 'token',
              label: 'Token 结构',
              children: (
                <>
                  <UsageRangeBar
                    testId="token-date-range"
                    value={tokenRange}
                    onChange={setTokenRange}
                  />
                  <TokenStructureTab dateRange={tokenRange} />
                </>
              ),
            },
            {
              key: 'heatmap',
              label: '活跃时段',
              children: (
                <>
                  <UsageRangeBar
                    testId="heatmap-date-range"
                    value={heatmapRange}
                    onChange={setHeatmapRange}
                  />
                  <HeatmapTab dateRange={heatmapRange} />
                </>
              ),
            },
          ]}
        />
      </Card>

      <Drawer
        title={`请求详情 - ${detailLog?.request_id ?? ''}`}
        open={!!detailLog}
        onClose={() => setDetailLog(null)}
        width={520}
        destroyOnClose
        data-testid="usage-detail-drawer"
      >
        {detailLog && (
          <div data-testid="usage-detail-content">
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="状态">
                <Space size={4} wrap>
                  <Tag color={STATUS_COLOR[detailLog.status] ?? 'default'}>
                    {UsageStatusLabel[detailLog.status] ?? detailLog.status}
                  </Tag>
                  {detailLog.usage_estimated && <Tag color="gold">估算</Tag>}
                  {detailLog.is_stream ? <Tag>流式</Tag> : <Tag color="default">非流式</Tag>}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="请求标识">
                <code style={{ fontSize: 12 }}>{detailLog.request_id}</code>
              </Descriptions.Item>
              <Descriptions.Item label="密钥 ID">{detailLog.api_key_id}</Descriptions.Item>
              <Descriptions.Item label="模型">{detailLog.model_name}</Descriptions.Item>
              <Descriptions.Item label="输入 Tokens（含缓存）">
                {formatNumber(detailLog.prompt_tokens)}
              </Descriptions.Item>
              <Descriptions.Item label="输出 Tokens">
                {formatNumber(detailLog.completion_tokens)}
              </Descriptions.Item>
              <Descriptions.Item label="缓存读 Tokens">
                {formatNumber(detailLog.cache_read_tokens)}
              </Descriptions.Item>
              <Descriptions.Item label="缓存写 Tokens">
                {formatNumber(detailLog.cache_write_tokens)}
              </Descriptions.Item>
              {detailLog.audio_input_tokens > 0 && (
                <Descriptions.Item label="音频输入 Tokens">
                  {formatNumber(detailLog.audio_input_tokens)}
                </Descriptions.Item>
              )}
              {detailLog.audio_output_tokens > 0 && (
                <Descriptions.Item label="音频输出 Tokens">
                  {formatNumber(detailLog.audio_output_tokens)}
                </Descriptions.Item>
              )}
              {detailLog.call_count > 0 && (
                <Descriptions.Item label="调用次数">
                  {formatNumber(detailLog.call_count)}
                </Descriptions.Item>
              )}
              <Descriptions.Item label="消费">
                {money.formatDetail(detailLog.credits_charged)}
              </Descriptions.Item>
              <Descriptions.Item label="总耗时">
                {formatElapsedTime(detailLog.latency_ms)}
              </Descriptions.Item>
              {detailLog.first_byte_ms > 0 && (
                <Descriptions.Item label="首字节耗时">
                  {formatElapsedTime(detailLog.first_byte_ms)}
                </Descriptions.Item>
              )}
              <Descriptions.Item label="时间">
                {formatTime(detailLog.created_at)}
              </Descriptions.Item>
              {detailLog.error_class && (
                <Descriptions.Item label="异常分类">
                  <Tag color="orange">
                    {ErrorClassLabel[detailLog.error_class] ?? detailLog.error_class}
                  </Tag>
                </Descriptions.Item>
              )}
              {detailLog.error_message && (
                <Descriptions.Item label="异常详情">
                  <span style={{ color: '#e34d59' }}>{detailLog.error_message}</span>
                </Descriptions.Item>
              )}
            </Descriptions>
            <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
              {detailLoading ? '加载中…' : '仅展示本人请求可见字段，不含渠道与成本信息。'}
            </Typography.Paragraph>
          </div>
        )}
      </Drawer>
    </div>
  );
}

export default UsagePage;
