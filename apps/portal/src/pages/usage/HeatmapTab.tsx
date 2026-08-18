import { Card } from 'antd';
import type { HeatmapCell, HeatmapResponse } from '@token-zen/shared';
import { Heatmap } from '@token-zen/shared';
import { useAsync } from '@token-zen/shared/hooks';
import { usageApi } from '@/api/usage';
import { rangeQueryParams, type UsageDateRange } from './UsageRangeBar';

interface Props {
  dateRange: UsageDateRange;
}

/** 活跃时段 Tab：当前用户调用按周×时的分布热力图。
 * 日期范围控件与区间文本由父级 UsageRangeBar 统一展示，此处只渲染热力图。 */
function HeatmapTab({ dateRange }: Props) {
  const { data, loading } = useAsync<HeatmapResponse>(
    () => usageApi.heatmap(rangeQueryParams(dateRange)),
    [dateRange],
  );
  const cells: HeatmapCell[] = data?.cells ?? [];

  return (
    <Card>
      <Heatmap cells={cells} loading={loading} />
    </Card>
  );
}

export default HeatmapTab;
