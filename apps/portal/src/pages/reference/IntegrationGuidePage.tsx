import { Card, Typography, Alert } from 'antd';
import { Link } from 'react-router-dom';
import { CodeBlock } from '../client-setup/CodeBlock';
import { useBaseUrl } from '../client-setup/useBaseUrl';
import BaseUrlNotice from '../client-setup/BaseUrlNotice';

const { Title, Paragraph, Text } = Typography;

/**
 * 客户端接入指南。
 *
 * 覆盖五类主流接入方式：Claude Code（编程助手）、OpenAI Python SDK、Anthropic Python SDK、
 * Cherry Studio（桌面客户端）、curl。两种协议端点各配示例：
 * - OpenAI 格式 /v1/chat/completions（base_url 须带 /v1 后缀）。
 * - Anthropic 格式 /v1/messages（base_url 不带 /v1）。
 *
 * base_url 来源：管理员配置的对外 API 基址（GET /api/site/config 的 server_address），
 * 未配置时由 useBaseUrl 推断并经 BaseUrlNotice 提示「地址靠推断可能连不通」。
 */
function IntegrationGuidePage() {
  const baseUrl = useBaseUrl();

  return (
    <div style={{ maxWidth: 900 }}>
      <Title level={4} style={{ marginTop: 0 }}>接入指南</Title>

      <BaseUrlNotice />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="开始之前"
        description={
          <>
            本平台对外提供两种协议端点：<Text code>/v1/chat/completions</Text>（OpenAI 格式）与
            <Text code>/v1/messages</Text>（Anthropic 格式）。两种请求头等价——
            <Text code>Authorization: Bearer &lt;key&gt;</Text> 与 <Text code>x-api-key: &lt;key&gt;</Text>，
            密钥均以 <Text code>tzl-</Text> 为前缀。请先在{' '}
            <Link to="/keys">API 密钥</Link> 页面创建密钥，再把示例里的「你的API密钥」替换为实际值。
          </>
        }
      />

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>Base URL</Title>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div>
            <Text strong>OpenAI 格式（OpenAI SDK、Codex、Cherry Studio OpenAI 模式、curl）</Text>
            <CodeBlock copyable>{`${baseUrl}/v1`}</CodeBlock>
            <Paragraph type="secondary" style={{ marginTop: 4 }}>
              所有 OpenAI 兼容客户端的 base_url 须包含 <Text code>/v1</Text> 后缀。
            </Paragraph>
          </div>
          <div>
            <Text strong>Anthropic 格式（Claude Code、Anthropic SDK、Cherry Studio Anthropic 模式）</Text>
            <CodeBlock copyable>{baseUrl}</CodeBlock>
            <Paragraph type="secondary" style={{ marginTop: 4 }}>
              不带 <Text code>/v1</Text>，SDK 与 Claude Code 自行拼 <Text code>/v1/messages</Text>。
            </Paragraph>
          </div>
        </div>
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>Claude Code</Title>
        <Paragraph>
          Anthropic 官方 AI 编程助手。编辑 <Text code>~/.claude/settings.json</Text>：
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
        <Paragraph type="secondary" style={{ marginTop: 8 }}>
          完整安装步骤与环境变量方式见{' '}
          <Link to="/client-setup/claude-code">Claude Code 配置</Link>。
        </Paragraph>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="guide-openai-sdk-card">
        <Title level={5} style={{ marginTop: 0 }}>OpenAI SDK（Python）</Title>
        <Paragraph type="secondary">走 <Text code>/v1/chat/completions</Text>，base_url 带 <Text code>/v1</Text>。</Paragraph>
        <Paragraph type="secondary">
          示例中的 <Text code>gpt-5.6-sol</Text>、<Text code>claude-sonnet-5</Text> 仅为占位型号，具体可用型号请调用 <Text code>GET /v1/models</Text> 核对你部署的实际情况。
        </Paragraph>
        <CodeBlock copyable>{`pip install openai`}</CodeBlock>
        <CodeBlock copyable>{`from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="你的API密钥",  # tzl- 前缀
)

resp = client.chat.completions.create(
    model="gpt-5.6-sol",
    messages=[{"role": "user", "content": "Hello!"}],
    max_tokens=128,
)
print(resp.choices[0].message.content)`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="guide-anthropic-sdk-card">
        <Title level={5} style={{ marginTop: 0 }}>Anthropic SDK（Python）</Title>
        <Paragraph type="secondary">走 <Text code>/v1/messages</Text>，base_url 不带 <Text code>/v1</Text>。</Paragraph>
        <Paragraph type="secondary">
          鉴权字段两种皆可：<Text code>ANTHROPIC_AUTH_TOKEN</Text>（走 <Text code>Authorization: Bearer</Text> 头）与 <Text code>ANTHROPIC_API_KEY</Text>（走 <Text code>x-api-key</Text> 头）平台都接受。SDK 端通过 <Text code>api_key</Text> 参数或对应环境变量设置均可。
        </Paragraph>
        <CodeBlock copyable>{`pip install anthropic`}</CodeBlock>
        <CodeBlock copyable>{`from anthropic import Anthropic

client = Anthropic(
    base_url="${baseUrl}",
    api_key="你的API密钥",  # tzl- 前缀
)

msg = client.messages.create(
    model="claude-sonnet-5",
    max_tokens=128,
    messages=[{"role": "user", "content": "Hello!"}],
)
print(msg.content[0].text)`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>Cherry Studio</Title>
        <Paragraph>
          开源桌面 AI 客户端，同时支持 OpenAI 与 Anthropic 两种提供商类型。在「设置 → 模型服务」
          新增自定义提供商，按目标端点选择对应配置：
        </Paragraph>
        <Card type="inner" style={{ marginBottom: 12 }}>
          <Text strong>OpenAI 模式（调 /v1/chat/completions）</Text>
          <ul style={{ margin: '8px 0 0', paddingLeft: 20 }}>
            <li>API 类型：<Text code>OpenAI</Text></li>
            <li>API Base URL：<Text code>{`${baseUrl}/v1`}</Text></li>
            <li>API Key：你的 <Text code>tzl-</Text> 密钥</li>
            <li>模型：在模型列表里添加本站上架的模型名（如 <Text code>gpt-5.6-sol</Text>），或先调 GET <Text code>{`${baseUrl}/v1/models`}</Text> 获取清单</li>
          </ul>
        </Card>
        <Card type="inner">
          <Text strong>Anthropic 模式（调 /v1/messages）</Text>
          <ul style={{ margin: '8px 0 0', paddingLeft: 20 }}>
            <li>API 类型：<Text code>Anthropic</Text></li>
            <li>API Base URL：<Text code>{baseUrl}</Text>（不带 <Text code>/v1</Text>）</li>
            <li>API Key：你的 <Text code>tzl-</Text> 密钥</li>
            <li>模型：如 <Text code>claude-sonnet-5</Text></li>
          </ul>
        </Card>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="guide-embeddings-card">
        <Title level={5} style={{ marginTop: 0 }}>Embeddings（向量）</Title>
        <Paragraph type="secondary">
          走 <Text code>POST /v1/embeddings</Text>，base_url 带 <Text code>/v1</Text>。模型须为 embedding 形态（如 <Text code>text-embedding-3-small</Text>），传入对话模型会返回错误；该端点仅由 <Text code>openai_compat</Text> 渠道承载。具体可用的 embedding 型号以 <Text code>GET /v1/models</Text> 为准。
        </Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/embeddings \\
  -H "Authorization: Bearer 你的API密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "text-embedding-3-small",
    "input": "Hello world"
  }'`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="guide-images-card">
        <Title level={5} style={{ marginTop: 0 }}>Images（图像生成）</Title>
        <Paragraph type="secondary">
          走 <Text code>POST /v1/images/generations</Text>，base_url 带 <Text code>/v1</Text>，按次计费（单价以渠道定价为准）。该端点仅由 <Text code>openai_compat</Text> 渠道承载，可用模型以 <Text code>GET /v1/models</Text> 为准。
        </Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/images/generations \\
  -H "Authorization: Bearer 你的API密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "dall-e-3",
    "prompt": "A serene mountain lake at dawn",
    "n": 1,
    "size": "1024x1024"
  }'`}</CodeBlock>
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="guide-provider-prefix-card">
        <Title level={5} style={{ marginTop: 0 }}>Provider 前缀入口（锁定上游厂商）</Title>
        <Paragraph>
          除默认的 <Text code>/v1/*</Text> 入口外，可在路径中加 provider 前缀显式锁定上游厂商：<Text code>/{"{provider}"}/v1/*</Text>，例如 <Text code>/anthropic/v1/messages</Text>、<Text code>/deepseek/v1/chat/completions</Text>。
        </Paragraph>
        <Paragraph type="secondary">
          base_url 拼法：<Text code>{`${baseUrl}/<provider>/v1`}</Text>（OpenAI 格式）或 <Text code>{`${baseUrl}/<provider>`}</Text>（Anthropic 格式，SDK 自行拼 <Text code>/v1/messages</Text>）。provider 前缀命中后候选仅在同 provider 内容错时回退，不跨厂商回退。
        </Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/anthropic/v1/messages \\
  -H "x-api-key: 你的API密钥" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 32
  }'`}</CodeBlock>
      </Card>

      <Card>
        <Title level={5} style={{ marginTop: 0 }}>curl</Title>
        <Paragraph type="secondary">OpenAI 格式（/v1/chat/completions）：</Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/chat/completions \\
  -H "Authorization: Bearer 你的API密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 32
  }'`}</CodeBlock>
        <Paragraph type="secondary" style={{ marginTop: 12 }}>Anthropic 格式（/v1/messages）：</Paragraph>
        <CodeBlock copyable>{`curl ${baseUrl}/v1/messages \\
  -H "x-api-key: 你的API密钥" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 32
  }'`}</CodeBlock>
        <Paragraph type="secondary" style={{ marginTop: 12 }}>
          程序化查询密钥余额与用量：<Text code>GET {baseUrl}/v1/key/info</Text>{' '}
          （<Text code>Authorization: Bearer 你的API密钥</Text>）。遇到错误码时去{' '}
          <Link to="/reference/error-codes">错误码自助诊断</Link> 查含义与建议动作。
        </Paragraph>
      </Card>
    </div>
  );
}

export default IntegrationGuidePage;
