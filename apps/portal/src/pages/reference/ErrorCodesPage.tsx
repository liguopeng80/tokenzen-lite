import { useMemo, useState } from 'react';
import { Card, Typography, Input, Table, Tag, Empty } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { RELAY_ERROR_CODES, type RelayErrorCodeDef } from '@token-zen/shared/constants';

const { Title, Paragraph, Text } = Typography;

/**
 * 错误码 HTTP 状态颜色：4xx 客户端侧（橙），5xx 服务端侧（红）。
 * 颜色只用于视觉分组，不承载额外语义。
 */
function httpTagColor(http: number): string {
  if (http >= 500) return 'red';
  if (http >= 400) return 'orange';
  return 'default';
}

function ErrorCodesPage() {
  const [keyword, setKeyword] = useState('');

  const filtered = useMemo<RelayErrorCodeDef[]>(() => {
    const k = keyword.trim().toLowerCase();
    if (!k) return [...RELAY_ERROR_CODES];
    return RELAY_ERROR_CODES.filter(
      (d) =>
        d.code.toLowerCase().includes(k) ||
        d.meaning.toLowerCase().includes(k) ||
        d.action.toLowerCase().includes(k) ||
        String(d.http).includes(k),
    );
  }, [keyword]);

  const columns: ColumnsType<RelayErrorCodeDef> = [
    {
      title: 'HTTP',
      dataIndex: 'http',
      key: 'http',
      width: 72,
      render: (http: number) => <Tag color={httpTagColor(http)}>{http}</Tag>,
    },
    {
      title: '业务码',
      dataIndex: 'code',
      key: 'code',
      width: 220,
      render: (code: string) => <Text code>{code}</Text>,
    },
    {
      title: '含义',
      dataIndex: 'meaning',
      key: 'meaning',
      render: (meaning: string) => <span style={{ fontSize: 13 }}>{meaning}</span>,
    },
    {
      title: '建议动作',
      dataIndex: 'action',
      key: 'action',
      width: 280,
      render: (action: string) => <span style={{ fontSize: 13 }}>{action}</span>,
    },
  ];

  return (
    <div style={{ maxWidth: 1100 }}>
      <Title level={4} style={{ marginTop: 0 }}>错误码自助诊断</Title>

      <Card style={{ marginBottom: 16 }}>
        <Paragraph type="secondary" style={{ marginBottom: 12 }}>
          调用 <Text code>/v1</Text> 下游端点（<Text code>/v1/chat/completions</Text>、
          <Text code>/v1/messages</Text>、<Text code>/v1/embeddings</Text>、
          <Text code>/v1/images/generations</Text>）返回错误时，先在此查码值对应的含义与建议动作。
          请求失败按响应体里的 <Text code>type</Text>/<Text code>code</Text> 字段定位。
        </Paragraph>
        <Input.Search
          data-testid="error-code-search"
          allowClear
          placeholder="按码值、HTTP、含义或动作关键词过滤（如 rate_limited、429、限流、充值）"
          onChange={(e) => setKeyword(e.target.value)}
          style={{ maxWidth: 520 }}
        />
      </Card>

      <Card>
        <Table<RelayErrorCodeDef>
          data-testid="error-code-table"
          rowKey="code"
          size="middle"
          columns={columns}
          dataSource={filtered}
          pagination={false}
          locale={{
            emptyText: <Empty description="未匹配到错误码，换个关键词试试" image={Empty.PRESENTED_IMAGE_SIMPLE} />,
          }}
        />
        <Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0, fontSize: 12 }}>
          另有一种非固定码：上游返回不可换渠道重试的 4xx（非 401/402/403/429，如参数校验错误）时，
          状态码与响应体原样透传，业务码即上游原码；响应体无 <Text code>error</Text> 字段时包装为
          <Text code> upstream_error</Text>。详见 API 契约。
        </Paragraph>
      </Card>
    </div>
  );
}

export default ErrorCodesPage;
