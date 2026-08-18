import { Space, DatePicker } from 'antd';
import type { Dayjs } from 'dayjs';

const { RangePicker } = DatePicker;

export type UsageDateRange = [Dayjs | null, Dayjs | null] | null;

interface Props {
  /** 透传给 RangePicker 的 data-testid，便于定位元素。 */
  testId?: string;
  value: UsageDateRange;
  onChange: (range: UsageDateRange) => void;
}

/** 用量页各 tab 共用的日期范围控件：RangePicker + 当前区间/「近 30 天」文本。
 * 收口原本在 summary/cache/heatmap/token 四处重复的同一段 JSX。 */
function UsageRangeBar({ testId, value, onChange }: Props) {
  const label =
    value?.[0] && value?.[1]
      ? `${value[0].format('YYYY-MM-DD')} ~ ${value[1].format('YYYY-MM-DD')}`
      : '近 30 天';
  return (
    <Space style={{ marginBottom: 12 }}>
      <RangePicker
        data-testid={testId}
        value={value}
        onChange={(dates) => onChange(dates as UsageDateRange)}
      />
      <span>{label}</span>
    </Space>
  );
}

export default UsageRangeBar;

/** 把日期范围归一为后端查询参数：选了区间出 start/end（自然日对齐），
 * 否则回退 days=30。供 summary/cache/heatmap/token 各 tab 复用，统一区间口径。 */
export function rangeQueryParams(range: UsageDateRange): {
  start_timestamp?: number;
  end_timestamp?: number;
  days?: number;
} {
  if (range?.[0] && range?.[1]) {
    return {
      start_timestamp: range[0].startOf('day').unix(),
      end_timestamp: range[1].endOf('day').unix(),
    };
  }
  return { days: 30 };
}
