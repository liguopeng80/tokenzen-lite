import type { ReactNode } from 'react';
import { App } from 'antd';
import { bindAntdApp } from '../feedback';

/**
 * 把 antd `<App>` 提供的 message 与 modal 实例接出来，供 `@token-zen/shared/feedback`
 * 里可直接调用的 message / modal 使用。这两个实例带 ConfigProvider 的主题与语言包，
 * 静态方法则没有——差异体现在提示条的外观，以及控制台每次调用留下的一条 warning。
 *
 * 必须放在 `<ConfigProvider>` → `<App>` 内部、路由之上。
 */
export function AntdAppBridge({ children }: { children: ReactNode }) {
  const app = App.useApp();
  // 在渲染期绑定而非 effect：首屏渲染中触发的提示也要用到带上下文的实例。
  // 赋值是幂等的，重复渲染无副作用。
  bindAntdApp({ message: app.message, modal: app.modal });
  return <>{children}</>;
}
