import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Form, Input, Button, Card, theme } from 'antd';
import { message } from '@token-zen/shared';
import { UserOutlined, LockOutlined, SafetyCertificateOutlined, DashboardOutlined, SettingOutlined } from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { warmGray, primaryPalette } from '@token-zen/shared/theme';

function LoginPage() {
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const login = useAuthStore((s) => s.login);

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true);
    try {
      await login(values.username, values.password);
      message.success('登录成功');
      navigate('/dashboard', { replace: true });
    } catch (err) {
      message.error(
        err instanceof Error ? err.message : '登录失败，请检查用户名和密码',
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{ minHeight: '100vh', display: 'flex', background: warmGray[50] }}>
      {/* Left: Site Introduction */}
      <div
        style={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          padding: '60px 48px 60px 64px',
          maxWidth: 600,
        }}
      >
        <div style={{ marginBottom: 48 }}>
          <h1
            style={{
              fontSize: 36,
              fontWeight: 700,
              color: warmGray[900],
              margin: '0 0 8px',
              letterSpacing: '-0.02em',
            }}
          >
            Token<span style={{ color: primaryPalette[500] }}>Zen</span>
          </h1>
          <p style={{ color: warmGray[500], fontSize: 15, margin: 0 }}>
            AI API 统一管理平台 · 管理后台
          </p>
        </div>

        <p
          style={{
            fontSize: 16,
            color: warmGray[700],
            lineHeight: 1.75,
            margin: '0 0 40px',
          }}
        >
          集中管理各家 AI 模型供应商渠道，按模型单价对团队成员的每次调用扣减积分，
          并留存可追溯的用量与费用记录。
        </p>

        {/* Feature highlights */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          {[
            {
              icon: <DashboardOutlined style={{ fontSize: 20, color: primaryPalette[500] }} />,
              title: '经营概览',
              desc: '今日请求量、收入成本毛利与按渠道、模型的分组明细',
            },
            {
              icon: <SettingOutlined style={{ fontSize: 20, color: primaryPalette[500] }} />,
              title: '渠道管理',
              desc: '多供应商渠道统一配置、优先级调度与故障自动切换',
            },
            {
              icon: <SafetyCertificateOutlined style={{ fontSize: 20, color: primaryPalette[500] }} />,
              title: '额度与计费',
              desc: '按账号发放积分额度，按模型单价与时段倍率逐次结算',
            },
          ].map((item) => (
            <div key={item.title} style={{ display: 'flex', gap: 14, alignItems: 'flex-start' }}>
              <div
                style={{
                  width: 40,
                  height: 40,
                  borderRadius: 10,
                  background: primaryPalette[50],
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  flexShrink: 0,
                }}
              >
                {item.icon}
              </div>
              <div>
                <div style={{ fontWeight: 600, color: warmGray[800], fontSize: 14 }}>{item.title}</div>
                <div style={{ color: warmGray[500], fontSize: 13, marginTop: 2 }}>{item.desc}</div>
              </div>
            </div>
          ))}
        </div>

        <p style={{ color: warmGray[400], fontSize: 12, marginTop: 48 }}>
          © {new Date().getFullYear()} Token Zen · Powered by AI
        </p>
      </div>

      {/* Right: Login Form */}
      <div
        style={{
          flex: 1,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: '#FFFFFF',
          borderLeft: `1px solid ${warmGray[100]}`,
          position: 'relative',
          overflow: 'hidden',
        }}
      >
        {/* Subtle decorative circle */}
        <div
          style={{
            position: 'absolute',
            width: 320,
            height: 320,
            borderRadius: '50%',
            background: `radial-gradient(circle, ${primaryPalette[50]}, transparent 70%)`,
            top: -80,
            right: -80,
            pointerEvents: 'none',
          }}
        />
        <Card
          style={{
            width: 400,
            border: 'none',
            boxShadow: 'none',
            background: 'transparent',
          }}
          styles={{ body: { padding: '0 24px' } }}
        >
          <div style={{ textAlign: 'center', marginBottom: 36 }}>
            <h2
              style={{
                fontSize: 24,
                fontWeight: 600,
                color: warmGray[800],
                margin: 0,
              }}
            >
              管理员登录
            </h2>
            <p style={{ color: token.colorTextSecondary, marginTop: 8, fontSize: 14 }}>
              请使用管理员账号登录后台
            </p>
          </div>
          <Form
            name="admin-login"
            onFinish={onFinish}
            size="large"
            autoComplete="off"
          >
            <Form.Item
              name="username"
              rules={[{ required: true, message: '请输入用户名' }]}
            >
              <Input prefix={<UserOutlined />} placeholder="用户名" />
            </Form.Item>
            <Form.Item
              name="password"
              rules={[{ required: true, message: '请输入密码' }]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder="密码" />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={loading} block>
                登录
              </Button>
            </Form.Item>
          </Form>
        </Card>
      </div>
    </div>
  );
}

export default LoginPage;
