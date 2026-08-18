import { useState } from 'react';
import { Alert, Button, Card, Form, Input, Typography } from 'antd';
import { message } from '../feedback';

const { Title, Paragraph } = Typography;

interface ForcePasswordChangeProps {
  /** 当前登录的用户名，展示用。 */
  username: string;
  /** 提交改密；成功后调用方负责刷新登录用户。 */
  onSubmit: (originalPassword: string, password: string) => Promise<void>;
  /** 退出登录，供本人暂不改密时离开。 */
  onLogout: () => void;
}

/**
 * 首次登录强制改密的全屏拦截页。
 *
 * 管理员建号、批量导入与重置密码给出的都是一次性初始密码，在本人改掉之前它同时
 * 存在于管理员与转达渠道上。后端对该账号的业务接口一律返回 403，因此这里不渲染
 * 任何常规界面——留着菜单只会让每次点击都撞上 403。
 *
 * 三态：默认展示改密表单；提交中按钮进入 loading 且表单禁止重复提交；
 * 失败时以 message 报出后端原因（原密码错误、新密码过短），表单保留已填内容。
 */
export function ForcePasswordChange({ username, onSubmit, onLogout }: ForcePasswordChangeProps) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);

  const handleFinish = async (values: {
    original_password: string;
    password: string;
    confirm_password: string;
  }) => {
    if (values.password !== values.confirm_password) {
      message.error('两次输入的新密码不一致');
      return;
    }
    if (values.password === values.original_password) {
      message.error('新密码不能与初始密码相同');
      return;
    }
    setLoading(true);
    try {
      await onSubmit(values.original_password, values.password);
      message.success('密码已修改');
    } catch (err) {
      message.error(err instanceof Error ? err.message : '密码修改失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 24,
      }}
    >
      <Card style={{ width: '100%', maxWidth: 420 }}>
        <Title level={4} style={{ marginTop: 0 }}>
          请先修改初始密码
        </Title>
        <Paragraph type="secondary">
          账号 <strong>{username}</strong> 当前使用的是管理员设定的初始密码。改掉它之后才能使用其余功能。
        </Paragraph>
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="初始密码在你改掉之前，管理员与转达它的渠道都持有一份。"
        />
        <Form form={form} layout="vertical" onFinish={handleFinish} disabled={loading}>
          <Form.Item
            name="original_password"
            label="初始密码"
            rules={[{ required: true, message: '请输入管理员给你的初始密码' }]}
          >
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '密码长度不能少于 8 位' },
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm_password"
            label="确认新密码"
            rules={[{ required: true, message: '请再次输入新密码' }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>
            修改密码并继续
          </Button>
        </Form>
        <Button type="link" block style={{ marginTop: 12 }} onClick={onLogout}>
          退出登录
        </Button>
      </Card>
    </div>
  );
}

export default ForcePasswordChange;
