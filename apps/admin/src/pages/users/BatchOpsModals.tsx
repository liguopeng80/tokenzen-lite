import { useState } from 'react';
import { Alert, Button, Form, Input, InputNumber, Modal, Radio, Select, Space, Table, Tag, Typography } from 'antd';
import { message } from '@token-zen/shared';
import type {
  BatchGrantSummary,
  DepartmentWithStats,
  ImportAction,
  UserImportSummary,
} from '@token-zen/shared';
import { ImportActionLabel } from '@token-zen/shared';
import { batchApi, type UserImportItem } from '@/api/organization';
import { errorMessageOf } from '@/api/error';

const { Text, Paragraph } = Typography;

interface BatchOpsModalsProps {
  departments: DepartmentWithStats[];
  importOpen: boolean;
  onImportClose: () => void;
  grantOpen: boolean;
  onGrantClose: () => void;
  onDone: () => void;
}

const actionColor: Record<ImportAction, string> = {
  created: 'green',
  updated: 'blue',
  skipped: 'default',
  failed: 'red',
};

const importPlaceholder = `zhangsan,,张三,zhangsan@example.com,50000
lisi,Password12345,李四,lisi@example.com,50000`;

/**
 * parseImportText 把每行 CSV 解析为一条导入记录。
 * 列序：用户名, 密码, 显示名, 邮箱, 初始积分；后三列可省略。
 * 行内解析错误不在此拦截——服务端逐条校验并逐条回报，前端拦截会让
 * 「哪一行有问题」的判定出现两套标准。
 */
function parseImportText(text: string): UserImportItem[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [username = '', password = '', displayName = '', email = '', credits = ''] = line
        .split(',')
        .map((cell) => cell.trim());
      return {
        username,
        password,
        display_name: displayName,
        email,
        initial_credits: Number(credits) || 0,
      };
    });
}

function BatchOpsModals({
  departments,
  importOpen,
  onImportClose,
  grantOpen,
  onGrantClose,
  onDone,
}: BatchOpsModalsProps) {
  const [importText, setImportText] = useState('');
  const [importDept, setImportDept] = useState<number | undefined>();
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<UserImportSummary | null>(null);

  const [grantForm] = Form.useForm();
  const [grantTarget, setGrantTarget] = useState<'department' | 'user_ids'>('department');
  const [granting, setGranting] = useState(false);
  const [grantResult, setGrantResult] = useState<BatchGrantSummary | null>(null);

  const handleImport = async () => {
    const items = parseImportText(importText);
    if (items.length === 0) {
      message.warning('请先粘贴要导入的用户');
      return;
    }
    setImporting(true);
    try {
      const summary = await batchApi.importUsers(items, importDept ?? null);
      setImportResult(summary);
      if (summary.created > 0) {
        message.success(`已创建 ${summary.created} 个账号`);
        onDone();
      }
    } catch (err) {
      message.error(errorMessageOf(err, '批量导入失败'));
    } finally {
      setImporting(false);
    }
  };

  const closeImport = () => {
    setImportResult(null);
    setImportText('');
    onImportClose();
  };

  const handleGrant = async () => {
    const values = await grantForm.validateFields();
    setGranting(true);
    try {
      const summary = await batchApi.grantCredits({
        amount: Math.round(values.amount),
        note: values.note ?? '',
        idempotency_key: values.idempotency_key || undefined,
        ...(grantTarget === 'department'
          ? { department_id: values.department_id }
          : {
              user_ids: String(values.user_ids ?? '')
                .split(/[\s,]+/)
                .map((id: string) => Number(id))
                .filter((id: number) => Number.isFinite(id) && id > 0),
            }),
      });
      setGrantResult(summary);
      if (summary.succeeded > 0 || summary.replayed > 0) {
        message.success(`成功 ${summary.succeeded} 个，重复提交跳过 ${summary.replayed} 个`);
        onDone();
      }
    } catch (err) {
      message.error(errorMessageOf(err, '批量发放失败'));
    } finally {
      setGranting(false);
    }
  };

  const closeGrant = () => {
    setGrantResult(null);
    grantForm.resetFields();
    onGrantClose();
  };

  return (
    <>
      <Modal
        title="批量导入用户"
        open={importOpen}
        onCancel={closeImport}
        width={720}
        footer={[
          <Button key="close" onClick={closeImport}>
            关闭
          </Button>,
          <Button key="submit" type="primary" loading={importing} onClick={handleImport}>
            开始导入
          </Button>,
        ]}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="每行一个用户，列序：用户名, 密码, 显示名, 邮箱, 初始积分"
          description="密码列留空时由系统生成一次性初始密码，导入结果中逐行给出，只展示这一次。显示名、邮箱、初始积分可省略。用户名已存在的记录会被跳过，不会改动既有账号的密码与归属；单条失败不影响同批其余记录。"
        />
        <Space direction="vertical" style={{ width: '100%' }} size="middle">
          <Select
            placeholder="默认部门（可留空）"
            allowClear
            style={{ width: 240 }}
            value={importDept}
            onChange={setImportDept}
            options={departments
              .filter((d) => d.status === 'enabled')
              .map((d) => ({ label: d.name, value: d.id }))}
          />
          <Input.TextArea
            rows={8}
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            placeholder={importPlaceholder}
          />
        </Space>

        {importResult && (
          <div style={{ marginTop: 16 }}>
            <Paragraph>
              新建 <Text strong>{importResult.created}</Text> 个，跳过{' '}
              <Text strong>{importResult.skipped}</Text> 个，失败{' '}
              <Text strong type={importResult.failed > 0 ? 'danger' : undefined}>
                {importResult.failed}
              </Text>{' '}
              个。
            </Paragraph>
            <Table
              size="small"
              rowKey={(row) => row.username}
              dataSource={importResult.results}
              pagination={{ pageSize: 8 }}
              columns={[
                { title: '用户名', dataIndex: 'username' },
                {
                  title: '结果',
                  dataIndex: 'action',
                  width: 100,
                  render: (v: ImportAction) => (
                    <Tag color={actionColor[v]}>{ImportActionLabel[v]}</Tag>
                  ),
                },
                {
                  title: '初始密码',
                  dataIndex: 'initial_password',
                  width: 180,
                  // 只对系统生成的行有值；管理员自行指定密码的行不回显。
                  render: (v?: string) =>
                    v ? (
                      <Text code copyable={{ text: v }}>
                        {v}
                      </Text>
                    ) : (
                      <Text type="secondary">—</Text>
                    ),
                },
                { title: '说明', dataIndex: 'message' },
              ]}
            />
            <Paragraph type="secondary" style={{ marginTop: 8 }}>
              初始密码只在此处展示一次，关闭本窗口后无法再次取得；导入的账号首次登录都必须自行改密。
            </Paragraph>
          </div>
        )}
      </Modal>

      <Modal
        title="批量发放积分"
        open={grantOpen}
        onCancel={closeGrant}
        width={640}
        footer={[
          <Button key="close" onClick={closeGrant}>
            关闭
          </Button>,
          <Button key="submit" type="primary" loading={granting} onClick={handleGrant}>
            确认发放
          </Button>,
        ]}
      >
        <Form form={grantForm} layout="vertical">
          <Form.Item label="发放对象">
            <Radio.Group value={grantTarget} onChange={(e) => setGrantTarget(e.target.value)}>
              <Radio.Button value="department">按部门</Radio.Button>
              <Radio.Button value="user_ids">按用户 ID</Radio.Button>
            </Radio.Group>
          </Form.Item>

          {grantTarget === 'department' ? (
            <Form.Item
              name="department_id"
              label="部门"
              rules={[{ required: true, message: '请选择部门' }]}
              extra="向该部门当前全部成员各发放一笔，每人一条独立流水"
            >
              <Select
                options={departments.map((d) => ({
                  label: `${d.name}（${d.member_count} 人）`,
                  value: d.id,
                }))}
              />
            </Form.Item>
          ) : (
            <Form.Item
              name="user_ids"
              label="用户 ID"
              rules={[{ required: true, message: '请填写用户 ID' }]}
              extra="多个 ID 以逗号、空格或换行分隔"
            >
              <Input.TextArea rows={3} placeholder="12, 15, 20" />
            </Form.Item>
          )}

          <Form.Item
            name="amount"
            label="每人发放积分"
            rules={[{ required: true, message: '请填写发放积分' }]}
            extra="正数为发放，负数为扣回；扣回超过某人余额时该人失败，不影响其余人"
          >
            <InputNumber precision={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input placeholder="如：2026 年三季度额度" />
          </Form.Item>
          <Form.Item
            name="idempotency_key"
            label="幂等键（选填）"
            extra="填写后，用同一个键重复提交只会记一次账，可防止误触或网络重试造成重复发放。建议用业务含义命名，如 q3-2026-alloc。"
            rules={[
              {
                pattern: /^[A-Za-z0-9_-]{1,64}$/,
                message: '幂等键须为 1-64 位字母、数字、下划线或连字符',
              },
            ]}
          >
            <Input placeholder="q3-2026-alloc" />
          </Form.Item>
        </Form>

        {grantResult && (
          <div>
            <Paragraph>
              成功 <Text strong>{grantResult.succeeded}</Text> 个，重复提交跳过{' '}
              <Text strong>{grantResult.replayed}</Text> 个，失败{' '}
              <Text strong type={grantResult.failed > 0 ? 'danger' : undefined}>
                {grantResult.failed}
              </Text>{' '}
              个。
            </Paragraph>
            <Table
              size="small"
              rowKey="user_id"
              dataSource={grantResult.results}
              pagination={{ pageSize: 8 }}
              columns={[
                { title: '用户 ID', dataIndex: 'user_id', width: 100 },
                {
                  title: '结果',
                  dataIndex: 'ok',
                  width: 90,
                  render: (ok: boolean, row) => (
                    <Tag color={ok ? (row.replay ? 'default' : 'green') : 'red'}>
                      {ok ? (row.replay ? '已记账' : '成功') : '失败'}
                    </Tag>
                  ),
                },
                { title: '说明', dataIndex: 'message' },
              ]}
            />
          </div>
        )}
      </Modal>
    </>
  );
}

export default BatchOpsModals;
