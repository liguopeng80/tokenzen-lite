// /v1 下游中继端点的业务错误码。权威来源为 docs/api-contract.md 的「错误码表」。
//
// 后端当前将这些码值以裸字符串形式分散在 server/internal/relay 与 server/internal/api/relay_handlers
// 中（无集中常量定义）；本表是前端可引用的权威收敛版本，码值、HTTP 状态、含义、客户端建议动作
// 与 api-contract.md 一一对应。后端是否同步收敛为常量由主会话决定，此处不假定。
//
// 注意两种错误响应形态：
// - OpenAI 格式：{"error": {"message", "type", "code"}}，type 与 code 同值。
// - Anthropic 格式：{"type": "error", "error": {"type", "message"}}，仅 type，无 code 字段。
// 认证/限流/并发闸门阶段（进入端点业务逻辑之前）的错误一律 OpenAI 格式，包括 /v1/messages。

/** 业务错误码字符串值（禁裸字符串：业务码必须经此常量引用） */
export const RelayErrorCode = {
  InvalidApiKey: 'invalid_api_key',
  KeyDisabled: 'key_disabled',
  KeyExpired: 'key_expired',
  KeyUnavailable: 'key_unavailable',
  IpNotAllowed: 'ip_not_allowed',
  UserDisabled: 'user_disabled',
  ModelNotAllowed: 'model_not_allowed',
  InvalidRequestError: 'invalid_request_error',
  UnsupportedFeature: 'unsupported_feature',
  ModelEndpointMismatch: 'model_endpoint_mismatch',
  ModelProviderMismatch: 'model_provider_mismatch',
  ProviderNotFound: 'provider_not_found',
  ModelNotFound: 'model_not_found',
  InsufficientCredits: 'insufficient_credits',
  KeyQuotaExceeded: 'key_quota_exceeded',
  DailySpendLimitExceeded: 'daily_spend_limit_exceeded',
  KeyDailySpendLimitExceeded: 'key_daily_spend_limit_exceeded',
  RateLimited: 'rate_limited',
  UpstreamError: 'upstream_error',
  Overloaded: 'overloaded',
  NoChannel: 'no_channel',
  ChannelProtocolUnsupported: 'channel_protocol_unsupported',
  ChannelError: 'channel_error',
  ModelNotPriced: 'model_not_priced',
  InternalError: 'internal_error',
} as const;

export type RelayErrorCodeValue = (typeof RelayErrorCode)[keyof typeof RelayErrorCode];

/** 单条错误码定义：码值 + HTTP 状态 + 中文含义 + 客户端建议动作 */
export interface RelayErrorCodeDef {
  readonly code: RelayErrorCodeValue;
  readonly http: number;
  readonly meaning: string;
  readonly action: string;
}

/**
 * 错误码全表，与 docs/api-contract.md 的错误码表逐行一致。
 *
 * 「上游原状态码 / 上游原业务码」是透传语义而非固定码值，不在本表内——上游返回不可换渠道重试的
 * 4xx（非 401/402/403/429）时转发上游错误，状态码与响应体原样透传。
 */
export const RELAY_ERROR_CODES: readonly RelayErrorCodeDef[] = [
  {
    code: RelayErrorCode.InvalidApiKey,
    http: 401,
    meaning: '缺少 API Key、格式非法或哈希无匹配',
    action: '检查 Key 明文与请求头写法，不重试',
  },
  {
    code: RelayErrorCode.KeyDisabled,
    http: 403,
    meaning: 'Key 被禁用',
    action: '联系管理员或换用其他 Key',
  },
  {
    code: RelayErrorCode.KeyExpired,
    http: 403,
    meaning: 'Key 已过期（首次触发时状态同步置为 expired）',
    action: '换用新 Key',
  },
  {
    code: RelayErrorCode.KeyUnavailable,
    http: 403,
    meaning: 'Key 处于其他不可用状态',
    action: '换用新 Key',
  },
  {
    code: RelayErrorCode.IpNotAllowed,
    http: 403,
    meaning: '来源 IP 不在 Key 的白名单内（白名单无法解析时同码返回并提示配置有误，服务端另发策略告警）',
    action: '从白名单 IP 发起，或调整 Key 白名单',
  },
  {
    code: RelayErrorCode.UserDisabled,
    http: 403,
    meaning: 'Key 所属账号被禁用',
    action: '联系管理员',
  },
  {
    code: RelayErrorCode.ModelNotAllowed,
    http: 403,
    meaning: '请求的模型不在有效模型集合内（部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄；任一层策略无法解析时同码返回并提示配置有误，服务端另发策略告警）',
    action: '换模型；策略由管理员维护的两层需联系管理员调整',
  },
  {
    code: RelayErrorCode.InvalidRequestError,
    http: 400,
    meaning: '请求体不是合法 JSON、缺少 model 字段，或跨协议转换时请求结构无法解析',
    action: '修正请求后重发，不重试',
  },
  {
    code: RelayErrorCode.UnsupportedFeature,
    http: 400,
    meaning: '跨协议路由时请求体携带目标上游协议无法表达的字段（如 logprobs/seed 路由到 Anthropic、response_format 路由到 Anthropic、top_k 路由到 OpenAI chat 端点等）；错误消息指明具体字段与目标协议。同协议直通不触发此错误',
    action: '移除该字段，或改用支持该字段的同协议渠道；不重试',
  },
  {
    code: RelayErrorCode.ModelEndpointMismatch,
    http: 400,
    meaning: '模型形态或计费方式与端点不匹配',
    action: '按错误消息改用正确端点',
  },
  {
    code: RelayErrorCode.ModelProviderMismatch,
    http: 400,
    meaning: '/{provider}/v1/* 入口：URL 前缀锁定的 provider 与请求体 model 的归属厂商（models.provider）不一致',
    action: '改用一致的前缀或模型，不重试；消息指明两侧 provider 取值',
  },
  {
    code: RelayErrorCode.ModelNotFound,
    http: 404,
    meaning: '模型不存在或未上架',
    action: '用 GET /v1/models 核对可用模型',
  },
  {
    code: RelayErrorCode.ProviderNotFound,
    http: 404,
    meaning: '/{provider}/v1/* 入口：URL 前缀的 provider slug 未命中任何已知厂商别名（在认证与限流之前返回，不占限流配额）',
    action: '核对 provider 前缀拼写',
  },
  {
    code: RelayErrorCode.InsufficientCredits,
    http: 402,
    meaning: '用户积分余额不足以完成预扣',
    action: '充值后重试',
  },
  {
    code: RelayErrorCode.KeyQuotaExceeded,
    http: 402,
    meaning: 'Key 剩余额度不足以完成预扣',
    action: '调整 Key 限额或换 Key；余额可查 GET /v1/key/info',
  },
  {
    code: RelayErrorCode.DailySpendLimitExceeded,
    http: 429,
    meaning: '当日累计扣费已达该用户的每日花费上限',
    action: '次日自动恢复；需更高额度请联系管理员上调',
  },
  {
    code: RelayErrorCode.KeyDailySpendLimitExceeded,
    http: 429,
    meaning: '当日累计扣费已达该 API Key 的每日花费上限（与用户级每日上限并行生效、各独立累计）',
    action: '次日自动恢复；换用其他密钥，或上调该 Key 上限',
  },
  {
    code: RelayErrorCode.RateLimited,
    http: 429,
    meaning: '触发每分钟请求数限流（按 Key 与按用户两个维度同时生效，任一超限即拒绝；被拒请求同样计入两个窗口）',
    action: '退避后重试',
  },
  {
    code: RelayErrorCode.UpstreamError,
    http: 502,
    meaning: '上游连接失败、上游 5xx/限流经换渠道重试后仍失败，或上游 200 响应体无法解析',
    action: '退避后重试',
  },
  {
    code: RelayErrorCode.Overloaded,
    http: 503,
    meaning: '并发闸门拒绝：用户子配额、全站总并发或大请求配额任一用尽（三种原因统一返回 overloaded，区分依据在服务端拒绝日志的 reason 字段，客户端不应据此判定服务不健康）',
    action: '退避后重试',
  },
  {
    code: RelayErrorCode.NoChannel,
    http: 503,
    meaning: '没有启用中的渠道承载该模型',
    action: '稍后重试或联系管理员',
  },
  {
    code: RelayErrorCode.ChannelProtocolUnsupported,
    http: 503,
    meaning: '该模型有启用中的渠道，但协议均不被当前端点支持（/v1/embeddings、/v1/images/generations 仅支持 openai_compat 渠道）',
    action: '联系管理员调整渠道协议或模型配置',
  },
  {
    code: RelayErrorCode.ChannelError,
    http: 503,
    meaning: '渠道配置异常（如密钥解密失败）且无其他候选',
    action: '联系管理员',
  },
  {
    code: RelayErrorCode.ModelNotPriced,
    http: 503,
    meaning: '模型未配置定价（或按次模型未配置按次单价）',
    action: '联系管理员',
  },
  {
    code: RelayErrorCode.InternalError,
    http: 500,
    meaning: '计费系统异常、构建上游请求失败等服务端错误',
    action: '退避后重试，持续出现联系管理员',
  },
];

/** 按码值查表 */
export const RelayErrorByCode: Readonly<Record<string, RelayErrorCodeDef>> = Object.fromEntries(
  RELAY_ERROR_CODES.map((d) => [d.code, d]),
);
