import { useEffect, useMemo, useState } from 'react';
import { Alert, Drawer, Empty, Spin, Table, Tag, Typography } from 'antd';
import { Line } from '@ant-design/charts';
import dayjs from 'dayjs';
import type { ColumnsType } from 'antd/es/table';
import {
  UsageStatusLabel,
  formatTime,
  type CostReportRow,
  type UsageLog,
  type UsageStatus,
} from '@token-zen/shared';
import { primaryPalette } from '@token-zen/shared/theme';
import { reportApi } from '@/api/organization';
import { usageLogApi } from '@/api/usageLogs';
import { errorMessageOf } from '@/api/error';
import { getMoney } from '@/stores/site';

const { Title } = Typography;

const statusColor: Record<UsageStatus, string> = {
  settled: 'green',
  refunded: 'purple',
  failed: 'red',
};

/** subtract(29) 含今日共 30 个日历日，与"近 30 天"语义一致。 */
const TREND_LOOKBACK_DAYS = 29;
const RECENT_LOG_PAGE_SIZE = 10;

interface UserConsumptionDrawerProps {
  userId: number | null;
  username?: string;
  onClose: () => void;
}

/**
 * 用户消费明细抽屉。从用户表的行操作唤起，展示该用户近 30 天的每日扣费趋势与
 * 最近若干条调用日志。数据全部来自既有管理端接口，无后端改动。
 */
function UserConsumptionDrawer({ userId, username, onClose }: UserConsumptionDrawerProps) {
  const open = userId !== null;

  const [trend, setTrend] = useState<CostReportRow[]>([]);
  const [trendLoading, setTrendLoading] = useState(false);
  const [trendError, setTrendError] = useState<string | null>(null);
  const [logs, setLogs] = useState<UsageLog[]>([]);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState<string | null>(null);

  useEffect(() => {
    if (!open || userId === null) return;

    // 趋势窗口固定为近 30 天（含今日）；窗口取整到日界，避免每日分桶随开窗时刻漂移。
    const startTimestamp = dayjs().subtract(TREND_LOOKBACK_DAYS, 'day').startOf('day').unix();
    const endTimestamp = dayjs().endOf('day').unix();

    setTrendLoading(true);
    setTrendError(null);
    reportApi
      .cost({
        group_by: 'day',
        user_id: userId,
        start_timestamp: startTimestamp,
        end_timestamp: endTimestamp,
      })
      .then((report) => {
        // group_key 为 "YYYY-MM-DD"，按字典序排序即按日历序，确保横轴自左向右递增。
        const sorted = [...(report.rows ?? [])].sort((a, b) =>
          a.group_key.localeCompare(b.group_key),
        );
        setTrend(sorted);
      })
      .catch((err) => {
        setTrendError(errorMessageOf(err, '加载消费趋势失败'));
        setTrend([]);
      })
      .finally(() => setTrendLoading(false));

    setLogsLoading(true);
    setLogsError(null);
    usageLogApi
      .list({ user_id: userId, page: 1, page_size: RECENT_LOG_PAGE_SIZE })
      .then((data) => setLogs(data.items ?? []))
      .catch((err) => {
        setLogsError(errorMessageOf(err, '加载最近调用失败'));
        setLogs([]);
      })
      .finally(() => setLogsLoading(false));
  }, [open, userId]);

  const chartData = useMemo(
    () => trend.map((row) => ({ date: row.group_key, credits: row.credits_charged })),
    [trend],
  );

  const totalCredits = useMemo(
    () => trend.reduce((sum, row) => sum + row.credits_charged, 0),
    [trend],
  );

  const logColumns: ColumnsType<UsageLog> = useMemo(
    () => [
      {
        title: '时间',
        dataIndex: 'created_at',
        width: 150,
        render: (t: string) => formatTime(t),
      },
      { title: '模型', dataIndex: 'model_name', ellipsis: true },
      {
        title: '扣费',
        dataIndex: 'credits_charged',
        width: 120,
        align: 'right',
        render: (v: number) => getMoney().formatDetail(v),
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 90,
        render: (v: UsageStatus) => (
          <Tag color={statusColor[v] ?? 'default'}>{UsageStatusLabel[v] ?? v}</Tag>
        ),
      },
    ],
    [],
  );

  return (
    <Drawer
      title={`消费明细${username ? ` — ${username}` : ''}`}
      open={open}
      onClose={onClose}
      width={640}
      destroyOnClose
    >
      <div data-testid="user-consumption-drawer">
        <Title level={5} style={{ marginTop: 0 }}>
          近 30 天消费趋势
        </Title>

        <div data-testid="user-consumption-trend" style={{ minHeight: 280 }}>
          {trendError ? (
            <Alert type="error" showIcon message={trendError} />
          ) : (
            <Spin spinning={trendLoading}>
              {chartData.length >= 2 ? (
                <>
                  <div style={{ marginBottom: 8, color: primaryPalette[700], fontSize: 13 }}>
                    累计扣费 {getMoney().format(totalCredits)}
                  </div>
                  <Line
                    data={chartData}
                    xField="date"
                    yField="credits"
                    height={260}
                    axis={{
                      x: {
                        // "YYYY-MM-DD" → "MM-DD"，避免横轴标签拥挤
                        labelFormatter: (v: string) => {
                          const parts = v.split('-');
                          return parts.length === 3 ? `${parts[1]}-${parts[2]}` : v;
                        },
                      },
                      y: {
                        labelFormatter: (v: number) => getMoney().format(v),
                      },
                    }}
                    style={{ stroke: primaryPalette[500], lineWidth: 2 }}
                    tooltip={{
                      items: [
                        {
                          channel: 'y' as const,
                          name: '扣费',
                          valueFormatter: (v: number) => getMoney().format(v),
                        },
                      ],
                    }}
                  />
                </>
              ) : (
                <Empty
                  description={
                    chartData.length === 1
                      ? '目前只有一天的数据，攒够两天才能看出趋势。'
                      : '近 30 天无调用记录。'
                  }
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  style={{
                    height: 260,
                    display: 'flex',
                    flexDirection: 'column',
                    justifyContent: 'center',
                  }}
                />
              )}
            </Spin>
          )}
        </div>

        <Title level={5} style={{ marginTop: 24 }}>
          最近调用（最多 {RECENT_LOG_PAGE_SIZE} 条）
        </Title>
        {logsError && (
          <Alert type="error" showIcon message={logsError} style={{ marginBottom: 12 }} />
        )}
        <Table
          dataSource={logs}
          columns={logColumns}
          rowKey="id"
          size="small"
          loading={logsLoading}
          pagination={false}
          locale={{ emptyText: '暂无调用记录' }}
          scroll={{ x: 480 }}
        />
      </div>
    </Drawer>
  );
}

export default UserConsumptionDrawer;
