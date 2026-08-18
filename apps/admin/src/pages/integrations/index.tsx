import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import { message } from '@token-zen/shared';
import {
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  PlusOutlined,
  ReloadOutlined,
  StopOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type {
  Integration,
  ServiceToken,
  ServiceTokenStatus,
} from '@token-zen/shared';
import {
  IntegrationStatusLabel,
  ServiceTokenStatusLabel,
  formatTime,
} from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import {
  integrationApi,
  type CreateIntegrationPayload,
} from '@/api/integrations';
import { errorMessageOf } from '@/api/error';

const { Title, Text, Paragraph } = Typography;

/** slug 合法形态，与后端 integrationSlugRe 一致：1-64 位字母、数字、下划线或连字符 */
const SLUG_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

interface CreateFormValues {
  name: string;
  slug: string;
}

interface RenameFormValues {
  name: string;
}

interface ServiceTokenFormValues {
  name: string;
}

function IntegrationsPage() {
  const [createOpen, setCreateOpen] = useState(false);
  const [createSubmitting, setCreateSubmitting] = useState(false);
  const [createForm] = Form.useForm<CreateFormValues>();

  const [renameTarget, setRenameTarget] = useState<Integration | null>(null);
  const [renameSubmitting, setRenameSubmitting] = useState(false);
  const [renameForm] = Form.useForm<RenameFormValues>();

  // 列表接口不返回 token_count，按当前页行逐条取详情补齐。接入方总量小，可接受。
  const [tokenCountMap, setTokenCountMap] = useState<Record<number, number>>({});

  // 令牌管理抽屉
  const [tokenTarget, setTokenTarget] = useState<Integration | null>(null);
  const [tokens, setTokens] = useState<ServiceToken[]>([]);
  const [tokensLoading, setTokensLoading] = useState(false);
  const [tokenFormOpen, setTokenFormOpen] = useState(false);
  const [tokenSubmitting, setTokenSubmitting] = useState(false);
  const [tokenForm] = Form.useForm<ServiceTokenFormValues>();

  // 签发后明文令牌只展示一次
  const [revealedToken, setRevealedToken] = useState<{
    name: string;
    token: string;
  } | null>(null);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) => integrationApi.list(params),
    [],
  );

  const { dataSource, loading, pagination, refresh } = useTable<Integration>({
    fetchFn,
  });

  // 列表行到达后补齐 token_count：列表接口不带该字段，只有详情接口有。
  useEffect(() => {
    if (dataSource.length === 0) {
      setTokenCountMap({});
      return;
    }
    let cancelled = false;
    void Promise.all(
      dataSource.map((it) =>
        integrationApi.getById(it.id).then(
          (detail) => [it.id, detail.token_count] as const,
          () => [it.id, 0] as const,
        ),
      ),
    ).then((entries) => {
      if (cancelled) return;
      setTokenCountMap(
        entries.reduce<Record<number, number>>((acc, [id, count]) => {
          acc[id] = count;
          return acc;
        }, {}),
      );
    });
    return () => {
      cancelled = true;
    };
  }, [dataSource]);

  const openCreate = () => {
    createForm.resetFields();
    setCreateOpen(true);
  };

  const handleCreate = async () => {
    const values = await createForm.validateFields();
    const payload: CreateIntegrationPayload = {
      name: values.name.trim(),
      slug: values.slug.trim(),
    };
    setCreateSubmitting(true);
    try {
      await integrationApi.create(payload);
      message.success('接入方已创建');
      setCreateOpen(false);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '创建接入方失败'));
    } finally {
      setCreateSubmitting(false);
    }
  };

  const openRename = (it: Integration) => {
    setRenameTarget(it);
    renameForm.setFieldsValue({ name: it.name });
  };

  const handleRename = async () => {
    if (!renameTarget) return;
    const values = await renameForm.validateFields();
    setRenameSubmitting(true);
    try {
      await integrationApi.update(renameTarget.id, { name: values.name.trim() });
      message.success('接入方已改名');
      setRenameTarget(null);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '改名失败'));
    } finally {
      setRenameSubmitting(false);
    }
  };

  const handleDisable = async (it: Integration) => {
    try {
      await integrationApi.disable(it.id);
      message.success('接入方已停用，其令牌与用户一并停用');
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '停用接入方失败'));
    }
  };

  const loadTokens = useCallback(async (integrationId: number) => {
    setTokensLoading(true);
    try {
      const list = await integrationApi.listServiceTokens(integrationId);
      setTokens(list ?? []);
    } catch (err) {
      message.error(errorMessageOf(err, '加载服务令牌失败'));
      setTokens([]);
    } finally {
      setTokensLoading(false);
    }
  }, []);

  const openTokenDrawer = async (it: Integration) => {
    setTokenTarget(it);
    setTokenFormOpen(false);
    await loadTokens(it.id);
  };

  const closeTokenDrawer = () => {
    setTokenTarget(null);
    setTokens([]);
    setTokenFormOpen(false);
    tokenForm.resetFields();
  };

  const handleCreateToken = async () => {
    if (!tokenTarget) return;
    const values = await tokenForm.validateFields();
    setTokenSubmitting(true);
    try {
      const created = await integrationApi.createServiceToken(
        tokenTarget.id,
        values.name.trim(),
      );
      message.success('服务令牌已签发');
      setTokenFormOpen(false);
      tokenForm.resetFields();
      // 明文令牌只此一次，就地展示供复制保存。
      setRevealedToken({ name: created.name, token: created.token });
      await loadTokens(tokenTarget.id);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '签发服务令牌失败'));
    } finally {
      setTokenSubmitting(false);
    }
  };

  const handleToggleToken = async (t: ServiceToken) => {
    if (!tokenTarget) return;
    const next: ServiceTokenStatus =
      t.status === 'enabled' ? 'disabled' : 'enabled';
    try {
      await integrationApi.updateServiceTokenStatus(tokenTarget.id, t.id, next);
      message.success(next === 'enabled' ? '令牌已启用' : '令牌已停用');
      await loadTokens(tokenTarget.id);
    } catch (err) {
      message.error(errorMessageOf(err, '变更令牌状态失败'));
    }
  };

  const handleDeleteToken = async (t: ServiceToken) => {
    if (!tokenTarget) return;
    try {
      await integrationApi.deleteServiceToken(tokenTarget.id, t.id);
      message.success('令牌已删除');
      await loadTokens(tokenTarget.id);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '删除令牌失败'));
    }
  };

  const columns: ColumnsType<Integration> = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      width: 200,
      ellipsis: true,
    },
    {
      title: '标识',
      dataIndex: 'slug',
      key: 'slug',
      width: 180,
      ellipsis: true,
      render: (slug: string) => <Text code>{slug}</Text>,
    },
    {
      title: '令牌数',
      key: 'token_count',
      width: 90,
      align: 'right' as const,
      render: (_, record) =>
        tokenCountMap[record.id] ?? <Text type="secondary">—</Text>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: Integration['status']) => (
        <Tag color={status === 'enabled' ? 'green' : 'default'}>
          {IntegrationStatusLabel[status]}
        </Tag>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (t: string) => formatTime(t),
    },
    {
      title: '操作',
      key: 'action',
      width: 260,
      fixed: 'right' as const,
      render: (_, record) => (
        <Space size="small">
          <Button
            size="small"
            icon={<KeyOutlined />}
            onClick={() => openTokenDrawer(record)}
          >
            令牌
          </Button>
          <Button
            size="small"
            icon={<EditOutlined />}
            onClick={() => openRename(record)}
          >
            改名
          </Button>
          {record.status === 'enabled' && (
            <Popconfirm
              title="停用该接入方？"
              description="将级联停用其全部服务令牌，并禁用其名下全部用户（含服务账号）。账务历史保留，可事后审计。"
              okText="停用"
              okButtonProps={{ danger: true }}
              cancelText="取消"
              onConfirm={() => handleDisable(record)}
            >
              <Button size="small" danger icon={<StopOutlined />}>
                停用
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];

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
            接入方管理
          </Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            接入方是对接本系统的外部租户。创建时自动建立无口令服务账号
            svc:&lt;slug&gt;，凭服务令牌（tzs- 前缀）调用管理 API。
          </Typography.Text>
        </Space>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={openCreate}
            data-testid="create-integration-btn"
          >
            新建接入方
          </Button>
        </Space>
      </div>

      <Card>
        <Table
          rowKey="id"
          columns={columns}
          dataSource={dataSource}
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1010 }}
          sticky
          data-testid="integrations-table"
          locale={{
            emptyText: (
              <Empty
                description="尚未创建接入方。新建后即可为其签发服务令牌。"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
        />
      </Card>

      {/* 新建接入方 */}
      <Modal
        title="新建接入方"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={createSubmitting}
        okText="创建"
        cancelText="取消"
      >
        <Form form={createForm} layout="vertical" autoComplete="off">
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true, message: '请填写接入方名称' },
              { max: 100, message: '名称不超过 100 字符' },
            ]}
          >
            <Input placeholder="如：内部调度系统" maxLength={100} />
          </Form.Item>
          <Form.Item
            name="slug"
            label="标识"
            extra="用于拼出服务账号 svc:<slug>，创建后不可变。须为 1-64 位字母、数字、下划线或连字符。"
            rules={[
              { required: true, message: '请填写标识' },
              {
                pattern: SLUG_PATTERN,
                message: '须为 1-64 位字母、数字、下划线或连字符',
              },
            ]}
          >
            <Input placeholder="如：internal-scheduler" maxLength={64} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 改名 */}
      <Modal
        title={`改名：${renameTarget?.name ?? ''}`}
        open={renameTarget !== null}
        onOk={handleRename}
        onCancel={() => setRenameTarget(null)}
        confirmLoading={renameSubmitting}
        okText="保存"
        cancelText="取消"
      >
        <Paragraph type="secondary" style={{ fontSize: 13 }}>
          标识（slug）不可变，仅可修改展示名称。
        </Paragraph>
        <Form form={renameForm} layout="vertical" autoComplete="off">
          <Form.Item
            name="name"
            label="名称"
            rules={[
              { required: true, message: '请填写名称' },
              { max: 100, message: '名称不超过 100 字符' },
            ]}
          >
            <Input maxLength={100} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 令牌管理 */}
      <Drawer
        title={
          tokenTarget
            ? `服务令牌：${tokenTarget.name}`
            : '服务令牌'
        }
        open={tokenTarget !== null}
        width={680}
        onClose={closeTokenDrawer}
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={
              tokenTarget?.status !== 'enabled' || tokenFormOpen
            }
            onClick={() => {
              tokenForm.resetFields();
              setTokenFormOpen(true);
            }}
          >
            签发令牌
          </Button>
        }
      >
        {tokenTarget?.status === 'disabled' && (
          <Paragraph type="warning" style={{ marginBottom: 16 }}>
            接入方已停用，不能签发新令牌。停用前签发的令牌也已一并停用。
          </Paragraph>
        )}

        {tokenFormOpen && (
          <Space.Compact style={{ width: '100%', marginBottom: 16 }}>
            <Form
              form={tokenForm}
              layout="vertical"
              style={{ flex: 1, marginRight: 8 }}
              autoComplete="off"
            >
              <Form.Item
                name="name"
                noStyle
                rules={[
                  { required: true, message: '请填写令牌名称' },
                  { max: 64, message: '名称不超过 64 字符' },
                ]}
              >
                <Input placeholder="令牌名称，如：生产调度节点" maxLength={64} />
              </Form.Item>
            </Form>
            <Space>
              <Button
                type="primary"
                loading={tokenSubmitting}
                onClick={handleCreateToken}
              >
                签发
              </Button>
              <Button
                onClick={() => {
                  setTokenFormOpen(false);
                  tokenForm.resetFields();
                }}
              >
                取消
              </Button>
            </Space>
          </Space.Compact>
        )}

        <Table
          rowKey="id"
          size="small"
          loading={tokensLoading}
          dataSource={tokens}
          pagination={false}
          data-testid="service-tokens-table"
          locale={{
            emptyText: (
              <Empty
                description="尚未签发服务令牌"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
          columns={[
            {
              title: '名称',
              dataIndex: 'name',
              key: 'name',
              ellipsis: true,
            },
            {
              title: '前缀',
              dataIndex: 'token_prefix',
              key: 'token_prefix',
              width: 160,
              render: (prefix: string) => (
                <code style={{ fontSize: 12 }}>{prefix}…</code>
              ),
            },
            {
              title: '状态',
              dataIndex: 'status',
              key: 'status',
              width: 90,
              render: (status: ServiceTokenStatus) => (
                <Tag color={status === 'enabled' ? 'green' : 'default'}>
                  {ServiceTokenStatusLabel[status]}
                </Tag>
              ),
            },
            {
              title: '最近使用',
              dataIndex: 'last_used_at',
              key: 'last_used_at',
              width: 160,
              render: (t: string | null) => formatTime(t),
            },
            {
              title: '操作',
              key: 'action',
              width: 150,
              render: (_, record) => (
                <Space size="small">
                  <Button
                    size="small"
                    onClick={() => handleToggleToken(record)}
                  >
                    {record.status === 'enabled' ? '停用' : '启用'}
                  </Button>
                  <Popconfirm
                    title="删除该令牌？"
                    description="软删除，记录保留供事后追溯。使用该令牌的调用会立即失败。"
                    okText="删除"
                    okButtonProps={{ danger: true }}
                    cancelText="取消"
                    onConfirm={() => handleDeleteToken(record)}
                  >
                    <Button
                      size="small"
                      danger
                      icon={<DeleteOutlined />}
                    />
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Drawer>

      {/* 签发后明文令牌只展示一次 */}
      <Modal
        title="服务令牌已签发"
        open={!!revealedToken}
        onOk={() => setRevealedToken(null)}
        onCancel={() => setRevealedToken(null)}
        okText="我已保存"
        cancelText="关闭"
        okButtonProps={{ type: 'primary' }}
      >
        <Paragraph>
          令牌 <Text strong>{revealedToken?.name}</Text> 的明文如下。
          该明文只在此处展示一次，关闭后无法再次取得；遗失只能删除重建。
        </Paragraph>
        <Paragraph copyable={{ text: revealedToken?.token ?? '' }}>
          <Text code style={{ fontSize: 16, wordBreak: 'break-all' }}>
            {revealedToken?.token}
          </Text>
        </Paragraph>
        <Paragraph type="warning" style={{ marginBottom: 0 }}>
          请立即复制并妥善保管，切勿提交到代码仓库或聊天工具。
        </Paragraph>
      </Modal>
    </div>
  );
}

export default IntegrationsPage;
