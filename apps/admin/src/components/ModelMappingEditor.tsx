import { useState, useCallback, useEffect, useMemo } from 'react';
import {
  Button,
  Input,
  Select,
  Segmented,
  Typography,
  theme,
  Alert,
} from 'antd';
import {
  PlusOutlined,
  DeleteOutlined,
  UnorderedListOutlined,
  CodeOutlined,
} from '@ant-design/icons';

const { TextArea } = Input;
const { Text } = Typography;

export interface ModelMappingEntry {
  callerModel: string;
  upstreamModel: string;
}

export interface ModelMappingEditorValue {
  entries: ModelMappingEntry[];
}

interface ModelMappingEditorProps {
  value?: ModelMappingEditorValue;
  onChange?: (value: ModelMappingEditorValue) => void;
  modelOptions?: string[];
}

// --- Conversion helpers ---
// 新契约：Channel.models 为 string[]，Channel.model_mapping 为 Record<string,string>。

export function toFormValue(
  models: string[] | undefined,
  modelMapping: Record<string, string> | undefined,
): ModelMappingEditorValue {
  const list = models ?? [];
  const mapping = modelMapping ?? {};

  const entries: ModelMappingEntry[] = list.map((m) => ({
    callerModel: m,
    upstreamModel: mapping[m] ?? '',
  }));

  // 防御性处理：映射中存在但不在 models 列表内的键
  for (const key of Object.keys(mapping)) {
    if (!list.includes(key)) {
      entries.push({ callerModel: key, upstreamModel: mapping[key] });
    }
  }

  return { entries };
}

export function fromFormValue(val: ModelMappingEditorValue): {
  models: string[];
  model_mapping: Record<string, string>;
} {
  const validEntries = val.entries.filter((e) => e.callerModel.trim());
  const models = validEntries.map((e) => e.callerModel.trim());

  const model_mapping: Record<string, string> = {};
  for (const e of validEntries) {
    const upstream = e.upstreamModel.trim();
    if (upstream && upstream !== e.callerModel.trim()) {
      model_mapping[e.callerModel.trim()] = upstream;
    }
  }

  return { models, model_mapping };
}

// --- Component ---

export default function ModelMappingEditor({
  value,
  onChange,
  modelOptions = [],
}: ModelMappingEditorProps) {
  const { token: themeToken } = theme.useToken();
  const [mode, setMode] = useState<'list' | 'json'>('list');
  const [jsonText, setJsonText] = useState('');
  const [jsonError, setJsonError] = useState('');

  const entries = value?.entries ?? [];

  const fireChange = useCallback(
    (newEntries: ModelMappingEntry[]) => {
      onChange?.({ entries: newEntries });
    },
    [onChange],
  );

  // Sync entries → JSON text when switching to JSON mode
  useEffect(() => {
    if (mode === 'json') {
      const obj: Record<string, string> = {};
      for (const e of entries) {
        if (e.callerModel.trim()) {
          obj[e.callerModel.trim()] = e.upstreamModel.trim();
        }
      }
      setJsonText(JSON.stringify(obj, null, 2));
      setJsonError('');
    }
    // Only sync when mode changes to json
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  const handleAddRow = () => {
    fireChange([...entries, { callerModel: '', upstreamModel: '' }]);
  };

  const handleRemoveRow = (index: number) => {
    fireChange(entries.filter((_, i) => i !== index));
  };

  const handleEntryChange = (
    index: number,
    field: 'callerModel' | 'upstreamModel',
    val: string,
  ) => {
    const updated = entries.map((e, i) =>
      i === index ? { ...e, [field]: val } : e,
    );
    fireChange(updated);
  };

  const handleJsonChange = (text: string) => {
    setJsonText(text);
    if (!text.trim()) {
      setJsonError('');
      fireChange([]);
      return;
    }
    try {
      const parsed = JSON.parse(text);
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        setJsonError('必须是 JSON 对象，如 {"model-a": "model-b"}');
        return;
      }
      // Validate all values are strings
      for (const [k, v] of Object.entries(parsed)) {
        if (typeof v !== 'string') {
          setJsonError(`键 "${k}" 的值必须是字符串`);
          return;
        }
      }
      setJsonError('');
      const newEntries: ModelMappingEntry[] = Object.entries(
        parsed as Record<string, string>,
      ).map(([k, v]) => ({
        callerModel: k,
        upstreamModel: v,
      }));
      fireChange(newEntries);
    } catch {
      setJsonError('JSON 格式错误');
    }
  };

  // Deduplicate model options that are already selected
  const usedCallerModels = useMemo(
    () => new Set(entries.map((e) => e.callerModel)),
    [entries],
  );

  const availableModelOptions = useMemo(
    () => modelOptions.filter((m) => !usedCallerModels.has(m)),
    [modelOptions, usedCallerModels],
  );

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 8,
        }}
      >
        <Text strong>模型与映射</Text>
        <Segmented
          size="small"
          value={mode}
          onChange={(v) => setMode(v as 'list' | 'json')}
          options={[
            { value: 'list', icon: <UnorderedListOutlined />, label: '列表' },
            { value: 'json', icon: <CodeOutlined />, label: 'JSON' },
          ]}
        />
      </div>

      <Text type="secondary" style={{ display: 'block', fontSize: 12, marginBottom: 8 }}>
        调用方模型名 = 对用户展示的名称，上游模型名 = 渠道实际请求的名称（留空表示与调用方模型名相同）
      </Text>

      {mode === 'list' ? (
        <div>
          {/* Header */}
          {entries.length > 0 && (
            <div
              style={{
                display: 'flex',
                gap: 8,
                marginBottom: 4,
                paddingRight: 32,
              }}
            >
              <Text
                type="secondary"
                style={{ flex: 1, fontSize: 12 }}
              >
                调用方模型名
              </Text>
              <Text
                type="secondary"
                style={{ flex: 1, fontSize: 12 }}
              >
                上游模型名（留空 = 不映射）
              </Text>
            </div>
          )}

          {/* Rows */}
          {entries.map((entry, index) => (
            <div
              key={index}
              style={{
                display: 'flex',
                gap: 8,
                marginBottom: 4,
                alignItems: 'center',
              }}
            >
              <Select
                mode="tags"
                maxCount={1}
                style={{ flex: 1 }}
                placeholder="选择或输入模型名"
                value={entry.callerModel ? [entry.callerModel] : []}
                onChange={(vals) => {
                  const newVal = vals.length > 0 ? vals[vals.length - 1] : '';
                  handleEntryChange(index, 'callerModel', newVal);
                }}
                options={availableModelOptions.map((m) => ({
                  label: m,
                  value: m,
                }))}
                filterOption={(input, option) =>
                  (option?.label as string)
                    ?.toLowerCase()
                    .includes(input.toLowerCase())
                }
              />
              <span style={{ color: themeToken.colorTextSecondary }}>→</span>
              <Input
                style={{ flex: 1 }}
                placeholder="留空 = 不映射"
                value={entry.upstreamModel}
                onChange={(e) =>
                  handleEntryChange(index, 'upstreamModel', e.target.value)
                }
              />
              <Button
                type="text"
                danger
                icon={<DeleteOutlined />}
                onClick={() => handleRemoveRow(index)}
                style={{ flexShrink: 0 }}
              />
            </div>
          ))}

          {/* Add button */}
          <Button
            type="dashed"
            onClick={handleAddRow}
            icon={<PlusOutlined />}
            style={{ width: '100%', marginTop: 4 }}
          >
            添加模型
          </Button>
        </div>
      ) : (
        <div>
          <TextArea
            rows={8}
            value={jsonText}
            onChange={(e) => handleJsonChange(e.target.value)}
            placeholder={'{\n  "claude-sonnet-4.6": "glm-5-turbo",\n  "gpt-4o": ""\n}'}
            style={{ fontFamily: 'monospace', fontSize: 13 }}
          />
          {jsonError && (
            <Alert
              type="error"
              message={jsonError}
              style={{ marginTop: 4 }}
              showIcon
              banner
            />
          )}
          <Text
            type="secondary"
            style={{ display: 'block', fontSize: 12, marginTop: 4 }}
          >
            键 = 调用方模型名，值 = 上游模型名（空字符串表示不映射）
          </Text>
        </div>
      )}
    </div>
  );
}
