import { Input, InputNumber, Select, Switch } from 'antd';
import type { SettingItem } from '@token-zen/shared';
import { SettingOptionLabels } from '@token-zen/shared';

interface SettingValueEditorProps {
  settingKey: string;
  kind: SettingItem['kind'];
  value: number | boolean | string;
  onChange: (value: number | boolean | string) => void;
  /** 密文设置项：读取接口返回的是掩码，此处按密码框渲染并提示留空即清除 */
  secret?: boolean;
  /** 枚举型设置项的合法取值，非空时渲染为下拉选择 */
  options?: string[];
}

/** 按 SettingItem.kind 渲染对应的编辑控件。 */
export function SettingValueEditor({
  settingKey, kind, value, onChange, secret, options,
}: SettingValueEditorProps) {
  if (secret) {
    return (
      <Input.Password
        value={String(value ?? '')}
        onChange={(e) => onChange(e.target.value)}
        style={{ width: 280 }}
        placeholder="留空即清除该密钥"
        autoComplete="new-password"
      />
    );
  }
  // 枚举型：合法取值由后端下发，避免管理员手打错值、提交后才被拒绝。
  if (options && options.length > 0) {
    const labels = SettingOptionLabels[settingKey] ?? {};
    return (
      <Select
        value={String(value ?? '')}
        onChange={onChange}
        style={{ width: 280 }}
        options={options.map((opt) => ({ label: labels[opt] ?? opt, value: opt }))}
      />
    );
  }
  switch (kind) {
    case 'bool':
      return <Switch checked={Boolean(value)} onChange={onChange} />;
    case 'int64':
      return (
        <InputNumber
          value={typeof value === 'number' ? value : Number(value) || 0}
          onChange={(v) => onChange(v ?? 0)}
          style={{ width: 220 }}
          precision={0}
        />
      );
    case 'string':
    default:
      return (
        <Input
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          style={{ width: 280 }}
        />
      );
  }
}
