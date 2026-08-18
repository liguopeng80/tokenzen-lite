/**
 * 服务状态页 —— /status
 *
 * 反映的是本站网关自己的可用性：上游通道是否还在服务、已上架模型是否都有渠道承载、
 * 近期调用的成功率与耗时。数据全部来自后端 `GET /api/me/service-status`。
 *
 * 早期版本展示的是 Anthropic 与 OpenAI 官方状态页的数据，由员工浏览器直连公网抓取。
 * 那与本站实际配置的上游无关——本站可能只接了其中一家，也可能接了官方状态页上没有的
 * 厂商；员工调用失败时来查，看到的是「所有系统正常运行」，排查方向被引偏。企业内网
 * 部署时员工浏览器也访问不了那两个域名，页面会长期空白。
 */
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Card, Col, Empty, Row, Space, Spin, Statistic, Table, Tag, Typography } from 'antd';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  ReloadOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { ProviderStatus, ServiceStatus, ServiceStatusLevel } from '@token-zen/shared';
import { ProviderLabel, formatNumber, formatTime, formatElapsedTime } from '@token-zen/shared';
import { serviceStatusApi } from '@/api/usage';

const { Title, Text, Paragraph } = Typography;

/** 自动刷新间隔。状态由后端按请求实时汇总，不需要更密的轮询。 */
const REFRESH_INTERVAL_MS = 60_000;

const LEVEL_CONFIG: Record<
  ServiceStatusLevel,
  { label: string; tagColor: string; icon: React.ReactNode; describe: string }
> = {
  operational: {
    label: '服务正常',
    tagColor: 'success',
    icon: <CheckCircleFilled />,
    describe: '全部已上架模型都有可用通道承载，近期调用未见异常。',
  },
  degraded: {
    label: '部分受影响',
    tagColor: 'warning',
    icon: <ExclamationCircleFilled />,
    describe: '有模型暂时不可用，或有通道被系统自动禁用，或近一小时的失败率偏高。',
  },
  outage: {
    label: '服务中断',
    tagColor: 'error',
    icon: <CloseCircleFilled />,
    describe: '当前没有可用通道，调用会全部失败。请联系管理员。',
  },
};

function LevelTag({ level }: { level: ServiceStatusLevel }) {
  const cfg = LEVEL_CONFIG[level] ?? LEVEL_CONFIG.degraded;
  return (
    <Tag icon={cfg.icon} color={cfg.tagColor} style={{ fontSize: 13, padding: '2px 12px' }}>
      {cfg.label}
    </Tag>
  );
}

const providerColumns: ColumnsType<ProviderStatus> = [
  {
    title: '上游厂商',
    dataIndex: 'provider',
    render: (p: string) => ProviderLabel[p as keyof typeof ProviderLabel] ?? p,
  },
  {
    title: '可用通道',
    key: 'enabled',
    align: 'right' as const,
    width: 120,
    render: (_, row) => `${row.enabled} / ${row.total}`,
  },
  {
    title: '自动禁用',
    dataIndex: 'auto_disabled',
    align: 'right' as const,
    width: 120,
    render: (n: number) =>
      n > 0 ? <Text type="danger">{n}</Text> : <Text type="secondary">0</Text>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 140,
    render: (level: ServiceStatusLevel) => <LevelTag level={level} />,
  },
];

function StatusPage() {
  const [status, setStatus] = useState<ServiceStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      setStatus(await serviceStatusApi.get());
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : '查询服务状态失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), REFRESH_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [load]);

  const cfg = status ? LEVEL_CONFIG[status.status] ?? LEVEL_CONFIG.degraded : null;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>
          服务状态
        </Title>
        <Button icon={<ReloadOutlined />} onClick={() => void load()} loading={loading}>
          刷新
        </Button>
      </div>

      <Paragraph type="secondary" style={{ marginBottom: 16 }}>
        这里反映的是本站网关的可用性，不是各家 AI 厂商的官方状态。调用报错时先看这一页：
        通道全部可用而这里显示正常，问题多半在客户端配置或本机网络。
      </Paragraph>

      {error && (
        <Alert type="error" showIcon style={{ marginBottom: 16 }} message="无法获取服务状态" description={error} />
      )}

      <Spin spinning={loading && !status}>
        {status && cfg && (
          <>
            <Card style={{ marginBottom: 16 }}>
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <Space size={12} align="center">
                  <LevelTag level={status.status} />
                  <Text type="secondary">检查时间 {formatTime(status.checked_at)}</Text>
                </Space>
                <Text>{cfg.describe}</Text>
                <Text type="secondary">
                  已上架模型 {status.models_total} 个，当前有通道承载 {status.models_available} 个。
                </Text>
                {status.unavailable_models.length > 0 && (
                  <Alert
                    type="warning"
                    showIcon
                    message="以下模型暂时不可用，调用会被拒绝"
                    description={
                      <Space size={4} wrap>
                        {status.unavailable_models.map((name) => (
                          <Tag key={name}>{name}</Tag>
                        ))}
                      </Space>
                    }
                  />
                )}
              </Space>
            </Card>

            <Row gutter={16} style={{ marginBottom: 16 }}>
              {[status.recent_hour, status.recent_day].map((win) => (
                <Col xs={24} md={12} key={win.window_minutes}>
                  <Card
                    title={win.window_minutes >= 24 * 60 ? '近 24 小时' : '近 1 小时'}
                    style={{ height: '100%' }}
                  >
                    {win.requests === 0 ? (
                      <Text type="secondary">该时段全站没有调用记录，无法判断成功率。</Text>
                    ) : (
                      <Row gutter={16}>
                        <Col span={8}>
                          <Statistic title="调用次数" value={formatNumber(win.requests)} />
                        </Col>
                        <Col span={8}>
                          <Statistic
                            title="失败占比"
                            value={`${win.failure_rate_percent}%`}
                            valueStyle={win.failure_rate_percent >= 20 ? { color: '#e34d59' } : undefined}
                          />
                        </Col>
                        <Col span={8}>
                          <Statistic title="耗时 P95" value={formatElapsedTime(win.p95_latency_ms)} />
                        </Col>
                      </Row>
                    )}
                  </Card>
                </Col>
              ))}
            </Row>

            <Card title="上游通道">
              <Paragraph type="secondary" style={{ fontSize: 13 }}>
                通道连续失败达到阈值会被系统自动禁用，之后由定时探测恢复。这里只显示数量，
                具体配置由管理员维护。
              </Paragraph>
              <Table
                rowKey="provider"
                size="small"
                pagination={false}
                columns={providerColumns}
                dataSource={status.providers}
                locale={{
                  emptyText: (
                    <Empty
                      description="尚未配置任何上游通道，调用会全部失败。请联系管理员。"
                      image={Empty.PRESENTED_IMAGE_SIMPLE}
                    />
                  ),
                }}
              />
            </Card>
          </>
        )}
      </Spin>
    </div>
  );
}

export default StatusPage;
