import { Card, Typography, Tabs, Alert } from 'antd';
import { Link } from 'react-router-dom';
import { CodeBlock } from './CodeBlock';
import { useBaseUrl } from './useBaseUrl';
import BaseUrlNotice from './BaseUrlNotice';

const { Title, Paragraph, Text } = Typography;

function ClaudeCodePage() {
  const baseUrl = useBaseUrl();

  return (
    <div style={{ maxWidth: 800 }}>
      <Title level={4} style={{ marginTop: 0 }}>Claude Code 配置</Title>

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
          1. 安装 Claude Code
        </Title>
        <Paragraph>
          <a href="https://docs.anthropic.com/en/docs/claude-code/overview" target="_blank" rel="noopener noreferrer">Claude Code</a> 是 Anthropic 官方 AI 编程助手，支持终端和 VS Code 扩展两种使用方式。
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
                  <CodeBlock copyable>{`brew install --cask claude-code`}</CodeBlock>
                  <Paragraph type="secondary" style={{ marginTop: 12 }}>方法二：curl 脚本</Paragraph>
                  <CodeBlock copyable>{`curl -fsSL https://claude.ai/install.sh | bash`}</CodeBlock>
                </>
              ),
            },
            {
              key: 'win',
              label: 'Windows',
              children: (
                <>
                  <Paragraph type="secondary">PowerShell（管理员）</Paragraph>
                  <CodeBlock copyable>{`irm https://claude.ai/install.ps1 | iex`}</CodeBlock>
                </>
              ),
            },
            {
              key: 'linux',
              label: 'Linux',
              children: (
                <CodeBlock copyable>{`curl -fsSL https://claude.ai/install.sh | bash`}</CodeBlock>
              ),
            },
          ]}
        />
      </Card>

      <Card style={{ marginBottom: 16 }}>
        <Title level={5} style={{ marginTop: 0 }}>2. 配置连接</Title>
        <Paragraph>
          选择以下任一方式配置 Claude Code 连接本平台。
        </Paragraph>
        <Tabs
          size="small"
          items={[
            {
              key: 'settings',
              label: 'settings.json（推荐）',
              children: (
                <>
                  <Paragraph type="secondary">
                    编辑 <Text code>~/.claude/settings.json</Text>（Windows 路径：<Text code>C:\Users\你的用户名\.claude\settings.json</Text>），添加以下内容：
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
                        <>
                          <Paragraph type="secondary">添加到 shell 配置文件（~/.zshrc 或 ~/.bashrc）：</Paragraph>
                          <CodeBlock copyable>{`export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="你的API密钥"`}</CodeBlock>
                          <Paragraph type="secondary" style={{ marginTop: 8 }}>执行 <Text code>source ~/.zshrc</Text> 使配置生效。</Paragraph>
                        </>
                      ),
                    },
                    {
                      key: 'win',
                      label: 'Windows (PowerShell)',
                      children: (
                        <CodeBlock copyable>{`[System.Environment]::SetEnvironmentVariable("ANTHROPIC_BASE_URL", "${baseUrl}", [System.EnvironmentVariableTarget]::User)
[System.Environment]::SetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", "你的API密钥", [System.EnvironmentVariableTarget]::User)`}</CodeBlock>
                      ),
                    },
                  ]}
                />
              ),
            },
          ]}
        />
      </Card>

      <Card style={{ marginBottom: 16 }} data-testid="claude-code-vscode-card">
        <Title level={5} style={{ marginTop: 0 }}>3. VS Code 扩展（可选）</Title>
        <Paragraph>
          在 VS Code 扩展市场搜索并安装 <Text strong>Claude Code for VS Code</Text>。安装完成后按扩展提示登录或配置即可，无需手动编辑额外配置文件；上面第 2 步的 <Text code>ANTHROPIC_BASE_URL</Text> 与 <Text code>ANTHROPIC_AUTH_TOKEN</Text> 对扩展同样生效。
        </Paragraph>
      </Card>

      <Card>
        <Title level={5} style={{ marginTop: 0 }}>4. 验证并启动</Title>
        <CodeBlock copyable>{`claude --version
cd /path/to/your/project
claude`}</CodeBlock>
      </Card>
    </div>
  );
}

export default ClaudeCodePage;
