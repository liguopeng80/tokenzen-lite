import { useState, useCallback, useMemo, useEffect } from 'react';
import { Card, Table, Button, Space, Tag, Typography, Progress, Modal, Form, Input, InputNumber, Switch, Select, DatePicker, Dropdown, Empty, Alert } from 'antd';
import { message, modal } from '@token-zen/shared';
import {
  PlusOutlined,
  ReloadOutlined,
  CopyOutlined,
  SearchOutlined,
  MoreOutlined,
  EditOutlined,
  StopOutlined,
  CheckCircleOutlined,
  DeleteOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { ApiKey, KeyStatus } from '@token-zen/shared';
import { KeyStatusLabel } from '@token-zen/shared';
import {
  formatTime,
  maskKey,
  copyToClipboard,
} from '@token-zen/shared';
import { useTable, useModalForm } from '@token-zen/shared/hooks';
import { keysApi, type CreateApiKeyRequest, type UpdateApiKeyRequest } from '@/api/keys';
import { modelsApi } from '@/api/models';
import { useMoney } from '@/stores/site';
import dayjs from 'dayjs';

const { Title, Paragraph } = Typography;

const STATUS_COLOR: Record<KeyStatus, string> = {
  enabled: 'green',
  disabled: 'default',
  expired: 'red',
  depleted: 'volcano',
};

function KeysPage() {
  const money = useMoney();
  const [keyword, setKeyword] = useState('');
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [createdKeyInfo, setCreatedKeyInfo] = useState<{ key: string; name: string } | null>(null);

  useEffect(() => {
    modelsApi.list().then((models) => {
      setModelOptions(models.map((m) => m.name).sort());
    }).catch(() => {
      message.warning('加载模型列表失败');
    });
  }, []);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      keysApi.list(keyword ? { ...params, keyword } : params),
    [keyword],
  );

  const { dataSource, loading, pagination, refresh } = useTable<ApiKey>({
    fetchFn,
    deps: [keyword],
  });

  const handleToggleStatus = async (record: ApiKey) => {
    const newStatus: KeyStatus = record.status === 'enabled' ? 'disabled' : 'enabled';
    await keysApi.update(record.id, { status: newStatus });
    message.success(newStatus === 'enabled' ? '密钥已启用' : '密钥已禁用');
    refresh();
  };

  const handleDelete = async (id: number) => {
    await keysApi.delete(id);
    message.success('密钥已删除');
    refresh();
  };

  const createModal = useModalForm({
    onSubmit: async (values) => {
      const data: CreateApiKeyRequest = { name: values.name as string };
      if (!values.no_limit && values.credit_limit != null) {
        data.credit_limit = money.toCredits(values.credit_limit as number);
      }
      if (!values.no_daily_limit && values.daily_spend_limit != null) {
        data.daily_spend_limit = money.toCredits(values.daily_spend_limit as number);
      }
      if (!values.no_expiry && values.expires_at) {
        data.expires_at = (values.expires_at as dayjs.Dayjs).toISOString();
      }
      if (values.allowed_models && (values.allowed_models as string[]).length > 0) {
        data.allowed_models = values.allowed_models as string[];
      }
      if (values.allowed_ips && (values.allowed_ips as string[]).length > 0) {
        data.allowed_ips = values.allowed_ips as string[];
      }
      data.project_id = (values.project_id as number | null | undefined) ?? null;
      const result = await keysApi.create(data);
      setCreatedKeyInfo({ key: result.key, name: result.name });
      await copyToClipboard(result.key);
    },
    onSuccess: refresh,
  });

  const editModal = useModalForm({
    onSubmit: async (values) => {
      const id = values.id as number;
      const data: UpdateApiKeyRequest = { name: values.name as string };
      if (values.no_limit) {
        data.clear_limit = true;
      } else if (values.credit_limit != null) {
        data.credit_limit = money.toCredits(values.credit_limit as number);
      }
      if (values.no_daily_limit) {
        data.clear_daily_limit = true;
      } else if (values.daily_spend_limit != null) {
        data.daily_spend_limit = money.toCredits(values.daily_spend_limit as number);
      }
      if (values.no_expiry) {
        data.clear_expires = true;
      } else if (values.expires_at) {
        data.expires_at = (values.expires_at as dayjs.Dayjs).toISOString();
      }
      data.allowed_models = (values.allowed_models as string[]) ?? [];
      data.allowed_ips = (values.allowed_ips as string[]) ?? [];
      const projectVal = values.project_id as number | null | undefined;
      if (projectVal != null) {
        data.project_id = projectVal;
      } else {
        // 表单中项目被清空：显式置为未归属（与 clear_limit 范式一致）。
        data.clear_project = true;
      }
      await keysApi.update(id, data);
      message.success('密钥已更新');
    },
    onSuccess: refresh,
  });

  const handleEdit = (record: ApiKey) => {
    editModal.show({
      id: record.id,
      name: record.name,
      no_limit: record.credit_limit === null,
      credit_limit: record.credit_limit != null ? money.fromCredits(record.credit_limit) : undefined,
      no_daily_limit: (record.daily_spend_limit ?? 0) === 0,
      daily_spend_limit: record.daily_spend_limit ? money.fromCredits(record.daily_spend_limit) : undefined,
      no_expiry: record.expires_at === null,
      expires_at: record.expires_at ? dayjs(record.expires_at) : undefined,
      allowed_models: record.allowed_models ?? undefined,
      allowed_ips: record.allowed_ips ?? undefined,
      project_id: record.project_id ?? undefined,
    });
  };

  const columns: ColumnsType<ApiKey> = useMemo(
    () => [
      {
        title: '名称',
        dataIndex: 'name',
        sorter: (a, b) => (a.name ?? '').localeCompare(b.name ?? ''),
        defaultSortOrder: 'ascend' as const,
      },
      {
        title: '密钥',
        dataIndex: 'key_prefix',
        render: (prefix: string) => (
          <code style={{ fontSize: 12 }}>{maskKey(prefix)}</code>
        ),
      },
      {
        title: '状态',
        dataIndex: 'status',
        render: (status: KeyStatus) => (
          <Tag color={STATUS_COLOR[status] ?? 'default'}>
            {KeyStatusLabel[status] ?? '未知'}
          </Tag>
        ),
        width: 90,
      },
      {
        title: '已用 / 额度',
        key: 'credit',
        sorter: (a, b) => a.credit_used - b.credit_used,
        render: (_: unknown, record: ApiKey) => {
          if (record.credit_limit === null) {
            return <span>{money.format(record.credit_used)} / 不限</span>;
          }
          const percent = record.credit_limit > 0
            ? (record.credit_used / record.credit_limit) * 100
            : 100;
          return (
            <div>
              <div style={{ fontSize: 12 }}>
                {money.format(record.credit_used)} / {money.format(record.credit_limit)}
              </div>
              <Progress percent={Math.round(percent)} size="small" />
            </div>
          );
        },
      },
      {
        title: '每日上限',
        dataIndex: 'daily_spend_limit',
        sorter: (a, b) => (a.daily_spend_limit ?? 0) - (b.daily_spend_limit ?? 0),
        render: (v: number) => (v ? money.format(v) : '不限'),
        width: 110,
      },
      {
        title: '最近使用',
        dataIndex: 'last_used_at',
        sorter: (a, b) => (a.last_used_at ?? '').localeCompare(b.last_used_at ?? ''),
        render: (t: string | null) => (t ? formatTime(t) : '从未使用'),
        width: 160,
      },
      {
        title: '过期时间',
        dataIndex: 'expires_at',
        sorter: (a, b) => (a.expires_at ?? '').localeCompare(b.expires_at ?? ''),
        render: (t: string | null) => (t ? formatTime(t, 'YYYY-MM-DD') : '永不过期'),
        width: 120,
      },
      {
        title: '操作',
        key: 'action',
        width: 80,
        render: (_: unknown, record: ApiKey) => {
          const items = [
            {
              key: 'edit',
              icon: <EditOutlined />,
              label: '编辑',
              onClick: () => handleEdit(record),
            },
            {
              key: 'toggle',
              icon: record.status === 'enabled' ? <StopOutlined /> : <CheckCircleOutlined />,
              label: record.status === 'enabled' ? '禁用' : '启用',
              onClick: () => handleToggleStatus(record),
            },
            { type: 'divider' as const },
            {
              key: 'delete',
              icon: <DeleteOutlined />,
              label: '删除',
              danger: true,
              onClick: () => {
                modal.confirm({
                  title: '确认删除该密钥？',
                  content: '密钥一旦删除将无法恢复，使用该密钥的客户端将立即失效。',
                  onOk: () => handleDelete(record.id),
                });
              },
            },
          ];
          return (
            <Dropdown menu={{ items }} trigger={['click']}>
              <Button type="text" icon={<MoreOutlined />} />
            </Dropdown>
          );
        },
      },
    ],
    [money],
  );

  const keyFormFields = (
    <>
      <Form.Item
        name="name"
        label="名称"
        rules={[{ required: true, message: '请输入名称' }]}
      >
        <Input placeholder="例如：本地开发" />
      </Form.Item>
      <Form.Item
        name="no_limit"
        label="不限额度"
        valuePropName="checked"
        initialValue={true}
      >
        <Switch />
      </Form.Item>
      <Form.Item noStyle shouldUpdate={(prev, cur) => prev.no_limit !== cur.no_limit}>
        {({ getFieldValue }) =>
          !getFieldValue('no_limit') ? (
            <Form.Item name="credit_limit" label={`额度（${money.symbol}）`}>
              <InputNumber style={{ width: '100%' }} min={0} />
            </Form.Item>
          ) : null
        }
      </Form.Item>
      <div data-testid="key-daily-spend-limit-field">
        <Form.Item
          name="no_daily_limit"
          label="不限每日花费"
          valuePropName="checked"
          initialValue={true}
          extra="单个自然日内该 Key 累计扣费的上限，与用户级每日上限并行生效、各独立累计"
        >
          <Switch />
        </Form.Item>
        <Form.Item noStyle shouldUpdate={(prev, cur) => prev.no_daily_limit !== cur.no_daily_limit}>
          {({ getFieldValue }) =>
            !getFieldValue('no_daily_limit') ? (
              <Form.Item name="daily_spend_limit" label={`每日花费上限（${money.symbol}）`}>
                <InputNumber style={{ width: '100%' }} min={0} />
              </Form.Item>
            ) : null
          }
        </Form.Item>
      </div>
      <Form.Item
        name="no_expiry"
        label="永不过期"
        valuePropName="checked"
        initialValue={true}
      >
        <Switch />
      </Form.Item>
      <Form.Item noStyle shouldUpdate={(prev, cur) => prev.no_expiry !== cur.no_expiry}>
        {({ getFieldValue }) =>
          !getFieldValue('no_expiry') ? (
            <Form.Item name="expires_at" label="过期时间">
              <DatePicker style={{ width: '100%' }} showTime />
            </Form.Item>
          ) : null
        }
      </Form.Item>
      <div data-testid="portal-key-project-input">
        <Form.Item
          name="project_id"
          label="归属项目"
          extra="项目 ID，留空表示不归属，可向管理员询问"
        >
          <InputNumber style={{ width: '100%' }} min={1} precision={0} placeholder="项目 ID" />
        </Form.Item>
      </div>
      <Form.Item name="allowed_models" label="可用模型">
        <Select
          mode="multiple"
          allowClear
          placeholder="留空表示不限制"
          options={modelOptions.map((m) => ({ label: m, value: m }))}
          filterOption={(input, option) =>
            (option?.label as string)?.toLowerCase().includes(input.toLowerCase())
          }
        />
      </Form.Item>
      <Form.Item name="allowed_ips" label="IP 白名单">
        <Select
          mode="tags"
          allowClear
          open={false}
          placeholder="输入 IP 或 CIDR 后回车添加，留空表示不限制"
          tokenSeparators={[',', ' ']}
        />
      </Form.Item>
    </>
  );

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
          API 密钥
        </Title>
        <Space>
          <Input.Search
            placeholder="搜索"
            onSearch={setKeyword}
            allowClear
            style={{ width: 200 }}
            prefix={<SearchOutlined />}
          />
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => createModal.show()}
          >
            创建密钥
          </Button>
        </Space>
      </div>
      <Card>
        {!loading && dataSource.length === 0 ? (
          <Empty description="暂无 API 密钥">
            <Paragraph type="secondary" style={{ marginBottom: 16 }}>
              创建一个 API 密钥来开始使用接口服务。密钥用于身份验证，请妥善保管。
            </Paragraph>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => createModal.show()}
            >
              创建第一个密钥
            </Button>
          </Empty>
        ) : (
          <Table
            columns={columns}
            dataSource={dataSource}
            rowKey="id"
            loading={loading}
            pagination={pagination}
          />
        )}
      </Card>

      <Modal
        title="创建密钥"
        open={createModal.open}
        onOk={createModal.handleOk}
        onCancel={createModal.close}
        confirmLoading={createModal.loading}
      >
        <Form form={createModal.form} layout="vertical">
          {keyFormFields}
        </Form>
      </Modal>

      <Modal
        title="编辑密钥"
        open={editModal.open}
        onOk={editModal.handleOk}
        onCancel={editModal.close}
        confirmLoading={editModal.loading}
      >
        <Form form={editModal.form} layout="vertical">
          <Form.Item name="id" hidden>
            <Input />
          </Form.Item>
          {keyFormFields}
        </Form>
      </Modal>

      <Modal
        title={<span><KeyOutlined style={{ marginRight: 8 }} />密钥创建成功</span>}
        open={!!createdKeyInfo}
        onCancel={() => setCreatedKeyInfo(null)}
        footer={
          <Button type="primary" onClick={() => setCreatedKeyInfo(null)}>
            我已安全保存密钥
          </Button>
        }
        maskClosable={false}
      >
        <Alert
          type="warning"
          showIcon
          message="密钥已自动复制到剪贴板，请立即妥善保存"
          description="关闭此对话框后，密钥将不再完整显示，且无法再次找回明文，仅可删除后重新创建。"
          style={{ marginBottom: 16 }}
        />
        <div style={{ marginBottom: 8, color: '#666', fontSize: 13 }}>
          密钥名称：<strong>{createdKeyInfo?.name}</strong>
        </div>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '12px 16px',
            background: '#f5f5f5',
            borderRadius: 6,
            border: '1px solid #d9d9d9',
          }}
        >
          <code
            style={{
              flex: 1,
              fontSize: 13,
              wordBreak: 'break-all',
              userSelect: 'all',
              lineHeight: 1.6,
            }}
          >
            {createdKeyInfo?.key}
          </code>
          <Button
            type="primary"
            icon={<CopyOutlined />}
            onClick={async () => {
              if (createdKeyInfo) {
                await copyToClipboard(createdKeyInfo.key);
                message.success('密钥已复制到剪贴板');
              }
            }}
          >
            复制
          </Button>
        </div>
      </Modal>
    </div>
  );
}

export default KeysPage;
