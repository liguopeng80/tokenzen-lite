import { useEffect, useState, useRef } from 'react';
import { Outlet, useNavigate, useLocation, Navigate } from 'react-router-dom';
import { Layout, Menu, theme, Avatar, Dropdown, Spin, Button, type MenuProps } from 'antd';
import { message } from '@token-zen/shared';
import {
  DashboardOutlined,
  KeyOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  DollarOutlined,
  BookOutlined,
  LaptopOutlined,
  SettingOutlined,
  UserOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  HeartOutlined,
  TeamOutlined,
  ReadOutlined,
} from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { portalSidebarStyles, warmGray, primaryPalette } from '@token-zen/shared/theme';
import { ForcePasswordChange } from '@token-zen/shared/components/ForcePasswordChange';
import { authApi } from '@/api/auth';
import HeaderStats from '@/components/HeaderStats';

const { Header, Sider, Content } = Layout;

const menuItems = [
  { key: '/dashboard', icon: <DashboardOutlined />, label: '控制台' },
  { key: '/keys', icon: <KeyOutlined />, label: 'API 密钥' },
  { key: '/models', icon: <AppstoreOutlined />, label: '可用模型' },
  { key: '/usage', icon: <BarChartOutlined />, label: '用量明细' },
  // 部门费用仅对部门负责人显示，插入位置见下方 buildMenuItems。
  { key: '/topup', icon: <DollarOutlined />, label: '兑换' },
  { key: '/quickstart', icon: <BookOutlined />, label: '快速开始' },
  {
    key: '/client-setup',
    icon: <LaptopOutlined />,
    label: '客户端设置',
    children: [
      { key: '/client-setup/claude-code', label: 'Claude Code' },
      { key: '/client-setup/codex-cli', label: 'Codex CLI' },
      { key: '/client-setup/opencode', label: 'OpenCode' },
    ],
  },
  {
    key: '/reference',
    icon: <ReadOutlined />,
    label: '参考资料',
    children: [
      { key: '/reference/integration', label: '接入指南' },
      { key: '/reference/error-codes', label: '错误码' },
    ],
  },
  { key: '/settings', icon: <SettingOutlined />, label: '个人设置' },
  { key: '/status', icon: <HeartOutlined />, label: '服务状态' },
];

const departmentMenuItem = {
  key: '/department',
  icon: <TeamOutlined />,
  label: '部门费用',
};

/**
 * 部门费用入口只对部门负责人显示。判定依据是 /auth/me 返回的负责部门列表，
 * 而非用户角色——负责人身份来自部门归属，普通角色的成员同样可以是负责人。
 * 后端对该组端点逐次校验归属，隐藏入口只是减少无效点击，不构成访问控制。
 */
function buildMenuItems(managesDepartment: boolean) {
  if (!managesDepartment) return menuItems;
  const at = menuItems.findIndex((item) => item.key === '/usage');
  return [
    ...menuItems.slice(0, at + 1),
    departmentMenuItem,
    ...menuItems.slice(at + 1),
  ];
}

function PortalLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { token } = theme.useToken();
  const { user, isLoggedIn, logout, fetchUser } = useAuthStore();
  const [authChecked, setAuthChecked] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [openKeys, setOpenKeys] = useState<string[]>([]);
  const wasLoggedIn = useRef(isLoggedIn());

  useEffect(() => {
    if (!isLoggedIn()) {
      setAuthChecked(true);
      return;
    }
    fetchUser()
      .then(() => {
        // fetchUser succeeded, user is valid
      })
      .catch(() => {
        // fetchUser failed (401) — session expired
        if (wasLoggedIn.current) {
          message.warning('登录已过期，请重新登录');
        }
      })
      .finally(() => setAuthChecked(true));
  }, [isLoggedIn, fetchUser]);

  // Keep client-setup sub-menu always expanded
  // (no auto-close effect needed)

  if (!authChecked) {
    return (
      <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Spin size="large" />
      </div>
    );
  }

  if (!isLoggedIn() || !user) {
    return <Navigate to="/login" replace />;
  }

  const handleLogout = () => {
    logout();
    navigate('/login', { replace: true });
  };

  // 未完成首次改密时后端对业务接口一律 403，此处不渲染菜单与页面，
  // 直接给出改密表单，改完刷新登录用户后恢复正常界面。
  if (user.must_change_password) {
    return (
      <ForcePasswordChange
        username={user.username}
        onSubmit={async (originalPassword, password) => {
          await authApi.changePassword({
            original_password: originalPassword,
            password,
          });
          await fetchUser();
        }}
        onLogout={handleLogout}
      />
    );
  }

  const visibleMenuItems = buildMenuItems(
    (user.managed_department_ids?.length ?? 0) > 0,
  );

  const handleMenuClick = ({ key }: { key: string }) => {
    // Skip navigation for parent menu items (sub-menu titles)
    const isParent = visibleMenuItems.some(
      (item) => 'children' in item && item.key === key
    );
    if (!isParent) {
      navigate(key);
    }
  };

  const dropdownItems: MenuProps['items'] = [
    {
      key: 'user-info',
      label: user?.display_name || user?.username || '用户',
      disabled: true,
    },
    { type: 'divider' },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出登录',
      onClick: handleLogout,
    },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        theme="light"
        width={220}
        collapsedWidth={64}
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        trigger={null}
        style={{
          background: portalSidebarStyles.background,
          borderRight: `1px solid ${warmGray[100]}`,
          overflow: 'auto',
          height: '100vh',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
        }}
      >
        <div
          style={{
            height: 56,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '12px 12px 8px',
            borderRadius: 8,
          }}
        >
          {collapsed ? (
            <span style={{ fontWeight: 800, fontSize: 16, color: primaryPalette[500] }}>
              TZ
            </span>
          ) : (
            <>
              <span style={{ fontWeight: 800, fontSize: 18, color: warmGray[900], letterSpacing: '-0.02em' }}>
                Token
              </span>
              <span style={{ fontWeight: 800, fontSize: 18, color: primaryPalette[500], letterSpacing: '-0.02em' }}>
                Zen
              </span>
              <span
                style={{
                  fontSize: 10,
                  color: warmGray[400],
                  fontWeight: 500,
                  marginLeft: 6,
                  letterSpacing: '1px',
                  textTransform: 'uppercase' as const,
                  borderLeft: `1px solid ${warmGray[200]}`,
                  paddingLeft: 6,
                }}
              >
                Portal
              </span>
            </>
          )}
        </div>
        <Menu
          mode="inline"
          selectedKeys={[location.pathname]}
          openKeys={openKeys}
          onOpenChange={setOpenKeys}
          items={visibleMenuItems}
          onClick={handleMenuClick}
          style={{ borderRight: 'none', marginTop: 4 }}
        />
        <div style={{ textAlign: 'center', padding: '12px 0' }}>
          <Button
            type="text"
            icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            onClick={() => setCollapsed(!collapsed)}
            style={{ color: warmGray[400] }}
          />
        </div>
      </Sider>
      <Layout style={{ marginLeft: collapsed ? 64 : 220, transition: 'margin-left 0.2s' }}>
        <Header
          style={{
            background: token.colorBgContainer,
            padding: '0 24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            borderBottom: `1px solid ${warmGray[100]}`,
            boxShadow: '0 1px 3px 0 rgba(0,0,0,0.03)',
            position: 'sticky',
            top: 0,
            zIndex: 10,
          }}
        >
          <HeaderStats />
          <Dropdown menu={{ items: dropdownItems }} placement="bottomRight">
            <div style={{ display: 'flex', alignItems: 'center', cursor: 'pointer', gap: 8 }}>
              <span style={{ color: warmGray[600], fontSize: 13 }}>
                {user?.display_name || user?.username || '用户'}
              </span>
              <Avatar
                icon={<UserOutlined />}
                style={{
                  backgroundColor: primaryPalette[500],
                  boxShadow: `0 2px 8px rgba(237, 123, 47, 0.3)`,
                }}
              />
            </div>
          </Dropdown>
        </Header>
        <Content style={{ margin: 24, minHeight: 'calc(100vh - 64px - 48px)' }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}

export default PortalLayout;
