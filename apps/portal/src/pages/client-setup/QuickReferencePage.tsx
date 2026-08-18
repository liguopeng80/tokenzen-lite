import { Card, Typography, Alert } from 'antd';
import { Link } from 'react-router-dom';
import { CodeBlock } from './CodeBlock';
import { useBaseUrl } from './useBaseUrl';
import BaseUrlNotice from './BaseUrlNotice';

const { Title, Paragraph, Text } = Typography;

function QuickReferencePage() {
  const baseUrl = useBaseUrl();

  return (
    <div style={{ maxWidth: 800 }}>
      <Title level={4} style={{ marginTop: 0 }}>快速开始</Title>

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
        <Title level={5} style={{ marginTop: 0 }}>Base URL</Title>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div>
            <Text strong>Anthropic 格式（Claude Code）</Text>
            <CodeBlock copyable>{baseUrl}</CodeBlock>
          </div>
          <div>
            <Text strong>OpenAI 格式（Codex、其他兼容客户端）</Text>
            <CodeBlock copyable>{`${baseUrl}/v1`}</CodeBlock>
          </div>
        </div>
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>Claude Code settings.json</Title>
        <Paragraph type="secondary">
          编辑 <Text code>~/.claude/settings.json</Text>：
        </Paragraph>
        <CodeBlock copyable>{`{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "你的API密钥",
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  },
  "permissions": {
    "allow": [],
    "deny": []
  }
}`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>环境变量</Title>
        <Paragraph type="secondary">
          或通过 <Text code>export</Text> 设置：
        </Paragraph>
        <CodeBlock copyable>{`export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="你的API密钥"`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="quick-test-connection-card">
        <Title level={5} style={{ marginTop: 0 }}>测试连接</Title>
        <Paragraph type="secondary">
          示例中的 <Text code>gpt-5.6-sol</Text>、<Text code>claude-sonnet-5</Text> 仅为占位型号，具体可用型号请调用 <Text code>GET /v1/models</Text> 核对你部署的实际情况。
        </Paragraph>
        <Paragraph type="secondary">OpenAI 格式（/v1/chat/completions）：</Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/chat/completions \\
  -H "Authorization: Bearer 你的API密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 5
  }'`}</CodeBlock>
        <Paragraph type="secondary" style={{ marginTop: 12 }}>Anthropic 格式（/v1/messages）：</Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/messages \\
  -H "x-api-key: 你的API密钥" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 5
  }'`}</CodeBlock>
      </Card>

      <Card>
        <Title level={5} style={{ marginTop: 0 }}>查询密钥余额</Title>
        <Paragraph type="secondary">
          可通过 <Text code>GET /v1/key/info</Text> 程序化查询当前密钥的余额与用量，便于在客户端或脚本中自动监控：
        </Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/key/info \\
  -H "Authorization: Bearer 你的API密钥"`}</CodeBlock>
      </Card>
    </div>
  );
}

export default QuickReferencePage;
