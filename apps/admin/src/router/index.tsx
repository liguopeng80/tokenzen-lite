import { Navigate, type RouteObject } from 'react-router-dom';
import AdminLayout from '@/layouts/AdminLayout';
import { RequireAuth } from '@/components/RequireAuth';
import LoginPage from '@/pages/login';
import DashboardPage from '@/pages/dashboard';
import UsersPage from '@/pages/users';
import ChannelsPage from '@/pages/channels';
import ModelsPage from '@/pages/models';
import BillingPage from '@/pages/billing';
import LogsPage from '@/pages/logs';
import DepartmentsPage from '@/pages/departments';
import ProjectsPage from '@/pages/projects';
import IntegrationsPage from '@/pages/integrations';
import ReportsPage from '@/pages/reports';
import AnalyticsPage from '@/pages/analytics';
import AuditPage from '@/pages/audit';
import AlertsPage from '@/pages/alerts';
import SettingsPage from '@/pages/settings';
import NotFoundPage from '@/pages/not-found';
import AnalyticsScreen from '@/pages/monitor/AnalyticsScreen';
import OpsScreen from '@/pages/monitor/OpsScreen';

export const routes: RouteObject[] = [
  {
    path: '/login',
    element: <LoginPage />,
  },
  // 全屏大屏路由：与 AdminLayout 同级，复用 RequireAuth 鉴权，不走侧栏/Header。
  {
    path: '/monitor/analytics',
    element: (
      <RequireAuth>
        <AnalyticsScreen />
      </RequireAuth>
    ),
  },
  {
    path: '/monitor/operations',
    element: (
      <RequireAuth>
        <OpsScreen />
      </RequireAuth>
    ),
  },
  {
    path: '/',
    element: <AdminLayout />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'users', element: <UsersPage /> },
      { path: 'channels', element: <ChannelsPage /> },
      { path: 'models', element: <ModelsPage /> },
      { path: 'billing', element: <BillingPage /> },
      { path: 'departments', element: <DepartmentsPage /> },
      { path: 'projects', element: <ProjectsPage /> },
      { path: 'integrations', element: <IntegrationsPage /> },
      { path: 'reports', element: <ReportsPage /> },
      { path: 'analytics', element: <AnalyticsPage /> },
      { path: 'logs', element: <LogsPage /> },
      { path: 'audit', element: <AuditPage /> },
      { path: 'alerts', element: <AlertsPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
];
