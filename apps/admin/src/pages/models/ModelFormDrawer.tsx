import { Drawer, Form, Input, InputNumber, Select, Button } from 'antd';
import type { FormInstance } from 'antd';
import type {
  ModelInfo,
  Modality,
  BillingMode,
  ModelStatus,
  Provider,
  Capability,
} from '@token-zen/shared';
import {
  ModalityLabel,
  BillingModeLabel,
  ModelStatusLabel,
  ProviderLabel,
  CapabilityLabel,
} from '@token-zen/shared';

const modalityOptions = Object.entries(ModalityLabel).map(([value, label]) => ({
  value: value as Modality,
  label,
}));
const billingModeOptions = Object.entries(BillingModeLabel).map(([value, label]) => ({
  value: value as BillingMode,
  label,
}));
const statusOptions = Object.entries(ModelStatusLabel).map(([value, label]) => ({
  value: value as ModelStatus,
  label,
}));
const providerOptions = Object.entries(ProviderLabel).map(([value, label]) => ({
  value: value as Provider,
  label,
}));
const capabilityOptions = Object.entries(CapabilityLabel).map(([value, label]) => ({
  value: value as Capability,
  label,
}));

const NAME_HINT =
  '名称是渠道路由、成本、密钥白名单与用量日志的关联键，创建后不可修改；如需改名请新建模型并下架本模型。';

interface ModelFormDrawerProps {
  mode: 'create' | 'edit';
  open: boolean;
  loading: boolean;
  form: FormInstance;
  editingModel: ModelInfo | null;
  onClose: () => void;
  onOk: () => void;
}

export default function ModelFormDrawer({
  mode,
  open,
  loading,
  form,
  editingModel,
  onClose,
  onOk,
}: ModelFormDrawerProps) {
  const title =
    mode === 'edit' ? `编辑模型 - ${editingModel?.name ?? ''}` : '新增模型';

  return (
    <Drawer
      title={title}
      placement="right"
      width={520}
      open={open}
      destroyOnHidden
      onClose={onClose}
      footer={
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={loading} onClick={onOk}>
            确定
          </Button>
        </div>
      }
    >
      <Form form={form} layout="vertical">
        {mode === 'create' ? (
          <Form.Item
            name="name"
            label="模型名称"
            rules={[{ required: true, message: '请输入模型名称' }]}
          >
            <Input placeholder="如 gpt-4o" />
          </Form.Item>
        ) : (
          <Form.Item name="name" label="模型名称" extra={NAME_HINT}>
            <Input disabled />
          </Form.Item>
        )}
        <Form.Item name="display_name" label="展示名称">
          <Input />
        </Form.Item>
        <Form.Item name="modality" label="形态">
          <Select options={modalityOptions} />
        </Form.Item>
        <Form.Item name="billing_mode" label="计费方式">
          <Select options={billingModeOptions} />
        </Form.Item>
        <Form.Item name="status" label="状态">
          <Select options={statusOptions} />
        </Form.Item>
        <Form.Item name="provider" label="厂商">
          <Select options={providerOptions} allowClear placeholder="选择厂商" />
        </Form.Item>
        <Form.Item name="context_window" label="上下文窗口" extra="0=未知；≥1,000,000 视为 1M">
          <InputNumber min={0} precision={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="max_output" label="最大输出" extra="0=未知">
          <InputNumber min={0} precision={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="capabilities" label="能力标签" extra="只标非常配能力（多模态、深度推理）">
          <Select mode="multiple" options={capabilityOptions} allowClear placeholder="选择能力" />
        </Form.Item>
        <Form.Item name="alias" label="对外别名" extra="全局唯一对外短名，如 opus">
          <Input placeholder="如 opus" />
        </Form.Item>
        <Form.Item name="tags" label="标签">
          <Input placeholder="逗号分隔（可选）" />
        </Form.Item>
        <Form.Item name="description" label="描述">
          <Input.TextArea rows={2} />
        </Form.Item>
      </Form>
    </Drawer>
  );
}
