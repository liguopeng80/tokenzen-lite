import { useEffect, useState } from 'react';
import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { Layout, Menu, theme, Avatar, Dropdown, Modal, Form, Input, Button, type MenuProps } from 'antd';
import { message, modal } from '@token-zen/shared';
import {
  DashboardOutlined,
  UserOutlined,
  ApiOutlined,
  AppstoreOutlined,
  DollarOutlined,
  FileTextOutlined,
  SettingOutlined,
  LogoutOutlined,
  KeyOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  TeamOutlined,
  ProjectOutlined,
  BarChartOutlined,
  AuditOutlined,
  AlertOutlined,
  DeploymentUnitOutlined,
  LineChartOutlined,
  DesktopOutlined,
} from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { useSiteStore } from '@/stores/site';
import { authApi } from '@/api/auth';
import { useUnsavedChangesStore } from '@/stores/unsavedChanges';
import { adminSidebarStyles, warmGray, primaryPalette } from '@token-zen/shared/theme';
import HeaderStats from '@/components/HeaderStats';
import { RequireAuth } from '@/components/RequireAuth';

const { Header, Sider, Content } = Layout;

/**
 * 外链菜单项 key 集合：点击这些 key 时在新窗口打开（用于全屏大屏），不走 SPA navigate，
 * 也不走未保存改动确认——开新窗口不离开当前页，未保存状态不受影响。
 */
const EXTERNAL_MENU_KEYS = new Set<string>(['/monitor/analytics', '/monitor/operations']);

const menuItems = [
  { key: '/dashboard', icon: <DashboardOutlined />, label: '仪表盘' },
  { key: '/users', icon: <UserOutlined />, label: '用户管理' },
  { key: '/departments', icon: <TeamOutlined />, label: '部门管理' },
  { key: '/projects', icon: <ProjectOutlined />, label: '项目管理' },
  { key: '/integrations', icon: <DeploymentUnitOutlined />, label: '接入方' },
  { key: '/channels', icon: <ApiOutlined />, label: '渠道管理' },
  { key: '/models', icon: <AppstoreOutlined />, label: '模型管理' },
  { key: '/billing', icon: <DollarOutlined />, label: '计费管理' },
  {
    key: 'analytics-reports',
    icon: <BarChartOutlined />,
    label: '统计分析',
    children: [
      { key: '/reports', icon: <BarChartOutlined />, label: '费用报表' },
      { key: '/analytics', icon: <LineChartOutlined />, label: '运维分析' },
      // 全屏大屏：external 项由 handleMenuClick 走 window.open，跳出常规布局。
      { key: '/monitor/analytics', icon: <DesktopOutlined />, label: '运营分析大屏' },
      { key: '/monitor/operations', icon: <DesktopOutlined />, label: '运维大屏' },
    ],
  },
  {
    key: 'logs-audit',
    icon: <FileTextOutlined />,
    label: '日志审计',
    children: [
      { key: '/logs', icon: <FileTextOutlined />, label: '用量日志' },
      { key: '/audit', icon: <AuditOutlined />, label: '操作审计' },
      { key: '/alerts', icon: <AlertOutlined />, label: '告警记录' },
    ],
  },
  { key: '/settings', icon: <SettingOutlined />, label: '系统设置' },
];

/**
 * 路径 → 所属 SubMenu key。深链直达子项时据此自动展开父菜单。
 * 顶级路由不在表中，不触发展开。
 */
const PATH_TO_SUBMENU: Record<string, string> = {
  '/reports': 'analytics-reports',
  '/analytics': 'analytics-reports',
  '/monitor/analytics': 'analytics-reports',
  '/monitor/operations': 'analytics-reports',
  '/logs': 'logs-audit',
  '/audit': 'logs-audit',
  '/alerts': 'logs-audit',
};

function AdminLayout() {
  const navigate = useNavigate();
  const unsavedCount = useUnsavedChangesStore((s) => s.count);
  const location = useLocation();
  const { token } = theme.useToken();
  const { user, logout } = useAuthStore();
  const fetchSiteConfig = useSiteStore((s) => s.fetchConfig);
  const [collapsed, setCollapsed] = useState(false);
  const [openKeys, setOpenKeys] = useState<string[]>([]);
  const [pwdModalOpen, setPwdModalOpen] = useState(false);
  const [pwdLoading, setPwdLoading] = useState(false);
  const [pwdForm] = Form.useForm();

  useEffect(() => {
    fetchSiteConfig();
  }, [fetchSiteConfig]);

  // 深链直达子项时自动展开所属 SubMenu（portal 端没做这步，admin 补上）。
  useEffect(() => {
    const parentKey = PATH_TO_SUBMENU[location.pathname];
    if (parentKey) {
      setOpenKeys((prev) => (prev.includes(parentKey) ? prev : [...prev, parentKey]));
    }
  }, [location.pathname]);

  const handleLogout = () => {
    logout();
    navigate('/login', { replace: true });
  };

  // 离开有未保存改动的页面前先确认：改动只存在浏览器内存里，切走即丢失。
  // 外链项（全屏大屏）例外：开新窗口不影响当前页未保存状态，故直接打开。
  const handleMenuClick = ({ key }: { key: string }) => {
    // 父项（SubMenu 标题）点击只展开/折叠，不 navigate、不走 window.open。
    const isParent = menuItems.some(
      (item) => 'children' in item && item.key === key,
    );
    if (isParent) return;
    if (EXTERNAL_MENU_KEYS.has(key)) {
      window.open(key, '_blank');
      return;
    }
    if (unsavedCount > 0) {
      modal.confirm({
        title: '有未保存的修改',
        content: `当前页面有 ${unsavedCount} 项修改尚未保存，离开后这些修改会丢失。`,
        okText: '放弃修改并离开',
        okButtonProps: { danger: true },
        cancelText: '留在本页',
        onOk: () => navigate(key),
      });
      return;
    }
    navigate(key);
  };

  const handleChangePassword = async () => {
    try {
      const values = await pwdForm.validateFields();
      setPwdLoading(true);
      await authApi.changePassword(values.original_password, values.new_password);
      message.success('密码已修改');
      setPwdModalOpen(false);
      pwdForm.resetFields();
    } catch {
      // validation or API error
    } finally {
      setPwdLoading(false);
    }
  };

  const dropdownItems: MenuProps['items'] = [
    {
      key: 'user-info',
      label: user?.display_name || user?.username || '管理员',
      disabled: true,
    },
    { type: 'divider' },
    {
      key: 'change-password',
      icon: <KeyOutlined />,
      label: '修改密码',
      onClick: () => setPwdModalOpen(true),
    },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出登录',
      onClick: handleLogout,
    },
  ];

  return (
    <RequireAuth>
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        theme="dark"
        width={240}
        collapsedWidth={64}
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        trigger={null}
        style={{
          background: adminSidebarStyles.background,
          borderRight: 'none',
          display: 'flex',
          flexDirection: 'column',
          height: '100vh',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
        }}
      >
        <div
          style={{
            height: 64,
            flexShrink: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '12px 12px 8px',
            borderRadius: 8,
          }}
        >
          {collapsed ? (
            <span style={{ fontWeight: 800, fontSize: 18, color: primaryPalette[400] }}>
              TZ
            </span>
          ) : (
            <>
              <span style={{ fontWeight: 800, fontSize: 20, color: 'rgba(255,255,255,0.9)', letterSpacing: '-0.02em' }}>
                Token
              </span>
              <span style={{ fontWeight: 800, fontSize: 20, color: primaryPalette[400], letterSpacing: '-0.02em' }}>
                Zen
              </span>
              <span
                style={{
                  fontSize: 10,
                  color: warmGray[500],
                  fontWeight: 500,
                  marginLeft: 6,
                  letterSpacing: '1px',
                  textTransform: 'uppercase' as const,
                  borderLeft: `1px solid ${warmGray[700]}`,
                  paddingLeft: 6,
                }}
              >
                Admin
              </span>
            </>
          )}
        </div>
        <div
          style={{ flex: 1, minHeight: 0, overflow: 'auto' }}
          data-testid="admin-sider-menu-scroll"
        >
          <Menu
            mode="inline"
            theme="dark"
            selectedKeys={[location.pathname]}
            openKeys={openKeys}
            onOpenChange={setOpenKeys}
            items={menuItems}
            onClick={handleMenuClick}
            data-testid="admin-sider-menu"
            style={{
              background: 'transparent',
              borderRight: 'none',
              marginTop: 4,
            }}
          />
        </div>
        <div
          style={{
            flexShrink: 0,
            textAlign: 'center',
            padding: '8px 0',
            borderTop: '1px solid rgba(255,255,255,0.06)',
          }}
        >
          <Button
            type="text"
            icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            onClick={() => setCollapsed(!collapsed)}
            style={{ color: warmGray[500], width: '100%' }}
          />
        </div>
      </Sider>
      <Layout style={{ marginLeft: collapsed ? 64 : 240, transition: 'margin-left 0.2s' }}>
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
                {user?.display_name || user?.username || '管理员'}
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
      <Modal
        title="修改密码"
        open={pwdModalOpen}
        onOk={handleChangePassword}
        onCancel={() => { setPwdModalOpen(false); pwdForm.resetFields(); }}
        confirmLoading={pwdLoading}
      >
        <Form form={pwdForm} layout="vertical">
          <Form.Item
            name="original_password"
            label="当前密码"
            rules={[
              { required: true, message: '请输入当前密码' },
            ]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '密码至少 8 位' },
            ]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item
            name="confirm_password"
            label="确认新密码"
            dependencies={['new_password']}
            rules={[
              { required: true, message: '请确认新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('new_password') === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error('两次密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password />
          </Form.Item>
        </Form>
      </Modal>
    </Layout>
    </RequireAuth>
  );
}

export default AdminLayout;
