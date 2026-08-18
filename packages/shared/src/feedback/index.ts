import { message as staticMessage, Modal as staticModal } from 'antd';
import type { MessageInstance } from 'antd/es/message/interface';
import type { HookAPI as ModalHookAPI } from 'antd/es/modal/useModal';

/**
 * 全局提示与确认框的调用入口。
 *
 * antd 的静态方法取不到 React 上下文：它们不继承 ConfigProvider 的主题与中文语言包，
 * 外观与站点脱节，且每次调用都在控制台留下一条 warning。带上下文的实例只能通过
 * `App.useApp()` 拿到，而 hook 用不了的地方（拦截器、工具函数）依然需要一个可直接
 * 调用的对象。
 *
 * 这里保留可直接调用的形态，内部委托给 AntdAppBridge 绑定进来的实例；
 * 绑定发生前（模块加载到首次渲染之间）回落到静态方法，保证任何时刻都能出提示。
 */
let messageApi: MessageInstance = staticMessage;
let modalApi: ModalHookAPI | typeof staticModal = staticModal;

/** 由 AntdAppBridge 在渲染树内调用，注入带上下文的实例。 */
export function bindAntdApp(instances: { message: MessageInstance; modal: ModalHookAPI }): void {
  messageApi = instances.message;
  modalApi = instances.modal;
}

export const message = {
  success: ((...args) => messageApi.success(...args)) as MessageInstance['success'],
  error: ((...args) => messageApi.error(...args)) as MessageInstance['error'],
  warning: ((...args) => messageApi.warning(...args)) as MessageInstance['warning'],
  info: ((...args) => messageApi.info(...args)) as MessageInstance['info'],
  loading: ((...args) => messageApi.loading(...args)) as MessageInstance['loading'],
  open: ((...args) => messageApi.open(...args)) as MessageInstance['open'],
  destroy: ((...args) => messageApi.destroy(...args)) as MessageInstance['destroy'],
};

export const modal = {
  confirm: ((...args) => modalApi.confirm(...args)) as ModalHookAPI['confirm'],
  info: ((...args) => modalApi.info(...args)) as ModalHookAPI['info'],
  success: ((...args) => modalApi.success(...args)) as ModalHookAPI['success'],
  error: ((...args) => modalApi.error(...args)) as ModalHookAPI['error'],
  warning: ((...args) => modalApi.warning(...args)) as ModalHookAPI['warning'],
};
