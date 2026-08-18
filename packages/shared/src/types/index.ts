// Token Zen Lite 实体类型定义。与后端 server/internal/store 的 GORM 模型一一对应，
// 枚举取值以 docs/glossary.md 为权威定义。

// ---- 响应信封 ----

export interface ApiResponse<T = unknown> {
  success: boolean;
  message: string;
  data?: T;
}

export interface PaginatedData<T> {
  page: number;
  page_size: number;
  total: number;
  items: T[];
}

// ---- 用户与认证 ----

export type Role = 'user' | 'managed' | 'admin' | 'root';
export type UserStatus = 'enabled' | 'disabled';

export interface User {
  id: number;
  username: string;
  display_name: string;
  email: string;
  role: Role;
  status: UserStatus;
  credit_balance: number;
  credit_used: number;
  request_count: number;
  /** 所属部门，null 表示未分配 */
  department_id: number | null;
  /** 管理员维护的用户级模型策略，空表示该层不施加限制 */
  allowed_models: string[] | null;
  /** 单自然日累计扣费积分上限，0 表示不限制 */
  daily_spend_limit: number;
  /**
   * 密码由管理员设定、本人尚未改过。为真时除改密与读取自身外的接口一律 403，
   * 前端须先引导本人改密。
   */
  must_change_password: boolean;
  created_at: string;
  updated_at: string;
  /**
   * 当前用户担任负责人的部门 ID 列表，仅 /auth/me 返回。
   * 非空表示可访问 /api/dept 下的本部门费用视图，前端据此决定是否显示入口。
   */
  managed_department_ids?: number[];
}

/** 部门负责人视角的部门条目（GET /dept/departments）。 */
export interface ManagedDepartment {
  id: number;
  name: string;
  code: string;
  status: DepartmentStatus;
  /** 月度预算积分，0 表示未设预算，不做超预算判定 */
  monthly_budget_credits: number;
}

/** 本部门当月消费与预算对比（GET /dept/budget）。 */
export interface DeptBudget {
  /** 自然月，格式 YYYY-MM */
  month: string;
  department_id: number;
  department_name: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  credits_charged: number;
  monthly_budget_credits: number;
  budget_used_percent: number;
  over_budget: boolean;
}

/** 部门负责人可用的费用聚合维度，不含渠道与部门。 */
export type DeptAggDimension = 'user' | 'model' | 'day' | 'project';

/** 部门费用明细行，不含网关采购成本与差额。 */
export interface DeptAggRow {
  group_id: number;
  group_key: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  credits_charged: number;
}

/** 本部门费用明细（GET /dept/cost-report）。 */
export interface DeptCostReport {
  group_by: DeptAggDimension;
  department_id: number;
  department_name: string;
  /** Unix 秒 */
  from: number;
  to: number;
  rows: DeptAggRow[];
}

/** 部门成员行（GET /dept/members）。 */
export interface DeptMember {
  user_id: number;
  username: string;
  display_name: string;
  status: UserStatus;
  credit_balance: number;
  month_credits_charged: number;
  month_requests: number;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface RegisterRequest {
  username: string;
  password: string;
  display_name?: string;
}

// ---- API Key ----

export type KeyStatus = 'enabled' | 'disabled' | 'expired' | 'depleted';

export interface ApiKey {
  id: number;
  user_id: number;
  name: string;
  key_prefix: string;
  status: KeyStatus;
  credit_limit: number | null;
  credit_used: number;
  /** 该 Key 单自然日累计扣费积分上限（0 = 不限），与用户级每日上限并行生效 */
  daily_spend_limit: number;
  /** 归属项目 ID，null 表示未归属项目（与部门正交的成本归属维度） */
  project_id: number | null;
  expires_at: string | null;
  allowed_models: string[] | null;
  allowed_ips: string[] | null;
  last_used_at: string | null;
  created_at: string;
  updated_at: string;
}

/** 创建响应中带一次性明文 key */
export interface CreatedApiKey extends ApiKey {
  key: string;
}

// ---- 渠道 ----

export type Provider =
  | 'openai' | 'anthropic' | 'gemini' | 'zhipu' | 'qwen'
  | 'deepseek' | 'minimax' | 'moonshot' | 'xai' | 'custom';
export type ChannelProtocol = 'openai_compat' | 'anthropic' | 'gemini';
export type ChannelStatus = 'enabled' | 'manual_disabled' | 'auto_disabled';

export interface Channel {
  id: number;
  name: string;
  provider: Provider;
  protocol: ChannelProtocol;
  base_url: string;
  models: string[];
  model_mapping: Record<string, string>;
  status: ChannelStatus;
  priority: number;
  weight: number;
  param_override: Record<string, unknown>;
  header_override: Record<string, string>;
  test_model: string;
  last_test_at: string | null;
  last_test_latency_ms: number | null;
  last_test_status: 'success' | 'failure' | null;
  disabled_reason: string;
  created_at: string;
  updated_at: string;
}

export type CostCurrency = 'credits' | 'usd';

export interface ChannelCost {
  id: number;
  channel_id: number;
  model_name: string;
  currency: CostCurrency;
  input_cost: number;
  output_cost: number;
  cache_read_cost: number;
  cache_write_cost: number;
  per_call_cost: number;
  updated_at: string;
}

// ---- 模型与定价 ----

export type Modality = 'text' | 'embedding' | 'image';
export type BillingMode = 'per_token' | 'per_call';
export type ModelStatus = 'enabled' | 'disabled';

/** 单价单位：token 类为 积分/1M tokens；per_call 为 积分/次 */
export interface ModelPrice {
  model_id: number;
  input_price: number;
  output_price: number;
  cache_read_price: number;
  cache_write_price: number;
  audio_input_price: number;
  audio_output_price: number;
  per_call_price: number;
  updated_at: string;
}

export interface ModelPeakRule {
  id: number;
  model_id: number;
  timezone: string;
  start_minute: number;
  end_minute: number;
  days_of_week: number[];
  multiplier_percent: number;
  enabled: boolean;
}

/**
 * 模型能力标签（权威定义见 docs/glossary.md「模型能力属性」节）。
 * 只标注非常配、有选型区分度的能力；工具调用、流式等全系标配不标注，
 * 以免选型清单噪音。多模态能力在此表达，不扩展 Modality 枚举。
 */
export type Capability = 'vision' | 'video' | 'audio' | 'reasoning';

export interface ModelInfo {
  id: number;
  name: string;
  display_name: string;
  description: string;
  modality: Modality;
  billing_mode: BillingMode;
  status: ModelStatus;
  tags: string;
  /** 所属厂商（受控枚举值，权威定义见 docs/glossary.md 的 Provider 节） */
  provider?: string;
  /** 上下文窗口（token 数）；0 表示未知，≥1,000,000 视为 1M */
  context_window?: number;
  /** 最大输出（token 数）；0 表示未知或沿用上游默认 */
  max_output?: number;
  /** 能力标签（取值见 Capability，权威定义见 docs/glossary.md） */
  capabilities?: string[];
  /** 模型级全局唯一对外短名（如 opus→claude-opus-5）；空串表示无别名 */
  alias?: string;
  created_at: string;
  updated_at: string;
  price?: ModelPrice;
  peak_rules?: ModelPeakRule[];
  /** 公开目录返回：是否存在启用渠道承载（false 时调用必然失败，前端标注"暂不可用"） */
  available?: boolean;
  /** 管理端列表返回：启用渠道承载数（0 表示上架后用户调用必然失败） */
  channel_count?: number;
}

// ---- 积分流水与兑换码 ----

export type LedgerEntryType =
  | 'grant' | 'revoke' | 'redeem' | 'consume' | 'refund' | 'settle_adjust';

export interface LedgerEntry {
  id: number;
  user_id: number;
  entry_type: LedgerEntryType;
  amount: number;
  balance_after: number;
  ref_type: string;
  ref_id: number;
  request_id: string;
  note: string;
  created_at: string;
}

/**
 * 员工视角的一条账目：同一次调用的预扣与结算差额已合并为一条净额。
 * `GET /api/me/ledger` 的默认返回形态（`view=raw` 返回未合并的 LedgerEntry）。
 */
export interface MergedLedgerRow {
  id: number;
  /** 非空表示这是一次调用；管理员发放、兑换码充值等单条账目为空。 */
  request_id: string;
  entry_type: LedgerEntryType;
  /** 净额：本次调用实际扣掉的积分。 */
  amount: number;
  balance_after: number;
  note: string;
  created_at: string;
  /** 构成本行的原始流水，按时间升序，供展开查看内部记账过程。 */
  entries: LedgerEntry[];
}

/** 按月自动发放积分的口径，与后端 domain.MonthlyGrantMode 一致。 */
export type MonthlyGrantMode = 'topup' | 'add';

/**
 * 兑换码状态。前三个是存储态；expired 只出现在 effective_status，
 * 由后端按过期时间推导，不能作为 status 写回。
 */
export type RedemptionStatus = 'unused' | 'used' | 'disabled' | 'expired';

/** 可写回的存储态，用于「作废/恢复」这类状态变更接口。 */
export type RedemptionStoredStatus = Exclude<RedemptionStatus, 'expired'>;

export interface Redemption {
  id: number;
  batch_id: string;
  name: string;
  credits: number;
  status: RedemptionStoredStatus;
  /** 展示态：未使用但已过期时为 expired，界面一律按它显示与筛选。 */
  effective_status: RedemptionStatus;
  used_by_user_id: number | null;
  redeemed_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface RedemptionBatchResult {
  batch_id: string;
  codes: string[];
}

// ---- 用量日志 ----

export type UsageStatus = 'settled' | 'refunded' | 'failed';
export type UsageSemantic = 'openai' | 'anthropic' | 'gemini' | '';

export interface UsageLog {
  id: number;
  request_id: string;
  user_id: number;
  api_key_id: number;
  model_name: string;
  upstream_model: string;
  channel_id: number;
  protocol: string;
  is_stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  audio_input_tokens: number;
  audio_output_tokens: number;
  call_count: number;
  usage_semantic: UsageSemantic;
  usage_estimated: boolean;
  peak_multiplier_percent: number;
  credits_precharged: number;
  credits_charged: number;
  credits_cost: number;
  status: UsageStatus;
  error_class: string;
  error_message: string;
  latency_ms: number;
  first_byte_ms: number;
  client_ip: string;
  price_snapshot: Record<string, unknown> | null;
  created_at: string;
  /** 用户名。仅管理端列表接口返回；用户已删除时为空串。 */
  username?: string;
  /** 渠道名称。仅管理端列表接口返回；渠道已删除时为空串。 */
  channel_name?: string;
}

/**
 * 用户侧用量日志行：/me/usage-logs 列表与详情的脱敏口径。
 * 剥离运营字段（渠道、采购成本、差额、价格快照、上游路由、协议、计费中间态、
 * 接入方、客户端 IP），只含本人请求的 token 明细、扣费、耗时、状态与时间。
 */
export interface MeUsageLog {
  id: number;
  request_id: string;
  user_id: number;
  api_key_id: number;
  model_name: string;
  is_stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  audio_input_tokens: number;
  audio_output_tokens: number;
  call_count: number;
  usage_estimated: boolean;
  credits_charged: number;
  status: UsageStatus;
  error_class: string;
  error_message: string;
  latency_ms: number;
  first_byte_ms: number;
  created_at: string;
}

// ---- 服务状态 ----

/** 本站服务可用性档位：正常 / 部分受影响 / 中断。 */
export type ServiceStatusLevel = 'operational' | 'degraded' | 'outage';

/** 一家上游厂商在本站的通道可用情况。只含数量，不含渠道配置细节。 */
export interface ProviderStatus {
  provider: Provider;
  total: number;
  enabled: number;
  /** 因连续失败被系统自动禁用的通道数。 */
  auto_disabled: number;
  status: ServiceStatusLevel;
}

/** 一个时间窗口内本站中继的健康度。 */
export interface RelayWindow {
  window_minutes: number;
  requests: number;
  failed: number;
  failure_rate_percent: number;
  p95_latency_ms: number;
}

export interface ServiceStatus {
  status: ServiceStatusLevel;
  checked_at: string;
  models_total: number;
  models_available: number;
  /** 已上架但当前没有渠道承载的模型名，调用这些模型会被拒绝。 */
  unavailable_models: string[];
  providers: ProviderStatus[];
  recent_hour: RelayWindow;
  recent_day: RelayWindow;
}

// ---- 统计 ----

export interface StatsOverview {
  total_users: number;
  active_users_today: number;
  requests_today: number;
  credits_charged_today: number;
  credits_cost_today: number;
  channels_enabled: number;
  channels_disabled: number;
  total_credit_balance: number;
}

export interface DailyStat {
  /**
   * 全站按日统计接口返回的日期字段，序列化为完整 ISO 8601 时间戳（含时区偏移，如
   * "2026-08-05T00:00:00+08:00"），而非纯日期字符串。用于图表前必须先用 dayjs(day).format('YYYY-MM-DD')
   * 归一化，不可直接按分隔符裁剪。与 SummaryRow.group_key 按天分组时返回的纯日期字符串（"YYYY-MM-DD"）
   * 表示形式不同，两者不可混用同一套解析逻辑。
   */
  day: string;
  requests: number;
  credits_charged: number;
  credits_cost: number;
  total_tokens: number;
}

export interface ProfitRow {
  group_key: string;
  requests: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
}

export interface SummaryRow {
  /**
   * 按 groupBy 参数变化含义：groupBy=day 时为纯日期字符串（"YYYY-MM-DD"，后端 to_char 生成），
   * groupBy=model/key 时为模型名或密钥名。按天分组返回的日期表示形式与 DailyStat.day 的
   * 完整 ISO 8601 时间戳不同，不可混用同一套解析逻辑。
   */
  group_key: string;
  requests: number;
  credits_charged: number;
  total_tokens: number;
}

/** 用户侧缓存分析的分组行（GET /me/cache-report 的 groups 元素）。不含网关采购成本。 */
export interface CacheReportGroup {
  /** 按 group_by 变化：day 时为 "YYYY-MM-DD"，model 时为模型名 */
  group_key: string;
  requests: number;
  prompt_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  /** 0–1 浮点，cache_read_tokens / max(1, cache_read_tokens + prompt_tokens) */
  cache_hit_rate: number;
  credits_charged: number;
  /** 网关采购成本（积分）。仅管理端 /admin/stats/cache-report 填充，portal 不填。 */
  credits_cost?: number;
  /** credits_cost 的货币串。仅管理端填充，portal 不填。 */
  credits_cost_money?: string;
}

/** 用户侧缓存分析的整体汇总。 */
export interface CacheReportOverall {
  requests: number;
  prompt_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  cache_hit_rate: number;
}

/** GET /me/cache-report 响应。 */
export interface CacheReportResponse {
  /** Unix 秒 */
  from: number;
  /** Unix 秒 */
  to: number;
  overall: CacheReportOverall;
  groups: CacheReportGroup[];
}

/** 用户侧 token 结构的分组行（GET /me/token-report 的 groups 元素）。不含网关采购成本。 */
export interface TokenReportGroup {
  /** 按 group_by 变化：day 时为 "YYYY-MM-DD"，model 时为模型名 */
  group_key: string;
  requests: number;
  prompt_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  completion_tokens: number;
  /** 四类 billed token 合计 */
  total_tokens: number;
  credits_charged: number;
}

/** 用户侧 token 结构的整体合计（输入/缓存命中读/缓存写入/输出四类）。 */
export interface TokenReportOverall {
  prompt_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

/** GET /me/token-report 响应。 */
export interface TokenReportResponse {
  /** Unix 秒 */
  from: number;
  /** Unix 秒 */
  to: number;
  overall: TokenReportOverall;
  groups: TokenReportGroup[];
}

/** 7×24 周×时热力图的一个格子。day_of_week: 0=周日..6=周六；hour: 0..23。 */
export interface HeatmapCell {
  day_of_week: number;
  hour: number;
  requests: number;
  credits_charged: number;
}

/** GET /me/heatmap 与 GET /admin/stats/heatmap 的响应。cells 只含产生数据的格子，前端补零。 */
export interface HeatmapResponse {
  /** Unix 秒 */
  from: number;
  /** Unix 秒 */
  to: number;
  cells: HeatmapCell[];
}

/** 健康度时间线的一个时间桶：请求量、失败率与延迟分位（毫秒）。 */
export interface HealthPoint {
  /** 桶起始时刻，ISO 8601（服务器本地时区） */
  bucket_start: string;
  requests: number;
  /** status != 'settled' 的条数 */
  failed: number;
  /** 0–1 浮点，failed/requests（无请求时为 0） */
  fail_rate: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
}

/** GET /admin/stats/health-timeline 的响应。 */
export interface HealthTimelineResponse {
  /** Unix 秒 */
  from: number;
  /** Unix 秒 */
  to: number;
  bucket: 'hour' | 'day';
  points: HealthPoint[];
}

/** 经营分析月度合计（OpsSummary 的单月小计）。 */
export interface OpsMonthTotals {
  requests: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
  /** 当月充值/发放合计（grant+redeem）；不可用时为 0 */
  topup_credits: number;
}

/** 本月相对上月的环比变化（百分比）。上月分母为 0 时对应字段为 null。 */
export interface OpsMoMDelta {
  charged_pct: number | null;
  cost_pct: number | null;
  request_pct: number | null;
  topup_pct: number | null;
}

/** 经营分析排行榜行（模型或用户 Top N）。 */
export interface OpsRankRow {
  group_key: string;
  group_id: number;
  requests: number;
  credits_charged: number;
  credits_cost: number;
}

/** GET /admin/stats/ops-summary 的响应。 */
export interface OpsSummary {
  /** YYYY-MM */
  month: string;
  this_month: OpsMonthTotals;
  prev_month: OpsMonthTotals;
  mom: OpsMoMDelta;
  top_models: OpsRankRow[];
  top_users: OpsRankRow[];
}

/**
 * 调用类型：由模型形态（modality）与是否流式（is_stream）派生的运营报表维度，
 * 不是 usage_logs 的物理列。权威定义见 docs/glossary.md 的 CallType 节。
 */
export type CallType = 'embedding' | 'image' | 'stream' | 'non_stream' | 'other';

/** 调用类型分布的一行：请求量、token、扣费、成本与毛利。 */
export interface CallTypeRow {
  call_type: CallType;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
}

/** GET /admin/stats/cost-by-calltype 的响应。 */
export interface CostByCallTypeResponse {
  /** Unix 秒 */
  from: number;
  /** Unix 秒 */
  to: number;
  rows: CallTypeRow[];
}

export interface MyBalance {
  credit_balance: number;
  credit_used: number;
  request_count: number;
  exchange_rate_credits_per_cny: number;
  /** 界面金额展示符号（与 SiteConfig 同源）。 */
  currency_symbol: string;
}

// ---- 系统设置与站点配置 ----

export interface SettingItem {
  key: string;
  kind: 'int64' | 'bool' | 'string';
  value: number | boolean | string;
  default: number | boolean | string;
  describe: string;
  /**
   * 为 true 表示该项以密文存储，读取接口返回的是掩码而非明文。
   * 前端应按密码框渲染，并把「留空」理解为清除、而非保持原值。
   */
  secret?: boolean;
  /**
   * 枚举型设置项的全部合法取值，由后端下发。非空时应渲染为下拉选择，
   * 而不是让管理员往自由文本框里手打枚举值再由后端拒绝。
   */
  options?: string[];
}

export interface SiteConfig {
  site_name: string;
  exchange_rate_credits_per_cny: number;
  /** 界面金额展示符号（默认 ¥）；积分不在用户界面出现，统一以此符号呈现。 */
  currency_symbol: string;
  register_enabled: boolean;
  /** 管理员配置的对外 API 基址；留空表示未配置，由前端按当前站点地址推断。 */
  server_address: string;
  /** 余额预警阈值（积分），低于该值时门户展示预警；0 表示关闭预警。 */
  low_balance_threshold_credits: number;
  /** 是否允许用户自助修改显示名称（默认 true，关闭后门户置灰、PUT /auth/profile 拦截）。 */
  profile_display_name_editable: boolean;
  /** 是否允许用户自助修改邮箱（默认 true，关闭后门户置灰、PUT /auth/profile 拦截）。 */
  profile_email_editable: boolean;
}

// ---- 部门与组织化管理 ----

export type DepartmentStatus = 'enabled' | 'disabled';

export interface Department {
  id: number;
  name: string;
  /** 成本中心编码，对接财务系统用；非空时唯一 */
  code: string;
  owner_user_id: number | null;
  /** 月度预算积分，0 表示未设预算（仅报表对比，不拦截调用） */
  monthly_budget_credits: number;
  /** 部门级模型策略，空表示该层不施加限制 */
  allowed_models: string[] | null;
  status: DepartmentStatus;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface DepartmentWithStats extends Department {
  member_count: number;
  /** 负责人用户名；未指定或负责人已不是本部门成员时为空串。 */
  owner_username: string;
}

// ---- 项目（与部门正交的第二层成本归属维度）----

export type ProjectStatus = 'enabled' | 'disabled';

/** 项目实体。与部门同构——扁平、不持余额、不参与扣费，月度预算仅作对比。 */
export interface Project {
  id: number;
  name: string;
  /** 项目编码，对接财务系统用；非空时唯一 */
  code: string;
  owner_user_id: number | null;
  /** 接入方作用域，null 表示本机直管项目 */
  integration_id: number | null;
  /** 接入方侧标识，写入后不可变，本机直管为空串 */
  external_ref: string;
  /** 月度预算积分，0 表示未设预算（仅报表对比，不拦截调用） */
  monthly_budget_credits: number;
  status: ProjectStatus;
  note: string;
  created_at: string;
  updated_at: string;
}

/** 项目列表行，附带归属密钥数与负责人用户名。 */
export interface ProjectWithStats extends Project {
  /** 归属该项目的密钥数（项目无用户成员，成员是密钥） */
  key_count: number;
  /** 负责人用户名；未指定时为空串 */
  owner_username: string;
}

/** 项目费用与预算对比行（结构与 DepartmentBudgetRow 同构）。 */
export interface ProjectBudgetRow {
  project_id: number;
  project_name: string;
  requests: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
  monthly_budget_credits: number;
  budget_used_percent: number;
  over_budget: boolean;
}

export interface ProjectBudgetReport {
  /** YYYY-MM */
  month: string;
  rows: ProjectBudgetRow[];
}

// ---- 接入方与服务令牌 ----

/**
 * 接入方：对接本系统的外部租户（批次 F）。运营 root 在管理端创建，
 * 后端事务内同时建立无口令服务账号 svc:<slug>。权威定义见 docs/glossary.md。
 */
export type IntegrationStatus = 'enabled' | 'disabled';

export interface Integration {
  id: number;
  name: string;
  /** 服务账号用户名由它拼成 svc:<slug>，创建后不可变 */
  slug: string;
  status: IntegrationStatus;
  created_at: string;
  updated_at: string;
}

/** GET /admin/integrations/{id} 详情，附服务账号 ID 与令牌计数。 */
export interface IntegrationDetail extends Integration {
  service_account_user_id: number | null;
  token_count: number;
}

/** 服务令牌状态，与 KeyStatus 对齐，仅启用/停用两态。 */
export type ServiceTokenStatus = 'enabled' | 'disabled';

/** 服务令牌：接入方调用管理 API 的凭证（明文以 tzs- 前缀，哈希不外泄）。 */
export interface ServiceToken {
  id: number;
  integration_id: number;
  name: string;
  token_prefix: string;
  status: ServiceTokenStatus;
  last_used_at: string | null;
  created_at: string;
}

/** 创建响应额外携带的一次性明文令牌，关闭后无法再次取得。 */
export interface CreatedServiceToken extends ServiceToken {
  token: string;
}

// ---- 操作审计 ----

export type AuditResult = 'success' | 'failure';

export type AuditTargetType =
  | 'user' | 'department' | 'project' | 'model' | 'channel'
  | 'redemption' | 'setting' | 'session' | 'audit' | 'api_key';

export interface AuditLog {
  id: number;
  operator_id: number;
  operator_name: string;
  operator_role: Role;
  action: string;
  target_type: AuditTargetType;
  target_id: number;
  target_name: string;
  result: AuditResult;
  before_state: Record<string, unknown> | null;
  after_state: Record<string, unknown> | null;
  client_ip: string;
  request_id: string;
  message: string;
  created_at: string;
}

// ---- 主动告警 ----

export type AlertSeverity = 'critical' | 'warning';
export type AlertStatus = 'pending' | 'sent' | 'failed' | 'suppressed' | 'dead_letter';
export type WebhookFormat = 'generic' | 'dingtalk' | 'feishu' | 'wecom' | 'slack';
export type SmtpSecurity = 'starttls' | 'tls' | 'none';

export interface AlertEvent {
  id: number;
  alert_type: string;
  severity: AlertSeverity;
  dedup_key: string;
  title: string;
  message: string;
  payload: Record<string, unknown> | null;
  status: AlertStatus;
  channels_sent: { channels: string[] } | null;
  attempts: number;
  last_error: string;
  sent_at: string | null;
  created_at: string;
}

// ---- 费用报表 ----

export type CostReportDimension = 'user' | 'department' | 'project' | 'model' | 'channel' | 'day' | 'key';

export interface CostReportRow {
  /** 维度对应的实体 ID；按日与按模型维度恒为 0 */
  group_id: number;
  group_key: string;
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
}

export interface CostReport {
  group_by: CostReportDimension;
  /** Unix 秒 */
  from: number;
  to: number;
  rows: CostReportRow[];
}

export interface DepartmentBudgetRow {
  department_id: number;
  department_name: string;
  requests: number;
  credits_charged: number;
  credits_cost: number;
  margin: number;
  monthly_budget_credits: number;
  budget_used_percent: number;
  over_budget: boolean;
}

export interface DepartmentBudgetReport {
  /** YYYY-MM */
  month: string;
  rows: DepartmentBudgetRow[];
}

// ---- 批量导入 ----

export type ImportAction = 'created' | 'updated' | 'skipped' | 'failed';

export interface UserImportResult {
  username: string;
  action: ImportAction;
  user_id: number;
  message: string;
  /**
   * 系统生成的一次性初始密码，仅在该行未提供密码时出现。
   * 只在本次响应中返回，不落库也不进审计。
   */
  initial_password?: string;
}

export interface UserImportSummary {
  created: number;
  skipped: number;
  failed: number;
  results: UserImportResult[];
}

export interface BatchGrantResult {
  user_id: number;
  ok: boolean;
  replay: boolean;
  message: string;
}

export interface BatchGrantSummary {
  succeeded: number;
  replayed: number;
  failed: number;
  results: BatchGrantResult[];
}

export interface BatchStatusResult {
  user_id: number;
  username: string;
  ok: boolean;
  message: string;
}

export interface BatchStatusSummary {
  /** 状态确实发生改变的账号数。 */
  succeeded: number;
  /** 本就是目标状态、未改动的账号数。 */
  unchanged: number;
  failed: number;
  results: BatchStatusResult[];
}

// ---- 首次配置引导 ----

export type SetupCheckKey =
  | 'channel'
  | 'model'
  | 'model_servable'
  | 'member'
  | 'credits'
  | 'server_address'
  | 'alert_channel';

export interface SetupCheckItem {
  key: SetupCheckKey;
  done: boolean;
  /** 必需项未完成时，系统无法完成一次成功调用。 */
  required: boolean;
  title: string;
  /** 未完成时的业务后果说明。 */
  detail: string;
  action: string;
  /** 管理端页面路径，供引导直接跳转。 */
  path: string;
}

export interface SetupCounts {
  channels_enabled: number;
  models_enabled: number;
  models_servable: number;
  member_users: number;
  users_with_credits: number;
}

export interface SetupStatus {
  /** 全部必需项已完成。 */
  completed: boolean;
  pending_count: number;
  checks: SetupCheckItem[];
  counts: SetupCounts;
  server_address: string;
}
