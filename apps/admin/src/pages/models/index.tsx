import { useState, useCallback, useMemo } from 'react';
import { Card, Table, Typography, Input, InputNumber, Button, Space, Form, Tag, Select, Switch, TimePicker, Drawer, Empty, Popconfirm, Tooltip } from 'antd';
import { message } from '@token-zen/shared';
import {
  ReloadOutlined,
  PlusOutlined,
  DollarOutlined,
  ClockCircleOutlined,
  BarChartOutlined,
  DeleteOutlined,
  ImportOutlined,
  EditOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type {
  ModelInfo,
  ModelPeakRule,
  ChannelCost,
  Modality,
  BillingMode,
  ModelStatus,
  Provider,
} from '@token-zen/shared';
import {
  ModalityLabel,
  BillingModeLabel,
  ModelStatusLabel,
  ProviderLabel,
  WeekdayLabel,
} from '@token-zen/shared';
import { useTable, useModalForm } from '@token-zen/shared/hooks';
import { modelApi, type ModelPayload, type ModelPricePayload } from '@/api/models';
import { useMoney } from '@/stores/site';
import ImportModal from './ImportModal';
import ModelFormDrawer from './ModelFormDrawer';
import dayjs, { type Dayjs } from 'dayjs';

const { Title } = Typography;

const statusOptions = Object.entries(ModelStatusLabel).map(([value, label]) => ({
  value: value as ModelStatus,
  label,
}));
const providerOptions = Object.entries(ProviderLabel).map(([value, label]) => ({
  value: value as Provider,
  label,
}));
const weekdayOptions = Object.entries(WeekdayLabel).map(([value, label]) => ({
  value: Number(value),
  label,
}));

function minutesToTime(minutes: number): Dayjs {
  return dayjs().startOf('day').add(minutes, 'minute');
}
function timeToMinutes(t: Dayjs | null): number {
  if (!t) return 0;
  return t.hour() * 60 + t.minute();
}

interface PeakRuleRow {
  key: string;
  timezone: string;
  start: Dayjs;
  end: Dayjs;
  days_of_week: number[];
  multiplier_percent: number;
  enabled: boolean;
}

function ModelsPage() {
  const money = useMoney();
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<ModelStatus | undefined>();
  const [providerFilter, setProviderFilter] = useState<Provider | undefined>();
  const [importOpen, setImportOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<ModelInfo | null>(null);
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create');

  // ─── Price drawer ───
  const [priceModel, setPriceModel] = useState<ModelInfo | null>(null);
  const [priceForm] = Form.useForm();
  const [priceSaving, setPriceSaving] = useState(false);

  // ─── Peak rule drawer ───
  const [peakModel, setPeakModel] = useState<ModelInfo | null>(null);
  const [peakRows, setPeakRows] = useState<PeakRuleRow[]>([]);
  const [peakSaving, setPeakSaving] = useState(false);

  // ─── Channel cost comparison drawer ───
  const [costsModel, setCostsModel] = useState<ModelInfo | null>(null);
  const [costsData, setCostsData] = useState<ChannelCost[]>([]);
  const [costsLoading, setCostsLoading] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      modelApi.list({
        ...params,
        ...(search ? { keyword: search } : {}),
        ...(statusFilter ? { status: statusFilter } : {}),
        ...(providerFilter ? { provider: providerFilter } : {}),
      }),
    [search, statusFilter, providerFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<ModelInfo>({
    fetchFn,
    defaultPageSize: 20,
    deps: [search, statusFilter, providerFilter],
  });

  const formDrawer = useModalForm({
    onSubmit: async (values) => {
      const payload = values as unknown as ModelPayload;
      if (drawerMode === 'edit') {
        await modelApi.update(editingModel!.id, payload);
        message.success('模型已更新');
      } else {
        await modelApi.create(payload);
        message.success('模型已创建');
      }
    },
    onSuccess: refresh,
  });

  const openEdit = (record: ModelInfo) => {
    setDrawerMode('edit');
    setEditingModel(record);
    formDrawer.show({
      name: record.name,
      display_name: record.display_name,
      description: record.description,
      modality: record.modality,
      billing_mode: record.billing_mode,
      status: record.status,
      tags: record.tags,
      provider: record.provider ?? '',
      context_window: record.context_window ?? 0,
      max_output: record.max_output ?? 0,
      capabilities: record.capabilities ?? [],
      alias: record.alias ?? '',
    });
  };

  const openCreate = () => {
    setDrawerMode('create');
    setEditingModel(null);
    formDrawer.show({
      modality: 'text',
      billing_mode: 'per_token',
      status: 'enabled',
      provider: '',
      context_window: 0,
      max_output: 0,
      capabilities: [],
      alias: '',
    });
  };

  const openPriceDrawer = (model: ModelInfo) => {
    setPriceModel(model);
    // 表单现为货币金额，从已有积分单价折算回显
    priceForm.setFieldsValue({
      input_price: money.fromCredits(model.price?.input_price ?? 0),
      output_price: money.fromCredits(model.price?.output_price ?? 0),
      cache_read_price: money.fromCredits(model.price?.cache_read_price ?? 0),
      cache_write_price: money.fromCredits(model.price?.cache_write_price ?? 0),
      audio_input_price: money.fromCredits(model.price?.audio_input_price ?? 0),
      audio_output_price: money.fromCredits(model.price?.audio_output_price ?? 0),
      per_call_price: money.fromCredits(model.price?.per_call_price ?? 0),
    });
  };

  const handleSavePrice = async () => {
    if (!priceModel) return;
    try {
      const values = await priceForm.validateFields();
      setPriceSaving(true);
      // 表单收集的是货币金额，提交时逐项转回积分（API 字段仍为积分）
      const v = values as Record<string, number>;
      const creditsValues: ModelPricePayload = {
        input_price: money.toCredits(Number(v.input_price) || 0),
        output_price: money.toCredits(Number(v.output_price) || 0),
        cache_read_price: money.toCredits(Number(v.cache_read_price) || 0),
        cache_write_price: money.toCredits(Number(v.cache_write_price) || 0),
        audio_input_price: money.toCredits(Number(v.audio_input_price) || 0),
        audio_output_price: money.toCredits(Number(v.audio_output_price) || 0),
        per_call_price: money.toCredits(Number(v.per_call_price) || 0),
      };
      await modelApi.setPrice(priceModel.id, creditsValues);
      message.success('定价已保存');
      setPriceModel(null);
      refresh();
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return;
      message.error(err instanceof Error ? err.message : '保存失败');
    } finally {
      setPriceSaving(false);
    }
  };

  const openPeakDrawer = (model: ModelInfo) => {
    setPeakModel(model);
    const rules = model.peak_rules ?? [];
    setPeakRows(
      rules.map((r: ModelPeakRule, i: number) => ({
        key: `${r.id ?? 'new'}-${i}`,
        timezone: r.timezone || 'Asia/Shanghai',
        start: minutesToTime(r.start_minute),
        end: minutesToTime(r.end_minute),
        days_of_week: r.days_of_week?.length ? r.days_of_week : [1, 2, 3, 4, 5, 6, 7],
        multiplier_percent: r.multiplier_percent,
        enabled: r.enabled,
      })),
    );
  };

  const handleAddPeakRow = () => {
    setPeakRows((prev) => [
      ...prev,
      {
        key: `new-${Date.now()}`,
        timezone: 'Asia/Shanghai',
        start: minutesToTime(0),
        end: minutesToTime(1439),
        days_of_week: [1, 2, 3, 4, 5, 6, 7],
        multiplier_percent: 100,
        enabled: true,
      },
    ]);
  };

  const handlePeakFieldChange = (key: string, field: keyof PeakRuleRow, value: unknown) => {
    setPeakRows((prev) => prev.map((r) => (r.key === key ? { ...r, [field]: value } : r)));
  };

  const handleRemovePeakRow = (key: string) => {
    setPeakRows((prev) => prev.filter((r) => r.key !== key));
  };

  const handleSavePeakRules = async () => {
    if (!peakModel) return;
    const invalid = peakRows.find(
      (r) => timeToMinutes(r.start) >= timeToMinutes(r.end) || r.multiplier_percent < 100,
    );
    if (invalid) {
      message.error('存在不合法的规则：结束时间须晚于开始时间，倍率须 ≥ 100');
      return;
    }
    setPeakSaving(true);
    try {
      await modelApi.setPeakRules(
        peakModel.id,
        peakRows.map((r) => ({
          timezone: r.timezone,
          start_minute: timeToMinutes(r.start),
          end_minute: timeToMinutes(r.end),
          days_of_week: r.days_of_week,
          multiplier_percent: r.multiplier_percent,
          enabled: r.enabled,
        })),
      );
      message.success('时段倍率已保存');
      setPeakModel(null);
      refresh();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '保存失败');
    } finally {
      setPeakSaving(false);
    }
  };

  const openCostsDrawer = async (model: ModelInfo) => {
    setCostsModel(model);
    setCostsLoading(true);
    try {
      const data = await modelApi.getChannelCosts(model.id);
      setCostsData(data);
    } catch {
      setCostsData([]);
    } finally {
      setCostsLoading(false);
    }
  };

  const unitLabel = (model: ModelInfo | null) =>
    model?.billing_mode === 'per_call' ? `${money.symbol} / 次` : `${money.symbol} / 1M tokens`;

  const columns: ColumnsType<ModelInfo> = useMemo(
    () => [
      {
        // 模型名是客户端调用时要照抄的标识，列宽优先给它，展示名称次之。
        title: '模型',
        dataIndex: 'name',
        width: 260,
        ellipsis: true,
        sorter: (a, b) => a.name.localeCompare(b.name),
        defaultSortOrder: 'ascend' as const,
        render: (name: string, record: ModelInfo) => (
          <a
            onClick={() => openEdit(record)}
            style={{ fontWeight: 500 }}
          >
            {name}
          </a>
        ),
      },
      { title: '展示名称', dataIndex: 'display_name', render: (v: string) => v || '-', width: 140, ellipsis: true },
      {
        title: '形态',
        dataIndex: 'modality',
        render: (v: Modality) => ModalityLabel[v] ?? v,
        width: 90,
      },
      {
        title: '计费方式',
        dataIndex: 'billing_mode',
        render: (v: BillingMode) => BillingModeLabel[v] ?? v,
        width: 100,
      },
      {
        title: '状态',
        dataIndex: 'status',
        render: (v: ModelStatus) => (
          <Tag color={v === 'enabled' ? 'green' : 'default'}>{ModelStatusLabel[v] ?? v}</Tag>
        ),
        width: 90,
      },
      {
        title: '承载渠道',
        dataIndex: 'channel_count',
        width: 90,
        render: (v: number | undefined, record: ModelInfo) =>
          (v ?? 0) > 0 ? (
            <Tag color="blue">{v} 条</Tag>
          ) : (
            <Tooltip
              title={
                record.status === 'enabled'
                  ? '无启用渠道承载，用户调用该模型会失败'
                  : '无启用渠道承载'
              }
            >
              <Tag color="orange">无渠道</Tag>
            </Tooltip>
          ),
      },
      {
        title: '输入单价',
        key: 'input_price',
        width: 140,
        align: 'right' as const,
        render: (_: unknown, record: ModelInfo) =>
          record.price ? (
            <span>
              {money.formatDetail(
                record.billing_mode === 'per_call'
                  ? record.price.per_call_price
                  : record.price.input_price,
              )}
            </span>
          ) : (
            <Tag>未定价</Tag>
          ),
      },
      {
        title: '时段规则',
        key: 'peak_rules',
        width: 90,
        render: (_: unknown, record: ModelInfo) =>
          record.peak_rules && record.peak_rules.length > 0 ? (
            <Tag color="blue">{record.peak_rules.filter((r) => r.enabled).length} 条</Tag>
          ) : (
            '-'
          ),
      },
      {
        title: '操作',
        key: 'action',
        width: 260,
        fixed: 'right' as const,
        render: (_: unknown, record: ModelInfo) => (
          <Space size="small">
            <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Button type="link" size="small" icon={<DollarOutlined />} onClick={() => openPriceDrawer(record)}>
              定价
            </Button>
            <Button type="link" size="small" icon={<ClockCircleOutlined />} onClick={() => openPeakDrawer(record)}>
              时段倍率
            </Button>
            <Button type="link" size="small" icon={<BarChartOutlined />} onClick={() => openCostsDrawer(record)}>
              比价
            </Button>
          </Space>
        ),
      },
    ],
    [money],
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
            模型管理
          </Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            维护模型定价（{money.symbol} / 1M tokens 或 {money.symbol} / 次）与时段倍率规则；渠道成本比价供定价参考。
          </Typography.Text>
        </Space>
        <Space>
          <Select
            placeholder="状态筛选"
            allowClear
            style={{ width: 120 }}
            onChange={setStatusFilter}
            options={statusOptions}
          />
          <Select
            placeholder="厂商筛选"
            allowClear
            style={{ width: 140 }}
            onChange={setProviderFilter}
            options={providerOptions}
          />
          <Input.Search
            placeholder="搜索模型"
            onSearch={setSearch}
            allowClear
            style={{ width: 200 }}
          />
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button icon={<ImportOutlined />} onClick={() => setImportOpen(true)}>
            批量导入
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新增模型
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
          scroll={{ x: 1330 }}
          sticky
        />
      </Card>

      <ImportModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={refresh}
      />

      <ModelFormDrawer
        mode={drawerMode}
        open={formDrawer.open}
        loading={formDrawer.loading}
        form={formDrawer.form}
        editingModel={editingModel}
        onClose={formDrawer.close}
        onOk={formDrawer.handleOk}
      />

      {/* Price Drawer */}
      <Drawer
        title={`定价 - ${priceModel?.name ?? ''}`}
        open={!!priceModel}
        onClose={() => setPriceModel(null)}
        width={480}
        extra={
          <Button type="primary" loading={priceSaving} onClick={handleSavePrice}>
            保存
          </Button>
        }
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
          单价单位：{unitLabel(priceModel)}。
        </Typography.Paragraph>
        <Form form={priceForm} layout="vertical">
          {priceModel?.billing_mode === 'per_call' ? (
            <PriceField name="per_call_price" label="按次单价" unit={`${money.symbol} / 次`} />
          ) : (
            <>
              <PriceField name="input_price" label="输入单价" unit={`${money.symbol} / 1M tokens`} />
              <PriceField name="output_price" label="输出单价" unit={`${money.symbol} / 1M tokens`} />
              <PriceField name="cache_read_price" label="缓存读取单价" unit={`${money.symbol} / 1M tokens`} />
              <PriceField name="cache_write_price" label="缓存写入单价" unit={`${money.symbol} / 1M tokens`} />
              <PriceField name="audio_input_price" label="音频输入单价" unit={`${money.symbol} / 1M tokens`} />
              <PriceField name="audio_output_price" label="音频输出单价" unit={`${money.symbol} / 1M tokens`} />
            </>
          )}
        </Form>
      </Drawer>

      {/* Peak Rules Drawer */}
      <Drawer
        title={`时段倍率 - ${peakModel?.name ?? ''}`}
        open={!!peakModel}
        onClose={() => setPeakModel(null)}
        width={780}
        extra={
          <Space>
            <Button icon={<PlusOutlined />} onClick={handleAddPeakRow}>
              添加规则
            </Button>
            <Button type="primary" loading={peakSaving} onClick={handleSavePeakRules}>
              保存
            </Button>
          </Space>
        }
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
          在指定时区、星期与时段内，按倍率百分数（≥100）上浮定价。保存为全量替换。
        </Typography.Paragraph>
        {peakRows.length === 0 ? (
          <Empty description="暂无时段倍率规则" />
        ) : (
          <Table
            dataSource={peakRows}
            rowKey="key"
            pagination={false}
            size="small"
            columns={[
              {
                title: '时区',
                dataIndex: 'timezone',
                width: 150,
                render: (v: string, record: PeakRuleRow) => (
                  <Input value={v} onChange={(e) => handlePeakFieldChange(record.key, 'timezone', e.target.value)} />
                ),
              },
              {
                title: '开始',
                dataIndex: 'start',
                width: 110,
                render: (v: Dayjs, record: PeakRuleRow) => (
                  <TimePicker
                    value={v}
                    format="HH:mm"
                    minuteStep={5}
                    onChange={(t) => handlePeakFieldChange(record.key, 'start', t ?? minutesToTime(0))}
                  />
                ),
              },
              {
                title: '结束',
                dataIndex: 'end',
                width: 110,
                render: (v: Dayjs, record: PeakRuleRow) => (
                  <TimePicker
                    value={v}
                    format="HH:mm"
                    minuteStep={5}
                    onChange={(t) => handlePeakFieldChange(record.key, 'end', t ?? minutesToTime(1439))}
                  />
                ),
              },
              {
                title: '星期',
                dataIndex: 'days_of_week',
                width: 220,
                render: (v: number[], record: PeakRuleRow) => (
                  <Select
                    mode="multiple"
                    value={v}
                    style={{ width: '100%' }}
                    options={weekdayOptions}
                    onChange={(val) => handlePeakFieldChange(record.key, 'days_of_week', val)}
                    maxTagCount={3}
                  />
                ),
              },
              {
                title: '倍率(%)',
                dataIndex: 'multiplier_percent',
                width: 100,
                render: (v: number, record: PeakRuleRow) => (
                  <InputNumber
                    value={v}
                    min={100}
                    style={{ width: '100%' }}
                    onChange={(val) => handlePeakFieldChange(record.key, 'multiplier_percent', val ?? 100)}
                  />
                ),
              },
              {
                title: '启用',
                dataIndex: 'enabled',
                width: 70,
                render: (v: boolean, record: PeakRuleRow) => (
                  <Switch checked={v} onChange={(val) => handlePeakFieldChange(record.key, 'enabled', val)} />
                ),
              },
              {
                title: '',
                key: 'action',
                width: 40,
                render: (_: unknown, record: PeakRuleRow) => (
                  <Popconfirm title="删除该规则？" onConfirm={() => handleRemovePeakRow(record.key)}>
                    <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                  </Popconfirm>
                ),
              },
            ]}
          />
        )}
      </Drawer>

      {/* Channel Cost Comparison Drawer (read-only) */}
      <Drawer
        title={`渠道成本比价 - ${costsModel?.name ?? ''}`}
        open={!!costsModel}
        onClose={() => setCostsModel(null)}
        width={640}
      >
        <Table
          dataSource={costsData}
          rowKey="id"
          loading={costsLoading}
          pagination={false}
          size="small"
          locale={{ emptyText: <Empty description="尚未配置任何渠道成本" /> }}
          columns={[
            { title: '渠道 ID', dataIndex: 'channel_id', width: 80 },
            {
              title: '币种',
              dataIndex: 'currency',
              width: 110,
              render: (v: string) => (v === 'usd' ? 'USD（微美元）' : money.symbol),
            },
            // usd 行是上游厂商微美元原值，保持不变；credits 行折算为本站货币明细
            {
              title: '输入 / 1M tokens',
              dataIndex: 'input_cost',
              align: 'right' as const,
              render: (v: number, record: ChannelCost) =>
                record.currency === 'usd' ? v : money.formatDetail(v ?? 0),
            },
            {
              title: '输出 / 1M tokens',
              dataIndex: 'output_cost',
              align: 'right' as const,
              render: (v: number, record: ChannelCost) =>
                record.currency === 'usd' ? v : money.formatDetail(v ?? 0),
            },
            {
              title: '缓存读 / 1M tokens',
              dataIndex: 'cache_read_cost',
              align: 'right' as const,
              render: (v: number, record: ChannelCost) =>
                record.currency === 'usd' ? v : money.formatDetail(v ?? 0),
            },
            {
              title: '缓存写 / 1M tokens',
              dataIndex: 'cache_write_cost',
              align: 'right' as const,
              render: (v: number, record: ChannelCost) =>
                record.currency === 'usd' ? v : money.formatDetail(v ?? 0),
            },
            {
              title: '按次 / 次',
              dataIndex: 'per_call_cost',
              align: 'right' as const,
              render: (v: number, record: ChannelCost) =>
                record.currency === 'usd' ? v : money.formatDetail(v ?? 0),
            },
          ]}
        />
      </Drawer>
    </div>
  );
}

function PriceField({
  name,
  label,
  unit,
}: {
  name: string;
  label: string;
  unit: string;
}) {
  return (
    <Form.Item name={name} label={label} extra={`单位：${unit}`}>
      <InputNumber style={{ width: '100%' }} min={0} step={0.01} />
    </Form.Item>
  );
}

export default ModelsPage;
