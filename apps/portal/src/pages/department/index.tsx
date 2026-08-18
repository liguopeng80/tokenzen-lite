import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Card,
  Col,
  DatePicker,
  Progress,
  Row,
  Select,
  Space,
  Spin,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type {
  DeptAggDimension,
  DeptAggRow,
  DeptBudget,
  DeptMember,
  ManagedDepartment,
} from '@token-zen/shared';
import { formatNumber } from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { deptApi } from '@/api/dept';
import { useMoney } from '@/stores/site';
import dayjs, { type Dayjs } from 'dayjs';

const { Title, Text } = Typography;

/** 明细页签的维度定义。渠道与部门维度不对负责人开放，故不在此列。 */
const DIMENSIONS: { key: DeptAggDimension; label: string; column: string }[] = [
  { key: 'user', label: '按成员', column: '成员' },
  { key: 'model', label: '按模型', column: '模型' },
  { key: 'day', label: '按日期', column: '日期' },
];

/** 预算使用率的呈现分档：超预算为红，接近上限为橙，其余为正常色。 */
function budgetStatusColor(percent: number, overBudget: boolean): string {
  if (overBudget) return '#cf1322';
  if (percent >= 80) return '#d46b08';
  return '#1677ff';
}

function DepartmentPage() {
  const money = useMoney();

  const [departments, setDepartments] = useState<ManagedDepartment[]>([]);
  const [departmentId, setDepartmentId] = useState<number | undefined>();
  const [month, setMonth] = useState<Dayjs>(dayjs());
  const [budget, setBudget] = useState<DeptBudget | null>(null);
  const [dimension, setDimension] = useState<DeptAggDimension>('user');
  const [rows, setRows] = useState<DeptAggRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [rowsLoading, setRowsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 负责部门列表决定整个页面是否有内容可显示，先于其余请求完成。
  useEffect(() => {
    deptApi
      .departments()
      .then((list) => {
        const items = list ?? [];
        setDepartments(items);
        setDepartmentId(items[0]?.id);
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (!departmentId) return;
    setError(null);
    deptApi
      .budget(departmentId, month.format('YYYY-MM'))
      .then(setBudget)
      .catch((e: Error) => setError(e.message));
  }, [departmentId, month]);

  useEffect(() => {
    if (!departmentId) return;
    setRowsLoading(true);
    // 明细的时间范围与预算卡保持同一自然月，避免两处口径不一致。
    deptApi
      .costReport(departmentId, dimension, {
        start_timestamp: month.startOf('month').unix(),
        end_timestamp: month.endOf('month').unix(),
      })
      .then((report) => setRows(report?.rows ?? []))
      .catch((e: Error) => setError(e.message))
      .finally(() => setRowsLoading(false));
  }, [departmentId, dimension, month]);

  const fetchMembers = useCallback(
    (params: Record<string, unknown>) =>
      deptApi.members(departmentId as number, params),
    [departmentId],
  );
  const members = useTable<DeptMember>({
    fetchFn: fetchMembers,
    defaultPageSize: 20,
    deps: [departmentId],
    // 负责部门列表尚未返回时不发请求：此时没有可用的 department_id。
    autoFetch: Boolean(departmentId),
  });

  const detailColumns: ColumnsType<DeptAggRow> = useMemo(() => {
    const label = DIMENSIONS.find((d) => d.key === dimension)?.column ?? '分组';
    return [
      { title: label, dataIndex: 'group_key', render: (v: string) => v || '-' },
      {
        title: '请求数',
        dataIndex: 'requests',
        align: 'right',
        render: (v: number) => formatNumber(v),
      },
      {
        title: '输入 tokens',
        dataIndex: 'prompt_tokens',
        align: 'right',
        render: (v: number) => formatNumber(v),
      },
      {
        title: '输出 tokens',
        dataIndex: 'completion_tokens',
        align: 'right',
        render: (v: number) => formatNumber(v),
      },
      {
        title: '消费',
        dataIndex: 'credits_charged',
        align: 'right',
        render: (v: number) => <Text>{money.format(v)}</Text>,
      },
    ];
  }, [dimension, money]);

  const memberColumns: ColumnsType<DeptMember> = [
    {
      title: '成员',
      dataIndex: 'username',
      render: (username: string, row) => (
        <Space direction="vertical" size={0}>
          <Text>{row.display_name || username}</Text>
          <Text type="secondary">{username}</Text>
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (status: string) =>
        status === 'enabled' ? <Tag color="green">正常</Tag> : <Tag>已禁用</Tag>,
    },
    {
      title: '当前余额',
      dataIndex: 'credit_balance',
      align: 'right',
      render: (v: number) => money.format(v),
    },
    {
      title: '本月消费',
      dataIndex: 'month_credits_charged',
      align: 'right',
      render: (v: number) => money.format(v),
    },
    {
      title: '本月请求数',
      dataIndex: 'month_requests',
      align: 'right',
      render: (v: number) => formatNumber(v),
    },
  ];

  if (loading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
        <Spin size="large" />
      </div>
    );
  }

  // 不是任何部门的负责人：明确说明原因与获取途径，不显示空表格。
  if (departments.length === 0) {
    return (
      <Alert
        type="info"
        showIcon
        message="当前账号不是任何部门的负责人"
        description="部门费用视图只对部门负责人开放。需要查看某部门的费用时，请管理员在管理端把该部门的负责人设为你的账号。"
      />
    );
  }

  const selected = departments.find((d) => d.id === departmentId);

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>部门费用</Title>
      <Text type="secondary">
        本视图只统计本部门的消费与用量，不含网关的采购成本。费用按记账时点的部门归属聚合，成员转部门后历史口径不变。
      </Text>

      <Space style={{ margin: '16px 0' }} wrap>
        {departments.length > 1 && (
          <Select
            value={departmentId}
            style={{ minWidth: 200 }}
            onChange={setDepartmentId}
            options={departments.map((d) => ({
              value: d.id,
              label: d.code ? `${d.name}（${d.code}）` : d.name,
            }))}
          />
        )}
        <DatePicker
          picker="month"
          value={month}
          allowClear={false}
          onChange={(v) => v && setMonth(v)}
        />
      </Space>

      {error && (
        <Alert
          type="error"
          showIcon
          closable
          style={{ marginBottom: 16 }}
          message="查询失败"
          description={error}
          onClose={() => setError(null)}
        />
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={8}>
          <Card>
            <Statistic
              title={`${month.format('YYYY 年 M 月')}消费`}
              value={money.format(budget?.credits_charged ?? 0)}
            />
          </Card>
        </Col>
        <Col xs={24} sm={8}>
          <Card>
            <Statistic
              title="月度预算"
              value={
                budget?.monthly_budget_credits
                  ? money.format(budget.monthly_budget_credits)
                  : '未设预算'
              }
            />
          </Card>
        </Col>
        <Col xs={24} sm={8}>
          <Card>
            <Text type="secondary">预算使用率</Text>
            {budget && budget.monthly_budget_credits > 0 ? (
              <>
                <Progress
                  percent={Math.min(budget.budget_used_percent, 100)}
                  strokeColor={budgetStatusColor(
                    budget.budget_used_percent,
                    budget.over_budget,
                  )}
                  format={() => `${budget.budget_used_percent}%`}
                />
                {budget.over_budget && <Tag color="red">已超预算</Tag>}
              </>
            ) : (
              <div style={{ marginTop: 8 }}>
                <Text type="secondary">未设预算，不做超预算判定</Text>
              </div>
            )}
          </Card>
        </Col>
      </Row>

      <Card
        style={{ marginBottom: 16 }}
        title={selected ? `${selected.name} · 费用明细` : '费用明细'}
      >
        <Tabs
          activeKey={dimension}
          onChange={(k) => setDimension(k as DeptAggDimension)}
          items={DIMENSIONS.map((d) => ({ key: d.key, label: d.label }))}
        />
        <Table<DeptAggRow>
          rowKey={(row) => `${row.group_id}-${row.group_key}`}
          columns={detailColumns}
          dataSource={rows}
          loading={rowsLoading}
          pagination={false}
          locale={{ emptyText: '所选月份内本部门没有消费记录' }}
        />
      </Card>

      <Card title="成员">
        <Table<DeptMember>
          rowKey="user_id"
          columns={memberColumns}
          dataSource={members.dataSource}
          loading={members.loading}
          pagination={members.pagination}
          locale={{ emptyText: '本部门暂无成员' }}
        />
      </Card>
    </div>
  );
}

export default DepartmentPage;
