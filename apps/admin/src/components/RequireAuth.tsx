import { useEffect, useState, type ReactNode } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { Spin } from 'antd';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/api/auth';
import { ForcePasswordChange } from '@token-zen/shared/components/ForcePasswordChange';

/**
 * 鉴权守卫：从 AdminLayout 抽出的通用版本，全屏路由（/monitor/*）与常规布局共用。
 *
 * 三态：
 *   1. 未完成鉴权拉取 → 全屏 Spin；
 *   2. 未登录或拉取后无 user → 跳 /login；
 *   3. 必须改密 → ForcePasswordChange 全屏拦截，改完刷新登录用户后放行；
 *   4. 其余 → 渲染 children。
 *
 * 用户未登录时直接 setChecked 不拉取，避免对未持 session 的访客多发一次 /auth/me。
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const { user, isLoggedIn, logout, fetchUser } = useAuthStore();
  const [authChecked, setAuthChecked] = useState(false);

  useEffect(() => {
    if (!isLoggedIn()) {
      setAuthChecked(true);
      return;
    }
    fetchUser().finally(() => setAuthChecked(true));
  }, [isLoggedIn, fetchUser]);

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

  // 未完成首次改密时后端对管理接口一律 403，此处不渲染任何业务界面，
  // 直接给出改密表单，改完刷新登录用户后由本组件重新放行。
  if (user.must_change_password) {
    return (
      <ForcePasswordChange
        username={user.username}
        onSubmit={async (originalPassword, password) => {
          await authApi.changePassword(originalPassword, password);
          await fetchUser();
        }}
        onLogout={handleLogout}
      />
    );
  }

  return <>{children}</>;
}

export default RequireAuth;
