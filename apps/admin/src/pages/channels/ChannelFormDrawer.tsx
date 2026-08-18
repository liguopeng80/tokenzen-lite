import { useEffect, useMemo, useRef } from 'react';
import { Drawer, Form, Input, Select, InputNumber, Space, Button } from 'antd';
import { EditOutlined } from '@ant-design/icons';
import type { FormInstance } from 'antd';
import type { Channel, ChannelProtocol, Provider } from '@token-zen/shared';
import {
  ProviderLabel,
  ProtocolLabel,
  ProviderCatalog,
  defaultBaseUrlFor,
} from '@token-zen/shared';
import ModelMappingEditor, {
  type ModelMappingEditorValue,
} from '@/components/ModelMappingEditor';

const { TextArea } = Input;

const providerOptions = Object.entries(ProviderLabel).map(([value, label]) => ({
  value: value as Provider,
  label,
}));

const protocolOptions = Object.entries(ProtocolLabel).map(([value, label]) => ({
  value: value as ChannelProtocol,
  label,
}));

function formLabel(title: string, hint: string) {
  return (
    <span style={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
      {title}
      <span style={{ fontWeight: 'normal', fontSize: 12, color: '#999' }}>{hint}</span>
    </span>
  );
}

interface ChannelFormDrawerProps {
  mode: 'create' | 'edit';
  open: boolean;
  loading: boolean;
  form: FormInstance;
  editingChannel: Channel | null;
  modelOptions: string[];
  keyEditing: boolean;
  onKeyEditingChange: (v: boolean) => void;
  onClose: () => void;
  onOk: () => void;
}

export default function ChannelFormDrawer({
  mode,
  open,
  loading,
  form,
  editingChannel,
  modelOptions,
  keyEditing,
  onKeyEditingChange,
  onClose,
  onOk,
}: ChannelFormDrawerProps) {
  // 护栏：用户是否手动编辑过 base_url。create 打开时为 false 允许自动填；
  // edit 打开时为 true，绝不覆盖既有地址。程序化 setFieldsValue 不触发 Input onChange，
  // 故只有真实输入/粘贴才置 true。
  const userEditedBaseUrl = useRef(false);

  const providerVal = Form.useWatch('provider', form) as Provider | undefined;

  useEffect(() => {
    if (open) userEditedBaseUrl.current = mode === 'edit';
  }, [open, mode]);

  // 按厂商支持协议收窄；未选厂商回退全量。
  const protocolSelectOptions = useMemo(() => {
    if (!providerVal) return protocolOptions;
    const supported = ProviderCatalog[providerVal]?.supported_protocols;
    if (!supported || supported.length === 0) return protocolOptions;
    const set = new Set<string>(supported);
    return protocolOptions.filter((o) => set.has(o.value));
  }, [providerVal]);

  const handleProviderChange = (nextProvider: Provider) => {
    const supported = ProviderCatalog[nextProvider]?.supported_protocols;
    const currentProtocol = form.getFieldValue('protocol') as ChannelProtocol | undefined;
    let resolvedProtocol = currentProtocol;
    if (supported && supported.length > 0) {
      if (!currentProtocol || !supported.includes(currentProtocol)) {
        resolvedProtocol = supported[0];
        form.setFieldsValue({ protocol: resolvedProtocol });
      }
    }
    if (!userEditedBaseUrl.current) {
      const url = resolvedProtocol
        ? defaultBaseUrlFor(nextProvider, resolvedProtocol)
        : undefined;
      if (url) form.setFieldsValue({ base_url: url });
    }
  };

  const handleProtocolChange = (nextProtocol: ChannelProtocol) => {
    const currentProvider = form.getFieldValue('provider') as Provider | undefined;
    if (!currentProvider || userEditedBaseUrl.current) return;
    const url = defaultBaseUrlFor(currentProvider, nextProtocol);
    if (url) form.setFieldsValue({ base_url: url });
  };

  const title =
    mode === 'edit'
      ? `编辑渠道 #${editingChannel?.id ?? ''} - ${editingChannel?.name ?? ''}`
      : '创建渠道';

  return (
    <Drawer
      title={title}
      placement="right"
      width={720}
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
      <Form form={form} layout="vertical" autoComplete="off" className="channel-form">
        <Form.Item
          name="name"
          label="渠道名称"
          rules={[{ required: true, message: '请输入渠道名称' }]}
        >
          <Input autoComplete="off" />
        </Form.Item>
        <Space.Compact block>
          <Form.Item
            name="provider"
            label={formLabel('厂商', '决定认证与请求适配方式')}
            rules={[{ required: true, message: '请选择厂商' }]}
            style={{ flex: 1, marginRight: 8 }}
          >
            <Select
              options={providerOptions}
              showSearch
              filterOption={(input, o) =>
                (o?.label as string)?.toLowerCase().includes(input.toLowerCase())
              }
              onChange={handleProviderChange}
            />
          </Form.Item>
          <Form.Item
            name="protocol"
            label={formLabel('协议', '请求转发时的下游协议')}
            rules={[{ required: true, message: '请选择协议' }]}
            style={{ flex: 1 }}
          >
            <Select options={protocolSelectOptions} onChange={handleProtocolChange} />
          </Form.Item>
        </Space.Compact>
        <Form.Item
          name="base_url"
          label="Base URL"
          rules={[{ required: true, message: '请输入 Base URL' }]}
          extra="地址需含版本根路径，如 /v1、/paas/v4、/coding/v1"
        >
          <Input
            placeholder="如 https://api.openai.com/v1"
            onChange={() => {
              userEditedBaseUrl.current = true;
            }}
          />
        </Form.Item>
        <Form.Item
          name="modelConfig"
          rules={[
            {
              validator: (_, val: ModelMappingEditorValue) => {
                if (!val?.entries?.some((e) => e.callerModel.trim())) {
                  return Promise.reject(new Error('请至少添加一个模型'));
                }
                return Promise.resolve();
              },
            },
          ]}
        >
          <ModelMappingEditor modelOptions={modelOptions} />
        </Form.Item>
        <Form.Item
          name="test_model"
          label={formLabel('测试模型', '用于连通性测试的模型，留空默认取第一个')}
        >
          <Select
            showSearch
            allowClear
            placeholder="用于连通性测试的模型名"
            options={modelOptions.map((m) => ({ label: m, value: m }))}
          />
        </Form.Item>
        <Space.Compact block>
          <Form.Item
            name="priority"
            label={formLabel('优先级', '数值越大越优先')}
            initialValue={5}
            style={{ flex: 1, marginRight: 8 }}
          >
            <InputNumber style={{ width: '100%' }} min={0} />
          </Form.Item>
          <Form.Item
            name="weight"
            label={formLabel('权重', '同优先级内负载均衡')}
            initialValue={1}
            style={{ flex: 1 }}
          >
            <InputNumber style={{ width: '100%' }} min={0} />
          </Form.Item>
        </Space.Compact>

        {mode === 'create' ? (
          <Form.Item
            name="api_key"
            label={formLabel('API Key', '多个密钥用换行分隔')}
            rules={[{ required: true, message: '请输入 API Key' }]}
          >
            <TextArea rows={2} placeholder="多个密钥用换行分隔" />
          </Form.Item>
        ) : (
          <Form.Item label={formLabel('API Key', '密钥不可查看，留空保持不变，输入新值则覆盖')}>
            {!keyEditing ? (
              <Button
                size="small"
                icon={<EditOutlined />}
                onClick={() => onKeyEditingChange(true)}
              >
                修改密钥
              </Button>
            ) : (
              <>
                <Form.Item name="api_key" noStyle>
                  <TextArea rows={2} placeholder="输入新密钥，多个密钥用换行分隔" autoFocus />
                </Form.Item>
                <Button
                  size="small"
                  type="link"
                  style={{ padding: 0, marginTop: 4 }}
                  onClick={() => {
                    onKeyEditingChange(false);
                    form.setFieldsValue({ api_key: undefined });
                  }}
                >
                  取消修改
                </Button>
              </>
            )}
          </Form.Item>
        )}
      </Form>
    </Drawer>
  );
}
