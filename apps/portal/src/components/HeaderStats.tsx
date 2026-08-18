import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Skeleton, theme, Tooltip, Badge } from 'antd';
import {
  WalletOutlined,
  LineChartOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { balanceApi, usageApi } from '@/api/usage';
import { keysApi } from '@/api/keys';
import { useMoney } from '@/stores/site';
import { warmGray } from '@token-zen/shared/theme';
import { isLowBalance as isBalanceLow, useLowBalanceThreshold } from '@/stores/lowBalance';

function StatItem({
  icon,
  label,
  value,
  onClick,
  tooltip,
  danger,
}: {
  icon: React.ReactNode;
  label: string;
  value: string | null;
  onClick?: () => void;
  tooltip?: string;
  danger?: boolean;
}) {
  const { token } = theme.useToken();

  const content = (
    <div
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 6,
        cursor: onClick ? 'pointer' : 'default',
        padding: '4px 8px',
        borderRadius: 6,
        transition: 'background 0.2s',
      }}
      onMouseEnter={(e) => {
        if (onClick) e.currentTarget.style.background = token.colorFillTertiary;
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = 'transparent';
      }}
    >
      <span style={{ color: danger ? token.colorError : warmGray[400], fontSize: 14 }}>{icon}</span>
      <span style={{ color: warmGray[500], fontSize: 12 }}>{label}</span>
      {value === null ? (
        <Skeleton.Input active size="small" style={{ width: 48, minWidth: 48, height: 18 }} />
      ) : (
        <span
          style={{
            color: danger ? token.colorError : token.colorText,
            fontSize: 13,
            fontWeight: 600,
          }}
        >
          {value}
        </span>
      )}
    </div>
  );

  return tooltip ? <Tooltip title={tooltip}>{content}</Tooltip> : content;
}

function HeaderStats() {
  const navigate = useNavigate();
  const money = useMoney();
  const [balance, setBalance] = useState<number | null>(null);
  const [todayCredits, setTodayCredits] = useState<number | null>(null);
  const [keyCount, setKeyCount] = useState<number | null>(null);

  useEffect(() => {
    balanceApi.get()
      .then((bal) => setBalance(bal.credit_balance))
      .catch(() => setBalance(0));

    keysApi.list({ page: 1, page_size: 1 })
      .then((result) => setKeyCount(result.total ?? 0))
      .catch(() => setKeyCount(0));

    usageApi.daily({ days: 1 })
      .then((rows) => {
        const today = dayjs().format('YYYY-MM-DD');
        const last = rows[rows.length - 1];
        const credits = last && dayjs(last.day).format('YYYY-MM-DD') === today ? last.credits_charged : 0;
        setTodayCredits(credits);
      })
      .catch(() => setTodayCredits(0));
  }, []);

  const lowBalanceThreshold = useLowBalanceThreshold();
  const isLowBalance = useMemo(
    () => isBalanceLow(balance, lowBalanceThreshold),
    [balance, lowBalanceThreshold],
  );
  const balanceText = balance !== null ? money.format(balance) : null;
  const todayText = todayCredits !== null ? money.format(todayCredits) : null;
  const keyCountText = keyCount !== null ? `${keyCount} 个` : null;

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      <StatItem
        icon={isLowBalance ? <Badge dot offset={[-2, 2]}><WalletOutlined /></Badge> : <WalletOutlined />}
        label="余额"
        value={balanceText}
        onClick={() => navigate('/topup')}
        tooltip={isLowBalance ? '余额不足，建议尽快兑换' : '账户可用余额'}
        danger={isLowBalance}
      />
      <Divider />
      <StatItem
        icon={<LineChartOutlined />}
        label="今日消耗"
        value={todayText}
        onClick={() => navigate('/usage')}
        tooltip="今日消耗"
      />
      <Divider />
      <StatItem
        icon={<KeyOutlined />}
        label="密钥"
        value={keyCountText}
        onClick={() => navigate('/keys')}
        tooltip="API 密钥总数"
      />
    </div>
  );
}

function Divider() {
  return (
    <div
      style={{
        width: 1,
        height: 16,
        background: warmGray[200],
        margin: '0 2px',
      }}
    />
  );
}

export default HeaderStats;
