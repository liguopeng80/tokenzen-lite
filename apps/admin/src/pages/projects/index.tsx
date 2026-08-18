import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import { modal, message } from '@token-zen/shared';
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { ProjectStatus, ProjectWithStats, User } from '@token-zen/shared';
import { ProjectStatusLabel } from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { projectApi, type ProjectPayload } from '@/api/organization';
import { userApi } from '@/api/users';
import { useMoney } from '@/stores/site';
import { errorMessageOf } from '@/api/error';

const { Title, Text, Paragraph } = Typography;

const statusOptions = [
  { label: ProjectStatusLabel.enabled, value: 'enabled' },
  { label: ProjectStatusLabel.disabled, value: 'disabled' },
];

interface ProjectFormValues {
  name: string;
  code?: string;
  owner_user_id?: number | null;
  monthly_budget_credits?: number;
  status: ProjectStatus;
  note?: string;
}

function ProjectsPage() {
  const money = useMoney();
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState<ProjectStatus | ''>('');
  const [editing, setEditing] = useState<ProjectWithStats | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<ProjectFormValues>();

  // 项目无成员概念，负责人可在全部用户中选择（不限定部门）。
  const [users, setUsers] = useState<User[]>([]);
  const [usersLoading, setUsersLoading] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      projectApi.list({
        ...params,
        ...(keyword ? { keyword } : {}),
        ...(statusFilter ? { status: statusFilter } : {}),
      }),
    [keyword, statusFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<ProjectWithStats>({
    fetchFn,
    deps: [keyword, statusFilter],
  });

  // 负责人下拉一次性加载全部用户；项目与部门正交，不按部门收窄。
  useEffect(() => {
    setUsersLoading(true);
    userApi
      .list({ page: 1, page_size: 100 })
      .then((res) => setUsers(res.items ?? []))
      .catch(() => setUsers([]))
      .finally(() => setUsersLoading(false));
  }, []);

  const userOptions = users.map((u) => ({
    label: `${u.username}${u.display_name ? `（${u.display_name}）` : ''}`,
    value: u.id,
  }));

  const openCreate = () => {
    setEditing(null);
    form.setFieldsValue({
      name: '',
      code: '',
      owner_user_id: null,
      monthly_budget_credits: 0,
      status: 'enabled',
      note: '',
    });
    setFormOpen(true);
  };

  const openEdit = (project: ProjectWithStats) => {
    setEditing(project);
    form.setFieldsValue({
      name: project.name,
      code: project.code,
      owner_user_id: project.owner_user_id,
      // 表单现为货币金额，从已有积分折算回显
      monthly_budget_credits: money.fromCredits(project.monthly_budget_credits ?? 0),
      status: project.status,
      note: project.note,
    });
    setFormOpen(true);
  };

  const handleSubmit = async () => {
    const values = await form.validateFields();
    const payload: ProjectPayload = {
      name: values.name.trim(),
      code: (values.code ?? '').trim(),
      owner_user_id: values.owner_user_id ?? null,
      // 表单收集的是货币金额，提交时转回积分（API 契约不变）
      monthly_budget_credits: money.toCredits(values.monthly_budget_credits ?? 0),
      status: values.status,
      note: values.note ?? '',
    };
    setSubmitting(true);
    try {
      if (editing) {
        await projectApi.update(editing.id, payload);
        message.success('项目已更新');
      } else {
        await projectApi.create(payload);
        message.success('项目已创建');
      }
      setFormOpen(false);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '保存项目失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const confirmDelete = (project: ProjectWithStats) => {
    modal.confirm({
      title: '删除该项目？',
      content:
        '删除后，归属该项目的密钥会自动变为「未归属项目」，不影响其调用。此操作不可撤销。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await projectApi.delete(project.id);
          message.success('项目已删除');
          refresh();
        } catch (err) {
          message.error(errorMessageOf(err, '删除项目失败'));
        }
      },
    });
  };

  const columns: ColumnsType<ProjectWithStats> = [
    { title: '项目名称', dataIndex: 'name', key: 'name', width: 180, ellipsis: true },
    {
      title: '成本中心编码',
      dataIndex: 'code',
      key: 'code',
      width: 150,
      ellipsis: true,
      render: (code: string) => code || <Text type="secondary">—</Text>,
    },
    {
      title: '归属密钥',
      dataIndex: 'key_count',
      key: 'key_count',
      width: 90,
      align: 'right' as const,
    },
    {
      title: '负责人',
      dataIndex: 'owner_username',
      key: 'owner_username',
      width: 140,
      render: (name: string) => name || <Text type="secondary">未指定</Text>,
    },
    {
      title: '月度预算',
      dataIndex: 'monthly_budget_credits',
      key: 'monthly_budget_credits',
      width: 160,
      align: 'right' as const,
      render: (credits: number) =>
        credits > 0 ? <span>{money.format(credits)}</span> : <Text type="secondary">未设预算</Text>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: ProjectStatus) => (
        <Tag color={status === 'enabled' ? 'green' : 'default'}>{ProjectStatusLabel[status]}</Tag>
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 160,
      fixed: 'right' as const,
      render: (_, record) => (
        <Space size="small">
          <Button
            size="small"
            icon={<EditOutlined />}
            onClick={() => openEdit(record)}
            data-testid="project-edit-btn"
          >
            编辑
          </Button>
          <Button
            size="small"
            danger
            icon={<DeleteOutlined />}
            onClick={() => confirmDelete(record)}
            data-testid="project-delete-btn"
          >
            删除
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div data-testid="admin-projects-page">
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Title level={4} style={{ margin: 0 }}>
          项目管理
        </Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={openCreate}
            data-testid="project-create-btn"
          >
            新建项目
          </Button>
        </Space>
      </div>

      <Paragraph type="secondary" style={{ marginBottom: 16 }}>
        项目是与部门正交的第二层成本归属维度，不持有余额，也不参与扣费。密钥可归属到项目，
        月度预算超出只在告警通道提醒，不拦截调用。
      </Paragraph>

      <Card>
        <Space style={{ marginBottom: 16 }} wrap>
          <Input.Search
            placeholder="搜索项目名称或编码"
            allowClear
            style={{ width: 260 }}
            prefix={<SearchOutlined />}
            onSearch={setKeyword}
          />
          <Select
            placeholder="全部状态"
            allowClear
            style={{ width: 140 }}
            options={statusOptions}
            onChange={(v) => setStatusFilter((v as ProjectStatus) ?? '')}
          />
        </Space>

        <Table
          rowKey="id"
          columns={columns}
          dataSource={dataSource}
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1040 }}
          sticky
          data-testid="projects-table"
          locale={{
            emptyText: (
              <Empty
                description="尚未创建项目。创建后可将密钥归属到项目，按项目出费用报表与预算对比。"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
        />
      </Card>

      <Drawer
        title={editing ? `编辑项目：${editing.name}` : '新建项目'}
        open={formOpen}
        width={520}
        onClose={() => setFormOpen(false)}
        extra={
          <Space>
            <Button onClick={() => setFormOpen(false)}>取消</Button>
            <Button type="primary" loading={submitting} onClick={handleSubmit}>
              保存
            </Button>
          </Space>
        }
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="name"
            label="项目名称"
            rules={[{ required: true, message: '请填写项目名称' }]}
          >
            <Input placeholder="如：官网改版" maxLength={100} />
          </Form.Item>
          <Form.Item
            name="code"
            label="成本中心编码"
            extra="对接财务系统用，可留空；非空时全站唯一"
          >
            <Input placeholder="如：PRJ-WEB-001" maxLength={50} />
          </Form.Item>
          <Form.Item
            name="owner_user_id"
            label="项目负责人"
            extra="负责人仅用于联络与报表展示，不获得任何管理员权限。可在全部用户中选择。"
          >
            <Select
              allowClear
              showSearch
              loading={usersLoading}
              placeholder="不指定负责人"
              options={userOptions}
              filterOption={(input, option) =>
                String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
              }
            />
          </Form.Item>
          <Form.Item
            name="monthly_budget_credits"
            label={`月度预算（${money.symbol}）`}
            extra="0 表示不设预算。超出预算只在告警通道提醒，不拦截调用。"
          >
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="status" label="状态" rules={[{ required: true }]}>
            <Select options={statusOptions} />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Drawer>
    </div>
  );
}

export default ProjectsPage;
