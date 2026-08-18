import { useCallback, useState } from 'react';
import { Alert, Button, Card, Descriptions, Drawer, Empty, Select, Space, Table, Tag, Typography } from 'antd';
import { message } from '@token-zen/shared';
import { ReloadOutlined, SendOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { AlertEvent, AlertSeverity, AlertStatus } from '@token-zen/shared';
import {
  AlertSeverityLabel,
  AlertStatusLabel,
  AlertTypeLabel,
  formatTime,
} from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { alertApi } from '@/api/organization';
import { errorMessageOf } from '@/api/error';

const { Title, Text, Paragraph } = Typography;

const severityOptions = (Object.keys(AlertSeverityLabel) as AlertSeverity[]).map((key) => ({
  label: AlertSeverityLabel[key],
  value: key,
}));

const statusOptions = (Object.keys(AlertStatusLabel) as AlertStatus[]).map((key) => ({
  label: AlertStatusLabel[key],
  value: key,
}));

const typeOptions = Object.keys(AlertTypeLabel).map((key) => ({
  label: AlertTypeLabel[key],
  value: key,
}));

const statusColor: Record<AlertStatus, string> = {
  pending: 'processing',
  sent: 'green',
  failed: 'red',
  suppressed: 'default',
  dead_letter: 'error',
};

function AlertsPage() {
  const [alertType, setAlertType] = useState('');
  const [severity, setSeverity] = useState<AlertSeverity | ''>('');
  const [status, setStatus] = useState<AlertStatus | ''>('');
  const [detail, setDetail] = useState<AlertEvent | null>(null);
  const [testing, setTesting] = useState(false);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      alertApi.list({
        ...params,
        ...(alertType ? { alert_type: alertType } : {}),
        ...(severity ? { severity } : {}),
        ...(status ? { status } : {}),
      }),
    [alertType, severity, status],
  );

  const { dataSource, loading, pagination, refresh } = useTable<AlertEvent>({
    fetchFn,
    deps: [alertType, severity, status],
  });

  const handleTest = async () => {
    setTesting(true);
    try {
      await alertApi.test();
      message.success('测试消息已发送，请到接收端确认');
      refresh();
    } catch (err) {
      message.error(errorMessageOf(err, '发送测试消息失败'));
    } finally {
      setTesting(false);
    }
  };

  const columns: ColumnsType<AlertEvent> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (v: string) => formatTime(v),
    },
    {
      title: '类型',
      dataIndex: 'alert_type',
      key: 'alert_type',
      width: 160,
      render: (v: string) => AlertTypeLabel[v] ?? v,
    },
    {
      title: '严重度',
      dataIndex: 'severity',
      key: 'severity',
      width: 90,
      render: (v: AlertSeverity) => (
        <Tag color={v === 'critical' ? 'red' : 'orange'}>{AlertSeverityLabel[v]}</Tag>
      ),
    },
    { title: '标题', dataIndex: 'title', key: 'title', ellipsis: true },
    {
      title: '投递状态',
      dataIndex: 'status',
      key: 'status',
      width: 130,
      render: (v: AlertStatus) => <Tag color={statusColor[v]}>{AlertStatusLabel[v]}</Tag>,
    },
    {
      title: '操作',
      key: 'detail',
      width: 90,
      render: (_, record) => (
        <Button size="small" onClick={() => setDetail(record)}>
          详情
        </Button>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>
          告警记录
        </Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
          <Button type="primary" icon={<SendOutlined />} loading={testing} onClick={handleTest}>
            发送测试消息
          </Button>
        </Space>
      </div>

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="告警通道在「系统设置」中配置"
        description="Webhook 地址与 SMTP 参数配置完成后，可用本页右上角的「发送测试消息」验证是否真的能送达。事件先落库再投递，因此收不到告警时可在此区分「未触发」与「触发了但投递失败」。"
      />

      <Card>
        <Space style={{ marginBottom: 16 }} wrap>
          <Select
            placeholder="全部类型"
            allowClear
            style={{ width: 180 }}
            value={alertType || undefined}
            onChange={(v) => setAlertType(v ?? '')}
            options={typeOptions}
          />
          <Select
            placeholder="全部严重度"
            allowClear
            style={{ width: 130 }}
            value={severity || undefined}
            onChange={(v) => setSeverity((v as AlertSeverity) ?? '')}
            options={severityOptions}
          />
          <Select
            placeholder="全部投递状态"
            allowClear
            style={{ width: 160 }}
            value={status || undefined}
            onChange={(v) => setStatus((v as AlertStatus) ?? '')}
            options={statusOptions}
          />
        </Space>

        <Table
          rowKey="id"
          columns={columns}
          dataSource={dataSource}
          loading={loading}
          pagination={pagination}
          locale={{
            emptyText: (
              <Empty
                description="暂无告警记录。系统在渠道自动禁用、对账未通过、用量日志丢弃等情形下会自动产生记录。"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
        />
      </Card>

      <Drawer title="告警详情" open={detail !== null} width={600} onClose={() => setDetail(null)}>
        {detail && (
          <>
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="时间">{formatTime(detail.created_at)}</Descriptions.Item>
              <Descriptions.Item label="类型">
                {AlertTypeLabel[detail.alert_type] ?? detail.alert_type}
              </Descriptions.Item>
              <Descriptions.Item label="严重度">
                <Tag color={detail.severity === 'critical' ? 'red' : 'orange'}>
                  {AlertSeverityLabel[detail.severity]}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="投递状态">
                <Tag color={statusColor[detail.status]}>{AlertStatusLabel[detail.status]}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="送达时间">
                {detail.sent_at ? formatTime(detail.sent_at) : '—'}
              </Descriptions.Item>
              <Descriptions.Item label="成功通道">
                {detail.channels_sent?.channels?.join('、') || '—'}
              </Descriptions.Item>
              <Descriptions.Item label="去重键">{detail.dedup_key || '—'}</Descriptions.Item>
              <Descriptions.Item label="最近错误">{detail.last_error || '—'}</Descriptions.Item>
            </Descriptions>

            <Title level={5} style={{ marginTop: 24 }}>
              正文
            </Title>
            <Paragraph>{detail.message || <Text type="secondary">无</Text>}</Paragraph>

            {detail.payload && (
              <>
                <Title level={5}>明细</Title>
                <pre
                  style={{
                    background: 'rgba(0,0,0,0.03)',
                    padding: 12,
                    borderRadius: 6,
                    maxHeight: 240,
                    overflow: 'auto',
                    margin: 0,
                  }}
                >
                  {JSON.stringify(detail.payload, null, 2)}
                </pre>
              </>
            )}
          </>
        )}
      </Drawer>
    </div>
  );
}

export default AlertsPage;
