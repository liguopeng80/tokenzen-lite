import { useCallback, useState } from 'react';
import { Card, Input, Button, Typography, Statistic, Table, Select, Space } from 'antd';
import { message } from '@token-zen/shared';
import { WalletOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { LedgerEntryType, MergedLedgerRow } from '@token-zen/shared';
import { useAuthStore } from '@/stores/auth';
import { useMoney } from '@/stores/site';
import { formatTime, LedgerEntryTypeLabel } from '@token-zen/shared';
import { useTable } from '@token-zen/shared/hooks';
import { ledgerApi, redeemApi } from '@/api/usage';

const { Title } = Typography;

/**
 * 类型筛选只给员工读得懂的四类。预扣与结算差额是同一次调用的内部记账动作，
 * 单独筛出来对员工没有意义，合并视角下由「调用扣费」一项代表。
 */
const ledgerFilterOptions: { label: string; value: LedgerEntryType }[] = [
  { label: '调用扣费', value: 'consume' },
  { label: LedgerEntryTypeLabel.grant, value: 'grant' },
  { label: LedgerEntryTypeLabel.redeem, value: 'redeem' },
  { label: LedgerEntryTypeLabel.revoke, value: 'revoke' },
];

function TopupPage() {
  const user = useAuthStore((s) => s.user);
  const refreshUser = useAuthStore((s) => s.fetchUser);
  const money = useMoney();
  const [code, setCode] = useState('');
  const [redeeming, setRedeeming] = useState(false);
  const [entryType, setEntryType] = useState<LedgerEntryType | undefined>();

  const fetchFn = useCallback(
    (params: Record<string, unknown>) =>
      ledgerApi.list(entryType ? { ...params, entry_type: entryType } : params),
    [entryType],
  );
  const ledgerTable = useTable<MergedLedgerRow>({
    fetchFn,
    defaultPageSize: 10,
    deps: [entryType],
  });

  const handleRedeem = async () => {
    if (!code.trim()) {
      message.warning('请输入兑换码');
      return;
    }
    setRedeeming(true);
    try {
      await redeemApi.redeem(code.trim());
      message.success('兑换成功');
      setCode('');
      ledgerTable.refresh();
      refreshUser();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '兑换失败，请检查兑换码是否有效');
    } finally {
      setRedeeming(false);
    }
  };

  const ledgerColumns: ColumnsType<MergedLedgerRow> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      render: (t: string) => formatTime(t),
      width: 170,
    },
    {
      title: '类型',
      dataIndex: 'entry_type',
      width: 120,
      render: (t: LedgerEntryType, row: MergedLedgerRow) =>
        row.request_id ? '调用扣费' : (LedgerEntryTypeLabel[t] ?? t),
    },
    {
      title: '金额变动',
      dataIndex: 'amount',
      render: (a: number) => (
        <span style={{ color: a >= 0 ? '#52c41a' : '#ff4d4f' }}>
          {a >= 0 ? '+' : ''}{money.formatDetail(a)}
        </span>
      ),
      align: 'right',
      width: 130,
    },
    {
      title: '变动后余额',
      dataIndex: 'balance_after',
      render: (v: number) => money.format(v),
      align: 'right',
      width: 130,
    },
    {
      title: '说明',
      key: 'detail',
      ellipsis: true,
      render: (_: unknown, row: MergedLedgerRow) =>
        row.request_id ? (
          <Typography.Text type="secondary">
            一次 API 调用，请求标识 {row.request_id}
          </Typography.Text>
        ) : (
          row.note || <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  /** 展开行：调用扣费由预扣与结算差额两笔构成，展开可核对这两笔。 */
  const renderEntries = (row: MergedLedgerRow) => (
    <Table
      size="small"
      rowKey="id"
      pagination={false}
      dataSource={row.entries}
      columns={[
        { title: '时间', dataIndex: 'created_at', width: 170, render: (t: string) => formatTime(t) },
        {
          title: '记账动作',
          dataIndex: 'entry_type',
          width: 120,
          render: (t: LedgerEntryType) => LedgerEntryTypeLabel[t] ?? t,
        },
        {
          title: '金额变动',
          dataIndex: 'amount',
          align: 'right' as const,
          width: 130,
          render: (a: number) => `${a >= 0 ? '+' : ''}${money.formatDetail(a)}`,
        },
        {
          title: '变动后余额',
          dataIndex: 'balance_after',
          align: 'right' as const,
          width: 130,
          render: (v: number) => money.format(v),
        },
        { title: '备注', dataIndex: 'note', ellipsis: true },
      ]}
    />
  );

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        兑换
      </Title>

      <Card style={{ marginBottom: 16 }}>
        <Statistic
          title="当前余额"
          value={money.format(user?.credit_balance ?? 0)}
          prefix={<WalletOutlined />}
          valueStyle={{ fontSize: 28 }}
        />
      </Card>

      <Card title="兑换码兑换" style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', gap: 12, maxWidth: 500 }}>
          <Input
            placeholder="请输入兑换码"
            style={{ flex: 1 }}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            onPressEnter={handleRedeem}
          />
          <Button type="primary" loading={redeeming} onClick={handleRedeem}>
            兑换
          </Button>
        </div>
      </Card>

      <Card
        title="账户流水"
        extra={
          <Space>
            <Select
              placeholder="全部类型"
              allowClear
              style={{ width: 140 }}
              value={entryType}
              onChange={setEntryType}
              options={ledgerFilterOptions}
            />
            <Button icon={<ReloadOutlined />} onClick={ledgerTable.refresh} size="small">
              刷新
            </Button>
          </Space>
        }
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 13, marginTop: -8 }}>
          每次 API 调用先按最大输出长度预扣一笔，结算时退回多扣的部分。这里显示的是相抵后的实际扣费，
          展开某一行可看到预扣与结算差额两笔记账。
        </Typography.Paragraph>
        <Table
          columns={ledgerColumns}
          dataSource={ledgerTable.dataSource}
          rowKey="id"
          loading={ledgerTable.loading}
          pagination={ledgerTable.pagination}
          expandable={{
            expandedRowRender: renderEntries,
            rowExpandable: (row) => row.entries.length > 1,
          }}
        />
      </Card>
    </div>
  );
}

export default TopupPage;
