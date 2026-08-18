import { useCallback, useState } from 'react';
import { Button, Card, Drawer, Empty, Form, Input, InputNumber, Popconfirm, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import { message } from '@token-zen/shared';
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { DepartmentStatus, DepartmentWithStats, User } from '@token-zen/shared';
import { DepartmentStatusLabel } from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { departmentApi, type DepartmentPayload } from '@/api/organization';
import { userApi } from '@/api/users';
import { useMoney } from '@/stores/site';
import { errorMessageOf } from '@/api/error';

const { Title, Text, Paragraph } = Typography;

const statusOptions = [
  { label: DepartmentStatusLabel.enabled, value: 'enabled' },
  { label: DepartmentStatusLabel.disabled, value: 'disabled' },
];

/** 表单取值形态：allowed_models 以换行分隔的文本编辑，提交时拆成数组。 */
interface DepartmentFormValues {
  name: string;
  code?: string;
  owner_user_id?: number | null;
  monthly_budget_credits?: number;
  allowed_models_text?: string;
  status: DepartmentStatus;
  note?: string;
}

function DepartmentsPage() {
  const money = useMoney();
  const [keyword, setKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState<DepartmentStatus | ''>('');
  const [editing, setEditing] = useState<DepartmentWithStats | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<DepartmentFormValues>();

  const [memberTarget, setMemberTarget] = useState<DepartmentWithStats | null>(null);
  const [members, setMembers] = useState<User[]>([]);
  const [membersLoading, setMembersLoading] = useState(false);
  const [candidates, setCandidates] = useState<User[]>([]);
  const [pendingMemberIds, setPendingMemberIds] = useState<number[]>([]);

  // 负责人只能从本部门成员中选：负责人能看到全部门的消费明细与成员余额。
  const [ownerCandidates, setOwnerCandidates] = useState<User[]>([]);
  const [ownerLoading, setOwnerLoading] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      departmentApi.list({
        ...params,
        ...(keyword ? { keyword } : {}),
        ...(statusFilter ? { status: statusFilter } : {}),
      }),
    [keyword, statusFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<DepartmentWithStats>({
    fetchFn,
    deps: [keyword, statusFilter],
  });

  const loadOwnerCandidates = useCallback(async (deptId: number) => {
    setOwnerLoading(true);
    try {
      const res = await userApi.list({ department_id: deptId, page: 1, page_size: 100 });
      setOwnerCandidates(res.items ?? []);
    } catch {
      setOwnerCandidates([]);
      message.error('加载部门成员失败，负责人暂时无法选择');
    } finally {
      setOwnerLoading(false);
    }
  }, []);

  const openCreate = () => {
    setEditing(null);
    setOwnerCandidates([]);
    form.setFieldsValue({
      name: '', code: '', owner_user_id: null, monthly_budget_credits: 0,
      allowed_models_text: '', status: 'enabled', note: '',
    });
    setFormOpen(true);
  };

  const openEdit = (dept: DepartmentWithStats) => {
    setEditing(dept);
    setOwnerCandidates([]);
    void loadOwnerCandidates(dept.id);
    form.setFieldsValue({
      name: dept.name,
      code: dept.code,
      owner_user_id: dept.owner_user_id,
      // 表单现为货币金额，从已有积分折算回显
      monthly_budget_credits: money.fromCredits(dept.monthly_budget_credits ?? 0),
      allowed_models_text: (dept.allowed_models ?? []).join('\n'),
      status: dept.status,
      note: dept.note,
    });
    setFormOpen(true);
  };

  const handleSubmit = async () => {
    const values = await form.validateFields();
    const payload: DepartmentPayload = {
      name: values.name.trim(),
      code: (values.code ?? '').trim(),
      owner_user_id: values.owner_user_id ?? null,
      // 表单收集的是货币金额，提交时转回积分（API 契约不变）
      monthly_budget_credits: money.toCredits(values.monthly_budget_credits ?? 0),
      allowed_models: (values.allowed_models_text ?? '')
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean),
      status: values.status,
      note: values.note ?? '',
    };
    setSubmitting(true);
    try {
      if (editing) {
        await departmentApi.update(editing.id, payload);
        message.success('部门已更新');
      } else {
        await departmentApi.create(payload);
        message.success('部门已创建');
      }
      setFormOpen(false);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '保存部门失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (dept: DepartmentWithStats) => {
    try {
      await departmentApi.delete(dept.id);
      message.success('部门已删除');
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '删除部门失败'));
    }
  };

  const openMembers = async (dept: DepartmentWithStats) => {
    setMemberTarget(dept);
    setPendingMemberIds([]);
    setMembersLoading(true);
    try {
      const [inDept, all] = await Promise.all([
        userApi.list({ department_id: dept.id, page: 1, page_size: 100 }),
        userApi.list({ page: 1, page_size: 100 }),
      ]);
      setMembers(inDept.items ?? []);
      setCandidates((all.items ?? []).filter((u) => u.department_id !== dept.id));
    } catch {
      message.error('加载部门成员失败');
    } finally {
      setMembersLoading(false);
    }
  };

  const handleAddMembers = async () => {
    if (!memberTarget || pendingMemberIds.length === 0) return;
    try {
      await departmentApi.setMembers(memberTarget.id, pendingMemberIds, false);
      message.success('成员已划入本部门');
      await openMembers(memberTarget);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '划入成员失败'));
    }
  };

  const handleRemoveMember = async (user: User) => {
    if (!memberTarget) return;
    try {
      await departmentApi.setMembers(memberTarget.id, [user.id], true);
      message.success('成员已转出本部门');
      await openMembers(memberTarget);
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '转出成员失败'));
    }
  };

  const columns: ColumnsType<DepartmentWithStats> = [
    { title: '部门名称', dataIndex: 'name', key: 'name', width: 180, ellipsis: true },
    {
      title: '成本中心编码',
      dataIndex: 'code',
      key: 'code',
      width: 150,
      ellipsis: true,
      render: (code: string) => code || <Text type="secondary">—</Text>,
    },
    {
      title: '成员数',
      dataIndex: 'member_count',
      key: 'member_count',
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
      title: '模型策略',
      dataIndex: 'allowed_models',
      key: 'allowed_models',
      width: 140,
      render: (models: string[] | null) =>
        models && models.length > 0 ? (
          <Tooltip title={models.join('、')}>
            <Tag color="blue">限 {models.length} 个模型</Tag>
          </Tooltip>
        ) : (
          <Text type="secondary">不限制</Text>
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: DepartmentStatus) => (
        <Tag color={status === 'enabled' ? 'green' : 'default'}>
          {DepartmentStatusLabel[status]}
        </Tag>
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 220,
      fixed: 'right' as const,
      render: (_, record) => (
        <Space size="small">
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Button size="small" icon={<TeamOutlined />} onClick={() => openMembers(record)}>
            成员
          </Button>
          <Popconfirm
            title="删除该部门？"
            description="部门仍有成员时无法删除，需先把成员转出。"
            onConfirm={() => handleDelete(record)}
          >
            <Button size="small" danger icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>
          部门管理
        </Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建部门
          </Button>
        </Space>
      </div>

      <Paragraph type="secondary" style={{ marginBottom: 16 }}>
        部门用于费用分摊与预算对比，不持有余额，也不参与扣费。给部门下发预算的实际动作是对其成员批量发放额度；
        月度预算超出只触发告警，不拦截调用。
      </Paragraph>

      <Card>
        <Space style={{ marginBottom: 16 }} wrap>
          <Input.Search
            placeholder="搜索部门名称或编码"
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
            onChange={(v) => setStatusFilter((v as DepartmentStatus) ?? '')}
          />
        </Space>

        <Table
          rowKey="id"
          columns={columns}
          dataSource={dataSource}
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1180 }}
          sticky
          locale={{
            emptyText: (
              <Empty
                description="尚未创建部门。创建后即可按部门出费用分摊报表与预算对比。"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
        />
      </Card>

      <Drawer
        title={editing ? `编辑部门：${editing.name}` : '新建部门'}
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
            label="部门名称"
            rules={[{ required: true, message: '请填写部门名称' }]}
          >
            <Input placeholder="如：研发部" maxLength={100} />
          </Form.Item>
          <Form.Item name="code" label="成本中心编码" extra="对接财务系统用，可留空；非空时全站唯一">
            <Input placeholder="如：CC-RD-001" maxLength={50} />
          </Form.Item>
          <Form.Item
            name="owner_user_id"
            label="部门负责人"
            extra={
              editing
                ? '负责人可在员工门户查看本部门的消费明细、预算进度与成员余额，不获得任何管理员权限。只能从本部门成员中选择；该成员转出部门后自动失去查账入口。'
                : '负责人只能从本部门成员中选择。请先创建部门、划入成员，再回到本表单指定负责人。'
            }
          >
            <Select
              allowClear
              showSearch
              disabled={!editing}
              loading={ownerLoading}
              placeholder={editing ? '不指定负责人' : '部门创建后可指定'}
              options={ownerCandidates.map((u) => ({
                label: `${u.username}${u.display_name ? `（${u.display_name}）` : ''}`,
                value: u.id,
              }))}
              notFoundContent={ownerLoading ? '加载中' : '本部门暂无成员，请先在「成员」中划入'}
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
          <Form.Item
            name="allowed_models_text"
            label="部门级模型策略"
            extra="每行一个模型名，留空表示本层不限制。有效模型集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄。"
          >
            <Input.TextArea rows={4} placeholder={'gpt-5\nclaude-5'} />
          </Form.Item>
          <Form.Item name="status" label="状态" rules={[{ required: true }]}>
            <Select options={statusOptions} />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        title={memberTarget ? `部门成员：${memberTarget.name}` : '部门成员'}
        open={memberTarget !== null}
        width={560}
        onClose={() => setMemberTarget(null)}
      >
        <Space.Compact style={{ width: '100%', marginBottom: 16 }}>
          <Select
            mode="multiple"
            placeholder="选择要划入本部门的用户"
            style={{ width: '100%' }}
            value={pendingMemberIds}
            onChange={setPendingMemberIds}
            options={candidates.map((u) => ({
              label: `${u.username}${u.display_name ? `（${u.display_name}）` : ''}`,
              value: u.id,
            }))}
            filterOption={(input, option) =>
              String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
            }
          />
          <Button type="primary" onClick={handleAddMembers} disabled={pendingMemberIds.length === 0}>
            划入
          </Button>
        </Space.Compact>

        <Table
          rowKey="id"
          size="small"
          loading={membersLoading}
          dataSource={members}
          pagination={false}
          locale={{
            emptyText: (
              <Empty description="该部门暂无成员" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ),
          }}
          columns={[
            { title: '用户名', dataIndex: 'username', key: 'username' },
            { title: '显示名', dataIndex: 'display_name', key: 'display_name' },
            {
              title: '余额',
              dataIndex: 'credit_balance',
              key: 'credit_balance',
              render: (v: number) => money.format(v),
            },
            {
              title: '操作',
              key: 'action',
              width: 90,
              render: (_, record: User) => (
                <Button size="small" danger onClick={() => handleRemoveMember(record)}>
                  转出
                </Button>
              ),
            },
          ]}
        />
      </Drawer>
    </div>
  );
}

export default DepartmentsPage;
