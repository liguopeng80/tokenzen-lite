import { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, Button, Empty, Form, Input, InputNumber, Modal, Select, Space, Spin, Switch, Table, Tabs, Tag, Typography, Upload } from 'antd';
import { UploadOutlined } from '@ant-design/icons';
import { message } from '@token-zen/shared';
import type { ColumnsType } from 'antd/es/table';
import type { Capability } from '@token-zen/shared';
import { ModalityLabel, CapabilityLabel } from '@token-zen/shared';
import {
  modelApi,
  type ModelImportItem,
  type ModelImportSummary,
  type PresetCatalog,
  type PresetModel,
} from '@/api/models';
import { useMoney } from '@/stores/site';
import { parseImportJSON } from './importParse';

const { Text, Paragraph } = Typography;

/** 加价百分数取值范围，与后端 minMarkupPercent / maxMarkupPercent 一致。 */
const MIN_MARKUP = 100;
const MAX_MARKUP = 1000;

/** 微美元转美元展示（1 美元 = 1,000,000 微美元）。 */
function formatUSD(microUSD: number): string {
  if (!microUSD) return '-';
  return `$${(microUSD / 1_000_000).toFixed(3)}`;
}

const JSON_TEMPLATE = `{
  "items": [
    {
      "name": "gpt-4o",
      "display_name": "GPT-4o",
      "modality": "text",
      "billing_mode": "per_token",
      "status": "enabled",
      "price": { "input_price": 18000000, "output_price": 72000000 }
    }
  ]
}`;

interface ImportModalProps {
  open: boolean;
  onClose: () => void;
  /** 导入产生了写入时回调，用于刷新模型列表。 */
  onImported: () => void;
}

function ImportModal({ open, onClose, onImported }: ImportModalProps) {
  const money = useMoney();

  const [markup, setMarkup] = useState(MIN_MARKUP);
  const [overwrite, setOverwrite] = useState(false);
  const [catalog, setCatalog] = useState<PresetCatalog | null>(null);
  const [loadingPresets, setLoadingPresets] = useState(false);
  const [providerId, setProviderId] = useState<string>();
  const [selectedNames, setSelectedNames] = useState<string[]>([]);
  const [jsonText, setJsonText] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [summary, setSummary] = useState<ModelImportSummary | null>(null);

  const fetchPresets = useCallback(async (markupPercent: number) => {
    setLoadingPresets(true);
    try {
      setCatalog(await modelApi.getPricingPresets(markupPercent));
    } catch {
      message.error('加载预置价目失败');
    } finally {
      setLoadingPresets(false);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    setSummary(null);
    fetchPresets(markup);
    // 打开时按当前加价百分数拉一次；加价变更由输入框的 onChange 单独触发。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, fetchPresets]);

  const presetRows = useMemo(() => {
    if (!catalog) return [] as (PresetModel & { providerName: string })[];
    return catalog.providers
      .filter((p) => !providerId || p.id === providerId)
      .flatMap((p) => p.models.map((m) => ({ ...m, providerName: p.name })));
  }, [catalog, providerId]);

  const providerOptions = useMemo(
    () => (catalog?.providers ?? []).map((p) => ({ value: p.id, label: p.name })),
    [catalog],
  );

  const submit = useCallback(
    async (items: ModelImportItem[]) => {
      if (items.length === 0) {
        message.warning('请先选择要导入的模型');
        return;
      }
      setSubmitting(true);
      try {
        const result = await modelApi.importModels(items, overwrite);
        setSummary(result);
        if (result.created + result.updated > 0) onImported();
      } catch (err) {
        message.error(err instanceof Error ? err.message : '导入失败');
      } finally {
        setSubmitting(false);
      }
    },
    [overwrite, onImported],
  );

  const importSelected = useCallback(() => {
    const items = presetRows
      .filter((m) => selectedNames.includes(m.name))
      .map<ModelImportItem>((m) => ({
        name: m.name,
        display_name: m.display_name,
        description: m.description,
        modality: m.modality,
        billing_mode: m.billing_mode,
        provider: m.provider,
        context_window: m.context_window,
        max_output: m.max_output,
        capabilities: m.capabilities,
        alias: m.alias,
        status: 'enabled',
        tags: '',
        price: m.price,
      }));
    submit(items);
  }, [presetRows, selectedNames, submit]);

  const importJSON = useCallback(() => {
    const [items, err] = parseImportJSON(jsonText);
    if (err) {
      message.error(err);
      return;
    }
    submit(items);
  }, [jsonText, submit]);

  // ─── URL 导入 tab ───
  const [urlSource, setUrlSource] = useState('');
  const [urlMarkup, setUrlMarkup] = useState(MIN_MARKUP);

  const importFromUrl = useCallback(async () => {
    const trimmed = urlSource.trim();
    if (!trimmed) {
      message.warning('请输入预置价目的 URL');
      return;
    }
    if (!/^https?:\/\//i.test(trimmed)) {
      message.error('URL 须以 http:// 或 https:// 开头');
      return;
    }
    setSubmitting(true);
    try {
      const result = await modelApi.importFromUrl(trimmed, urlMarkup, overwrite);
      setSummary(result);
      if (result.created + result.updated > 0) onImported();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '导入失败');
    } finally {
      setSubmitting(false);
    }
  }, [urlSource, urlMarkup, overwrite, onImported]);

  // ─── 文件上传 tab ───
  const handleFileUpload = useCallback(
    (file: File) => {
      const reader = new FileReader();
      reader.onload = () => {
        const [items, err] = parseImportJSON(String(reader.result));
        if (err) {
          message.error(err);
          return;
        }
        submit(items);
      };
      reader.onerror = () => message.error('文件读取失败');
      reader.readAsText(file);
      // 返回 false 阻止 antd 自动上传
      return false;
    },
    [submit],
  );

  const presetColumns: ColumnsType<PresetModel & { providerName: string }> = [
    {
      title: '模型',
      dataIndex: 'name',
      width: 180,
      render: (name: string, row) => (
        <Space direction="vertical" size={0}>
          <Text strong>{row.display_name || name}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {name}
          </Text>
        </Space>
      ),
    },
    { title: '厂商', dataIndex: 'providerName', width: 90 },
    {
      title: '形态',
      dataIndex: 'modality',
      width: 70,
      render: (v: keyof typeof ModalityLabel) => <Tag>{ModalityLabel[v] ?? v}</Tag>,
    },
    {
      title: '上下文',
      dataIndex: 'context_window',
      width: 70,
      render: (v: number | undefined) =>
        v ? (v >= 1_000_000 ? `${v / 1_000_000}M` : v.toLocaleString()) : '-',
    },
    {
      title: '能力',
      dataIndex: 'capabilities',
      width: 110,
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
      title: '官价输入 / 1M',
      dataIndex: 'input_usd',
      width: 90,
      render: (v: number) => formatUSD(v),
    },
    {
      title: '官价输出 / 1M',
      dataIndex: 'output_usd',
      width: 90,
      render: (v: number) => formatUSD(v),
    },
    {
      title: '输入 / 1M',
      width: 120,
      render: (_, row) => <Text>{money.formatDetail(row.price.input_price)}</Text>,
    },
    {
      title: '输出 / 1M',
      width: 120,
      render: (_, row) => <Text>{money.formatDetail(row.price.output_price)}</Text>,
    },
  ];

  const presetTab = (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Alert
        type="warning"
        showIcon
        message={`预置价目采集于 ${catalog?.priced_at ?? '未知时间'}，仅作起点`}
        description={
          <Space direction="vertical" size={2}>
            <Text>{catalog?.note}</Text>
            <Text>
              导入前请对照厂商定价页核对：
              {(catalog?.providers ?? []).map((p) => (
                <a
                  key={p.id}
                  href={p.pricing_url}
                  target="_blank"
                  rel="noreferrer"
                  style={{ marginLeft: 8 }}
                >
                  {p.name}
                </a>
              ))}
            </Text>
          </Space>
        }
      />
      <Space wrap>
        <Select
          placeholder="全部厂商"
          allowClear
          style={{ width: 160 }}
          value={providerId}
          onChange={setProviderId}
          options={providerOptions}
        />
        <Space size={4}>
          <Text>加价</Text>
          <InputNumber
            min={MIN_MARKUP}
            max={MAX_MARKUP}
            step={10}
            value={markup}
            addonAfter="%"
            style={{ width: 130 }}
            onChange={(v) => {
              const next = v ?? MIN_MARKUP;
              setMarkup(next);
              fetchPresets(next);
            }}
          />
          <Text type="secondary" style={{ fontSize: 12 }}>
            100 = 按官价平价折算（汇率 {((catalog?.usd_cny_rate_milli ?? 0) / 1000).toFixed(3)}）
          </Text>
        </Space>
      </Space>
      {loadingPresets ? (
        <div style={{ padding: 48, textAlign: 'center' }}>
          <Spin />
        </div>
      ) : presetRows.length === 0 ? (
        <Empty description="没有可导入的预置模型" />
      ) : (
        <Table
          rowKey="name"
          size="small"
          columns={presetColumns}
          dataSource={presetRows}
          pagination={false}
          scroll={{ y: 320 }}
          rowSelection={{
            selectedRowKeys: selectedNames,
            onChange: (keys) => setSelectedNames(keys as string[]),
          }}
        />
      )}
      <Button type="primary" loading={submitting} onClick={importSelected}>
        导入选中的 {selectedNames.length} 个模型
      </Button>
    </Space>
  );

  const jsonTab = (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        粘贴导入内容。单价的单位是积分 / 1M tokens（按次计费的模型用 per_call_price，单位是积分 /
        次）。每个条目都必须带 price，缺定价的模型上架后会被零扣费调用。
      </Paragraph>
      <Input.TextArea
        rows={12}
        value={jsonText}
        placeholder={JSON_TEMPLATE}
        onChange={(e) => setJsonText(e.target.value)}
        style={{ fontFamily: 'monospace', fontSize: 12 }}
      />
      <Button type="primary" loading={submitting} onClick={importJSON}>
        导入
      </Button>
    </Space>
  );

  const urlTab = (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        从可公开访问的 URL 拉取预置价目（JSON，格式同「自定义 JSON」），由服务端按给定加价百分数折算后导入。
        是否覆盖已存在的同名模型由顶部开关控制。
      </Paragraph>
      <Form layout="vertical">
        <Form.Item label="价目 URL">
          <Input
            placeholder="https://example.com/models.json"
            value={urlSource}
            onChange={(e) => setUrlSource(e.target.value)}
          />
        </Form.Item>
        <Form.Item label="加价百分数" extra="100 = 按官价平价折算（汇率由服务端按当前系统设置取）">
          <InputNumber
            min={MIN_MARKUP}
            max={MAX_MARKUP}
            step={10}
            value={urlMarkup}
            addonAfter="%"
            style={{ width: 200 }}
            onChange={(v) => setUrlMarkup(v ?? MIN_MARKUP)}
          />
        </Form.Item>
      </Form>
      <Button type="primary" loading={submitting} onClick={importFromUrl}>
        从 URL 导入
      </Button>
    </Space>
  );

  const fileTab = (
    <Space direction="vertical" style={{ width: '100%' }} size="middle">
      <Paragraph type="secondary" style={{ marginBottom: 0 }}>
        上传本地 JSON 文件（格式同「自定义 JSON」的 items 数组或 {'{items:[...]}'})。
        每个条目都必须带 price，缺定价的模型上架后会被零扣费调用。
      </Paragraph>
      <Upload
        accept=".json,application/json"
        showUploadList={false}
        beforeUpload={handleFileUpload}
      >
        <Button icon={<UploadOutlined />}>选择 JSON 文件上传</Button>
      </Upload>
    </Space>
  );

  const failed = summary?.results.filter((r) => r.action === 'failed') ?? [];

  return (
    <Modal
      title="批量导入模型与定价"
      open={open}
      onCancel={onClose}
      footer={null}
      width={960}
      destroyOnHidden
    >
      <Space direction="vertical" style={{ width: '100%' }} size="middle">
        <Space>
          <Switch checked={overwrite} onChange={setOverwrite} />
          <Text>覆盖已存在的同名模型</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>
            关闭时，已存在的模型跳过，站点自定的价格不受影响
          </Text>
        </Space>

        {summary && (
          <Alert
            type={summary.failed > 0 ? 'warning' : 'success'}
            showIcon
            message={`新建 ${summary.created} 个，覆盖 ${summary.updated} 个，跳过 ${summary.skipped} 个，失败 ${summary.failed} 个`}
            description={
              failed.length > 0 && (
                <Space direction="vertical" size={0}>
                  {failed.map((r) => (
                    <Text key={r.name} type="danger" style={{ fontSize: 12 }}>
                      {r.name || '(未命名)'}：{r.message}
                    </Text>
                  ))}
                </Space>
              )
            }
          />
        )}

        <Tabs
          items={[
            { key: 'preset', label: '预置价目', children: presetTab },
            { key: 'url', label: '从 URL 导入', children: urlTab },
            { key: 'json', label: '自定义 JSON', children: jsonTab },
            { key: 'file', label: '文件上传', children: fileTab },
          ]}
        />
      </Space>
    </Modal>
  );
}

export default ImportModal;
