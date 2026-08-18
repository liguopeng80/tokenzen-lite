import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Col, DatePicker, Empty, Progress, Row, Segmented, Select, Space, Statistic, Table, Tabs, Tag, Typography } from 'antd';
import { message } from '@token-zen/shared';
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import dayjs, { type Dayjs } from 'dayjs';
import type {
  CostReportDimension,
  CostReportRow,
  DepartmentBudgetRow,
  DepartmentWithStats,
  ProjectBudgetRow,
  ProjectWithStats,
} from '@token-zen/shared';
import { CostReportDimensionLabel } from '@token-zen/shared';
import { departmentApi, projectApi, reportApi } from '@/api/organization';
import { usageLogApi } from '@/api/usageLogs';
import { useMoney } from '@/stores/site';
import { errorMessageOf } from '@/api/error';
import HeatmapTab from './HeatmapTab';

const { Title, Text, Paragraph } = Typography;
const { RangePicker } = DatePicker;

const dimensions: CostReportDimension[] = ['user', 'department', 'project', 'model', 'channel', 'day', 'key'];

function CostReportTab() {
  const money = useMoney();
  const [dimension, setDimension] = useState<CostReportDimension>('department');
  const [range, setRange] = useState<[Dayjs, Dayjs]>([dayjs().subtract(30, 'day'), dayjs()]);
  const [departmentId, setDepartmentId] = useState<number | undefined>();
  const [departments, setDepartments] = useState<DepartmentWithStats[]>([]);
  const [projectId, setProjectId] = useState<number | undefined>();
  const [projects, setProjects] = useState<ProjectWithStats[]>([]);
  const [rows, setRows] = useState<CostReportRow[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    departmentApi
      .options()
      .then((data) => setDepartments(data.items ?? []))
      .catch(() => setDepartments([]));
  }, []);

  useEffect(() => {
    projectApi
      .options()
      .then((data) => setProjects(data.items ?? []))
      .catch(() => setProjects([]));
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const report = await reportApi.cost({
        group_by: dimension,
        start_timestamp: range[0].startOf('day').unix(),
        end_timestamp: range[1].endOf('day').unix(),
        ...(departmentId !== undefined ? { department_id: departmentId } : {}),
        ...(projectId !== undefined ? { project_id: projectId } : {}),
      });
      setRows(report.rows ?? []);
    } catch (err) {
      message.error(errorMessageOf(err, '费用报表查询失败'));
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [dimension, range, departmentId, projectId]);

  useEffect(() => {
    load();
  }, [load]);

  const totals = rows.reduce(
    (acc, row) => ({
      requests: acc.requests + row.requests,
      charged: acc.charged + row.credits_charged,
      cost: acc.cost + row.credits_cost,
      margin: acc.margin + row.margin,
    }),
    { requests: 0, charged: 0, cost: 0, margin: 0 },
  );

  const columns: ColumnsType<CostReportRow> = [
    { title: CostReportDimensionLabel[dimension].replace('按', ''), dataIndex: 'group_key', key: 'group_key' },
    { title: '请求数', dataIndex: 'requests', key: 'requests', width: 110 },
    {
      title: 'Token 合计',
      key: 'tokens',
      width: 130,
      render: (_, row) => (row.prompt_tokens + row.completion_tokens).toLocaleString(),
    },
    {
      title: '用户扣费',
      dataIndex: 'credits_charged',
      key: 'credits_charged',
      width: 160,
      render: (v: number) => money.format(v),
    },
    {
      title: '渠道成本',
      dataIndex: 'credits_cost',
      key: 'credits_cost',
      width: 160,
      render: (v: number) => money.format(v),
    },
    {
      title: '毛利',
      dataIndex: 'margin',
      key: 'margin',
      width: 160,
      render: (v: number) => (
        <Text type={v < 0 ? 'danger' : undefined}>{money.format(v)}</Text>
      ),
    },
  ];

  const handleExport = () => {
    const url = usageLogApi.exportUrl({
      start_timestamp: range[0].startOf('day').unix(),
      end_timestamp: range[1].endOf('day').unix(),
    });
    window.open(url, '_blank');
  };

  return (
    <div>
      <Space style={{ marginBottom: 16 }} wrap>
        <Segmented
          value={dimension}
          onChange={(v) => setDimension(v as CostReportDimension)}
          options={dimensions.map((d) => ({ label: CostReportDimensionLabel[d], value: d }))}
        />
        <RangePicker
          value={range}
          allowClear={false}
          onChange={(values) => {
            if (values && values[0] && values[1]) setRange([values[0], values[1]]);
          }}
        />
        <Select
          placeholder="全部部门"
          allowClear
          style={{ width: 180 }}
          value={departmentId}
          onChange={(v) => setDepartmentId(v)}
          options={[
            { label: '未分配部门', value: 0 },
            ...departments.map((d) => ({ label: d.name, value: d.id })),
          ]}
        />
        <Select
          placeholder="全部项目"
          allowClear
          style={{ width: 180 }}
          value={projectId}
          onChange={(v) => setProjectId(v)}
          options={[
            { label: '未归属项目', value: 0 },
            ...projects.map((p) => ({ label: p.name, value: p.id })),
          ]}
        />
        <Button icon={<ReloadOutlined />} onClick={load}>
          刷新
        </Button>
        <Button icon={<DownloadOutlined />} onClick={handleExport}>
          导出该区间明细
        </Button>
      </Space>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card size="small">
            <Statistic title="请求数" value={totals.requests} />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic title="用户扣费" value={money.format(totals.charged)} />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic title="渠道成本" value={money.format(totals.cost)} />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="毛利"
              value={money.format(totals.margin)}
              valueStyle={totals.margin < 0 ? { color: '#cf1322' } : undefined}
            />
          </Card>
        </Col>
      </Row>

      <Table
        rowKey={(row) => `${row.group_id}-${row.group_key}`}
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={{ pageSize: 20, showSizeChanger: true }}
        locale={{
          emptyText: (
            <Empty
              description="所选区间内没有已结算的用量。报表只统计结算成功的请求，失败与已退款的请求不计入分摊。"
              image={Empty.PRESENTED_IMAGE_SIMPLE}
            />
          ),
        }}
      />
    </div>
  );
}

function DepartmentBudgetTab() {
  const money = useMoney();
  const [month, setMonth] = useState<Dayjs>(dayjs());
  const [rows, setRows] = useState<DepartmentBudgetRow[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const report = await reportApi.departmentBudget(month.format('YYYY-MM'));
      setRows(report.rows ?? []);
    } catch (err) {
      message.error(errorMessageOf(err, '部门预算对比查询失败'));
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [month]);

  useEffect(() => {
    load();
  }, [load]);

  const columns: ColumnsType<DepartmentBudgetRow> = [
    { title: '部门', dataIndex: 'department_name', key: 'department_name' },
    { title: '请求数', dataIndex: 'requests', key: 'requests', width: 110 },
    {
      title: '当月消费',
      dataIndex: 'credits_charged',
      key: 'credits_charged',
      width: 170,
      render: (v: number) => money.format(v),
    },
    {
      title: '月度预算',
      dataIndex: 'monthly_budget_credits',
      key: 'monthly_budget_credits',
      width: 170,
      render: (v: number) => (v > 0 ? money.format(v) : <Text type="secondary">未设预算</Text>),
    },
    {
      title: '预算使用率',
      key: 'usage',
      width: 220,
      render: (_, row) =>
        row.monthly_budget_credits > 0 ? (
          <Progress
            percent={Math.min(row.budget_used_percent, 100)}
            status={row.over_budget ? 'exception' : 'normal'}
            format={() => `${row.budget_used_percent}%`}
          />
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: '状态',
      key: 'status',
      width: 110,
      render: (_, row) => {
        if (row.monthly_budget_credits <= 0) return <Text type="secondary">未设预算</Text>;
        return row.over_budget ? <Tag color="red">已超预算</Tag> : <Tag color="green">预算内</Tag>;
      },
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <DatePicker picker="month" value={month} allowClear={false} onChange={(v) => v && setMonth(v)} />
        <Button icon={<ReloadOutlined />} onClick={load}>
          刷新
        </Button>
      </Space>

      <Paragraph type="secondary">
        预算按自然月核算，与财务的月度分摊口径一致。超出预算只在告警通道提醒，不拦截调用；
        需要硬性限制时，请为部门成员设置每日花费上限。
      </Paragraph>

      <Table
        rowKey="department_id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        locale={{
          emptyText: (
            <Empty description="当月没有部门消费记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />
          ),
        }}
      />
    </div>
  );
}

function ProjectBudgetTab() {
  const money = useMoney();
  const [month, setMonth] = useState<Dayjs>(dayjs());
  const [rows, setRows] = useState<ProjectBudgetRow[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const report = await reportApi.projectBudget(month.format('YYYY-MM'));
      setRows(report.rows ?? []);
    } catch (err) {
      message.error(errorMessageOf(err, '项目预算对比查询失败'));
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, [month]);

  useEffect(() => {
    load();
  }, [load]);

  const columns: ColumnsType<ProjectBudgetRow> = [
    { title: '项目', dataIndex: 'project_name', key: 'project_name' },
    { title: '请求数', dataIndex: 'requests', key: 'requests', width: 110 },
    {
      title: '当月消费',
      dataIndex: 'credits_charged',
      key: 'credits_charged',
      width: 170,
      render: (v: number) => money.format(v),
    },
    {
      title: '月度预算',
      dataIndex: 'monthly_budget_credits',
      key: 'monthly_budget_credits',
      width: 170,
      render: (v: number) => (v > 0 ? money.format(v) : <Text type="secondary">未设预算</Text>),
    },
    {
      title: '预算使用率',
      key: 'usage',
      width: 220,
      render: (_, row) =>
        row.monthly_budget_credits > 0 ? (
          <Progress
            percent={Math.min(row.budget_used_percent, 100)}
            status={row.over_budget ? 'exception' : 'normal'}
            format={() => `${row.budget_used_percent}%`}
          />
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
    {
      title: '状态',
      key: 'status',
      width: 110,
      render: (_, row) => {
        if (row.monthly_budget_credits <= 0) return <Text type="secondary">未设预算</Text>;
        return row.over_budget ? <Tag color="red">已超预算</Tag> : <Tag color="green">预算内</Tag>;
      },
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <DatePicker picker="month" value={month} allowClear={false} onChange={(v) => v && setMonth(v)} />
        <Button icon={<ReloadOutlined />} onClick={load}>
          刷新
        </Button>
      </Space>

      <Paragraph type="secondary">
        项目预算按自然月核算，口径与部门预算一致。超出预算只在告警通道提醒，不拦截调用。
      </Paragraph>

      <Table
        rowKey="project_id"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        locale={{
          emptyText: (
            <Empty description="当月没有项目消费记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />
          ),
        }}
      />
    </div>
  );
}

function ReportsPage() {
  return (
    <div>
      <Title level={4} style={{ marginBottom: 16 }}>
        费用报表
      </Title>
      <Card>
        <Tabs
          items={[
            { key: 'cost', label: '费用分摊', children: <CostReportTab /> },
            { key: 'budget', label: '部门预算', children: <DepartmentBudgetTab /> },
            { key: 'project-budget', label: '项目预算', children: <ProjectBudgetTab /> },
            { key: 'heatmap', label: '活跃时段热力图', children: <HeatmapTab /> },
          ]}
        />
      </Card>
    </div>
  );
}

export default ReportsPage;
