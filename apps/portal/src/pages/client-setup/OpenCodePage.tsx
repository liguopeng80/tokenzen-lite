import { Card, Typography, Tabs, Alert } from 'antd';
import { Link } from 'react-router-dom';
import { CodeBlock } from './CodeBlock';
import { useBaseUrl } from './useBaseUrl';
import BaseUrlNotice from './BaseUrlNotice';

const { Title, Paragraph, Text } = Typography;

function OpenCodePage() {
  const baseUrl = useBaseUrl();

  return (
    <div style={{ maxWidth: 800 }}>
      <Title level={4} style={{ marginTop: 0 }}>OpenCode 配置</Title>

      <BaseUrlNotice />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="开始之前"
        description={
          <>
            请先在 <Link to="/keys">API 密钥</Link> 页面创建密钥，然后将配置中的「你的API密钥」替换为实际密钥值。
          </>
        }
      />

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>
          1. 安装 OpenCode
        </Title>
        <Paragraph>
          <a href="https://opencode.ai" target="_blank" rel="noopener noreferrer">OpenCode</a> 是开源 AI 编程终端工具，支持 75+ 模型提供商，可通过自定义 Provider 接入本平台。
        </Paragraph>
        <Tabs
          size="small"
          items={[
            {
              key: 'mac',
              label: 'macOS',
              children: (
                <>
                  <Paragraph type="secondary">方法一：Homebrew（推荐）</Paragraph>
                  <CodeBlock copyable>{`brew install opencode-ai/tap/opencode`}</CodeBlock>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>方法二：curl 脚本</Paragraph>
                  <CodeBlock copyable>{`curl -fsSL https://opencode.ai/install | bash`}</CodeBlock>
                </>
              ),
            },
            {
              key: 'linux',
              label: 'Linux',
              children: (
                <CodeBlock copyable>{`curl -fsSL https://opencode.ai/install | bash`}</CodeBlock>
              ),
            },
            {
              key: 'win',
              label: 'Windows',
              children: (
                <>
                  <Paragraph type="secondary">方法一：Chocolatey</Paragraph>
                  <CodeBlock copyable>{`choco install opencode`}</CodeBlock>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>方法二：Scoop</Paragraph>
                  <CodeBlock copyable>{`scoop install opencode`}</CodeBlock>
                </>
              ),
            },
            {
              key: 'npm',
              label: 'npm / bun',
              children: (
                <CodeBlock copyable>{`npm install -g opencode-ai`}</CodeBlock>
              ),
            },
          ]}
        />
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="opencode-config-card">
        <Title level={5} style={{ marginTop: 0 }}>2. 配置连接</Title>
        <Paragraph>
          在项目根目录或 <Text code>~/.config/opencode/</Text> 下创建 <Text code>opencode.json</Text>，只声明自定义 provider 的必需字段（npm 包与 baseURL）：
        </Paragraph>
        <CodeBlock copyable>{`{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "token-zen": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Token Zen",
      "options": {
        "baseURL": "${baseUrl}/v1"
      }
    }
  }
}`}</CodeBlock>
        <Paragraph type="secondary" style={{ marginTop: 12 }}>
          不在配置里枚举 <Text code>models</Text>，避免随版本迭代过期。在 OpenCode 终端运行 <Text code>/connect</Text> 粘贴密钥后，模型用 <Text code>gpt-5.6-sol</Text> 或 <Text code>claude-sonnet-5</Text>（以 <Text code>GET /v1/models</Text> 为准）。
        </Paragraph>
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>3. 设置 API 密钥</Title>
        <Paragraph>
          在 OpenCode 终端中运行 <Text code>/connect</Text> 命令，选择 <Text strong>Other</Text>，输入 provider ID <Text code>token-zen</Text>，然后粘贴你的 API 密钥。
        </Paragraph>
        <CodeBlock copyable>{`/connect
# 选择 Other → 输入 provider id: token-zen → 粘贴 API 密钥`}</CodeBlock>
      </Card>

      <Card>
        <Title level={5} style={{ marginTop: 0 }}>4. 选择模型并启动</Title>
        <CodeBlock copyable>{`cd /path/to/your/project
opencode
# 在 TUI 中运行 /models 选择模型`}</CodeBlock>
      </Card>
    </div>
  );
}

export default OpenCodePage;
