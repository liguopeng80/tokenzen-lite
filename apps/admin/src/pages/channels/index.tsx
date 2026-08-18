import { useState, useMemo, useCallback } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Card, Table, Button, Space, Tag, Typography, Input, Select, InputNumber, Popconfirm, Tooltip, Dropdown, Drawer, Descriptions, Empty, notification } from 'antd';
import { message, modal } from '@token-zen/shared';
import {
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  EllipsisOutlined,
  EditOutlined,
  ThunderboltOutlined,
  DollarOutlined,
  DeleteOutlined,
  PlusCircleOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type {
  Channel,
  ChannelCost,
  ChannelStatus,
  Provider,
  ChannelProtocol,
  CostCurrency,
} from '@token-zen/shared';
import {
  ChannelStatusLabel,
  ProviderLabel,
  ProviderCatalog,
  ProtocolLabel,
  formatTime,
} from '@token-zen/shared';
import { useTable, useModalForm } from '@token-zen/shared/hooks';
import { channelApi, type ChannelPayload } from '@/api/channels';
import {
  toFormValue,
  fromFormValue,
  type ModelMappingEditorValue,
} from '@/components/ModelMappingEditor';
import ChannelFormDrawer from './ChannelFormDrawer';
import PackageCreateDrawer from './PackageCreateDrawer';

const { Title, Text } = Typography;

const statusFilterOptions: { value: ChannelStatus; label: string }[] = [
  { value: 'enabled', label: ChannelStatusLabel.enabled },
  { value: 'manual_disabled', label: ChannelStatusLabel.manual_disabled },
  { value: 'auto_disabled', label: ChannelStatusLabel.auto_disabled },
];

const statusColor: Record<ChannelStatus, string> = {
  enabled: 'green',
  manual_disabled: 'orange',
  auto_disabled: 'red',
};

type CostRow = Omit<ChannelCost, 'id' | 'channel_id' | 'updated_at'> & { key: string };

function ChannelsPage() {
  const [searchParams] = useSearchParams();
  const [keyword, setKeyword] = useState(searchParams.get('search') ?? '');
  const [statusFilter, setStatusFilter] = useState<ChannelStatus | undefined>();
  const [editingChannel, setEditingChannel] = useState<Channel | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);
  const [keyEditing, setKeyEditing] = useState(false);
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create');

  // ─── Cost drawer ───
  const [costChannel, setCostChannel] = useState<Channel | null>(null);
  const [costRows, setCostRows] = useState<CostRow[]>([]);
  const [costLoading, setCostLoading] = useState(false);
  const [costSaving, setCostSaving] = useState(false);

  // ─── Provider catalog drawer ───
  const [providerDrawer, setProviderDrawer] = useState<Provider | null>(null);

  // ─── Package create drawer ───
  const [packageOpen, setPackageOpen] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) => {
      const merged = { ...params };
      if (keyword) merged.keyword = keyword;
      if (statusFilter) merged.status = statusFilter;
      return channelApi.list(merged);
    },
    [keyword, statusFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<Channel>({
    fetchFn,
    deps: [keyword, statusFilter],
  });

  const modelOptions = useMemo(() => {
    const all = new Set<string>();
    for (const c of dataSource) {
      for (const m of c.models ?? []) all.add(m);
    }
    return [...all].sort();
  }, [dataSource]);

  const handleTest = async (id: number, testModel?: string) => {
    setTestingId(id);
    try {
      const result = await channelApi.test(id, testModel);
      if (result.ok) {
        message.success(`测试通过，响应时间 ${result.latency_ms}ms`);
      } else {
        // 失败用常驻通知：完整错误文本可选中复制，用户手动关闭。
        notification.error({
          message: '渠道测试失败',
          description: (
            <Typography.Paragraph copyable style={{ marginBottom: 0 }}>
              {result.message}
            </Typography.Paragraph>
          ),
          duration: 0,
          placement: 'topRight',
        });
      }
      refresh();
    } catch (err: unknown) {
      const errText = err instanceof Error ? err.message : '未知错误';
      notification.error({
        message: '渠道测试失败',
        description: (
          <Typography.Paragraph copyable style={{ marginBottom: 0 }}>
            {errText}
          </Typography.Paragraph>
        ),
        duration: 0,
        placement: 'topRight',
      });
    } finally {
      setTestingId(null);
    }
  };

  const handleDelete = async (id: number) => {
    await channelApi.delete(id);
    message.success('渠道已删除');
    refresh();
  };

  const handleToggleStatus = async (channel: Channel) => {
    const newStatus: ChannelStatus =
      channel.status === 'enabled' ? 'manual_disabled' : 'enabled';
    await channelApi.setStatus(channel.id, newStatus);
    message.success(newStatus === 'enabled' ? '渠道已启用' : '渠道已禁用');
    refresh();
  };

  const buildPayload = (values: Record<string, unknown>): ChannelPayload => {
    const data = { ...values };
    const modelConfig = data.modelConfig as ModelMappingEditorValue | undefined;
    const { models, model_mapping } = fromFormValue(modelConfig ?? { entries: [] });
    delete data.modelConfig;
    return {
      ...(data as unknown as Omit<ChannelPayload, 'models' | 'model_mapping'>),
      models,
      model_mapping,
    };
  };

  const formDrawer = useModalForm({
    onSubmit: async (values) => {
      const payload = buildPayload(values);
      if (drawerMode === 'edit') {
        if (!payload.api_key) delete payload.api_key;
        await channelApi.update(editingChannel!.id, payload);
        message.success('渠道已更新');
      } else {
        await channelApi.create(payload);
        message.success('渠道创建成功');
      }
    },
    onSuccess: refresh,
  });

  const openEdit = (record: Channel) => {
    setDrawerMode('edit');
    setEditingChannel(record);
    setKeyEditing(false);
    formDrawer.show({
      name: record.name,
      provider: record.provider,
      protocol: record.protocol,
      base_url: record.base_url,
      modelConfig: toFormValue(record.models, record.model_mapping),
      priority: record.priority,
      weight: record.weight,
      test_model: record.test_model,
    });
  };

  const handleOpenCosts = async (channel: Channel) => {
    setCostChannel(channel);
    setCostLoading(true);
    try {
      const costs = await channelApi.getCosts(channel.id);
      setCostRows(
        costs.map((c, i) => ({
          key: `${c.model_name}-${i}`,
          model_name: c.model_name,
          currency: c.currency,
          input_cost: c.input_cost,
          output_cost: c.output_cost,
          cache_read_cost: c.cache_read_cost,
          cache_write_cost: c.cache_write_cost,
          per_call_cost: c.per_call_cost,
        })),
      );
    } catch {
      message.error('加载成本数据失败');
      setCostRows([]);
    } finally {
      setCostLoading(false);
    }
  };

  const handleAddCostRow = () => {
    setCostRows((prev) => [
      ...prev,
      {
        key: `new-${Date.now()}`,
        model_name: costChannel?.models?.[0] ?? '',
        currency: 'credits' as CostCurrency,
        input_cost: 0,
        output_cost: 0,
        cache_read_cost: 0,
        cache_write_cost: 0,
        per_call_cost: 0,
      },
    ]);
  };

  const handleCostFieldChange = (key: string, field: keyof CostRow, value: unknown) => {
    setCostRows((prev) => prev.map((r) => (r.key === key ? { ...r, [field]: value } : r)));
  };

  const handleRemoveCostRow = (key: string) => {
    setCostRows((prev) => prev.filter((r) => r.key !== key));
  };

  const handleSaveCosts = async () => {
    if (!costChannel) return;
    const invalid = costRows.find((r) => !r.model_name.trim());
    if (invalid) {
      message.error('存在未填写模型名的成本记录');
      return;
    }
    setCostSaving(true);
    try {
      await channelApi.setCosts(
        costChannel.id,
        costRows.map(({ key: _key, ...rest }) => rest),
      );
      message.success('成本价已保存');
      setCostChannel(null);
    } catch (err: unknown) {
      message.error(`保存失败：${err instanceof Error ? err.message : '未知错误'}`);
    } finally {
      setCostSaving(false);
    }
  };

  const columns: ColumnsType<Channel> = useMemo(
    () => [
      { title: 'ID', dataIndex: 'id', width: 60, sorter: (a, b) => a.id - b.id },
      {
        title: '名称', dataIndex: 'name', ellipsis: true, width: 220,
        sorter: (a: Channel, b: Channel) => a.name.localeCompare(b.name),
      },
      {
        title: '厂商',
        dataIndex: 'provider',
        render: (p: Provider) => {
          const meta = ProviderCatalog[p];
          const label = meta?.name ?? ProviderLabel[p] ?? p;
          return meta ? (
            <a onClick={() => setProviderDrawer(p)}>{label}</a>
          ) : (
            <span>{label}</span>
          );
        },
        width: 110,
      },
      {
        title: '协议',
        dataIndex: 'protocol',
        render: (p: ChannelProtocol) => ProtocolLabel[p] ?? p,
        width: 110,
        responsive: ['lg'] as const,
      },
      {
        title: '模型',
        dataIndex: 'models',
        render: (models: string[]) => {
          if (!models || models.length === 0) return '-';
          const display = models.length <= 2 ? models.join(', ') : `${models.slice(0, 2).join(', ')} 等`;
          return (
            <Tooltip title={models.join('\n')} overlayStyle={{ whiteSpace: 'pre-line' }}>
              <span>{display} ({models.length})</span>
            </Tooltip>
          );
        },
        width: 200,
        ellipsis: true,
      },
      {
        title: '状态',
        dataIndex: 'status',
        render: (status: ChannelStatus, record: Channel) => (
          <Tooltip title={record.disabled_reason || undefined}>
            <Tag color={statusColor[status] ?? 'default'}>{ChannelStatusLabel[status] ?? '未知'}</Tag>
          </Tooltip>
        ),
        width: 110,
      },
      {
        title: '优先级/权重',
        key: 'priority_weight',
        width: 110,
        align: 'center',
        responsive: ['lg'] as const,
        render: (_: unknown, record: Channel) => `${record.priority}/${record.weight}`,
        sorter: (a: Channel, b: Channel) => a.priority - b.priority || a.weight - b.weight,
      },
      {
        title: '最近测试',
        key: 'last_test',
        width: 160,
        responsive: ['xl'] as const,
        render: (_: unknown, record: Channel) => {
          if (!record.last_test_at) return '-';
          const statusTag =
            record.last_test_status === 'success' ? (
              <Tag color="green" data-testid={`channel-test-status-${record.id}`}>成功</Tag>
            ) : record.last_test_status === 'failure' ? (
              <Tag color="red" data-testid={`channel-test-status-${record.id}`}>失败</Tag>
            ) : null;
          return (
            <Space size={4} wrap>
              {statusTag}
              <span>
                {formatTime(record.last_test_at, 'MM-DD HH:mm')} ({record.last_test_latency_ms ?? '-'}ms)
              </span>
            </Space>
          );
        },
      },
      {
        // 操作列固定在右侧：1440 像素视口下表格宽度超出可视区，不固定的话
        // 编辑与测试按钮整列落在视口外，管理员看不到。
        title: '操作',
        key: 'action',
        width: 220,
        fixed: 'right' as const,
        render: (_: unknown, record: Channel) => (
          <Space size="small">
            <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Button
              type="link"
              size="small"
              icon={<ThunderboltOutlined />}
              loading={testingId === record.id}
              onClick={() => handleTest(record.id)}
            >
              测试
            </Button>
            <Dropdown
              menu={{
                items: [
                  {
                    key: 'costs',
                    icon: <DollarOutlined />,
                    label: '成本价',
                    onClick: () => handleOpenCosts(record),
                  },
                  {
                    key: 'toggle',
                    label: record.status === 'enabled' ? '禁用' : '启用',
                    onClick: () => handleToggleStatus(record),
                  },
                  { type: 'divider' },
                  {
                    key: 'delete',
                    label: '删除',
                    danger: true,
                    onClick: () => {
                      modal.confirm({
                        title: '确认删除该渠道？',
                        onOk: () => handleDelete(record.id),
                      });
                    },
                  },
                ],
              }}
              trigger={['click']}
            >
              <Button type="link" size="small" icon={<EllipsisOutlined />} />
            </Dropdown>
          </Space>
        ),
      },
    ],
    [testingId],
  );

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          marginBottom: 16,
        }}
      >
        <Space direction="vertical" size={4}>
          <Title level={4} style={{ margin: 0 }}>
            渠道管理
          </Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            厂商决定认证与请求适配方式，协议决定下游转发的兼容格式。模型列表可直接输入任意模型名。
          </Typography.Text>
        </Space>
        <Space>
          <Input.Search
            placeholder="搜索渠道"
            defaultValue={keyword}
            onSearch={setKeyword}
            allowClear
            style={{ width: 200 }}
            prefix={<SearchOutlined />}
          />
          <Select
            placeholder="状态筛选"
            style={{ width: 130 }}
            allowClear
            onChange={setStatusFilter}
            options={statusFilterOptions}
          />
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            icon={<ThunderboltOutlined />}
            onClick={() => setPackageOpen(true)}
          >
            按套餐创建
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setDrawerMode('create');
              setEditingChannel(null);
              setKeyEditing(false);
              formDrawer.show({ priority: 5, weight: 1 });
            }}
          >
            创建渠道
          </Button>
        </Space>
      </div>

      <Card>
        <Table
          columns={columns}
          dataSource={dataSource}
          rowKey="id"
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1300 }}
          sticky
        />
      </Card>

      <ChannelFormDrawer
        mode={drawerMode}
        open={formDrawer.open}
        loading={formDrawer.loading}
        form={formDrawer.form}
        editingChannel={editingChannel}
        modelOptions={modelOptions}
        keyEditing={keyEditing}
        onKeyEditingChange={setKeyEditing}
        onClose={() => {
          setKeyEditing(false);
          formDrawer.close();
        }}
        onOk={formDrawer.handleOk}
      />

      <PackageCreateDrawer
        open={packageOpen}
        onClose={() => setPackageOpen(false)}
        onCreated={refresh}
      />

      {/* Costs Drawer */}
      <Drawer
        title={`成本价 - ${costChannel?.name ?? ''}`}
        open={!!costChannel}
        onClose={() => setCostChannel(null)}
        width={800}
        extra={
          <Space>
            <Button icon={<PlusCircleOutlined />} onClick={handleAddCostRow}>
              添加
            </Button>
            <Button type="primary" loading={costSaving} onClick={handleSaveCosts}>
              保存
            </Button>
          </Space>
        }
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
          按模型维护此渠道的上游采购成本，用于模型定价页的跨渠道比价与利润分析。币种为积分时单位与模型定价一致（积分 /
          1M tokens，按次为积分 / 次）；币种为 USD 时按微美元填写（微美元 / 1M tokens，按次为微美元 / 次，1 美元 =
          1,000,000 微美元）。
        </Typography.Paragraph>
        <Table
          dataSource={costRows}
          rowKey="key"
          loading={costLoading}
          pagination={false}
          size="small"
          columns={[
            {
              title: '模型',
              dataIndex: 'model_name',
              width: 160,
              render: (v: string, record: CostRow) => (
                <Select
                  showSearch
                  value={v || undefined}
                  style={{ width: '100%' }}
                  placeholder="选择模型"
                  options={(costChannel?.models ?? []).map((m) => ({ label: m, value: m }))}
                  onChange={(val) => handleCostFieldChange(record.key, 'model_name', val)}
                />
              ),
            },
            {
              title: '币种',
              dataIndex: 'currency',
              width: 100,
              render: (v: CostCurrency, record: CostRow) => (
                <Select
                  value={v}
                  style={{ width: '100%' }}
                  options={[
                    { label: '积分', value: 'credits' },
                    { label: 'USD', value: 'usd' },
                  ]}
                  onChange={(val) => handleCostFieldChange(record.key, 'currency', val)}
                />
              ),
            },
            {
              title: '输入 / 1M tokens',
              dataIndex: 'input_cost',
              width: 100,
              render: (v: number, record: CostRow) => (
                <InputNumber
                  value={v}
                  min={0}
                  style={{ width: '100%' }}
                  onChange={(val) => handleCostFieldChange(record.key, 'input_cost', val ?? 0)}
                />
              ),
            },
            {
              title: '输出 / 1M tokens',
              dataIndex: 'output_cost',
              width: 100,
              render: (v: number, record: CostRow) => (
                <InputNumber
                  value={v}
                  min={0}
                  style={{ width: '100%' }}
                  onChange={(val) => handleCostFieldChange(record.key, 'output_cost', val ?? 0)}
                />
              ),
            },
            {
              title: '缓存读 / 1M tokens',
              dataIndex: 'cache_read_cost',
              width: 100,
              render: (v: number, record: CostRow) => (
                <InputNumber
                  value={v}
                  min={0}
                  style={{ width: '100%' }}
                  onChange={(val) => handleCostFieldChange(record.key, 'cache_read_cost', val ?? 0)}
                />
              ),
            },
            {
              title: '缓存写 / 1M tokens',
              dataIndex: 'cache_write_cost',
              width: 100,
              render: (v: number, record: CostRow) => (
                <InputNumber
                  value={v}
                  min={0}
                  style={{ width: '100%' }}
                  onChange={(val) => handleCostFieldChange(record.key, 'cache_write_cost', val ?? 0)}
                />
              ),
            },
            {
              title: '按次 / 次',
              dataIndex: 'per_call_cost',
              width: 100,
              render: (v: number, record: CostRow) => (
                <InputNumber
                  value={v}
                  min={0}
                  style={{ width: '100%' }}
                  onChange={(val) => handleCostFieldChange(record.key, 'per_call_cost', val ?? 0)}
                />
              ),
            },
            {
              title: '',
              key: 'action',
              width: 40,
              render: (_: unknown, record: CostRow) => (
                <Popconfirm title="删除该行？" onConfirm={() => handleRemoveCostRow(record.key)}>
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                </Popconfirm>
              ),
            },
          ]}
        />
      </Drawer>

      {/* Provider catalog drawer (read-only) */}
      <Drawer
        title={providerDrawer ? (ProviderCatalog[providerDrawer]?.name ?? ProviderLabel[providerDrawer]) : ''}
        open={!!providerDrawer}
        onClose={() => setProviderDrawer(null)}
        width={520}
      >
        {providerDrawer && <ProviderDetail provider={providerDrawer} />}
      </Drawer>
    </div>
  );
}

function ProviderDetail({ provider }: { provider: Provider }) {
  const meta = ProviderCatalog[provider];
  if (!meta) {
    return <Empty description="该厂商暂无目录元数据" />;
  }
  return (
    <Descriptions column={1} bordered size="small">
      <Descriptions.Item label="厂商">{meta.name}</Descriptions.Item>
      <Descriptions.Item label="官网">
        {meta.website_url ? (
          <a href={meta.website_url} target="_blank" rel="noreferrer">
            {meta.website_url}
          </a>
        ) : (
          '-'
        )}
      </Descriptions.Item>
      <Descriptions.Item label="定价页">
        {meta.pricing_url ? (
          <a href={meta.pricing_url} target="_blank" rel="noreferrer">
            {meta.pricing_url}
          </a>
        ) : (
          '-'
        )}
      </Descriptions.Item>
      <Descriptions.Item label="默认接入地址">
        {meta.default_base_url ? <Text copyable>{meta.default_base_url}</Text> : '-'}
      </Descriptions.Item>
      <Descriptions.Item label="支持协议">
        <Space size={4} wrap>
          {meta.supported_protocols.length > 0
            ? meta.supported_protocols.map((p) => <Tag key={p}>{ProtocolLabel[p]}</Tag>)
            : '-'}
        </Space>
      </Descriptions.Item>
      <Descriptions.Item label="简介">{meta.description || '-'}</Descriptions.Item>
    </Descriptions>
  );
}

export default ChannelsPage;
