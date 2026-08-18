import { useEffect, useState } from 'react';
import { Alert, Button, Card, List, Skeleton, Tag, Typography } from 'antd';
import { CheckCircleFilled, ExclamationCircleFilled, RocketOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import type { SetupStatus } from '@token-zen/shared';
import { dashboardApi } from '@/api/dashboard';
import { semantic, warmGray } from '@token-zen/shared/theme';

const { Text } = Typography;

/**
 * 首次配置引导。新装系统在管理员配置完成前，任何 /v1 调用都必然被拒绝，
 * 而仪表盘的统计卡片此时全是零值，不指向下一步。本卡片列出未完成的配置项、
 * 各项缺失时的业务后果与对应页面入口。
 *
 * 三态：加载中显示骨架；必需项全部完成且可选项也完成时整体不渲染；
 * 其余情况列出全部检查项（已完成项一并列出，使管理员看得到进度）。
 */
function SetupGuide() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    dashboardApi
      .setupStatus()
      .then(setStatus)
      .catch(() => setStatus(null))
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <Card style={{ marginBottom: 16 }}>
        <Skeleton active paragraph={{ rows: 3 }} />
      </Card>
    );
  }
  // 查询失败或全部配置到位时不占用仪表盘版面。
  if (!status || status.checks.every((c) => c.done)) return null;

  return (
    <Card
      style={{ marginBottom: 16 }}
      title={
        <span>
          <RocketOutlined /> 配置引导
        </span>
      }
    >
      <Alert
        type={status.completed ? 'info' : 'warning'}
        showIcon
        style={{ marginBottom: 16 }}
        message={
          status.completed
            ? '必需配置已全部完成，员工可以开始调用。以下为建议补齐的可选项。'
            : `还有 ${status.pending_count} 项必需配置未完成，在此之前员工的每次调用都会被拒绝。`
        }
      />
      <List
        itemLayout="horizontal"
        dataSource={status.checks}
        renderItem={(item) => (
          <List.Item
            actions={
              item.done
                ? []
                : [
                    <Button key="go" type="link" onClick={() => navigate(item.path)}>
                      {item.action}
                    </Button>,
                  ]
            }
          >
            <List.Item.Meta
              avatar={
                item.done ? (
                  <CheckCircleFilled style={{ fontSize: 18, color: semantic.success }} />
                ) : (
                  <ExclamationCircleFilled
                    style={{ fontSize: 18, color: item.required ? semantic.warning : warmGray[300] }}
                  />
                )
              }
              title={
                <span>
                  {item.title}
                  {!item.required && (
                    <Tag style={{ marginLeft: 8 }} color="default">
                      可选
                    </Tag>
                  )}
                </span>
              }
              description={
                <Text type="secondary" style={{ fontSize: 13 }}>
                  {item.detail}
                </Text>
              }
            />
          </List.Item>
        )}
      />
    </Card>
  );
}

export default SetupGuide;
