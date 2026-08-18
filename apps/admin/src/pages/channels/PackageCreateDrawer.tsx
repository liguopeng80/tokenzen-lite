import { useState } from 'react';
import { Drawer, Form, Select, Input, InputNumber, Button, Space, Descriptions, Tag } from 'antd';
import { message, ProviderLabel, ProtocolLabel, PackageCatalog } from '@token-zen/shared';
import { channelApi, type ChannelPayload } from '@/api/channels';

interface PackageCreateDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}

const packageOptions = PackageCatalog.map((p) => ({
  value: p.id,
  label: `${p.name}（${ProviderLabel[p.provider]}）`,
}));

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : '创建失败';
}

export default function PackageCreateDrawer({
  open,
  onClose,
  onCreated,
}: PackageCreateDrawerProps) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const packageId = Form.useWatch('packageId', form);
  const pkg = PackageCatalog.find((p) => p.id === packageId);

  const handleSubmit = async () => {
    if (!pkg) return;
    const vals = await form.validateFields().catch(() => null);
    if (!vals) return; // 校验失败：antd 已在字段下内联提示，不进入提交流程
    setLoading(true);
    let created = 0;
    let lastErr: unknown;
    try {
      for (const ep of pkg.endpoints) {
        try {
          const payload: ChannelPayload = {
            name: `${vals.name_prefix ?? ''}${pkg.name} - ${ProtocolLabel[ep.protocol]}`,
            provider: pkg.provider,
            protocol: ep.protocol,
            base_url: ep.base_url,
            api_key: vals.api_key,
            models: pkg.models,
            model_mapping: {},
            priority: vals.priority,
            weight: vals.weight,
          };
          await channelApi.create(payload);
          created++;
        } catch (e) {
          lastErr = e;
          break;
        }
      }
      if (created === pkg.endpoints.length) {
        message.success(`已创建 ${created} 条渠道`);
        onCreated();
        onClose();
        form.resetFields();
      } else if (created > 0) {
        message.warning(`已创建 ${created} 条，第 ${created + 1} 条失败：${errMsg(lastErr)}`);
        onCreated();
        onClose();
        form.resetFields();
      } else {
        message.error(errMsg(lastErr));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Drawer
      title="按套餐创建渠道"
      placement="right"
      width={480}
      open={open}
      destroyOnHidden
      onClose={onClose}
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onClose}>取消</Button>
          <Button
            type="primary"
            loading={loading}
            onClick={handleSubmit}
            data-testid="package-submit"
          >
            确定
          </Button>
        </div>
      }
    >
      <Form form={form} layout="vertical" autoComplete="off" preserve={false}>
        <Form.Item
          name="packageId"
          label="套餐"
          rules={[{ required: true, message: '请选择套餐' }]}
          data-testid="package-select"
        >
          <Select options={packageOptions} placeholder="选择套餐" />
        </Form.Item>
        <Form.Item
          name="api_key"
          label="API Key"
          rules={[{ required: true, message: '请输入 API Key' }]}
          data-testid="package-api-key"
        >
          <Input.TextArea rows={2} placeholder="输入套餐 API Key" />
        </Form.Item>
        <Form.Item name="name_prefix" label="渠道名前缀（可选）">
          <Input placeholder="可选，渠道名前缀" allowClear />
        </Form.Item>
        <Space.Compact block>
          <Form.Item
            name="priority"
            label="优先级"
            initialValue={5}
            style={{ flex: 1, marginRight: 8 }}
          >
            <InputNumber style={{ width: '100%' }} min={0} />
          </Form.Item>
          <Form.Item name="weight" label="权重" initialValue={1} style={{ flex: 1 }}>
            <InputNumber style={{ width: '100%' }} min={0} />
          </Form.Item>
        </Space.Compact>
      </Form>

      {pkg && (
        <Descriptions
          column={1}
          size="small"
          bordered
          style={{ marginTop: 8 }}
          title="创建预览"
        >
          <Descriptions.Item label="厂商">
            {ProviderLabel[pkg.provider]}
          </Descriptions.Item>
          <Descriptions.Item label="渠道数">
            {pkg.endpoints.length === 2 ? '双协议：建 2 条渠道' : `建 ${pkg.endpoints.length} 条渠道`}
          </Descriptions.Item>
          <Descriptions.Item label={`模型（${pkg.models.length}）`}>
            <Space size={4} wrap>
              {pkg.models.map((m) => (
                <Tag key={m}>{m}</Tag>
              ))}
            </Space>
          </Descriptions.Item>
          {pkg.endpoints.map((ep) => (
            <Descriptions.Item key={ep.protocol} label={ProtocolLabel[ep.protocol]}>
              {ep.base_url}
            </Descriptions.Item>
          ))}
        </Descriptions>
      )}
    </Drawer>
  );
}
