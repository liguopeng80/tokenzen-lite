import { useState } from 'react';
import { Card, Form, Input, Button, Typography } from 'antd';
import { message } from '@token-zen/shared';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/api/auth';
import { useSiteStore } from '@/stores/site';

const { Title } = Typography;

function SettingsPage() {
  const user = useAuthStore((s) => s.user);
  const fetchUser = useAuthStore((s) => s.fetchUser);
  const config = useSiteStore((s) => s.config);
  const [profileForm] = Form.useForm();
  const [passwordForm] = Form.useForm();
  const [profileLoading, setProfileLoading] = useState(false);
  const [passwordLoading, setPasswordLoading] = useState(false);

  const handleUpdateProfile = async (values: { display_name?: string; email?: string }) => {
    setProfileLoading(true);
    try {
      await authApi.updateProfile({
        display_name: values.display_name,
        email: values.email || undefined,
      });
      message.success('个人信息已更新');
      fetchUser();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '更新失败');
    } finally {
      setProfileLoading(false);
    }
  };

  const handleChangePassword = async (values: {
    old_password: string;
    new_password: string;
    confirm_password: string;
  }) => {
    if (values.new_password !== values.confirm_password) {
      message.error('两次密码输入不一致');
      return;
    }
    setPasswordLoading(true);
    try {
      await authApi.changePassword({
        original_password: values.old_password,
        password: values.new_password,
      });
      message.success('密码已修改');
      passwordForm.resetFields();
    } catch (err) {
      message.error(err instanceof Error ? err.message : '密码修改失败，请检查当前密码是否正确');
    } finally {
      setPasswordLoading(false);
    }
  };

  return (
    <div>
      <Title level={4} style={{ marginTop: 0 }}>
        个人设置
      </Title>

      <Card title="基本信息" style={{ marginBottom: 16 }}>
        <Form
          form={profileForm}
          layout="vertical"
          initialValues={{
            username: user?.username ?? '',
            display_name: user?.display_name ?? '',
            email: user?.email ?? '',
          }}
          onFinish={handleUpdateProfile}
          style={{ maxWidth: 400 }}
        >
          <Form.Item name="username" label="用户名">
            <Input disabled />
          </Form.Item>
          <Form.Item
            name="display_name"
            label="显示名称"
            extra={!config.profile_display_name_editable ? '已由管理员锁定' : undefined}
          >
            <Input disabled={!config.profile_display_name_editable} />
          </Form.Item>
          <Form.Item
            name="email"
            label="邮箱"
            extra={
              !config.profile_email_editable
                ? '已由管理员锁定'
                : '余额不足时，系统会发邮件提醒你。留空则收不到提醒，只能等调用被拒绝时才发现余额用完。'
            }
            rules={[{ type: 'email', message: '邮箱格式不正确，示例：name@example.com' }]}
          >
            <Input type="email" placeholder="name@example.com" disabled={!config.profile_email_editable} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={profileLoading}>
              保存
            </Button>
          </Form.Item>
        </Form>
      </Card>

      <Card title="修改密码">
        <Form
          form={passwordForm}
          layout="vertical"
          onFinish={handleChangePassword}
          style={{ maxWidth: 400 }}
        >
          <Form.Item
            name="old_password"
            label="当前密码"
            rules={[{ required: true, message: '请输入当前密码' }]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '密码至少 8 位' },
            ]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item
            name="confirm_password"
            label="确认新密码"
            rules={[
              { required: true, message: '请确认新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('new_password') === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error('两次密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={passwordLoading}>
              修改密码
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}

export default SettingsPage;
