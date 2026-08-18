import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Skeleton, theme, Tooltip } from 'antd';
import {
  DollarOutlined,
  ApiOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { dashboardApi } from '@/api/dashboard';
import { formatNumber } from '@token-zen/shared';
import { useMoney } from '@/stores/site';
import { warmGray } from '@token-zen/shared/theme';
import type { StatsOverview } from '@token-zen/shared';

function StatItem({
  icon,
  label,
  value,
  onClick,
  tooltip,
}: {
  icon: React.ReactNode;
  label: string;
  value: string | null;
  onClick?: () => void;
  tooltip?: string;
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
      <span style={{ color: warmGray[400], fontSize: 14 }}>{icon}</span>
      <span style={{ color: warmGray[500], fontSize: 12 }}>{label}</span>
      {value === null ? (
        <Skeleton.Input active size="small" style={{ width: 48, minWidth: 48, height: 18 }} />
      ) : (
        <span style={{ color: token.colorText, fontSize: 13, fontWeight: 600 }}>
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
  const [overview, setOverview] = useState<StatsOverview | null>(null);

  useEffect(() => {
    const fetchStats = async () => {
      try {
        const data = await dashboardApi.overview();
        setOverview(data);
      } catch {
        // keep previous value on transient failure
      }
    };
    fetchStats();

    const interval = setInterval(fetchStats, 60_000);
    return () => clearInterval(interval);
  }, []);

  const todayCreditsText = overview
    ? money.format(overview.credits_charged_today)
    : null;
  const channelText = overview
    ? `${overview.channels_enabled}/${overview.channels_enabled + overview.channels_disabled}`
    : null;
  const userText = overview !== null ? formatNumber(overview.total_users) : null;
  const requestText = overview ? `${formatNumber(overview.requests_today)} 次/日` : null;

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      <StatItem
        icon={<DollarOutlined />}
        label="今日收入"
        value={todayCreditsText}
        onClick={() => navigate('/dashboard')}
        tooltip="今日已结算收入（折合人民币）"
      />
      <Divider />
      <StatItem
        icon={<ApiOutlined />}
        label="活跃渠道"
        value={channelText}
        onClick={() => navigate('/channels')}
        tooltip="启用渠道数 / 总渠道数"
      />
      <Divider />
      <StatItem
        icon={<TeamOutlined />}
        label="用户"
        value={userText}
        onClick={() => navigate('/users')}
        tooltip="注册用户总数"
      />
      <Divider />
      <StatItem
        icon={<ThunderboltOutlined />}
        label="今日请求"
        value={requestText}
        tooltip="今日请求总数"
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
