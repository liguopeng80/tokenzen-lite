import { useEffect, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, ConfigProvider, Space, Switch, Tooltip, Typography, theme } from 'antd';
import {
  ReloadOutlined,
  RollbackOutlined,
  PoweroffOutlined,
} from '@ant-design/icons';

const { Title } = Typography;

export interface MonitorLayoutProps {
  /** 顶部条左侧标题。 */
  title: string;
  children: ReactNode;
  /** 立即刷新回调；不传时隐藏刷新按钮与自动刷新开关。 */
  onRefresh?: () => void;
  /** 刷新中状态，控制刷新按钮 loading。 */
  refreshing?: boolean;
  /** 自动刷新间隔毫秒，默认 60000。 */
  refreshIntervalMs?: number;
  /** 受控自动刷新开关。 */
  autoRefresh?: boolean;
  /** 自动刷新开关变化回调。 */
  onAutoRefreshChange?: (v: boolean) => void;
}

/**
 * 全屏深色大屏外壳，供运营分析 / 运维大屏复用。
 *
 * 仅提供顶部条（标题、自动刷新开关、立即刷新、退出）与深色背景容器，不嵌入业务布局
 * （无侧栏、无 AdminLayout Header）。轮询由本组件按 refreshIntervalMs 触发：仅在
 * autoRefresh=true 且提供 onRefresh 时启动 interval，父组件掌控开关状态。
 *
 * 退出按钮优先 navigate 回 /dashboard；window.close 仅对脚本打开的窗口有效，
 * 而用户从地址栏进入大屏时 window.close 会被浏览器忽略，因此回 dashboard 更可靠。
 */
export function MonitorLayout({
  title,
  children,
  onRefresh,
  refreshing = false,
  refreshIntervalMs = 60000,
  autoRefresh = false,
  onAutoRefreshChange,
}: MonitorLayoutProps) {
  const navigate = useNavigate();

  useEffect(() => {
    if (!autoRefresh || !onRefresh) return;
    const id = window.setInterval(onRefresh, refreshIntervalMs);
    return () => window.clearInterval(id);
  }, [autoRefresh, onRefresh, refreshIntervalMs]);

  const handleExit = () => navigate('/dashboard');

  return (
    <ConfigProvider
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorBgContainer: '#141822',
          colorBgElevated: '#1a1f2b',
          colorBgLayout: '#0b0f17',
          colorTextBase: '#ffffff',
          colorBorder: 'rgba(255,255,255,0.12)',
          colorBorderSecondary: 'rgba(255,255,255,0.08)',
        },
      }}
    >
      <div
        data-testid="monitor-layout"
        style={{
          minHeight: '100vh',
          background: '#0b0f17',
          color: 'rgba(255,255,255,0.85)',
          padding: 20,
          display: 'flex',
          flexDirection: 'column',
          gap: 16,
        }}
      >
        <header
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '4px 8px',
          }}
        >
          <Title level={4} style={{ margin: 0, color: 'rgba(255,255,255,0.92)' }}>
            {title}
          </Title>
          <Space size="middle" align="center">
            {onRefresh && onAutoRefreshChange && (
              <Space size={6} data-testid="monitor-refresh-toggle">
                <Switch
                  size="small"
                  checked={autoRefresh}
                  onChange={onAutoRefreshChange}
                />
                <span style={{ fontSize: 13, color: 'rgba(255,255,255,0.65)' }}>
                  自动刷新
                </span>
              </Space>
            )}
            {onRefresh && (
              <Tooltip title="立即刷新">
                <Button
                  size="small"
                  ghost
                  icon={<ReloadOutlined spin={refreshing} />}
                  onClick={onRefresh}
                >
                  刷新
                </Button>
              </Tooltip>
            )}
            <Button
              size="small"
              ghost
              icon={<RollbackOutlined />}
              onClick={handleExit}
              data-testid="monitor-exit"
            >
              退出大屏
            </Button>
            <Tooltip title="关闭窗口（仅对弹窗方式打开有效）">
              <Button
                size="small"
                type="text"
                icon={<PoweroffOutlined />}
                onClick={() => window.close()}
                style={{ color: 'rgba(255,255,255,0.55)' }}
              />
            </Tooltip>
          </Space>
        </header>
        <div style={{ flex: 1, minHeight: 0 }}>{children}</div>
      </div>
    </ConfigProvider>
  );
}

export default MonitorLayout;
