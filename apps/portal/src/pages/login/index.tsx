import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Form, Input, Button, Card, Tabs, theme, Modal, Typography } from 'antd';
import { message } from '@token-zen/shared';
import {
  UserOutlined,
  LockOutlined,
  IdcardOutlined,
  ThunderboltOutlined,
  ApiOutlined,
  SafetyCertificateOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import { useAuthStore } from '@/stores/auth';
import { useSiteStore } from '@/stores/site';
import { warmGray, primaryPalette } from '@token-zen/shared/theme';

const { Paragraph } = Typography;

function LoginPage() {
  const [loading, setLoading] = useState(false);
  const [resetModalOpen, setResetModalOpen] = useState(false);
  const navigate = useNavigate();
  const { token } = theme.useToken();
  const { login, register } = useAuthStore();
  const registerEnabled = useSiteStore((s) => s.config.register_enabled);

  const onLogin = async (values: { username: string; password: string }) => {
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

  const onRegister = async (values: {
    username: string;
    password: string;
    display_name?: string;
  }) => {
    setLoading(true);
    try {
      await register(values.username, values.password, values.display_name);
      // 注册成功后自动登录
      await login(values.username, values.password);
      message.success('注册成功');
      navigate('/dashboard', { replace: true });
    } catch (err) {
      message.error(
        err instanceof Error ? err.message : '注册失败，请重试',
      );
    } finally {
      setLoading(false);
    }
  };

  const features = [
    {
      icon: <ApiOutlined style={{ fontSize: 22, color: primaryPalette[500] }} />,
      title: '统一 API 接入',
      desc: '一个 Key 调用管理员上架的全部模型，兼容 OpenAI、Anthropic、Gemini 三种协议',
    },
    {
      icon: <ThunderboltOutlined style={{ fontSize: 22, color: primaryPalette[500] }} />,
      title: '渠道路由与故障切换',
      desc: '按优先级选择渠道，当前渠道调用失败时自动改用其它可用渠道',
    },
    {
      icon: <SafetyCertificateOutlined style={{ fontSize: 22, color: primaryPalette[500] }} />,
      title: '用量透明可控',
      desc: '逐次记录调用的 token 消耗与费用开销，账单明细随时可查',
    },
    {
      icon: <KeyOutlined style={{ fontSize: 22, color: primaryPalette[500] }} />,
      title: '密钥自管',
      desc: '自助创建和吊销 API Key，可限定可调用模型与来源 IP',
    },
  ];

  const tabItems = [
    {
      key: 'login',
      label: '登录',
      children: (
        <Form
          name="portal-login"
          onFinish={onLogin}
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
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="密码"
            />
          </Form.Item>
          <Form.Item>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              block
            >
              登录
            </Button>
            <div style={{ textAlign: 'center', marginTop: 8 }}>
              <Button type="link" size="small" onClick={() => setResetModalOpen(true)}>
                忘记密码？
              </Button>
            </div>
          </Form.Item>
        </Form>
      ),
    },
  ];

  if (registerEnabled) {
    tabItems.push({
      key: 'register',
      label: '注册',
      children: (
        <Form
          name="portal-register"
          onFinish={onRegister}
          size="large"
          autoComplete="off"
        >
          <Form.Item
            name="username"
            rules={[
              { required: true, message: '请输入用户名' },
              { min: 3, message: '用户名至少 3 个字符' },
            ]}
          >
            <Input prefix={<UserOutlined />} placeholder="用户名" />
          </Form.Item>
          <Form.Item name="display_name">
            <Input
              prefix={<IdcardOutlined />}
              placeholder="显示名称（可选）"
            />
          </Form.Item>
          <Form.Item
            name="password"
            rules={[
              { required: true, message: '请设置密码' },
              { min: 8, message: '密码至少 8 个字符' },
            ]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="密码"
            />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            dependencies={['password']}
            rules={[
              { required: true, message: '请确认密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('password') === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error('两次密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password
              prefix={<LockOutlined />}
              placeholder="确认密码"
            />
          </Form.Item>
          <Form.Item>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              block
            >
              注册
            </Button>
          </Form.Item>
        </Form>
      ),
    });
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', background: warmGray[50] }}>
      {/* Left: Site Introduction */}
      <div
        style={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          padding: '48px 48px 48px 64px',
          maxWidth: 580,
          position: 'relative',
          overflow: 'hidden',
        }}
      >
        {/* Decorative bg dots pattern */}
        <div
          style={{
            position: 'absolute',
            top: 40,
            left: 40,
            width: 180,
            height: 180,
            backgroundImage: `radial-gradient(${warmGray[200]} 1px, transparent 1px)`,
            backgroundSize: '16px 16px',
            opacity: 0.5,
            pointerEvents: 'none',
          }}
        />

        <div style={{ position: 'relative', zIndex: 1 }}>
          {/* Brand */}
          <div style={{ marginBottom: 36 }}>
            <h1
              style={{
                fontSize: 38,
                fontWeight: 700,
                color: warmGray[900],
                margin: '0 0 8px',
                letterSpacing: '-0.02em',
              }}
            >
              Token<span style={{ color: primaryPalette[500] }}>Zen</span>
            </h1>
            <p style={{ color: warmGray[500], fontSize: 16, margin: 0 }}>
              公司内部 AI 模型接入网关
            </p>
          </div>

          {/* Tagline */}
          <p
            style={{
              fontSize: 17,
              color: warmGray[700],
              lineHeight: 1.8,
              margin: '0 0 12px',
              maxWidth: 440,
            }}
          >
            用公司统一分配的密钥调用 AI 模型，
            <br />
            用量按部门归集，额度由管理员发放。
          </p>

          {/* Feature grid */}
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr',
              gap: 20,
            }}
          >
            {features.map((f) => (
              <div key={f.title} style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
                <div
                  style={{
                    width: 42,
                    height: 42,
                    borderRadius: 10,
                    background: primaryPalette[50],
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flexShrink: 0,
                  }}
                >
                  {f.icon}
                </div>
                <div>
                  <div style={{ fontWeight: 600, color: warmGray[800], fontSize: 14 }}>
                    {f.title}
                  </div>
                  <div style={{ color: warmGray[500], fontSize: 12, marginTop: 3, lineHeight: 1.5 }}>
                    {f.desc}
                  </div>
                </div>
              </div>
            ))}
          </div>

          <p style={{ color: warmGray[400], fontSize: 12, marginTop: 40 }}>
            © {new Date().getFullYear()} Token Zen · Powered by AI
          </p>
        </div>
      </div>

      {/* Right: Login/Register Form */}
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
        {/* Decorative circle */}
        <div
          style={{
            position: 'absolute',
            width: 360,
            height: 360,
            borderRadius: '50%',
            background: `radial-gradient(circle, ${primaryPalette[50]}, transparent 70%)`,
            bottom: -100,
            right: -100,
            pointerEvents: 'none',
          }}
        />
        <Card
          style={{
            width: 420,
            border: 'none',
            boxShadow: 'none',
            background: 'transparent',
          }}
          styles={{ body: { padding: '0 16px' } }}
        >
          <div style={{ textAlign: 'center', marginBottom: 16 }}>
            <h2
              style={{
                fontSize: 22,
                fontWeight: 600,
                color: warmGray[800],
                margin: 0,
              }}
            >
              欢迎使用
            </h2>
            <p style={{ color: token.colorTextSecondary, marginTop: 6, fontSize: 14 }}>
              登录{registerEnabled ? '或注册' : ''}以开始使用 API 服务
            </p>
          </div>
          <Tabs centered items={tabItems} />
        </Card>
      </div>
      <Modal
        title="忘记密码"
        open={resetModalOpen}
        onCancel={() => setResetModalOpen(false)}
        footer={
          <Button type="primary" onClick={() => setResetModalOpen(false)}>
            我知道了
          </Button>
        }
      >
        <Paragraph>
          出于安全考虑，密码重置暂不支持自助操作，请联系管理员为您重置密码。
        </Paragraph>
      </Modal>
    </div>
  );
}

export default LoginPage;
