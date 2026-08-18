import { useEffect, useState, useMemo } from 'react';
import { Card, Input, Table, Typography, Tooltip, Tag, Space } from 'antd';
import { SearchOutlined, InfoCircleOutlined, ClockCircleOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { ModelInfo, ModelPeakRule, Provider, Capability } from '@token-zen/shared';
import { ModalityLabel, BillingModeLabel, CapabilityLabel, ProviderCatalog } from '@token-zen/shared';
import { modelsApi } from '@/api/models';
import { useMoney } from '@/stores/site';

const { Title } = Typography;

function minuteToTime(minute: number): string {
  const h = Math.floor(minute / 60).toString().padStart(2, '0');
  const m = (minute % 60).toString().padStart(2, '0');
  return `${h}:${m}`;
}

function formatMultiplier(percent: number): string {
  const value = percent / 100;
  return Number.isInteger(value) ? `×${value}` : `×${value.toFixed(1)}`;
}

function PeakRuleTags({ rules }: { rules: ModelPeakRule[] }) {
  const active = rules.filter((r) => r.enabled);
  if (active.length === 0) return <span>-</span>;
  return (
    <Space size={4} wrap>
      {active.map((r) => (
        <Tooltip key={r.id} title={`时区 ${r.timezone}`}>
          <Tag icon={<ClockCircleOutlined />} color="orange">
            {minuteToTime(r.start_minute)}-{minuteToTime(r.end_minute)} {formatMultiplier(r.multiplier_percent)}
          </Tag>
        </Tooltip>
      ))}
    </Space>
  );
}

function ModelsPage() {
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const money = useMoney();

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      try {
        const rows = await modelsApi.list();
        rows.sort((a, b) => a.display_name.localeCompare(b.display_name));
        setModels(rows);
      } catch {
        setModels([]);
      } finally {
        setLoading(false);
      }
    };
    load();
  }, []);

  const dataSource = useMemo(() => {
    if (!keyword) return models;
    const kw = keyword.toLowerCase();
    return models.filter(
      (m) =>
        m.name.toLowerCase().includes(kw) ||
        m.display_name.toLowerCase().includes(kw),
    );
  }, [models, keyword]);

  const priceCell = (credits: number) => (
    <span>{money.formatDetail(credits)}</span>
  );

  const columns: ColumnsType<ModelInfo> = [
    {
      title: '模型',
      dataIndex: 'display_name',
      sorter: (a, b) => a.display_name.localeCompare(b.display_name),
      defaultSortOrder: 'ascend' as const,
      render: (name: string, record: ModelInfo) => (
        <div>
          <div>
            {name}
            {record.available === false && (
              <Tooltip title="该模型暂无可用渠道，当前调用会失败">
                <Tag style={{ marginLeft: 8 }}>暂不可用</Tag>
              </Tooltip>
            )}
          </div>
          <div style={{ fontSize: 12, color: '#999' }}>{record.name}</div>
        </div>
      ),
      width: '22%',
    },
    {
      title: '模态',
      dataIndex: 'modality',
      width: 90,
      render: (m: ModelInfo['modality']) => <Tag>{ModalityLabel[m]}</Tag>,
    },
    {
      title: '计费方式',
      dataIndex: 'billing_mode',
      width: 100,
      render: (b: ModelInfo['billing_mode']) => <Tag color="blue">{BillingModeLabel[b]}</Tag>,
    },
    {
      title: '厂商',
      dataIndex: 'provider',
      width: 120,
      render: (p: string | undefined) => {
        if (!p) return '-';
        const name = ProviderCatalog[p as Provider]?.name ?? p;
        return <Tag color="blue">{name}</Tag>;
      },
    },
    {
      title: '上下文',
      dataIndex: 'context_window',
      width: 100,
      align: 'right' as const,
      render: (v: number | undefined) =>
        v ? (v >= 1_000_000 ? `${v / 1_000_000}M` : v.toLocaleString()) : '-',
    },
    {
      title: '能力',
      dataIndex: 'capabilities',
      width: 170,
      render: (caps: string[] | undefined) =>
        caps && caps.length > 0 ? (
          <Space size={4} wrap>
            {caps.map((c) => (
              <Tag key={c}>{CapabilityLabel[c as Capability] ?? c}</Tag>
            ))}
          </Space>
        ) : (
          '-'
        ),
    },
    {
      title: `输入价格（${money.symbol} / 1M tokens）`,
      key: 'input_price',
      align: 'right',
      render: (_: unknown, record: ModelInfo) => {
        if (record.billing_mode !== 'per_token' || !record.price) return '-';
        return priceCell(record.price.input_price);
      },
    },
    {
      title: `输出价格（${money.symbol} / 1M tokens）`,
      key: 'output_price',
      align: 'right',
      render: (_: unknown, record: ModelInfo) => {
        if (record.billing_mode !== 'per_token' || !record.price) return '-';
        return priceCell(record.price.output_price);
      },
    },
    {
      title: `单次价格（${money.symbol} / 次）`,
      key: 'per_call_price',
      align: 'right',
      render: (_: unknown, record: ModelInfo) => {
        if (record.billing_mode !== 'per_call' || !record.price) return '-';
        return priceCell(record.price.per_call_price);
      },
    },
    {
      title: (
        <span>
          时段倍率
          <Tooltip title="部分时段按更高倍率计费，未列出的时段按标准价格结算">
            <InfoCircleOutlined style={{ marginLeft: 4, color: '#999', fontSize: 12 }} />
          </Tooltip>
        </span>
      ),
      key: 'peak_rules',
      render: (_: unknown, record: ModelInfo) => (
        <PeakRuleTags rules={record.peak_rules ?? []} />
      ),
    },
  ];

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        可用模型
      </Title>
      <div style={{ marginBottom: 16 }}>
        <Input
          placeholder="搜索模型..."
          prefix={<SearchOutlined />}
          style={{ maxWidth: 300 }}
          allowClear
          onChange={(e) => setKeyword(e.target.value)}
        />
      </div>
      <Card>
        <Table
          columns={columns}
          dataSource={dataSource}
          rowKey="id"
          loading={loading}
          rowClassName={(record) => (record.available === false ? 'model-row-unavailable' : '')}
          pagination={{
            pageSize: 20,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 个模型`,
          }}
          size="small"
        />
      </Card>
    </div>
  );
}

export default ModelsPage;
