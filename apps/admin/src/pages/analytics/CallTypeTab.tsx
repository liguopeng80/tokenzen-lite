import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Card,
  Col,
  DatePicker,
  Empty,
  Row,
  Spin,
  Table,
  Typography,
} from 'antd';
import { message } from '@token-zen/shared';
import { Pie } from '@ant-design/charts';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import type { ColumnsType } from 'antd/es/table';
import type { CallType, CallTypeRow } from '@token-zen/shared';
import { CallTypeLabel } from '@token-zen/shared';
import { warmGray, primaryPalette, semantic } from '@token-zen/shared/theme';
import { analyticsApi, CALL_TYPE_ORDER } from '@/api/analytics';
import { useMoney } from '@/stores/site';

const { Title, Text } = Typography;
const { RangePicker } = DatePicker;

const DEFAULT_DAYS = 30;

/** 调用类型固定的图表配色，key 即 CallType，避免依赖系列顺序。 */
const CALL_TYPE_COLOR: Record<CallType, string> = {
  stream: primaryPalette[500],
  non_stream: primaryPalette[300],
  embedding: semantic.success,
  image: semantic.warning,
  other: warmGray[400],
};

/**
 * 调用类型分布 Tab：按派生调用类型（向量嵌入/图像/流式/非流式/其他）聚合的扣费分布，
 * 来自 GET /admin/stats/cost-by-calltype（原始 usage_logs，受保留期约束）。
 */
export default function CallTypeTab() {
  const money = useMoney();
  const [range, setRange] = useState<[Dayjs, Dayjs]>([
    dayjs().subtract(DEFAULT_DAYS, 'day').startOf('day'),
    dayjs().endOf('day'),
  ]);
  const [rows, setRows] = useState<CallTypeRow[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    analyticsApi
      .costByCallType({
        start_timestamp: range[0].unix(),
        end_timestamp: range[1].unix(),
      })
      .then((data) => setRows(data.rows ?? []))
      .catch(() => {
        message.error('加载调用类型分布失败');
        setRows([]);
      })
      .finally(() => setLoading(false));
  }, [range]);

  useEffect(() => {
    load();
  }, [load]);

  // 按固定顺序展示，便于跨日对比；缺项补空（仅展示有数据的）。
  const orderedRows = useMemo(
    () =>
      CALL_TYPE_ORDER.map((ct) => rows.find((r) => r.call_type === ct))
        .filter((r): r is CallTypeRow => Boolean(r)),
    [rows],
  );

  const totals = useMemo(() => {
    let charged = 0;
    let cost = 0;
    let margin = 0;
    let requests = 0;
    for (const r of rows) {
      charged += r.credits_charged;
      cost += r.credits_cost;
      margin += r.margin;
      requests += r.requests;
    }
    return { charged, cost, margin, requests };
  }, [rows]);

  const pieData = useMemo(
    () =>
      orderedRows.map((r) => ({
        call_type: r.call_type,
        label: CallTypeLabel[r.call_type],
        credits_charged: r.credits_charged,
      })),
    [orderedRows],
  );

  const pieConfig = {
    data: pieData,
    angleField: 'credits_charged',
    colorField: 'call_type',
    scale: {
      color: {
        domain: CALL_TYPE_ORDER,
        range: CALL_TYPE_ORDER.map((ct) => CALL_TYPE_COLOR[ct]),
      },
    },
    legend: {
      color: {
        itemMarker: 'circle',
        labelFormatter: (v: string) => CallTypeLabel[v as CallType] ?? v,
      },
    },
    tooltip: {
      items: [
        {
          channel: 'y' as const,
          name: '扣费',
          valueFormatter: (v: number) => money.format(v),
        },
      ],
    },
    innerRadius: 0.55,
    height: 320,
  };

  const columns: ColumnsType<CallTypeRow> = useMemo(
    () => [
      {
        title: '调用类型',
        dataIndex: 'call_type',
        key: 'call_type',
        render: (v: CallType) => CallTypeLabel[v] ?? v,
      },
      {
        title: '请求数',
        dataIndex: 'requests',
        key: 'requests',
        align: 'right',
        width: 100,
      },
      {
        title: 'Prompt tokens',
        dataIndex: 'prompt_tokens',
        key: 'prompt_tokens',
        align: 'right',
        render: (v: number) => v.toLocaleString(),
      },
      {
        title: 'Completion tokens',
        dataIndex: 'completion_tokens',
        key: 'completion_tokens',
        align: 'right',
        render: (v: number) => v.toLocaleString(),
      },
      {
        title: '扣费',
        dataIndex: 'credits_charged',
        key: 'credits_charged',
        align: 'right',
        render: (v: number) => money.format(v),
      },
      {
        title: '成本',
        dataIndex: 'credits_cost',
        key: 'credits_cost',
        align: 'right',
        render: (v: number) => money.format(v),
      },
      {
        title: '毛利',
        dataIndex: 'margin',
        key: 'margin',
        align: 'right',
        render: (v: number) => money.format(v),
      },
    ],
    [money],
  );

  return (
    <Spin spinning={loading}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 20,
          flexWrap: 'wrap',
          gap: 12,
        }}
      >
        <Title level={4} style={{ marginTop: 0, marginBottom: 0 }}>
          调用类型分布
        </Title>
        <RangePicker
          value={range}
          onChange={(v) => {
            if (v && v[0] && v[1]) {
              setRange([v[0].startOf('day'), v[1].endOf('day')]);
            }
          }}
          allowClear={false}
          disabledDate={(current) => current && current.isAfter(dayjs().endOf('day'))}
          data-testid="calltype-range-picker"
        />
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Typography.Text type="secondary">窗口扣费</Typography.Text>
            <div style={{ fontSize: 22, fontWeight: 600, color: warmGray[900] }}>
              {money.format(totals.charged)}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Typography.Text type="secondary">窗口成本</Typography.Text>
            <div style={{ fontSize: 22, fontWeight: 600, color: warmGray[900] }}>
              {money.format(totals.cost)}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Typography.Text type="secondary">窗口毛利</Typography.Text>
            <div style={{ fontSize: 22, fontWeight: 600, color: warmGray[900] }}>
              {money.format(totals.margin)}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Typography.Text type="secondary">窗口请求</Typography.Text>
            <div style={{ fontSize: 22, fontWeight: 600, color: warmGray[900] }}>
              {totals.requests.toLocaleString()}
            </div>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={10}>
          <Card title="扣费占比">
            <div data-testid="calltype-pie-chart" style={{ minHeight: 320 }}>
              {pieData.length >= 1 ? (
                <Pie {...pieConfig} />
              ) : (
                <Empty
                  description="所选时间范围内还没有调用记录。"
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  style={{
                    height: 320,
                    display: 'flex',
                    flexDirection: 'column',
                    justifyContent: 'center',
                  }}
                />
              )}
            </div>
          </Card>
        </Col>
        <Col xs={24} lg={14}>
          <Card title="按调用类型明细">
            <Table<CallTypeRow>
              rowKey="call_type"
              columns={columns}
              dataSource={orderedRows}
              pagination={false}
              size="small"
              locale={{
                emptyText: (
                  <Empty
                    description="所选时间范围内还没有调用记录。"
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                  />
                ),
              }}
            />
          </Card>
        </Col>
      </Row>

      <Text type="secondary" style={{ display: 'block', marginTop: 12, fontSize: 12 }}>
        数据来自原始 usage_logs，按模型形态（modality）与是否流式派生调用类型；
        模型已删除或不在目录中时归入「其他」。受用量日志保留期约束，建议查询 30 天内。
      </Text>
    </Spin>
  );
}
