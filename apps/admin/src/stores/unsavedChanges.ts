import { create } from 'zustand';
import { useEffect } from 'react';

/**
 * 未保存改动的全站标记。
 *
 * 系统设置页把改动先存在本地，点保存才写回后端。改完直接切菜单会静默丢失，
 * 管理员既没有提示也无从察觉自己改的兑换率、限流参数根本没生效。
 * 侧栏导航与浏览器关闭都从这里读取待保存数量，先确认再放行。
 *
 * 这里只放数量，不放具体改动：确认弹窗只需要知道「有几项没保存」。
 */
interface UnsavedChangesState {
  count: number;
  setCount: (count: number) => void;
}

export const useUnsavedChangesStore = create<UnsavedChangesState>((set) => ({
  count: 0,
  setCount: (count) => set({ count }),
}));

/**
 * 供有未保存改动的页面登记待保存数量。组件卸载时自动清零，
 * 同时挂上浏览器关闭/刷新的原生确认。
 */
export function useUnsavedChanges(count: number): void {
  const setCount = useUnsavedChangesStore((s) => s.setCount);

  useEffect(() => {
    setCount(count);
    return () => setCount(0);
  }, [count, setCount]);

  useEffect(() => {
    if (count === 0) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      // 浏览器只认「阻止默认行为」这一个信号，自定义文案早已不被展示。
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [count]);
}
