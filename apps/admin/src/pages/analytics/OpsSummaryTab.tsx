import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Card,
  Col,
  Row,
  Statistic,
  DatePicker,
  Spin,
  Table,
  Empty,
  Typography,
} from 'antd';
import { message } from '@token-zen/shared';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import type { ColumnsType } from 'antd/es/table';
import type { OpsRankRow, OpsSummary } from '@token-zen/shared';
import { analyticsApi } from '@/api/analytics';
import { semantic, warmGray } from '@token-zen/shared/theme';
import { useMoney } from '@/stores/site';

const { Text } = Typography;

/** 环比百分比格式化：null（上月分母为 0）显示「—」，正数前加「+」。 */
function formatMom(pct: number | null | undefined): string {
  if (pct === null || pct === undefined) return '—';
  const sign = pct > 0 ? '+' : '';
  return `${sign}${pct.toFixed(1)}%`;
}

/** 环比颜色：增长用中性、下降用绿色、上升超过阈值用警示色。与费用增减直觉一致。 */
function momColor(pct: number | null | undefined): string {
  if (pct === null || pct === undefined) return warmGray[500];
  if (pct > 50) return semantic.warning;
  return warmGray[700];
}

/**
 * 经营分析 Tab：本月与上月消费对比、模型/用户 Top 5。
 * 数据来自 GET /admin/stats/ops-summary，复用保留期安全的聚合路径。
 */
export default function OpsSummaryTab() {
  const money = useMoney();
  const [month, setMonth] = useState<Dayjs>(dayjs().startOf('month'));
  const [summary, setSummary] = useState<OpsSummary | null>(null);
  const [loading, setLoading] = useState(false);

  const monthStr = month.format('YYYY-MM');

  const load = useCallback(() => {
    setLoading(true);
    analyticsApi
      .opsSummary(monthStr)
      .then((data) => setSummary(data))
      .catch(() => {
        message.error('加载经营分析失败');
        setSummary(null);
      })
      .finally(() => setLoading(false));
  }, [monthStr]);

  useEffect(() => {
    load();
  }, [load]);

  const modelColumns: ColumnsType<OpsRankRow> = useMemo(
    () => [
      { title: '模型', dataIndex: 'group_key', key: 'group_key' },
      {
        title: '请求数',
        dataIndex: 'requests',
        key: 'requests',
        align: 'right',
        width: 100,
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
    ],
    [money],
  );

  const userColumns: ColumnsType<OpsRankRow> = useMemo(
    () => [
      { title: '用户', dataIndex: 'group_key', key: 'group_key' },
      {
        title: '请求数',
        dataIndex: 'requests',
        key: 'requests',
        align: 'right',
        width: 100,
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
    ],
    [money],
  );

  const tm = summary?.this_month;
  const mom = summary?.mom;

  return (
    <Spin spinning={loading}>
      <div
        data-testid="ops-summary-section"
        style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}
      >
        <DatePicker
          picker="month"
          value={month}
          onChange={(v) => v && setMonth(v)}
          allowClear={false}
          disabledDate={(current) => current && current.isAfter(dayjs().endOf('month'))}
          data-testid="ops-summary-month-picker"
        />
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="本月扣费"
              value={tm ? money.format(tm.credits_charged) : '—'}
              valueStyle={{ color: warmGray[900] }}
            />
            <Text type="secondary" style={{ fontSize: 13 }}>
              上月 {summary ? money.format(summary.prev_month.credits_charged) : '—'} · 环比{' '}
              <span style={{ color: momColor(mom?.charged_pct) }}>
                {formatMom(mom?.charged_pct)}
              </span>
            </Text>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="本月成本"
              value={tm ? money.format(tm.credits_cost) : '—'}
              valueStyle={{ color: warmGray[900] }}
            />
            <Text type="secondary" style={{ fontSize: 13 }}>
              环比{' '}
              <span style={{ color: momColor(mom?.cost_pct) }}>{formatMom(mom?.cost_pct)}</span>
            </Text>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="本月请求"
              value={tm?.requests ?? 0}
              valueStyle={{ color: warmGray[900] }}
            />
            <Text type="secondary" style={{ fontSize: 13 }}>
              环比{' '}
              <span style={{ color: momColor(mom?.request_pct) }}>
                {formatMom(mom?.request_pct)}
              </span>
            </Text>
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="本月充值"
              value={tm ? money.format(tm.topup_credits) : '—'}
              valueStyle={{ color: warmGray[900] }}
            />
            <Text type="secondary" style={{ fontSize: 13 }}>
              环比{' '}
              <span style={{ color: momColor(mom?.topup_pct) }}>{formatMom(mom?.topup_pct)}</span>
            </Text>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card title="模型成本 Top 5">
            <Table<OpsRankRow>
              rowKey={(r) => `m-${r.group_id}-${r.group_key}`}
              columns={modelColumns}
              dataSource={summary?.top_models ?? []}
              pagination={false}
              size="small"
              locale={{
                emptyText: (
                  <Empty
                    description="本月暂无消费记录"
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                  />
                ),
              }}
            />
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card title="用户消费 Top 5">
            <Table<OpsRankRow>
              rowKey={(r) => `u-${r.group_id}-${r.group_key}`}
              columns={userColumns}
              dataSource={summary?.top_users ?? []}
              pagination={false}
              size="small"
              locale={{
                emptyText: (
                  <Empty
                    description="本月暂无消费记录"
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                  />
                ),
              }}
            />
          </Card>
        </Col>
      </Row>
    </Spin>
  );
}
