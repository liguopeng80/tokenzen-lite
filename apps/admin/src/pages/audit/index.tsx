import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Card,
  DatePicker,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import { ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { Dayjs } from 'dayjs';
import type { AuditLog, AuditResult, AuditTargetType } from '@token-zen/shared';
import {
  AuditActionLabel,
  AuditResultLabel,
  AuditTargetTypeLabel,
  RoleLabel,
  formatTime,
} from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { auditApi } from '@/api/organization';

const { Title, Text, Paragraph } = Typography;
const { RangePicker } = DatePicker;

const resultOptions = [
  { label: AuditResultLabel.success, value: 'success' },
  { label: AuditResultLabel.failure, value: 'failure' },
];

const targetTypeOptions = (Object.keys(AuditTargetTypeLabel) as AuditTargetType[]).map((key) => ({
  label: AuditTargetTypeLabel[key],
  value: key,
}));

/** actionLabel 未收录的动作按原始取值显示，不隐藏记录。 */
function actionLabel(action: string): string {
  return AuditActionLabel[action] ?? action;
}

function AuditPage() {
  const [keyword, setKeyword] = useState('');
  const [action, setAction] = useState<string>('');
  const [targetType, setTargetType] = useState<string>('');
  const [result, setResult] = useState<AuditResult | ''>('');
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [actions, setActions] = useState<string[]>([]);
  const [detail, setDetail] = useState<AuditLog | null>(null);

  useEffect(() => {
    auditApi.actions().then(setActions).catch(() => setActions([]));
  }, []);

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      auditApi.list({
        ...params,
        ...(keyword ? { keyword } : {}),
        ...(action ? { action } : {}),
        ...(targetType ? { target_type: targetType } : {}),
        ...(result ? { result } : {}),
        ...(range ? { start_timestamp: range[0].unix(), end_timestamp: range[1].unix() } : {}),
      }),
    [keyword, action, targetType, result, range],
  );

  const { dataSource, loading, pagination, refresh } = useTable<AuditLog>({
    fetchFn,
    deps: [keyword, action, targetType, result, range],
  });

  const columns: ColumnsType<AuditLog> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (v: string) => formatTime(v),
    },
    {
      title: '操作人',
      key: 'operator',
      width: 160,
      render: (_, record) =>
        record.operator_id === 0 ? (
          <Tag>系统</Tag>
        ) : (
          <span>
            {record.operator_name}
            <Text type="secondary" style={{ marginLeft: 6 }}>
              {RoleLabel[record.operator_role] ?? record.operator_role}
            </Text>
          </span>
        ),
    },
    {
      title: '动作',
      dataIndex: 'action',
      key: 'action',
      width: 160,
      render: (v: string) => actionLabel(v),
    },
    {
      title: '对象',
      key: 'target',
      render: (_, record) => {
        const typeLabel = AuditTargetTypeLabel[record.target_type] ?? record.target_type;
        if (!record.target_name && record.target_id === 0) {
          return <Text type="secondary">{typeLabel}</Text>;
        }
        return (
          <span>
            {typeLabel}
            <Text style={{ marginLeft: 6 }}>{record.target_name || `#${record.target_id}`}</Text>
          </span>
        );
      },
    },
    {
      title: '结果',
      dataIndex: 'result',
      key: 'result',
      width: 90,
      render: (v: AuditResult) => (
        <Tag color={v === 'success' ? 'green' : 'red'}>{AuditResultLabel[v]}</Tag>
      ),
    },
    { title: '来源 IP', dataIndex: 'client_ip', key: 'client_ip', width: 140 },
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
          操作审计
        </Title>
        <Button icon={<ReloadOutlined />} onClick={refresh}>
          刷新
        </Button>
      </div>

      <Paragraph type="secondary" style={{ marginBottom: 16 }}>
        记录管理侧的写操作与全部账号的认证事件，只追加不可修改。密码、上游渠道密钥、SMTP 密码等敏感字段
        只记录「已变更」，不记录任何取值。保留期由系统设置中的审计保留天数控制。
      </Paragraph>

      <Card>
        <Space style={{ marginBottom: 16 }} wrap>
          <Input.Search
            placeholder="搜索操作人、对象或说明"
            allowClear
            style={{ width: 240 }}
            prefix={<SearchOutlined />}
            onSearch={setKeyword}
          />
          <Select
            placeholder="全部动作"
            allowClear
            showSearch
            style={{ width: 200 }}
            value={action || undefined}
            onChange={(v) => setAction(v ?? '')}
            options={actions.map((a) => ({ label: actionLabel(a), value: a }))}
            filterOption={(input, option) =>
              String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
            }
          />
          <Select
            placeholder="全部对象类型"
            allowClear
            style={{ width: 150 }}
            value={targetType || undefined}
            onChange={(v) => setTargetType(v ?? '')}
            options={targetTypeOptions}
          />
          <Select
            placeholder="全部结果"
            allowClear
            style={{ width: 120 }}
            value={result || undefined}
            onChange={(v) => setResult((v as AuditResult) ?? '')}
            options={resultOptions}
          />
          <RangePicker
            showTime
            onChange={(values) =>
              setRange(values && values[0] && values[1] ? [values[0], values[1]] : null)
            }
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
                description="所选条件下没有审计记录"
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            ),
          }}
        />
      </Card>

      <Drawer
        title="审计记录详情"
        open={detail !== null}
        width={620}
        onClose={() => setDetail(null)}
      >
        {detail && (
          <>
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="时间">{formatTime(detail.created_at)}</Descriptions.Item>
              <Descriptions.Item label="操作人">
                {detail.operator_id === 0
                  ? '系统'
                  : `${detail.operator_name}（${RoleLabel[detail.operator_role] ?? detail.operator_role}）`}
              </Descriptions.Item>
              <Descriptions.Item label="动作">{actionLabel(detail.action)}</Descriptions.Item>
              <Descriptions.Item label="对象">
                {(AuditTargetTypeLabel[detail.target_type] ?? detail.target_type) +
                  (detail.target_name ? ` · ${detail.target_name}` : '') +
                  (detail.target_id ? ` (#${detail.target_id})` : '')}
              </Descriptions.Item>
              <Descriptions.Item label="结果">
                <Tag color={detail.result === 'success' ? 'green' : 'red'}>
                  {AuditResultLabel[detail.result]}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="来源 IP">{detail.client_ip || '—'}</Descriptions.Item>
              <Descriptions.Item label="请求标识">{detail.request_id || '—'}</Descriptions.Item>
              <Descriptions.Item label="说明">{detail.message || '—'}</Descriptions.Item>
            </Descriptions>

            <Title level={5} style={{ marginTop: 24 }}>
              变更前
            </Title>
            <StatePreview state={detail.before_state} emptyText="本次操作无变更前状态（新建或纯动作）" />

            <Title level={5} style={{ marginTop: 16 }}>
              变更后
            </Title>
            <StatePreview state={detail.after_state} emptyText="本次操作无变更后状态（删除或查询类动作）" />
          </>
        )}
      </Drawer>
    </div>
  );
}

function StatePreview({
  state,
  emptyText,
}: {
  state: Record<string, unknown> | null;
  emptyText: string;
}) {
  if (!state || Object.keys(state).length === 0) {
    return <Text type="secondary">{emptyText}</Text>;
  }
  return (
    <pre
      style={{
        background: 'rgba(0,0,0,0.03)',
        padding: 12,
        borderRadius: 6,
        maxHeight: 260,
        overflow: 'auto',
        margin: 0,
      }}
    >
      {JSON.stringify(state, null, 2)}
    </pre>
  );
}

export default AuditPage;
