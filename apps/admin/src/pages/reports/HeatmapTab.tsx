import { useCallback, useEffect, useState } from 'react';
import { Button, Card, DatePicker, Input, InputNumber, Space } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import dayjs, { type Dayjs } from 'dayjs';
import type { HeatmapCell } from '@token-zen/shared';
import { Heatmap } from '@token-zen/shared';
import { reportApi } from '@/api/organization';
import { message } from '@token-zen/shared';
import { errorMessageOf } from '@/api/error';

const { RangePicker } = DatePicker;

/** 管理端活跃时段热力图 Tab：全站或按 user_id/model 收窄的周×时分布。
 * 与费用分摊 Tab 共用同一组时间范围与筛选风格。 */
function HeatmapTab() {
  const [range, setRange] = useState<[Dayjs, Dayjs]>([dayjs().subtract(30, 'day'), dayjs()]);
  const [userId, setUserId] = useState<number | undefined>();
  const [model, setModel] = useState<string | undefined>();
  const [cells, setCells] = useState<HeatmapCell[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await reportApi.heatmap({
        start_timestamp: range[0].startOf('day').unix(),
        end_timestamp: range[1].endOf('day').unix(),
        ...(userId !== undefined ? { user_id: userId } : {}),
        ...(model ? { model } : {}),
      });
      setCells(data?.cells ?? []);
    } catch (err) {
      message.error(errorMessageOf(err, '活跃时段查询失败'));
      setCells([]);
    } finally {
      setLoading(false);
    }
  }, [range, userId, model]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div>
      <Space style={{ marginBottom: 16 }} wrap>
        <RangePicker
          data-testid="heatmap-date-range"
          value={range}
          allowClear={false}
          onChange={(values) => {
            if (values && values[0] && values[1]) setRange([values[0], values[1]]);
          }}
        />
        <InputNumber
          data-testid="heatmap-user-id"
          placeholder="用户 ID（可选）"
          style={{ width: 160 }}
          min={0}
          value={userId}
          onChange={(v) => setUserId(v === null ? undefined : (v as number))}
        />
        <Input
          data-testid="heatmap-model"
          placeholder="模型名（可选）"
          style={{ width: 200 }}
          allowClear
          value={model}
          onChange={(e) => setModel(e.target.value || undefined)}
        />
        <Button icon={<ReloadOutlined />} onClick={load}>
          刷新
        </Button>
      </Space>
      <Card>
        <Heatmap cells={cells} loading={loading} />
      </Card>
    </div>
  );
}

export default HeatmapTab;
