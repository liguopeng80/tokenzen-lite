import { useEffect, useState, useMemo } from 'react';
import { Card, Col, Row, Statistic, Typography, Alert, Button, Space, Spin, Empty, Table, Segmented } from 'antd';
import {
  WalletOutlined,
  KeyOutlined,
  ThunderboltOutlined,
  PlusOutlined,
  BookOutlined,
  RocketOutlined,
  CalendarOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { DualAxes } from '@ant-design/charts';
import { useNavigate } from 'react-router-dom';
import dayjs from 'dayjs';
import type { MeUsageLog, DailyStat } from '@token-zen/shared';
import { CalendarHeatmap } from '@token-zen/shared';
import { formatTime, formatNumber } from '@token-zen/shared';
import { balanceApi, usageApi } from '@/api/usage';
import { keysApi } from '@/api/keys';
import { brand, primaryPalette, semantic, warmGray } from '@token-zen/shared/theme';
import { isLowBalance, useLowBalanceThreshold } from '@/stores/lowBalance';
import { useMoney } from '@/stores/site';

const { Title } = Typography;

/** Colored circle icon wrapper for stat cards */
function StatIcon({ icon, bgColor }: { icon: React.ReactNode; bgColor: string }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 40,
        height: 40,
        borderRadius: 10,
        background: bgColor,
        fontSize: 20,
      }}
    >
      {icon}
    </span>
  );
}

function DashboardPage() {
  const navigate = useNavigate();
  const money = useMoney();

  const [balance, setBalance] = useState<number>(0);
  const [creditUsed, setCreditUsed] = useState<number>(0);
  const [keyCount, setKeyCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [dailyData, setDailyData] = useState<DailyStat[]>([]);
  const [chartLoading, setChartLoading] = useState(true);
  const [recentLogs, setRecentLogs] = useState<MeUsageLog[]>([]);
  const [recentLoading, setRecentLoading] = useState(true);
  const [chartDays, setChartDays] = useState<number>(7);
  const [calendarDays, setCalendarDays] = useState<number>(365);
  const [calendarData, setCalendarData] = useState<DailyStat[]>([]);
  const [calendarLoading, setCalendarLoading] = useState(true);
  const lowBalanceThreshold = useLowBalanceThreshold();

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      try {
        const [bal, keys] = await Promise.all([
          balanceApi.get(),
          keysApi.list({ page: 1, page_size: 1 }),
        ]);
        setBalance(bal.credit_balance);
        setCreditUsed(bal.credit_used);
        setKeyCount(keys.total ?? 0);
      } catch {
        // ignore
      } finally {
        setLoading(false);
      }
    };
    load();
  }, []);

  // 消费趋势
  useEffect(() => {
    const load = async () => {
      setChartLoading(true);
      try {
        const data = await usageApi.daily({ days: chartDays });
        setDailyData(data ?? []);
      } catch {
        setDailyData([]);
      } finally {
        setChartLoading(false);
      }
    };
    load();
  }, [chartDays]);

  // 最近调用记录
  useEffect(() => {
    const load = async () => {
      setRecentLoading(true);
      try {
        const result = await usageApi.logs({ page: 1, page_size: 5 });
        setRecentLogs(result?.items ?? []);
      } catch {
        setRecentLogs([]);
      } finally {
        setRecentLoading(false);
      }
    };
    load();
  }, []);

  // 活跃日历：复用 /me/usage-daily（已 rollup-safe，支持 365 天回看）。
  useEffect(() => {
    const load = async () => {
      setCalendarLoading(true);
      try {
        const data = await usageApi.daily({ days: calendarDays });
        setCalendarData(data ?? []);
      } catch {
        setCalendarData([]);
      } finally {
        setCalendarLoading(false);
      }
    };
    load();
  }, [calendarDays]);

  const todayCredits = useMemo(() => {
    if (dailyData.length === 0) return 0;
    const today = dayjs().format('YYYY-MM-DD');
    const last = dailyData[dailyData.length - 1];
    return dayjs(last.day).format('YYYY-MM-DD') === today ? last.credits_charged : 0;
  }, [dailyData]);

  const chartData = useMemo(
    () =>
      dailyData.map((d) => ({
        date: dayjs(d.day).format('YYYY-MM-DD'),
        credits: d.credits_charged,
        total_tokens: d.total_tokens,
      })),
    [dailyData],
  );

  const dualAxesConfig = {
    data: chartData,
    xField: 'date',
    axis: {
      x: {
        title: false as const,
        labelFormatter: (v: string) => {
          const parts = v.split('-');
          return parts.length === 3 ? `${parts[1]}-${parts[2]}` : v;
        },
      },
    },
    children: [
      {
        type: 'line' as const,
        yField: 'credits',
        // 悬浮提示显式命名，否则显示的是字段名 credits。
        tooltip: { items: [{ channel: 'y' as const, name: '消费' }] },
        style: { lineWidth: 2, stroke: primaryPalette[500] },
        axis: {
          y: {
            title: '消费',
            position: 'left' as const,
            style: { titleFill: primaryPalette[500] },
            labelFormatter: (v: number) => money.format(v),
          },
        },
      },
      {
        type: 'line' as const,
        yField: 'total_tokens',
        tooltip: { items: [{ channel: 'y' as const, name: 'Token 用量' }] },
        style: { lineWidth: 2, stroke: semantic.info },
        axis: {
          y: {
            title: 'Token 用量',
            position: 'right' as const,
            style: { titleFill: semantic.info },
            labelFormatter: (v: number) => formatNumber(v),
          },
        },
      },
    ],
    height: 200,
  };

  const recentColumns: ColumnsType<MeUsageLog> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      render: (t: string) => formatTime(t, 'MM-DD HH:mm'),
      width: 120,
    },
    { title: '模型', dataIndex: 'model_name', ellipsis: true },
    {
      title: '消费',
      dataIndex: 'credits_charged',
      render: (v: number) => money.formatDetail(v),
      align: 'right',
      width: 100,
    },
  ];

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        控制台
      </Title>

      {balance <= 0 ? (
        <Alert
          message="余额不足"
          description="您的账户余额已耗尽，API 调用将被拒绝。请尽快兑换。"
          type="error"
          showIcon
          action={
            <Button type="primary" onClick={() => navigate('/topup')}>
              立即兑换
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
      ) : isLowBalance(balance, lowBalanceThreshold) && (
        <Alert
          message="余额不足"
          description="您的账户余额即将耗尽，建议尽快兑换以避免 API 调用中断。"
          type="warning"
          showIcon
          action={
            <Button onClick={() => navigate('/topup')}>
              去兑换
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
      )}

      {!loading && !recentLoading && keyCount === 0 && recentLogs.length === 0 && (
        <Card style={{ marginBottom: 16, textAlign: 'center', padding: '24px 0' }}>
          <RocketOutlined style={{ fontSize: 40, color: brand.primary, marginBottom: 12 }} />
          <Title level={5} style={{ marginBottom: 8 }}>欢迎使用 Token Zen</Title>
          <p style={{ color: warmGray[500], marginBottom: 20 }}>只需创建 API 密钥并配置客户端，即可开始调用 AI 模型</p>
          <Space>
            <Button type="primary" icon={<BookOutlined />} onClick={() => navigate('/quickstart')}>
              快速开始
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => navigate('/keys')}>
              创建 API 密钥
            </Button>
          </Space>
        </Card>
      )}

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} style={{ height: '100%' }} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="当前余额"
              value={money.format(balance)}
              prefix={<StatIcon icon={<WalletOutlined style={{ color: brand.primary }} />} bgColor={primaryPalette[50]} />}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} style={{ height: '100%' }} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="累计消费"
              value={money.format(creditUsed)}
              prefix={<StatIcon icon={<WalletOutlined style={{ color: semantic.info }} />} bgColor={semantic.infoBg} />}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={chartLoading} style={{ height: '100%' }} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="今日消费"
              value={money.format(todayCredits)}
              prefix={<StatIcon icon={<ThunderboltOutlined style={{ color: brand.tertiary }} />} bgColor={semantic.warningBg} />}
            />
          </Card>
        </Col>
        <Col xs={24} sm={12} lg={6}>
          <Card loading={loading} style={{ height: '100%' }} styles={{ body: { padding: '20px 24px' } }}>
            <Statistic
              title="API 密钥数"
              value={keyCount}
              prefix={<StatIcon icon={<KeyOutlined style={{ color: semantic.success }} />} bgColor={semantic.successBg} />}
            />
          </Card>
        </Col>
      </Row>

      <Card
        style={{ marginTop: 16 }}
        title={<span><ThunderboltOutlined /> 近 {chartDays} 天消费趋势</span>}
        extra={
          <Segmented
            options={[
              { label: '7 天', value: 7 },
              { label: '30 天', value: 30 },
            ]}
            value={chartDays}
            onChange={(v) => setChartDays(v as number)}
            size="small"
          />
        }
      >
        <Spin spinning={chartLoading}>
          {/* 只有一天数据时折线退化成一个孤立数据点，读不出任何趋势。 */}
          {chartData.length >= 2 ? (
            <DualAxes {...dualAxesConfig} />
          ) : (
            <Empty
              description={
                chartData.length === 1
                  ? '目前只有一天的数据，攒够两天才能看出趋势。'
                  : '这段时间还没有调用记录。'
              }
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              style={{ height: 200, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}
            />
          )}
        </Spin>
      </Card>

      <Card
        style={{ marginTop: 16 }}
        title={<span><CalendarOutlined /> 活跃日历</span>}
        extra={
          <Segmented
            options={[
              { label: '近 90 天', value: 90 },
              { label: '近半年', value: 183 },
              { label: '近一年', value: 365 },
            ]}
            value={calendarDays}
            onChange={(v) => setCalendarDays(v as number)}
            size="small"
          />
        }
      >
        <Spin spinning={calendarLoading}>
          <CalendarHeatmap data={calendarData} formatMoney={money.format} />
        </Spin>
      </Card>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card title="最近调用记录">
            <Table
              columns={recentColumns}
              dataSource={recentLogs}
              rowKey="id"
              loading={recentLoading}
              pagination={false}
              size="small"
              locale={{ emptyText: '暂无调用记录' }}
            />
            {recentLogs.length > 0 && (
              <div style={{ textAlign: 'center', marginTop: 8 }}>
                <Button type="link" size="small" onClick={() => navigate('/usage')}>
                  查看全部
                </Button>
              </div>
            )}
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card title="快捷操作">
            <Space direction="vertical" style={{ width: '100%' }}>
              <Button
                type="primary"
                icon={<WalletOutlined />}
                onClick={() => navigate('/topup')}
                block
              >
                兑换
              </Button>
              <Button
                icon={<PlusOutlined />}
                onClick={() => navigate('/keys')}
                block
              >
                创建 API 密钥
              </Button>
              <Button
                icon={<BookOutlined />}
                onClick={() => navigate('/quickstart')}
                block
              >
                查看文档
              </Button>
            </Space>
          </Card>
        </Col>
      </Row>
    </div>
  );
}

export default DashboardPage;
