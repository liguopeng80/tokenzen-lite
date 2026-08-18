import { useState, useMemo, useCallback } from 'react';
import { Card, Table, Tabs, Button, Space, Tag, Typography, Input, Modal, Form, InputNumber, DatePicker, Select } from 'antd';
import { message } from '@token-zen/shared';
import {
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  CopyOutlined,
  DownloadOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { Redemption, LedgerEntry, RedemptionStatus, LedgerEntryType } from '@token-zen/shared';
import {
  RedemptionStatusLabel,
  LedgerEntryTypeLabel,
  formatTime,
  copyToClipboard,
  exportToCSV,
} from '@token-zen/shared';
import { useTable, useModalForm } from '@token-zen/shared/hooks';
import { redemptionApi, ledgerApi, type RedemptionBatchRequest } from '@/api/billing';
import { useMoney } from '@/stores/site';
import dayjs from 'dayjs';

const { Title } = Typography;

const redemptionStatusColor: Record<RedemptionStatus, string> = {
  unused: 'success',
  used: 'default',
  disabled: 'error',
  expired: 'warning',
};

const ledgerEntryTypeOptions = Object.entries(LedgerEntryTypeLabel).map(([value, label]) => ({
  value: value as LedgerEntryType,
  label,
}));

function RedemptionTab() {
  const money = useMoney();
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState<RedemptionStatus | undefined>();
  const [generatedCodes, setGeneratedCodes] = useState<string[]>([]);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      redemptionApi.list({
        ...params,
        ...(keyword ? { keyword } : {}),
        ...(statusFilter ? { status: statusFilter } : {}),
      }),
    [keyword, statusFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<Redemption>({
    fetchFn,
    deps: [keyword, statusFilter],
  });

  const handleDisable = async (record: Redemption) => {
    await redemptionApi.setStatus(record.id, 'disabled');
    message.success('兑换码已禁用');
    refresh();
  };

  const createModal = useModalForm({
    onSubmit: async (values) => {
      const req: RedemptionBatchRequest = {
        name: values.name as string,
        // 表单收集的是货币面值，提交时转回积分（API 契约不变）
        credits: money.toCredits(values.credits as number),
        count: values.count as number,
      };
      if (values.expires_at) {
        req.expires_at = (values.expires_at as dayjs.Dayjs).toISOString();
      }
      const result = await redemptionApi.createBatch(req);
      setGeneratedCodes(result.codes ?? []);
      message.success(`成功生成 ${(result.codes ?? []).length} 个兑换码`);
    },
    onSuccess: refresh,
  });

  const handleCopyCodes = () => {
    copyToClipboard(generatedCodes.join('\n'));
    message.success('已复制到剪贴板');
  };

  const handleExportCodes = () => {
    exportToCSV(
      generatedCodes.map((code) => ({ code })),
      `兑换码_${dayjs().format('YYYYMMDD-HHmmss')}.csv`,
      [{ key: 'code', title: '兑换码' }],
    );
  };

  const columns: ColumnsType<Redemption> = useMemo(
    () => [
      { title: 'ID', dataIndex: 'id', width: 60, sorter: (a, b) => a.id - b.id },
      { title: '批次', dataIndex: 'batch_id', width: 140, responsive: ['lg'] as const },
      {
        title: '名称',
        dataIndex: 'name',
        width: 180,
        ellipsis: true,
        sorter: (a, b) => (a.name ?? '').localeCompare(b.name ?? ''),
      },
      {
        title: '面值',
        dataIndex: 'credits',
        width: 200,
        sorter: (a, b) => a.credits - b.credits,
        render: (v: number) => money.format(v),
        align: 'right',
      },
      {
        // 按展示态显示：库里的 status 对已过期的码仍是 unused，
        // 直接显示会让管理员以为这批码还能用。
        title: '状态',
        dataIndex: 'effective_status',
        render: (status: RedemptionStatus) => (
          <Tag color={redemptionStatusColor[status] ?? 'default'}>{RedemptionStatusLabel[status] ?? '未知'}</Tag>
        ),
        width: 100,
      },
      {
        title: '使用者',
        dataIndex: 'used_by_user_id',
        render: (uid: number | null) => (uid ? `用户 #${uid}` : '—'),
        width: 110,
      },
      {
        title: '兑换时间',
        dataIndex: 'redeemed_at',
        render: (t: string | null) => (t ? formatTime(t) : '—'),
        width: 170,
      },
      {
        title: '过期时间',
        dataIndex: 'expires_at',
        render: (t: string | null) => (t ? formatTime(t) : '永不过期'),
        width: 170,
        responsive: ['lg'] as const,
      },
      {
        title: '创建时间',
        dataIndex: 'created_at',
        sorter: (a, b) => a.created_at.localeCompare(b.created_at),
        render: (t: string) => formatTime(t),
        width: 170,
        responsive: ['xl'] as const,
      },
      {
        title: '操作',
        key: 'action',
        width: 90,
        fixed: 'right' as const,
        // 已过期的码不再提供禁用入口：它已经兑不了，禁用不改变任何结果。
        render: (_: unknown, record: Redemption) =>
          record.effective_status === 'unused' ? (
            <Button type="link" size="small" onClick={() => handleDisable(record)}>
              禁用
            </Button>
          ) : null,
      },
    ],
    [money],
  );

  return (
    <Card>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>
        列表只显示兑换码的批次与面额，不显示兑换码本身——明文只在生成时展示一次，
        库中存的是哈希，事后无法取回。生成后请立即导出保存；遗失只能作废该批次重新生成。
      </Typography.Paragraph>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <Space>
          <Select
            placeholder="状态筛选"
            allowClear
            style={{ width: 120 }}
            onChange={setStatusFilter}
            options={Object.entries(RedemptionStatusLabel).map(([value, label]) => ({ value, label }))}
          />
          <Input.Search
            placeholder="搜索"
            onSearch={setKeyword}
            allowClear
            style={{ width: 200 }}
            prefix={<SearchOutlined />}
          />
        </Space>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setGeneratedCodes([]);
              createModal.show({ count: 1, credits: money.fromCredits(1000) });
            }}
          >
            生成兑换码
          </Button>
        </Space>
      </div>
      <Table
        columns={columns}
        dataSource={dataSource}
        rowKey="id"
        loading={loading}
        pagination={pagination}
        scroll={{ x: 1390 }}
        sticky
      />

      <Modal
        title="生成兑换码"
        open={createModal.open}
        onOk={generatedCodes.length > 0 ? createModal.close : createModal.handleOk}
        onCancel={createModal.close}
        confirmLoading={createModal.loading}
        footer={
          generatedCodes.length > 0
            ? [
                <Button key="copy" icon={<CopyOutlined />} onClick={handleCopyCodes}>
                  复制全部
                </Button>,
                <Button key="export" icon={<DownloadOutlined />} onClick={handleExportCodes}>
                  导出 CSV
                </Button>,
                <Button key="close" type="primary" onClick={createModal.close}>
                  完成
                </Button>,
              ]
            : undefined
        }
      >
        {generatedCodes.length > 0 ? (
          <div>
            <p style={{ color: '#d54941', fontWeight: 500 }}>
              明文兑换码仅在此展示一次，请立即复制或导出保存：
            </p>
            <Input.TextArea
              value={generatedCodes.join('\n')}
              readOnly
              rows={Math.min(generatedCodes.length, 10)}
              style={{ fontFamily: 'monospace' }}
            />
          </div>
        ) : (
          <Form form={createModal.form} layout="vertical">
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
              <Input />
            </Form.Item>
            <Form.Item name="credits" label={`面值（${money.symbol}）`} rules={[{ required: true, message: '请输入面值' }]}>
              <InputNumber style={{ width: '100%' }} min={0} step={0.01} />
            </Form.Item>
            <Form.Item
              name="count"
              label="生成数量"
              rules={[{ required: true, message: '请输入数量' }]}
              initialValue={1}
            >
              <InputNumber style={{ width: '100%' }} min={1} max={1000} />
            </Form.Item>
            <Form.Item name="expires_at" label="过期时间">
              <DatePicker style={{ width: '100%' }} placeholder="留空表示永不过期" showTime />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </Card>
  );
}

function LedgerTab() {
  const money = useMoney();
  const [entryType, setEntryType] = useState<LedgerEntryType | undefined>();
  const [userId, setUserId] = useState<string>('');

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      ledgerApi.list({
        ...params,
        ...(entryType ? { entry_type: entryType } : {}),
        ...(userId ? { user_id: userId } : {}),
      }),
    [entryType, userId],
  );

  const { dataSource, loading, pagination, refresh } = useTable<LedgerEntry>({
    fetchFn,
    defaultPageSize: 20,
    deps: [entryType, userId],
  });

  const columns: ColumnsType<LedgerEntry> = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    { title: '用户', dataIndex: 'user_id', width: 90, render: (v: number) => `#${v}` },
    {
      title: '类型',
      dataIndex: 'entry_type',
      width: 110,
      render: (v: LedgerEntryType) => <Tag>{LedgerEntryTypeLabel[v] ?? v}</Tag>,
    },
    {
      title: '变动金额',
      dataIndex: 'amount',
      align: 'right' as const,
      width: 150,
      render: (v: number) => (
        <span style={{ color: v >= 0 ? '#00a870' : '#e34d59', fontWeight: 500 }}>
          {v >= 0 ? '+' : ''}
          {money.formatDetail(v)}
        </span>
      ),
    },
    {
      title: '余额',
      dataIndex: 'balance_after',
      align: 'right' as const,
      width: 150,
      render: (v: number) => money.format(v),
    },
    { title: '关联请求', dataIndex: 'request_id', ellipsis: true, width: 160, responsive: ['lg'] as const },
    { title: '备注', dataIndex: 'note', ellipsis: true },
    {
      title: '时间',
      dataIndex: 'created_at',
      render: (t: string) => formatTime(t),
      width: 170,
    },
  ];

  return (
    <Card>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <Space>
          <Select
            placeholder="流水类型"
            allowClear
            style={{ width: 140 }}
            onChange={setEntryType}
            options={ledgerEntryTypeOptions}
          />
          <Input
            placeholder="用户 ID"
            allowClear
            style={{ width: 140 }}
            onPressEnter={(e) => setUserId((e.target as HTMLInputElement).value)}
            onBlur={(e) => setUserId(e.target.value)}
          />
        </Space>
        <Button icon={<ReloadOutlined />} onClick={refresh}>
          刷新
        </Button>
      </div>
      <Table
        columns={columns}
        dataSource={dataSource}
        rowKey="id"
        loading={loading}
        pagination={pagination}
        scroll={{ x: 1200 }}
        sticky
      />
    </Card>
  );
}

function BillingPage() {
  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        计费管理
      </Title>
      <Tabs
        items={[
          { key: 'redemption', label: '兑换码管理', children: <RedemptionTab /> },
          { key: 'ledger', label: '流水', children: <LedgerTab /> },
        ]}
      />
    </div>
  );
}

export default BillingPage;
