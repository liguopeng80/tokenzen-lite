import { create } from 'zustand';
import type { User } from '@token-zen/shared';
import { RoleRank, clearUserId, setUserId } from '@token-zen/shared';
import { authApi } from '@/api/auth';

interface AuthState {
  user: User | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => void;
  fetchUser: () => Promise<void>;
  isLoggedIn: () => boolean;
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  loading: false,

  login: async (username: string, password: string) => {
    set({ loading: true });
    try {
      const user = await authApi.login({ username, password });
      setUserId(user.id);

      if (RoleRank[user.role] < RoleRank.admin) {
        clearUserId();
        throw new Error('您没有管理员权限');
      }
      set({ user, loading: false });
    } catch (error) {
      set({ loading: false });
      throw error;
    }
  },

  logout: () => {
    authApi.logout().catch(() => {});
    clearUserId();
    set({ user: null });
  },

  fetchUser: async () => {
    try {
      const user = await authApi.getSelf();
      if (RoleRank[user.role] < RoleRank.admin) {
        clearUserId();
        set({ user: null });
        return;
      }
      set({ user });
    } catch {
      clearUserId();
      set({ user: null });
    }
  },

  isLoggedIn: () => {
    return localStorage.getItem('tzl_user_id') !== null;
  },
}));
