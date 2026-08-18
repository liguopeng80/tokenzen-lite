import { theme } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import { copyToClipboard } from '@token-zen/shared';
import { message } from '@token-zen/shared';

export function CodeBlock({ children, copyable = false }: { children: string; copyable?: boolean }) {
  const { token } = theme.useToken();
  return (
    <div style={{ position: 'relative' }}>
      <pre
        style={{
          padding: 16,
          background: token.colorBgLayout,
          borderRadius: token.borderRadius,
          overflow: 'auto',
          fontSize: 13,
          lineHeight: 1.6,
          margin: 0,
        }}
      >
        {children}
      </pre>
      {copyable && (
        <CopyOutlined
          style={{
            position: 'absolute',
            top: 8,
            right: 8,
            cursor: 'pointer',
            color: token.colorTextSecondary,
            fontSize: 14,
          }}
          onClick={() => {
            copyToClipboard(children);
            message.success('已复制');
          }}
        />
      )}
    </div>
  );
}
