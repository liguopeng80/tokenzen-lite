import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { App as AntdApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { portalTheme } from '@token-zen/shared/theme';
import App from './App';
import { AntdAppBridge } from '@token-zen/shared';
import './index.css';

// Global error handlers for unhandled exceptions
window.addEventListener('error', (event) => {
  console.error('[Global] Unhandled error:', event.error);
});

window.addEventListener('unhandledrejection', (event) => {
  console.error('[Global] Unhandled promise rejection:', event.reason);
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider locale={zhCN} theme={portalTheme}>
      {/* AntdApp 提供带主题上下文的 message/modal 实例，AntdAppBridge 把它们
          接给 @token-zen/shared/feedback，供 hook 用不了的位置直接调用。 */}
      <AntdApp>
        <AntdAppBridge>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </AntdAppBridge>
      </AntdApp>
    </ConfigProvider>
  </React.StrictMode>,
);
