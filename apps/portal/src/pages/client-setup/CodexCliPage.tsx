import { Card, Typography, Tabs, Alert } from 'antd';
import { Link } from 'react-router-dom';
import { CodeBlock } from './CodeBlock';
import { useBaseUrl } from './useBaseUrl';
import BaseUrlNotice from './BaseUrlNotice';

const { Title, Paragraph, Text } = Typography;

function CodexCliPage() {
  const baseUrl = useBaseUrl();

  return (
    <div style={{ maxWidth: 800 }}>
      <Title level={4} style={{ marginTop: 0 }}>Codex CLI 配置</Title>

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
          1. 安装 Codex CLI
        </Title>
        <Paragraph>
          <a href="https://github.com/openai/codex" target="_blank" rel="noopener noreferrer">Codex CLI</a> 是 OpenAI 官方编程命令行工具，使用 OpenAI 兼容格式（端点需包含 <Text code>/v1</Text>）。本平台提供 <Text code>/v1/chat/completions</Text>，因此配置中的 <Text code>wire_api</Text> 须为 <Text code>chat</Text>；填 <Text code>responses</Text> 会让 Codex CLI 调用本平台未提供的 <Text code>/v1/responses</Text>，每次请求都返回 404。
        </Paragraph>
        <Tabs
          size="small"
          items={[
            {
              key: 'mac',
              label: 'macOS / Linux',
              children: (
                <CodeBlock copyable>{`npm i -g @openai/codex`}</CodeBlock>
              ),
            },
            {
              key: 'win',
              label: 'Windows (管理员 PowerShell)',
              children: (
                <CodeBlock copyable>{`npm i -g @openai/codex`}</CodeBlock>
              ),
            },
          ]}
        />
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>2. 配置连接</Title>
        <Tabs
          size="small"
          items={[
            {
              key: 'config',
              label: '配置文件（推荐）',
              children: (
                <>
                  <Paragraph type="secondary">
                    在 <Text code>~/.codex/</Text> 目录下创建 <Text code>config.toml</Text>（仅列接入本平台必需的字段）：
                  </Paragraph>
                  <CodeBlock copyable>{`model_provider = "custom"
model = "gpt-5.6-sol"

[model_providers.custom]
name = "custom"
base_url = "${baseUrl}/v1"
wire_api = "chat"
env_key = "CUSTOM_API_KEY"`}</CodeBlock>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>
                    <Text code>model</Text> 默认值 <Text code>gpt-5.6-sol</Text> 仅为占位型号，具体可用型号请调用 <Text code>GET /v1/models</Text> 核对你部署的实际情况。其余如 reasoning effort、sandbox、features 等选项按 Codex CLI 官方文档按需配置，与本平台接入无关。
                  </Paragraph>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>
                    再创建 <Text code>~/.codex/auth.json</Text>：
                  </Paragraph>
                  <CodeBlock copyable>{`{
  "OPENAI_API_KEY": "你的API密钥"
}`}</CodeBlock>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>
                    设置环境变量（<Text code>env_key</Text> 指定的变量名）：
                  </Paragraph>
                  <Tabs
                    size="small"
                    items={[
                      {
                        key: 'unix',
                        label: 'macOS / Linux',
                        children: (
                          <CodeBlock copyable>{`echo 'export CUSTOM_API_KEY="你的API密钥"' >> ~/.zshrc
source ~/.zshrc`}</CodeBlock>
                        ),
                      },
                      {
                        key: 'win',
                        label: 'Windows (PowerShell)',
                        children: (
                          <CodeBlock copyable>{`[System.Environment]::SetEnvironmentVariable("CUSTOM_API_KEY", "你的API密钥", [System.EnvironmentVariableTarget]::User)`}</CodeBlock>
                        ),
                      },
                    ]}
                  />
                </>
              ),
            },
            {
              key: 'env',
              label: '环境变量',
              children: (
                <Tabs
                  size="small"
                  items={[
                    {
                      key: 'unix',
                      label: 'macOS / Linux',
                      children: (
                        <CodeBlock copyable>{`export OPENAI_API_KEY="你的API密钥"
export CUSTOM_API_KEY="你的API密钥"`}</CodeBlock>
                      ),
                    },
                    {
                      key: 'win',
                      label: 'Windows (PowerShell)',
                      children: (
                        <CodeBlock copyable>{`[System.Environment]::SetEnvironmentVariable("OPENAI_API_KEY", "你的API密钥", [System.EnvironmentVariableTarget]::User)
[System.Environment]::SetEnvironmentVariable("CUSTOM_API_KEY", "你的API密钥", [System.EnvironmentVariableTarget]::User)`}</CodeBlock>
                      ),
                    },
                  ]}
                />
              ),
            },
          ]}
        />
      </Card>

      <Card>
        <Title level={5} style={{ marginTop: 0 }}>3. 验证并启动</Title>
        <CodeBlock copyable>{`codex --version
cd /path/to/your/project
codex`}</CodeBlock>
      </Card>
    </div>
  );
}

export default CodexCliPage;
