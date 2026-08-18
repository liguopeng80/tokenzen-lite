import { Navigate, type RouteObject } from 'react-router-dom';
import PortalLayout from '@/layouts/PortalLayout';
import LoginPage from '@/pages/login';
import StatusPage from '@/pages/status';
import DashboardPage from '@/pages/dashboard';
import KeysPage from '@/pages/keys';
import ModelsPage from '@/pages/models';
import UsagePage from '@/pages/usage';
import DepartmentPage from '@/pages/department';
import TopupPage from '@/pages/topup';
import QuickReferencePage from '@/pages/client-setup/QuickReferencePage';
import ClaudeCodePage from '@/pages/client-setup/ClaudeCodePage';
import CodexCliPage from '@/pages/client-setup/CodexCliPage';
import OpenCodePage from '@/pages/client-setup/OpenCodePage';
import IntegrationGuidePage from '@/pages/reference/IntegrationGuidePage';
import ErrorCodesPage from '@/pages/reference/ErrorCodesPage';
import SettingsPage from '@/pages/settings';
import NotFoundPage from '@/pages/not-found';

export const routes: RouteObject[] = [
  {
    path: '/login',
    element: <LoginPage />,
  },
  {
    path: '/',
    element: <PortalLayout />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'keys', element: <KeysPage /> },
      { path: 'models', element: <ModelsPage /> },
      { path: 'usage', element: <UsagePage /> },
      { path: 'department', element: <DepartmentPage /> },
      { path: 'topup', element: <TopupPage /> },
      { path: 'quickstart', element: <QuickReferencePage /> },
      { path: 'client-setup', element: <Navigate to="/client-setup/claude-code" replace /> },
      { path: 'client-setup/claude-code', element: <ClaudeCodePage /> },
      { path: 'client-setup/codex-cli', element: <CodexCliPage /> },
      { path: 'client-setup/opencode', element: <OpenCodePage /> },
      { path: 'reference', element: <Navigate to="/reference/integration" replace /> },
      { path: 'reference/integration', element: <IntegrationGuidePage /> },
      { path: 'reference/error-codes', element: <ErrorCodesPage /> },
      { path: 'settings', element: <SettingsPage /> },
      { path: 'status', element: <StatusPage /> },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
];
