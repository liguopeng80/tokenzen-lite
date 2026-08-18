import { create } from 'zustand';
import type { User } from '@token-zen/shared';
import { clearUserId, setUserId } from '@token-zen/shared';
import { authApi } from '@/api/auth';

/** 与 @token-zen/shared 的 api/index.ts 中 USER_ID_KEY 保持一致 */
const USER_ID_KEY = 'tzl_user_id';

interface AuthState {
  user: User | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (
    username: string,
    password: string,
    displayName?: string,
  ) => Promise<void>;
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
      set({ user, loading: false });
    } catch (error) {
      set({ loading: false });
      throw error;
    }
  },

  register: async (username: string, password: string, displayName?: string) => {
    set({ loading: true });
    try {
      await authApi.register({
        username,
        password,
        display_name: displayName,
      });
      set({ loading: false });
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
      const user = await authApi.getMe();
      set({ user });
    } catch {
      clearUserId();
      set({ user: null });
    }
  },

  isLoggedIn: () => {
    return localStorage.getItem(USER_ID_KEY) !== null;
  },
}));
