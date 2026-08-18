// Token Zen Lite 枚举常量与展示标签。取值与后端 server/internal/domain 一致，
// 权威定义见 docs/glossary.md。全部为字符串枚举。

export * from './error-codes';
export * from './packages';

import type {
  Role, UserStatus, KeyStatus, ChannelStatus, ChannelProtocol, Provider,
  LedgerEntryType, RedemptionStatus, Modality, BillingMode, ModelStatus,
  UsageStatus, DepartmentStatus, ProjectStatus, AuditResult, AuditTargetType,
  AlertSeverity, AlertStatus, WebhookFormat, SmtpSecurity,
  CostReportDimension, ImportAction, MonthlyGrantMode, Capability,
  IntegrationStatus, ServiceTokenStatus, CallType,
} from '../types';

export const Roles = { User: 'user', Managed: 'managed', Admin: 'admin', Root: 'root' } as const;

export const RoleLabel: Record<Role, string> = {
  user: '普通用户',
  managed: '托管管理员',
  admin: '管理员',
  root: '超级管理员',
};

/** 角色权重：门禁比较用（managed 及以上可进管理端托管桶，admin 及以上进运营桶） */
export const RoleRank: Record<Role, number> = { user: 1, managed: 5, admin: 10, root: 100 };

export const UserStatusLabel: Record<UserStatus, string> = {
  enabled: '已启用',
  disabled: '已禁用',
};

export const KeyStatusLabel: Record<KeyStatus, string> = {
  enabled: '已启用',
  disabled: '已禁用',
  expired: '已过期',
  depleted: '额度耗尽',
};

export const ChannelStatusLabel: Record<ChannelStatus, string> = {
  enabled: '已启用',
  manual_disabled: '手动禁用',
  auto_disabled: '自动禁用',
};

export const ProviderLabel: Record<Provider, string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
  gemini: 'Google Gemini',
  zhipu: '智谱 GLM',
  qwen: '阿里 Qwen',
  deepseek: 'DeepSeek',
  minimax: 'MiniMax',
  moonshot: 'Moonshot (Kimi)',
  xai: 'xAI',
  custom: '自定义',
};

/**
 * 能力标签的展示名（权威定义见 docs/glossary.md「模型能力属性」节）。
 * 只标注非常配能力，避免选型清单噪音。
 */
export const CapabilityLabel: Record<Capability, string> = {
  vision: '视觉输入',
  video: '视频输入',
  audio: '音频',
  reasoning: '深度推理',
};

/**
 * 厂商目录元数据（预置只读目录，权威定义见 docs/glossary.md 的 Provider 节）。
 * supported_protocols 按各厂商实际暴露
 * 的接入协议填写，与 ChannelProtocol 枚举一致；同一厂商可同时支持多种协议
 * （如智谱 GLM 的 coding plan 同时暴露 Anthropic 与 OpenAI 兼容端点）。
 * 管理端只读展示，不开放运营态增删改。
 */
export interface ProviderMeta {
  id: Provider;
  name: string;
  website_url: string;
  pricing_url: string;
  /** 默认接入地址；多协议场景在 description 里补充说明 */
  default_base_url: string;
  /**
   * 按协议区分的接入地址覆盖。键为 ChannelProtocol，值覆盖 default_base_url。
   * 仅双协议厂商需要：如智谱的 anthropic 端点、Gemini 的 openai_compat 端点。
   */
  base_url_overrides?: Partial<Record<ChannelProtocol, string>>;
  supported_protocols: readonly ChannelProtocol[];
  description: string;
  /** 排序权重，数值越小越靠前 */
  sort: number;
  /** 目录状态：disabled 表示不纳入新预置但保留兼容 */
  status: 'enabled' | 'disabled';
}

export const ProviderCatalog: Record<Provider, ProviderMeta> = {
  anthropic: {
    id: 'anthropic',
    name: 'Anthropic',
    website_url: 'https://www.anthropic.com',
    pricing_url: 'https://www.anthropic.com/pricing',
    default_base_url: 'https://api.anthropic.com',
    supported_protocols: ['anthropic'],
    description: 'Claude 系列原厂，opus/sonnet/fable/haiku 短名映射的归属厂商。',
    sort: 10,
    status: 'enabled',
  },
  openai: {
    id: 'openai',
    name: 'OpenAI',
    website_url: 'https://openai.com',
    pricing_url: 'https://openai.com/api/pricing/',
    default_base_url: 'https://api.openai.com/v1',
    supported_protocols: ['openai_compat'],
    description: 'GPT 系列原厂，提供 OpenAI 兼容协议接入。',
    sort: 20,
    status: 'enabled',
  },
  gemini: {
    id: 'gemini',
    name: 'Google Gemini',
    website_url: 'https://ai.google.dev/',
    pricing_url: 'https://ai.google.dev/pricing',
    default_base_url: 'https://generativelanguage.googleapis.com',
    supported_protocols: ['gemini', 'openai_compat'],
    base_url_overrides: { openai_compat: 'https://generativelanguage.googleapis.com/v1beta/openai' },
    description: 'Google Gemini 系列与图像生成模型（Nano Banana 等），经 Vertex API 接入。',
    sort: 30,
    status: 'enabled',
  },
  zhipu: {
    id: 'zhipu',
    name: '智谱 GLM',
    website_url: 'https://www.zhipuai.cn',
    pricing_url: 'https://open.bigmodel.cn/pricing',
    default_base_url: 'https://open.bigmodel.cn/api/paas/v4',
    supported_protocols: ['openai_compat', 'anthropic'],
    base_url_overrides: { anthropic: 'https://open.bigmodel.cn/api/anthropic' },
    description: '智谱 GLM 系列厂商。coding plan 同时暴露 OpenAI 兼容与 Anthropic 协议端点（Anthropic 端点为 https://open.bigmodel.cn/api/anthropic）。',
    sort: 40,
    status: 'enabled',
  },
  deepseek: {
    id: 'deepseek',
    name: 'DeepSeek',
    website_url: 'https://www.deepseek.com',
    pricing_url: 'https://api-docs.deepseek.com/quick_start/pricing',
    default_base_url: 'https://api.deepseek.com/v1',
    supported_protocols: ['openai_compat'],
    description: 'DeepSeek 模型厂商，提供 OpenAI 兼容协议接入。',
    sort: 50,
    status: 'enabled',
  },
  minimax: {
    id: 'minimax',
    name: 'MiniMax',
    website_url: 'https://www.minimaxi.com',
    pricing_url: 'https://platform.minimaxi.com/document/Price',
    default_base_url: 'https://api.minimaxi.com/v1',
    supported_protocols: ['openai_compat', 'anthropic'],
    base_url_overrides: { anthropic: 'https://api.minimaxi.com/anthropic' },
    description: 'MiniMax 模型厂商，旗舰 M 系列原生支持图像与视频输入；同时暴露 OpenAI 兼容与 Anthropic 协议端点。',
    sort: 60,
    status: 'enabled',
  },
  moonshot: {
    id: 'moonshot',
    name: 'Moonshot (Kimi)',
    website_url: 'https://www.moonshot.cn',
    pricing_url: 'https://platform.moonshot.cn/docs/pricing',
    default_base_url: 'https://api.moonshot.cn/v1',
    supported_protocols: ['openai_compat', 'anthropic'],
    base_url_overrides: { anthropic: 'https://api.moonshot.cn/anthropic' },
    description: 'Moonshot Kimi 系列厂商，长上下文旗舰 K 系列；同时暴露 OpenAI 兼容与 Anthropic 协议端点。',
    sort: 70,
    status: 'enabled',
  },
  qwen: {
    id: 'qwen',
    name: '阿里 Qwen',
    website_url: 'https://tongyi.aliyun.com',
    pricing_url: 'https://help.aliyun.com/zh/dashscope/product-overview/billing',
    default_base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    supported_protocols: ['openai_compat'],
    description: '阿里通义千问系列，经 DashScope OpenAI 兼容模式接入。本次未纳入预置清单。',
    sort: 80,
    status: 'disabled',
  },
  xai: {
    id: 'xai',
    name: 'xAI',
    website_url: 'https://x.ai',
    pricing_url: 'https://docs.x.ai/docs/models',
    default_base_url: 'https://api.x.ai/v1',
    supported_protocols: ['openai_compat'],
    description: 'xAI Grok 系列厂商。本次未纳入预置清单。',
    sort: 90,
    status: 'disabled',
  },
  custom: {
    id: 'custom',
    name: '自定义',
    website_url: '',
    pricing_url: '',
    default_base_url: '',
    supported_protocols: ['openai_compat', 'anthropic', 'gemini'],
    description: '自定义上游厂商，接入地址与协议由渠道自行配置。',
    sort: 100,
    status: 'enabled',
  },
};

/**
 * 按厂商与协议查默认接入地址。回退优先级：
 * base_url_overrides[protocol] > default_base_url > undefined。
 * custom 厂商 default_base_url 为空串，归一为 undefined，由调用方 required 校验兜底。
 */
export function defaultBaseUrlFor(
  provider: Provider,
  protocol: ChannelProtocol,
): string | undefined {
  const meta = ProviderCatalog[provider];
  if (!meta) return undefined;
  return meta.base_url_overrides?.[protocol] || meta.default_base_url || undefined;
}

export const ProtocolLabel: Record<ChannelProtocol, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
};

export const LedgerEntryTypeLabel: Record<LedgerEntryType, string> = {
  grant: '管理员分配',
  revoke: '管理员扣回',
  redeem: '兑换码充值',
  consume: '请求预扣',
  refund: '失败退款',
  settle_adjust: '结算差额',
};

export const RedemptionStatusLabel: Record<RedemptionStatus, string> = {
  unused: '未使用',
  used: '已使用',
  disabled: '已禁用',
  expired: '已过期',
};

export const ModalityLabel: Record<Modality, string> = {
  text: '文本',
  embedding: '向量嵌入',
  image: '图像',
};

/**
 * 调用类型标签：由 modality + is_stream 派生的运营报表维度，与后端
 * store.CallTypeEmbedding 等常量同源。权威定义见 docs/glossary.md 的 CallType 节。
 */
export const CallTypeLabel: Record<CallType, string> = {
  embedding: '向量嵌入',
  image: '图像',
  stream: '流式对话',
  non_stream: '非流式对话',
  other: '其他',
};

/**
 * 模型形态 → 可承载的渠道协议（下游端点 × 上游协议支持矩阵，权威定义见
 * docs/glossary.md 的 ChannelProtocol 节，与后端 domain.ProtocolSupportsModality 一致）：
 * 文本模型三种协议均可；向量/图像模型仅 openai_compat 协议渠道可承载。
 */
export const ModalitySupportedProtocols: Record<Modality, readonly ChannelProtocol[]> = {
  text: ['openai_compat', 'anthropic', 'gemini'],
  embedding: ['openai_compat'],
  image: ['openai_compat'],
};

export const BillingModeLabel: Record<BillingMode, string> = {
  per_token: '按 Token',
  per_call: '按次',
};

export const ModelStatusLabel: Record<ModelStatus, string> = {
  enabled: '已上架',
  disabled: '已下架',
};

export const UsageStatusLabel: Record<UsageStatus, string> = {
  settled: '已结算',
  refunded: '已退款',
  failed: '失败',
};

export const ErrorClassLabel: Record<string, string> = {
  auth_fatal: '密钥失效',
  quota_fatal: '上游欠费',
  rate_limited: '限流',
  transient: '瞬时错误',
  bad_request: '请求错误',
  stream_aborted: '流式中断',
};

/** 每周日枚举（ISO：1=周一 ... 7=周日），时段倍率规则编辑用 */
export const WeekdayLabel: Record<number, string> = {
  1: '周一', 2: '周二', 3: '周三', 4: '周四', 5: '周五', 6: '周六', 7: '周日',
};

export const DepartmentStatusLabel: Record<DepartmentStatus, string> = {
  enabled: '正常',
  disabled: '已停用',
};

export const ProjectStatusLabel: Record<ProjectStatus, string> = {
  enabled: '正常',
  disabled: '已停用',
};

export const IntegrationStatusLabel: Record<IntegrationStatus, string> = {
  enabled: '已启用',
  disabled: '已停用',
};

export const ServiceTokenStatusLabel: Record<ServiceTokenStatus, string> = {
  enabled: '已启用',
  disabled: '已停用',
};

export const AuditResultLabel: Record<AuditResult, string> = {
  success: '成功',
  failure: '失败',
};

export const AuditTargetTypeLabel: Record<AuditTargetType, string> = {
  user: '用户',
  department: '部门',
  project: '项目',
  model: '模型',
  channel: '渠道',
  redemption: '兑换码',
  setting: '系统设置',
  api_key: 'API 密钥',
  session: '登录会话',
  audit: '审计记录',
};

/**
 * 审计动作的中文标签。动作枚举本身由 GET /admin/audit-logs/actions 下发，
 * 此处只提供展示名；下发了本表未收录的动作时按原始取值显示，不隐藏记录。
 */
export const AuditActionLabel: Record<string, string> = {
  'auth.login': '登录',
  'auth.logout': '登出',
  'auth.password_change': '修改密码',
  'auth.register': '自助注册',
  'user.create': '创建用户',
  'user.update': '修改用户',
  'user.delete': '删除用户',
  'user.status_change': '变更账号状态',
  'user.password_reset': '重置密码',
  'user.credit_grant': '调整积分',
  'user.import': '批量导入用户',
  'user.credit_batch_grant': '批量发放积分',
  'user.status_batch_change': '批量变更账号状态',
  'user.policy_change': '变更用量策略',
  'department.create': '创建部门',
  'department.update': '修改部门',
  'department.delete': '删除部门',
  'department.member_change': '调整部门成员',
  'model.create': '创建模型',
  'model.update': '修改模型',
  'model.delete': '删除模型',
  'model.price_change': '调整模型定价',
  'model.peak_rules_change': '调整时段倍率',
  'model.import': '批量导入模型',
  'channel.create': '创建渠道',
  'channel.update': '修改渠道',
  'channel.delete': '删除渠道',
  'channel.status_change': '变更渠道状态',
  'channel.cost_change': '调整渠道成本',
  'channel.test': '测试渠道连通',
  'redemption.batch_create': '批量生成兑换码',
  'redemption.status_change': '变更兑换码状态',
  'api_key.create': '创建 API 密钥',
  'api_key.update': '修改 API 密钥',
  'api_key.delete': '删除 API 密钥',
  'setting.update': '修改系统设置',
  'alert.test': '测试告警通道',
  'audit.purge': '清理过期审计记录',
};

export const AlertSeverityLabel: Record<AlertSeverity, string> = {
  critical: '严重',
  warning: '提醒',
};

export const AlertStatusLabel: Record<AlertStatus, string> = {
  pending: '待投递',
  sent: '已送达',
  failed: '投递失败',
  suppressed: '窗口内已抑制',
  dead_letter: '死信（重试耗尽）',
};

export const AlertTypeLabel: Record<string, string> = {
  channel_auto_disabled: '渠道自动禁用',
  reconcile_failed: '积分对账未通过',
  usage_log_dropped: '用量日志丢弃',
  orphan_precharge_found: '孤儿预扣回收',
  department_over_budget: '部门超预算',
  backup_failed: '备份失败',
  user_low_balance: '用户余额不足',
  user_balance_notice: '余额提醒（发给本人）',
  monthly_grant_failed: '按月发放有失败',
  error_rate_high: '中继失败率偏高',
  latency_degraded: '中继耗时劣化',
  policy_malformed: '策略配置有误',
  alert_test: '通道测试',
};

/** 按月自动发放积分的口径，与后端 domain.MonthlyGrantMode 一致。 */
export const MonthlyGrantModeLabel: Record<MonthlyGrantMode, string> = {
  topup: '补足到额度（未用完不累积）',
  add: '增发固定额度（未用完累积）',
};

export const WebhookFormatLabel: Record<WebhookFormat, string> = {
  generic: '通用 JSON',
  dingtalk: '钉钉群机器人',
  feishu: '飞书群机器人',
  wecom: '企业微信群机器人',
  slack: 'Slack',
};

/** 需要加签密钥的 Webhook 目标平台；其余平台填写密钥不生效。 */
export const WebhookFormatsWithSecret: readonly WebhookFormat[] = ['dingtalk', 'feishu'];

export const SmtpSecurityLabel: Record<SmtpSecurity, string> = {
  starttls: 'STARTTLS（常用 587 端口）',
  tls: 'SSL/TLS（常用 465 端口）',
  none: '不加密（仅内网自建服务器）',
};

export const CostReportDimensionLabel: Record<CostReportDimension, string> = {
  user: '按用户',
  department: '按部门',
  project: '按项目',
  model: '按模型',
  channel: '按渠道',
  day: '按日期',
  key: '按密钥',
};

export const ImportActionLabel: Record<ImportAction, string> = {
  created: '已新建',
  updated: '已更新',
  skipped: '已跳过',
  failed: '失败',
};

/**
 * 用户名格式，与后端 `server/internal/api/auth_handlers.go` 的 usernameRe 一致：
 * 3-32 位字母、数字、下划线或连字符。两端同时校验，前端负责在提交前给出行内提示。
 */
export const UsernamePattern = /^[a-zA-Z0-9_-]{3,32}$/;

/** 用户名规则的说明文案，表单 extra 与错误提示共用。 */
export const UsernameRuleHint = '用户名须为 3-32 位字母、数字、下划线或连字符';

/**
 * 枚举型设置项取值的展示标签。键为设置项的 key，值为「取值 → 中文说明」。
 * 后端在设置读取接口里下发合法取值（SettingItem.options），这里只负责展示名。
 */
export const SettingOptionLabels: Record<string, Record<string, string>> = {
  monthly_grant_mode: MonthlyGrantModeLabel,
  alert_webhook_format: WebhookFormatLabel,
  alert_smtp_tls: SmtpSecurityLabel,
};

/** 未分配部门在报表与筛选中的固定 ID。 */
export const UnassignedDepartmentId = 0;
