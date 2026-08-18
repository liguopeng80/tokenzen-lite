import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Badge, Button, Card, Input, Spin, Table, Typography } from 'antd';
import { message, modal } from '@token-zen/shared';
import { ReloadOutlined, SaveOutlined, UndoOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { SettingItem } from '@token-zen/shared';
import { settingsApi } from '@/api/settings';
import { useAuthStore } from '@/stores/auth';
import { SettingValueEditor } from './SettingValueEditor';
import { useUnsavedChanges } from '@/stores/unsavedChanges';

const { Title, Text } = Typography;

function SettingsPage() {
  const currentUser = useAuthStore((s) => s.user);
  const isRoot = currentUser?.role === 'root';

  const [items, setItems] = useState<SettingItem[]>([]);
  const [localValues, setLocalValues] = useState<Record<string, number | boolean | string>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [forbidden, setForbidden] = useState(false);
  const touchedKeysRef = useRef<Set<string>>(new Set());

  const fetchSettings = useCallback(async () => {
    if (!isRoot) return;
    setLoading(true);
    setForbidden(false);
    try {
      const data = await settingsApi.list();
      setItems(data);
      const map: Record<string, number | boolean | string> = {};
      for (const item of data) map[item.key] = item.value;
      setLocalValues(map);
      touchedKeysRef.current = new Set();
    } catch (err: unknown) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        message.error('加载系统设置失败');
      }
    } finally {
      setLoading(false);
    }
  }, [isRoot]);

  useEffect(() => {
    fetchSettings();
  }, [fetchSettings]);

  const handleChange = useCallback((key: string, value: number | boolean | string) => {
    touchedKeysRef.current.add(key);
    setLocalValues((prev) => ({ ...prev, [key]: value }));
  }, []);

  const pendingChanges = useMemo(() => {
    const changes: Record<string, number | boolean | string> = {};
    for (const key of touchedKeysRef.current) {
      const local = localValues[key];
      const server = items.find((i) => i.key === key)?.value;
      if (local !== undefined && local !== server) {
        changes[key] = local;
      }
    }
    return changes;
  }, [localValues, items]);

  const pendingCount = Object.keys(pendingChanges).length;
  // 登记待保存数量：侧栏导航与浏览器关闭据此拦截，避免改动静默丢失。
  useUnsavedChanges(pendingCount);

  const handleSaveAll = async () => {
    // 货币符号是全局展示口径，且不进行汇率换算：改动一旦生效会改变全体界面的符号，
    // 保存前必须显式确认，避免管理员误以为是币种/汇率切换。
    if (pendingChanges.currency_symbol !== undefined) {
      const ok = await new Promise<boolean>((resolve) => {
        modal.confirm({
          title: '确认修改货币符号？',
          content: '货币符号修改仅影响展示，不进行汇率换算；保存后全体界面的货币符号将统一改变。',
          okText: '确认保存',
          cancelText: '取消',
          onOk: () => resolve(true),
          onCancel: () => resolve(false),
        });
      });
      if (!ok) return;
    }
    setSaving(true);
    const entries = Object.entries(pendingChanges);
    let successCount = 0;
    let failCount = 0;
    for (const [key, value] of entries) {
      try {
        await settingsApi.update(key, value);
        successCount++;
      } catch {
        failCount++;
      }
    }
    if (failCount === 0) {
      message.success(`已保存 ${successCount} 项配置`);
    } else {
      message.warning(`${successCount} 项成功，${failCount} 项失败`);
    }
    setSaving(false);
    await fetchSettings();
  };

  const handleDiscard = () => {
    touchedKeysRef.current = new Set();
    const map: Record<string, number | boolean | string> = {};
    for (const item of items) map[item.key] = item.value;
    setLocalValues(map);
  };

  const filteredItems = useMemo(() => {
    if (!keyword.trim()) return items;
    const kw = keyword.toLowerCase();
    return items.filter(
      (item) => item.key.toLowerCase().includes(kw) || item.describe.toLowerCase().includes(kw),
    );
  }, [items, keyword]);

  const columns: ColumnsType<SettingItem> = [
    {
      title: '配置项',
      dataIndex: 'key',
      width: 280,
      render: (v: string) => (
        <Text code copyable={{ text: v }} style={{ fontSize: 13 }}>
          {v}
        </Text>
      ),
    },
    { title: '说明', dataIndex: 'describe', render: (v: string) => v || '-' },
    {
      title: '当前值',
      key: 'value',
      width: 320,
      render: (_: unknown, record: SettingItem) => (
        <SettingValueEditor
          settingKey={record.key}
          kind={record.kind}
          secret={record.secret}
          options={record.options}
          value={localValues[record.key] ?? record.value}
          onChange={(v) => handleChange(record.key, v)}
        />
      ),
    },
    {
      title: '默认值',
      dataIndex: 'default',
      width: 120,
      render: (v: number | boolean | string, record: SettingItem) =>
        record.secret ? <Text type="secondary">—</Text> : <Text type="secondary">{String(v)}</Text>,
    },
    {
      title: '',
      key: 'changed',
      width: 70,
      render: (_: unknown, record: SettingItem) =>
        pendingChanges[record.key] !== undefined ? <Tag>已修改</Tag> : null,
    },
  ];

  if (!isRoot) {
    return (
      <div>
        <Title level={4} style={{ marginTop: 0 }}>
          系统设置
        </Title>
        <Alert type="warning" showIcon message="权限不足" description="系统设置仅超级管理员可查看和修改。" />
      </div>
    );
  }

  if (forbidden) {
    return (
      <div>
        <Title level={4} style={{ marginTop: 0 }}>
          系统设置
        </Title>
        <Alert type="error" showIcon message="无权访问" description="服务端拒绝了本次请求，请确认账号权限。" />
      </div>
    );
  }

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            系统设置
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            管理系统运行参数，修改后需点击保存才会生效
          </Text>
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Input.Search
            placeholder="搜索配置项"
            onChange={(e) => setKeyword(e.target.value)}
            allowClear
            style={{ width: 220 }}
          />
          <Button icon={<ReloadOutlined />} onClick={fetchSettings} loading={loading}>
            刷新
          </Button>
          {pendingCount > 0 && (
            <>
              <Button icon={<UndoOutlined />} onClick={handleDiscard}>
                撤销
              </Button>
              <Badge count={pendingCount} size="small">
                <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={handleSaveAll}>
                  保存修改
                </Button>
              </Badge>
            </>
          )}
        </div>
      </div>

      <Card>
        <Spin spinning={loading}>
          <Table
            columns={columns}
            dataSource={filteredItems}
            rowKey="key"
            pagination={{ pageSize: 20, showSizeChanger: true }}
            size="small"
          />
        </Spin>
      </Card>
    </div>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        color: '#ED7B2F',
        fontSize: 12,
        fontWeight: 600,
        background: 'rgba(237,123,47,0.1)',
        padding: '1px 6px',
        borderRadius: 4,
      }}
    >
      {children}
    </span>
  );
}

export default SettingsPage;
