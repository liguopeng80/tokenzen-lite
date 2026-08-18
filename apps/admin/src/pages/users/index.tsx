import { useState, useMemo, useCallback, useEffect } from 'react';
import { Alert, Card, Table, Button, Space, Typography, Input, Modal, Form, Select, InputNumber, Tag, Drawer, Dropdown } from 'antd';
import { message, modal } from '@token-zen/shared';
import {
  PlusOutlined,
  ReloadOutlined,
  UploadOutlined,
  GiftOutlined,
  SearchOutlined,
  KeyOutlined,
  MoreOutlined,
  EditOutlined,
  DollarOutlined,
  BarChartOutlined,
  StopOutlined,
  CheckCircleOutlined,
  LockOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { User, ApiKey, Role, UserStatus, DepartmentWithStats } from '@token-zen/shared';
import {
  Roles,
  RoleLabel,
  UserStatusLabel,
  KeyStatusLabel,
  formatTime,
  maskKey,
  UsernamePattern,
  UsernameRuleHint,
} from '@token-zen/shared';
import { useTable, useModalForm } from '@token-zen/shared/hooks';
import { userApi } from '@/api/users';
import { batchApi, departmentApi } from '@/api/organization';
import { errorMessageOf } from '@/api/error';
import BatchOpsModals from './BatchOpsModals';
import UserConsumptionDrawer from './UserConsumptionDrawer';
import { useAuthStore } from '@/stores/auth';
import { useMoney } from '@/stores/site';

const { Title, Paragraph, Text } = Typography;

const roleOptions = [
  { label: RoleLabel.user, value: Roles.User },
  { label: RoleLabel.admin, value: Roles.Admin },
  { label: RoleLabel.root, value: Roles.Root },
];

const roleColorMap: Record<Role, string> = {
  root: 'red',
  admin: 'blue',
  managed: 'purple',
  user: 'default',
};

function UsersPage() {
  const currentUser = useAuthStore((s) => s.user);
  const money = useMoney();
  const isRoot = currentUser?.role === Roles.Root;
  const [keyword, setKeyword] = useState('');
  const [roleFilter, setRoleFilter] = useState<Role | ''>('');
  // undefined = 不按部门筛选；0 = 只看未分配部门（两者语义不同，不能合并）
  const [departmentFilter, setDepartmentFilter] = useState<number | undefined>();
  const [departments, setDepartments] = useState<DepartmentWithStats[]>([]);
  const [importOpen, setImportOpen] = useState(false);
  const [batchGrantOpen, setBatchGrantOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [resetPwdUser, setResetPwdUser] = useState<User | null>(null);
  // 系统生成的初始密码只在建号响应中返回一次，关闭弹窗后无法再次取得。
  const [generatedPassword, setGeneratedPassword] = useState<
    { username: string; password: string } | null
  >(null);
  const [resetPwdLoading, setResetPwdLoading] = useState(false);
  const [resetPwdForm] = Form.useForm();
  const [keysDrawerUser, setKeysDrawerUser] = useState<User | null>(null);
  const [userKeys, setUserKeys] = useState<ApiKey[]>([]);
  const [keysLoading, setKeysLoading] = useState(false);
  // 消费明细抽屉的目标用户。userId 非 null 即打开。
  const [consumptionUser, setConsumptionUser] = useState<{ id: number; username?: string } | null>(
    null,
  );

  useEffect(() => {
    departmentApi
      .options()
      .then((data) => setDepartments(data.items ?? []))
      .catch(() => setDepartments([]));
  }, []);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      userApi.list({
        ...params,
        ...(keyword ? { keyword } : {}),
        ...(roleFilter ? { role: roleFilter } : {}),
        ...(departmentFilter !== undefined ? { department_id: departmentFilter } : {}),
      }),
    [keyword, roleFilter, departmentFilter],
  );

  const { dataSource, loading, pagination, refresh } = useTable<User>({
    fetchFn,
    deps: [keyword, roleFilter, departmentFilter],
  });

  /**
   * 批量状态变更：选中行的启用与禁用。用于离职与调岗的集中处置，
   * 逐条处理，单条失败不影响其余账号，失败明细在结果弹窗中逐条展示。
   */
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [batchStatusLoading, setBatchStatusLoading] = useState(false);

  const runBatchStatus = useCallback(
    async (status: UserStatus) => {
      setBatchStatusLoading(true);
      try {
        const summary = await batchApi.setUserStatus({ user_ids: selectedIds, status });
        const label = status === 'disabled' ? '禁用' : '启用';
        const failures = summary.results.filter((r) => !r.ok);
        if (failures.length === 0) {
          message.success(
            `已${label} ${summary.succeeded} 个账号` +
              (summary.unchanged > 0 ? `，${summary.unchanged} 个本就是该状态` : ''),
          );
        } else {
          modal.warning({
            title: `${failures.length} 个账号未能${label}`,
            content: (
              <div>
                <p>
                  成功 {summary.succeeded} 个，未改动 {summary.unchanged} 个。以下账号未处理：
                </p>
                <ul>
                  {failures.map((r) => (
                    <li key={r.user_id}>
                      {r.username || `ID ${r.user_id}`}：{r.message}
                    </li>
                  ))}
                </ul>
              </div>
            ),
          });
        }
        setSelectedIds([]);
        refresh();
      } catch (err) {
        message.error(errorMessageOf(err, '批量变更账号状态失败'));
      } finally {
        setBatchStatusLoading(false);
      }
    },
    [selectedIds, refresh],
  );

  const confirmBatchDisable = useCallback(() => {
    modal.confirm({
      title: `确认禁用选中的 ${selectedIds.length} 个账号？`,
      content: '禁用后这些账号无法登录，其全部 API Key 立即停止调用。可再次批量启用恢复。',
      okText: '禁用',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => runBatchStatus('disabled'),
    });
  }, [selectedIds.length, runBatchStatus]);

  const departmentName = useCallback(
    (id: number | null) => departments.find((d) => d.id === id)?.name,
    [departments],
  );

  /** 编辑表单的初值：模型策略以换行分隔文本编辑，部门 null 映射为 0（未分配）。 */
  const editFormValues = useCallback(
    (record: User) => ({
      display_name: record.display_name,
      email: record.email,
      role: record.role,
      department_id: record.department_id ?? 0,
      allowed_models_text: (record.allowed_models ?? []).join('\n'),
      // 表单现为货币金额，从已有积分折算回显
      daily_spend_limit: money.fromCredits(record.daily_spend_limit ?? 0),
    }),
    [money],
  );

  // Create user modal
  const createModal = useModalForm({
    onSubmit: async (values) => {
      const { initial_credits, ...rest } = values as Record<string, unknown> & {
        initial_credits?: number;
      };
      // 表单收集的是货币金额，提交时转回积分（API 契约不变）
      const created = await userApi.create({
        ...rest,
        initial_credits: money.toCredits(Number(initial_credits) || 0),
      } as unknown as Parameters<typeof userApi.create>[0]);
      message.success('用户创建成功');
      // 系统生成的初始密码只在本次响应中返回一次，就地展示供管理员取走转达。
      if (created.initial_password) {
        setGeneratedPassword({
          username: created.username,
          password: created.initial_password,
        });
      }
    },
    onSuccess: refresh,
  });

  // Edit user modal
  const editModal = useModalForm({
    onSubmit: async (values) => {
      const { allowed_models_text, daily_spend_limit, ...rest } = values as Record<
        string,
        unknown
      > & {
        allowed_models_text?: string;
        daily_spend_limit?: number;
      };
      await userApi.update(editingUser!.id, {
        ...rest,
        // 表单收集的是货币金额，提交时转回积分
        daily_spend_limit: money.toCredits(Number(daily_spend_limit) || 0),
        allowed_models: (allowed_models_text ?? '')
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
      } as unknown as Parameters<typeof userApi.update>[1]);
      message.success('用户信息已更新');
    },
    onSuccess: refresh,
  });

  // Credit adjustment modal
  const creditModal = useModalForm({
    onSubmit: async (values) => {
      const { amount, note } = values as { amount: number; note: string };
      // 表单输入为货币金额，提交时转回积分
      await userApi.adjustCredits(editingUser!.id, money.toCredits(amount), note);
      message.success('余额已调整');
    },
    onSuccess: refresh,
  });

  const handleToggleStatus = async (user: User) => {
    const newStatus: UserStatus = user.status === 'enabled' ? 'disabled' : 'enabled';
    await userApi.setStatus(user.id, newStatus);
    message.success(newStatus === 'enabled' ? '用户已启用' : '用户已禁用');
    refresh();
  };

  const handleResetPassword = async () => {
    try {
      const values = await resetPwdForm.validateFields();
      setResetPwdLoading(true);
      await userApi.resetPassword(resetPwdUser!.id, values.password);
      message.success('密码已重置，该用户全部登录已失效');
      setResetPwdUser(null);
      resetPwdForm.resetFields();
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return;
      message.error(err instanceof Error ? err.message : '重置失败');
    } finally {
      setResetPwdLoading(false);
    }
  };

  const handleViewKeys = async (user: User) => {
    setKeysDrawerUser(user);
    setKeysLoading(true);
    try {
      const result = await userApi.listKeys(user.id);
      setUserKeys(result.items ?? []);
    } catch {
      setUserKeys([]);
    } finally {
      setKeysLoading(false);
    }
  };

  const columns: ColumnsType<User> = useMemo(
    () => [
      { title: 'ID', dataIndex: 'id', width: 60, sorter: (a, b) => a.id - b.id },
      {
        title: '用户名',
        dataIndex: 'username',
        width: 170,
        ellipsis: true,
        sorter: (a, b) => (a.username ?? '').localeCompare(b.username ?? ''),
        defaultSortOrder: 'ascend' as const,
        render: (name: string, record: User) => (
          <a
            onClick={() => {
              setEditingUser(record);
              editModal.show(editFormValues(record));
            }}
            style={{ fontWeight: 500 }}
          >
            {name || '(未知)'}
          </a>
        ),
      },
      {
        title: '显示名称',
        dataIndex: 'display_name',
        width: 140,
        ellipsis: true,
        render: (name: string) => name || '-',
      },
      {
        title: '部门',
        dataIndex: 'department_id',
        width: 130,
        ellipsis: true,
        render: (id: number | null) =>
          id ? departmentName(id) ?? `已删除部门 #${id}` : <span style={{ color: '#a8a89e' }}>未分配</span>,
      },
      {
        title: '角色',
        dataIndex: 'role',
        width: 100,
        render: (role: Role) => (
          <Tag color={roleColorMap[role] ?? 'default'}>
            {RoleLabel[role] ?? `未知(${role})`}
          </Tag>
        ),
      },
      {
        title: '余额',
        dataIndex: 'credit_balance',
        width: 200,
        sorter: (a, b) => a.credit_balance - b.credit_balance,
        render: (v: number) => money.format(v),
        align: 'right',
      },
      {
        title: '已用',
        dataIndex: 'credit_used',
        width: 120,
        sorter: (a, b) => a.credit_used - b.credit_used,
        render: (v: number) => money.format(v),
        align: 'right',
      },
      {
        title: '请求数',
        dataIndex: 'request_count',
        width: 100,
        sorter: (a, b) => a.request_count - b.request_count,
        align: 'right',
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 90,
        render: (status: UserStatus) => (
          <Tag color={status === 'enabled' ? 'green' : 'red'}>
            {UserStatusLabel[status] ?? '未知'}
          </Tag>
        ),
      },
      {
        title: '注册时间',
        dataIndex: 'created_at',
        render: (t: string) => formatTime(t),
        width: 160,
        responsive: ['lg'] as const,
      },
      {
        title: '操作',
        key: 'action',
        width: 80,
        fixed: 'right' as const,
        render: (_: unknown, record: User) => {
          const isTargetRoot = record.role === Roles.Root;
          // Regular admin cannot modify root users
          const canModify = isRoot || !isTargetRoot;
          const isSelf = record.id === currentUser?.id;

          const menuItems = [
            {
              key: 'keys',
              icon: <KeyOutlined />,
              label: '密钥',
              onClick: () => handleViewKeys(record),
            },
            {
              key: 'consumption',
              icon: <BarChartOutlined />,
              label: '查看消费',
              onClick: () =>
                setConsumptionUser({ id: record.id, username: record.username ?? undefined }),
            },
          ] as { key: string; icon: React.ReactNode; label: string; onClick: () => void; danger?: boolean }[];
          if (canModify) {
            menuItems.push(
              {
                key: 'edit',
                icon: <EditOutlined />,
                label: '编辑',
                onClick: () => {
                  setEditingUser(record);
                  editModal.show(editFormValues(record));
                },
              },
              {
                key: 'credits',
                icon: <DollarOutlined />,
                label: '调整余额',
                onClick: () => {
                  setEditingUser(record);
                  creditModal.show({ amount: 0, note: '' });
                },
              },
              {
                key: 'reset-password',
                icon: <LockOutlined />,
                label: '重置密码',
                onClick: () => setResetPwdUser(record),
              },
            );
            if (!isSelf) {
              menuItems.push({
                key: 'toggle',
                icon: record.status === 'enabled' ? <StopOutlined /> : <CheckCircleOutlined />,
                label: record.status === 'enabled' ? '禁用' : '启用',
                onClick: () => handleToggleStatus(record),
              });
            }
          }
          return (
            <Dropdown menu={{ items: menuItems }} trigger={['click']}>
              <Button type="text" size="small" icon={<MoreOutlined />} />
            </Dropdown>
          );
        },
      },
    ],
    [editModal, creditModal, isRoot, currentUser, money, departmentName, editFormValues],
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
          用户管理
        </Title>
        <Space>
          <Select
            value={roleFilter}
            onChange={setRoleFilter}
            style={{ width: 130 }}
            options={[{ label: '全部角色', value: '' }, ...roleOptions]}
          />
          <Select
            placeholder="全部部门"
            allowClear
            style={{ width: 150 }}
            value={departmentFilter}
            onChange={(v) => setDepartmentFilter(v)}
            options={[
              { label: '未分配部门', value: 0 },
              ...departments.map((d) => ({ label: d.name, value: d.id })),
            ]}
          />
          <Input.Search
            placeholder="搜索用户名"
            onSearch={setKeyword}
            allowClear
            style={{ width: 200 }}
            prefix={<SearchOutlined />}
          />
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button icon={<UploadOutlined />} onClick={() => setImportOpen(true)}>
            批量导入
          </Button>
          <Button icon={<GiftOutlined />} onClick={() => setBatchGrantOpen(true)}>
            批量发放
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => createModal.show({ role: Roles.User })}
          >
            创建用户
          </Button>
        </Space>
      </div>
      <Card>
        {selectedIds.length > 0 && (
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message={`已选中 ${selectedIds.length} 个账号`}
            action={
              <Space>
                <Button
                  size="small"
                  icon={<CheckCircleOutlined />}
                  loading={batchStatusLoading}
                  onClick={() => runBatchStatus('enabled')}
                >
                  批量启用
                </Button>
                <Button
                  size="small"
                  danger
                  icon={<StopOutlined />}
                  loading={batchStatusLoading}
                  onClick={confirmBatchDisable}
                >
                  批量禁用
                </Button>
                <Button size="small" type="text" onClick={() => setSelectedIds([])}>
                  取消选择
                </Button>
              </Space>
            }
          />
        )}
        <Table
          columns={columns}
          dataSource={dataSource}
          rowKey="id"
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1430 }}
          sticky
          rowSelection={{
            selectedRowKeys: selectedIds,
            onChange: (keys) => setSelectedIds(keys as number[]),
            // 自己的账号与无权管理的超级管理员不可选：勾上也只会逐条失败。
            getCheckboxProps: (record: User) => ({
              disabled: record.id === currentUser?.id || (!isRoot && record.role === Roles.Root),
            }),
          }}
        />
      </Card>

      {/* Create User Modal */}
      <Modal
        title="创建用户"
        open={createModal.open}
        onOk={createModal.handleOk}
        onCancel={createModal.close}
        confirmLoading={createModal.loading}
      >
        <Form form={createModal.form} layout="vertical" autoComplete="off">
          <Form.Item
            name="username"
            label="用户名"
            extra={`${UsernameRuleHint}，创建后不可修改。`}
            rules={[
              { required: true, message: '请输入用户名' },
              {
                pattern: UsernamePattern,
                message: UsernameRuleHint,
              },
            ]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="password"
            label="初始密码"
            extra="留空则由系统生成一次性初始密码，创建后展示一次。无论哪种方式，该用户首次登录都必须自行改密。"
            rules={[{ min: 8, message: '密码至少8位' }]}
          >
            <Input.Password autoComplete="new-password" placeholder="留空自动生成" />
          </Form.Item>
          <Form.Item name="display_name" label="显示名称">
            <Input />
          </Form.Item>
          <Form.Item
            name="email"
            label="邮箱"
            extra="余额不足提醒会发到该地址，留空则该用户收不到提醒。"
            rules={[{ type: 'email', message: '邮箱格式不正确，示例：name@example.com' }]}
          >
            <Input type="email" placeholder="name@example.com" />
          </Form.Item>
          <Form.Item
            name="initial_credits"
            label={`初始余额（${money.symbol}）`}
            initialValue={0}
            extra="余额为零的账号即使密钥正确也会被拒绝调用。留 0 表示建号后再单独发放。"
          >
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="role" label="角色" initialValue={Roles.User}>
            <Select options={isRoot ? roleOptions : roleOptions.filter((o) => o.value === Roles.User)} />
          </Form.Item>
          <Form.Item name="department_id" label="所属部门">
            <Select
              allowClear
              placeholder="未分配"
              options={departments
                .filter((d) => d.status === 'enabled')
                .map((d) => ({ label: d.name, value: d.id }))}
            />
          </Form.Item>
        </Form>
      </Modal>

      {/* Edit User Modal */}
      <Modal
        title={`编辑用户 - ${editingUser?.username ?? ''}`}
        open={editModal.open}
        onOk={editModal.handleOk}
        onCancel={editModal.close}
        confirmLoading={editModal.loading}
      >
        <Form form={editModal.form} layout="vertical" autoComplete="off">
          <Form.Item name="display_name" label="显示名称">
            <Input disabled={editingUser?.role === Roles.Managed} />
          </Form.Item>
          <Form.Item
            name="email"
            label="邮箱"
            extra="余额不足提醒会发到该地址，留空则该用户收不到提醒。"
            rules={[{ type: 'email', message: '邮箱格式不正确，示例：name@example.com' }]}
          >
            <Input type="email" placeholder="name@example.com" />
          </Form.Item>
          <Form.Item
            name="role"
            label="角色"
            extra={
              editingUser?.role === Roles.Managed
                ? '服务账号由接入方管理，角色与显示名称不可修改'
                : !isRoot
                  ? '仅超级管理员可变更角色'
                  : undefined
            }
          >
            <Select
              options={
                editingUser?.role === Roles.Managed
                  ? [...roleOptions, { label: RoleLabel.managed, value: Roles.Managed }]
                  : roleOptions
              }
              disabled={!isRoot || editingUser?.id === currentUser?.id || editingUser?.role === Roles.Managed}
            />
          </Form.Item>
          <Form.Item name="department_id" label="所属部门" extra="选择「未分配」即从原部门转出">
            <Select
              options={[
                { label: '未分配', value: 0 },
                ...departments
                  .filter((d) => d.status === 'enabled' || d.id === editingUser?.department_id)
                  .map((d) => ({ label: d.name, value: d.id })),
              ]}
            />
          </Form.Item>
          <Form.Item
            name="allowed_models_text"
            label="用户级模型策略"
            extra="每行一个模型名，留空表示本层不限制。有效模型集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄。"
          >
            <Input.TextArea rows={3} placeholder={'gpt-5\nclaude-5'} />
          </Form.Item>
          <Form.Item
            name="daily_spend_limit"
            label={`每日花费上限（${money.symbol}）`}
            extra="0 表示不限制。当日累计扣费触及上限后，该用户的调用会被拒绝，次日自动恢复。"
          >
            <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
          </Form.Item>
        </Form>
      </Modal>

      <BatchOpsModals
        departments={departments}
        importOpen={importOpen}
        onImportClose={() => setImportOpen(false)}
        grantOpen={batchGrantOpen}
        onGrantClose={() => setBatchGrantOpen(false)}
        onDone={refresh}
      />

      {/* Credit Adjustment Modal */}
      <Modal
        title={`调整余额 - ${editingUser?.username ?? ''}`}
        open={creditModal.open}
        onOk={creditModal.handleOk}
        onCancel={creditModal.close}
        confirmLoading={creditModal.loading}
      >
        <p>当前余额：{money.format(editingUser?.credit_balance ?? 0)}</p>
        <Form form={creditModal.form} layout="vertical">
          <Form.Item
            name="amount"
            label={`调整金额（${money.symbol}，正数增加，负数扣回）`}
            rules={[{ required: true, message: '请输入调整金额' }]}
          >
            <InputNumber style={{ width: '100%' }} step={0.01} />
          </Form.Item>
          <Form.Item
            name="note"
            label="备注"
            rules={[{ required: true, message: '请填写调整备注' }]}
          >
            <Input.TextArea rows={2} placeholder="调整原因，将记入流水" />
          </Form.Item>
        </Form>
      </Modal>

      {/* Reset Password Modal */}
      <Modal
        title={`重置密码 - ${resetPwdUser?.username ?? ''}`}
        open={!!resetPwdUser}
        onOk={handleResetPassword}
        onCancel={() => {
          setResetPwdUser(null);
          resetPwdForm.resetFields();
        }}
        confirmLoading={resetPwdLoading}
      >
        <Form form={resetPwdForm} layout="vertical">
          <Form.Item
            name="password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '密码至少 8 位' },
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 系统生成的初始密码：只在建号响应中返回一次 */}
      <Modal
        title="初始密码已生成"
        open={!!generatedPassword}
        onCancel={() => setGeneratedPassword(null)}
        onOk={() => setGeneratedPassword(null)}
        okText="我已记录"
        cancelText="关闭"
      >
        <Paragraph>
          账号 <Text strong>{generatedPassword?.username}</Text> 的初始密码如下。
          该密码只在此处展示一次，关闭后无法再次取得；需要时可为该用户重置密码。
        </Paragraph>
        <Paragraph copyable={{ text: generatedPassword?.password ?? '' }}>
          <Text code style={{ fontSize: 16 }}>
            {generatedPassword?.password}
          </Text>
        </Paragraph>
        <Paragraph type="secondary">
          该用户首次登录后必须自行修改密码，在此之前除改密外的功能不可用。
        </Paragraph>
      </Modal>

      {/* User Keys Drawer */}
      <Drawer
        title={`密钥列表 - ${keysDrawerUser?.username ?? ''}`}
        open={!!keysDrawerUser}
        onClose={() => setKeysDrawerUser(null)}
        width={640}
      >
        <Table
          dataSource={userKeys}
          rowKey="id"
          loading={keysLoading}
          pagination={false}
          size="small"
          columns={[
            { title: '名称', dataIndex: 'name' },
            {
              title: '前缀',
              dataIndex: 'key_prefix',
              render: (prefix: string) => <code style={{ fontSize: 12 }}>{maskKey(prefix)}</code>,
            },
            {
              title: '状态',
              dataIndex: 'status',
              width: 90,
              render: (status: keyof typeof KeyStatusLabel) => {
                const colorMap: Record<string, string> = {
                  enabled: 'green',
                  disabled: 'orange',
                  expired: 'red',
                  depleted: 'red',
                };
                return <Tag color={colorMap[status] ?? 'default'}>{KeyStatusLabel[status] ?? '未知'}</Tag>;
              },
            },
            {
              title: '已用',
              dataIndex: 'credit_used',
              sorter: (a: ApiKey, b: ApiKey) => a.credit_used - b.credit_used,
              render: (v: number) => money.format(v),
              align: 'right',
              width: 110,
            },
            {
              title: '每日上限',
              dataIndex: 'daily_spend_limit',
              render: (v: number) => (v ? money.format(v) : '不限'),
              width: 110,
            },
            {
              title: '最后使用',
              dataIndex: 'last_used_at',
              render: (t: string | null) => formatTime(t),
              width: 150,
            },
          ]}
        />
      </Drawer>

      {/* 用户消费明细抽屉 */}
      <UserConsumptionDrawer
        userId={consumptionUser?.id ?? null}
        username={consumptionUser?.username}
        onClose={() => setConsumptionUser(null)}
      />
    </div>
  );
}

export default UsersPage;
