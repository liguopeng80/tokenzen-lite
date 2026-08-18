import { useState, useCallback } from 'react';
import { Card, Table, DatePicker, Input, Typography, Button, Space, Tag, Select, Modal, Tooltip } from 'antd';
import { message } from '@token-zen/shared';
import { ReloadOutlined, SearchOutlined, DownloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { UsageLog, UsageStatus } from '@token-zen/shared';
import {
  UsageStatusLabel,
  ErrorClassLabel,
  formatTime,
  formatElapsedTime,
  formatNumber,
  exportToCSV,
} from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { usageLogApi } from '@/api/usageLogs';
import { useMoney, getMoney } from '@/stores/site';
import type { Dayjs } from 'dayjs';
import dayjs from 'dayjs';

const { Title } = Typography;
const { RangePicker } = DatePicker;

const statusColor: Record<UsageStatus, string> = {
  settled: 'green',
  refunded: 'purple',
  failed: 'red',
};

const statusOptions = Object.entries(UsageStatusLabel).map(([value, label]) => ({
  value: value as UsageStatus,
  label,
}));

/** 名称优先、ID 兜底：用户或渠道被删除后名称为空，仍要看出这条日志属于谁。 */
function nameWithId(name: string | undefined, id: number) {
  if (name) return <Tooltip title={`#${id}`}><span>{name}</span></Tooltip>;
  return <Typography.Text type="secondary">已删除 #{id}</Typography.Text>;
}

function LogsPage() {
  const money = useMoney();
  const [dateRange, setDateRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
  const [username, setUsername] = useState('');
  const [modelName, setModelName] = useState('');
  const [channelId, setChannelId] = useState('');
  const [status, setStatus] = useState<UsageStatus | undefined>();
  const [requestId, setRequestId] = useState('');
  const [detailLog, setDetailLog] = useState<UsageLog | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) => {
      const filters: Record<string, unknown> = { ...params };
      if (username) filters.username = username;
      if (modelName) filters.model = modelName;
      if (channelId) filters.channel_id = channelId;
      if (status) filters.status = status;
      if (requestId) filters.request_id = requestId;
      if (dateRange?.[0]) filters.start_timestamp = dateRange[0].unix();
      if (dateRange?.[1]) filters.end_timestamp = dateRange[1].unix();
      return usageLogApi.list(filters);
    },
    [username, modelName, channelId, status, requestId, dateRange],
  );

  const { dataSource, loading, pagination, refresh } = useTable<UsageLog>({
    fetchFn,
    defaultPageSize: 20,
    deps: [username, modelName, channelId, status, requestId, dateRange],
  });

  const handleViewDetail = async (record: UsageLog) => {
    setDetailLoading(true);
    setDetailLog(record);
    try {
      const detail = await usageLogApi.getDetail(record.request_id);
      setDetailLog(detail);
    } catch {
      message.error('加载详情失败');
    } finally {
      setDetailLoading(false);
    }
  };

  const handleExport = () => {
    const m = getMoney();
    exportToCSV(
      dataSource.map((row) => ({
        time: formatTime(row.created_at),
        request_id: row.request_id,
        user: row.username || `#${row.user_id}`,
        model: row.model_name,
        upstream_model: row.upstream_model,
        channel: row.channel_name || `#${row.channel_id}`,
        prompt_tokens: row.prompt_tokens,
        completion_tokens: row.completion_tokens,
        credits_charged: row.credits_charged,
        charged_amount: m.formatValue(row.credits_charged).toFixed(m.detailDecimals),
        credits_cost: row.credits_cost,
        cost_amount: m.formatValue(row.credits_cost).toFixed(m.detailDecimals),
        status: UsageStatusLabel[row.status] ?? row.status,
        error_class: row.error_class ? (ErrorClassLabel[row.error_class] ?? row.error_class) : '',
        latency_ms: row.latency_ms,
        is_stream: row.is_stream ? '是' : '否',
        estimated: row.usage_estimated ? '是' : '否',
      })),
      `usage-logs-${dayjs().format('YYYYMMDD')}.csv`,
      [
        { key: 'time', title: '时间' },
        { key: 'request_id', title: 'Request ID' },
        { key: 'user', title: '用户' },
        { key: 'model', title: '模型' },
        { key: 'upstream_model', title: '上游模型' },
        { key: 'channel', title: '渠道' },
        { key: 'prompt_tokens', title: '输入 Tokens' },
        { key: 'completion_tokens', title: '输出 Tokens' },
        { key: 'credits_charged', title: '扣费积分' },
        { key: 'charged_amount', title: '扣费金额' },
        { key: 'credits_cost', title: '成本积分' },
        { key: 'cost_amount', title: '成本金额' },
        { key: 'status', title: '状态' },
        { key: 'error_class', title: '异常分类' },
        { key: 'latency_ms', title: '耗时(ms)' },
        { key: 'is_stream', title: '流式' },
        { key: 'estimated', title: '估算标记' },
      ],
    );
  };

  const columns: ColumnsType<UsageLog> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      sorter: (a, b) => a.created_at.localeCompare(b.created_at),
      render: (t: string) => formatTime(t),
      width: 160,
    },
    {
      title: 'Request ID',
      dataIndex: 'request_id',
      ellipsis: true,
      width: 140,
      render: (v: string, record: UsageLog) => (
        <a onClick={() => handleViewDetail(record)}>
          <code style={{ fontSize: 12 }}>{v}</code>
        </a>
      ),
    },
    {
      title: '用户',
      dataIndex: 'username',
      width: 130,
      ellipsis: true,
      render: (name: string, record: UsageLog) => nameWithId(name, record.user_id),
    },
    { title: '模型', dataIndex: 'model_name', ellipsis: true, width: 190 },
    {
      title: '上游模型',
      dataIndex: 'upstream_model',
      ellipsis: true,
      width: 160,
      render: (v: string) => v || '-',
      responsive: ['lg'] as const,
    },
    {
      title: '渠道',
      dataIndex: 'channel_name',
      width: 130,
      ellipsis: true,
      render: (name: string, record: UsageLog) => nameWithId(name, record.channel_id),
    },
    {
      title: '输入',
      dataIndex: 'prompt_tokens',
      render: (n: number) => formatNumber(n),
      align: 'right' as const,
      width: 90,
      responsive: ['lg'] as const,
    },
    {
      title: '输出',
      dataIndex: 'completion_tokens',
      render: (n: number) => formatNumber(n),
      align: 'right' as const,
      width: 90,
      responsive: ['lg'] as const,
    },
    {
      title: '收费',
      dataIndex: 'credits_charged',
      sorter: (a, b) => a.credits_charged - b.credits_charged,
      render: (v: number) => money.formatDetail(v),
      align: 'right' as const,
      width: 100,
    },
    {
      title: '成本',
      dataIndex: 'credits_cost',
      sorter: (a, b) => a.credits_cost - b.credits_cost,
      render: (v: number) => money.formatDetail(v),
      align: 'right' as const,
      width: 100,
      responsive: ['xl'] as const,
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (v: UsageStatus, record: UsageLog) => (
        <Space size={4} wrap>
          <Tag color={statusColor[v] ?? 'default'}>{UsageStatusLabel[v] ?? v}</Tag>
          {record.usage_estimated && <Tag color="gold">估算</Tag>}
          {/* 流式中断的调用照常结算，状态与正常结束的完全一样，靠这个标记才认得出。 */}
          {record.error_class && (
            <Tooltip title={record.error_message || undefined}>
              <Tag color={v === 'settled' ? 'orange' : 'red'}>
                {ErrorClassLabel[record.error_class] ?? record.error_class}
              </Tag>
            </Tooltip>
          )}
        </Space>
      ),
      width: 190,
    },
    {
      title: '耗时',
      dataIndex: 'latency_ms',
      sorter: (a, b) => a.latency_ms - b.latency_ms,
      render: (t: number) => formatElapsedTime(t),
      align: 'right' as const,
      width: 80,
      responsive: ['xl'] as const,
    },
    {
      title: '流式',
      dataIndex: 'is_stream',
      render: (v: boolean) => (v ? '是' : '否'),
      width: 60,
      responsive: ['xl'] as const,
    },
  ];

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Title level={4} style={{ margin: 0 }}>
          用量日志
        </Title>
      </div>

      <Card>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'center', marginBottom: 16 }}>
          <RangePicker showTime onChange={(dates) => setDateRange(dates as [Dayjs | null, Dayjs | null] | null)} />
          <Input
            placeholder="用户名"
            style={{ width: 150 }}
            allowClear
            prefix={<SearchOutlined />}
            onPressEnter={(e) => setUsername((e.target as HTMLInputElement).value)}
            onBlur={(e) => setUsername(e.target.value)}
          />
          <Input
            placeholder="模型名称"
            style={{ width: 160 }}
            allowClear
            prefix={<SearchOutlined />}
            onPressEnter={(e) => setModelName((e.target as HTMLInputElement).value)}
            onBlur={(e) => setModelName(e.target.value)}
          />
          <Input
            placeholder="渠道 ID"
            style={{ width: 110 }}
            allowClear
            onPressEnter={(e) => setChannelId((e.target as HTMLInputElement).value)}
            onBlur={(e) => setChannelId(e.target.value)}
          />
          <Select
            placeholder="状态"
            allowClear
            style={{ width: 120 }}
            onChange={setStatus}
            options={statusOptions}
          />
          <Input
            placeholder="Request ID"
            style={{ width: 160 }}
            allowClear
            onPressEnter={(e) => setRequestId((e.target as HTMLInputElement).value)}
            onBlur={(e) => setRequestId(e.target.value)}
          />
          <Space>
            <Button icon={<ReloadOutlined />} onClick={refresh}>
              刷新
            </Button>
            <Button icon={<DownloadOutlined />} onClick={handleExport} disabled={dataSource.length === 0}>
              导出当前页
            </Button>
          </Space>
        </div>
        <Table
          columns={columns}
          dataSource={dataSource}
          rowKey="id"
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1620 }}
          sticky
        />
      </Card>

      <Modal
        title={`请求详情 - ${detailLog?.request_id ?? ''}`}
        open={!!detailLog}
        onCancel={() => setDetailLog(null)}
        footer={null}
        width={700}
      >
        {detailLog && (
          <div style={{ fontSize: 13 }}>
            <p>状态：<Tag color={statusColor[detailLog.status]}>{UsageStatusLabel[detailLog.status]}</Tag></p>
            <p>模型：{detailLog.model_name}（上游：{detailLog.upstream_model || '-'}）</p>
            <p>
              Tokens：输入 {formatNumber(detailLog.prompt_tokens)} / 输出 {formatNumber(detailLog.completion_tokens)} /
              缓存读 {formatNumber(detailLog.cache_read_tokens)} / 缓存写 {formatNumber(detailLog.cache_write_tokens)}
            </p>
            <p>
              收费 {money.formatDetail(detailLog.credits_charged)} · 成本 {money.formatDetail(detailLog.credits_cost)} ·
              时段倍率 {detailLog.peak_multiplier_percent}%
            </p>
            {detailLog.error_message && (
              <p style={{ color: '#e34d59' }}>
                错误：[{detailLog.error_class}] {detailLog.error_message}
              </p>
            )}
            <Typography.Paragraph strong style={{ marginTop: 12, marginBottom: 4 }}>
              计费快照 (price_snapshot)
            </Typography.Paragraph>
            <pre
              style={{
                background: '#f5f5f0',
                padding: 12,
                borderRadius: 6,
                fontSize: 12,
                maxHeight: 300,
                overflow: 'auto',
              }}
            >
              {detailLoading
                ? '加载中...'
                : JSON.stringify(detailLog.price_snapshot ?? {}, null, 2)}
            </pre>
          </div>
        )}
      </Modal>
    </div>
  );
}

export default LogsPage;
