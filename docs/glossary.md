# 术语表

本文档是 Token Zen Lite 全部业务术语与枚举取值的唯一权威定义。新增业务常量、枚举时必须先更新本文档，再同步到后端 `server/internal/domain` 与前端 `packages/shared/src/constants`。

## 核心业务概念

| 术语 | 英文 | 定义 |
|------|------|------|
| 积分 | credit | 系统内部记账单位（int64 整数）。所有余额、扣费、定价均以积分存储与运算；用户界面不直接展示积分，统一经兑换率折算为货币金额（符号由 `currency_symbol` 决定，默认 ¥）后呈现。 |
| 兑换率 | exchange rate | 货币单位与积分的全局换算比例，默认 1 货币单位 = 1,000,000 积分。由管理员在系统设置 `exchange_rate_credits_per_cny` 配置，对全部用户一致。界面金额 = 积分 ÷ 兑换率。 |
| 货币符号 | currency symbol | 界面金额展示符号（系统设置 `currency_symbol`，默认 ¥）。仅决定展示符号，不进行汇率换算——数值始终是"积分 ÷ 兑换率"。展示精度：总额类 2 位小数、明细类 ceil(log10(兑换率)) 位（默认 6 位，无损），尾部补零。 |
| 模型单价 | model price | 每个对外模型的直接标价：input/output/cache_read/cache_write 按"积分 / 1M tokens"，图像等按次计费模型用"积分 / 次"（per_call_price）。不存在倍率叠乘。 |
| 时段倍率 | peak multiplier | 按模型配置的特殊时段消耗倍率（`multiplier_percent`，150 表示 1.5 倍，下限 100），带时区与星期几条件；同一时刻命中多条规则时取最大值，无命中按 100 计。规则语义：时段区间为当日分钟数的左闭右开区间 `[start_minute, end_minute)`，`start_minute` 必须小于 `end_minute`，跨午夜时段（如 22:00-次日 6:00）必须拆成两条规则；`timezone` 缺省为 `Asia/Shanghai`；`days_of_week` 为 ISO 星期（1=周一 ... 7=周日），空数组表示全周生效（保存时展开为 1-7）。 |
| 渠道 | channel | 一个上游供应商接入点（base_url + API Key + 支持的模型清单）。 |
| 渠道成本价 | channel cost | 渠道 × 模型维度的上游成本单价，用于利润分析（利润 = 用户扣费 − 渠道成本）。 |
| API Key | api key | 发放给终端用户、用于调用 /v1 下游 API 的密钥（`tzl-` 前缀）。仅创建时返回一次明文，库中存 SHA-256 哈希。 |
| 预置价目 | pricing preset | 随二进制内置的主流厂商公开价目（美元官价，以微美元表示，1 美元 = 1,000,000 微美元），供新装系统批量上架模型时作为定价起点。价目带采集月份标记，厂商随时可能调价，导入前须对照厂商定价页核对。 |
| 加价百分数 | markup percent | 把厂商美元官价折算为本站积分单价时施加的加价倍数（100 = 按官价平价折算，130 = 在官价基础上加价 30%）。折算式：官价美元 × 美元兑人民币汇率 × 兑换率 × 加价百分数 ÷ 100，向上取整。取值范围 100-1000。 |
| 积分流水 | credit ledger | 积分变动的唯一事实源，所有分配、兑换、消费、退款均落流水；对账不变式：用户余额 = 流水金额之和。 |
| 部门 | department | 组织内部的成本归属单元，单层无上下级，用户归属 0 或 1 个部门。部门不持有积分余额、不参与扣费，仅作费用分摊与预算对比的维度。给部门下发预算的实际动作是对其成员批量发放积分。 |
| 项目 | project | 与部门正交的第二层成本归属单元，单层无上下级，密钥归属 0 或 1 个项目（`api_keys.project_id`）。项目与部门同构——不持余额、不参与扣费、月度预算仅作对比；刻意不加 `department_id`，使同一密钥可同时归属一个部门与一个项目，两个维度各自独立聚合。 |
| 月度预算 | monthly budget | 部门或项目每自然月的消费目标积分数（0 表示未设）。仅用于报表对比与超预算告警，不拦截调用。 |
| 部门快照 | department snapshot | 用量日志与积分流水上记录的记账时点部门 ID。报表按快照聚合，使用户转部门后已出账月份的分摊口径保持不变；未分配部门记为 0，报表显示为"未分配"。 |
| 项目快照 | project snapshot | 用量日志与按日汇总上记录的记账时点密钥所属项目 ID。报表按快照聚合，使密钥改挂项目后已出账月份的分摊口径保持不变；未归属项目记为 0，报表显示为"未归属"（含密钥未挂项目与 0019 迁移前的历史汇总两种，合并展示）。 |
| 有效模型集合 | effective models | 一个 API Key 实际可调用的模型集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单。各层都只能收窄不能放宽，某层为空表示该层不施加限制。 |
| 每日花费上限 | daily spend limit | 单个用户单个自然日内的累计扣费积分上限（0 = 不限）。预扣前校验，超出返回 429 `daily_spend_limit_exceeded`。 |
| Key 每日花费上限 | key daily spend limit | 单个 API Key 单个自然日内的累计扣费积分上限（0 = 不限），与用户级每日花费上限并行生效、各独立累计。预扣前校验，超出返回 429 `key_daily_spend_limit_exceeded`。 |
| 操作审计 | audit log | 管理侧写操作与全部账号认证事件的只追加记录，用于追溯"谁在什么时候改了什么"。不覆盖读操作、/v1 中继调用与用户自助的密钥增删改。 |
| 告警事件 | alert event | 需要管理员知晓的系统异常记录（渠道自动禁用、对账失败等），落库后经 Webhook 与邮件通道投递，同一去重键在抑制窗口内只投递一次。 |
| 管理侧幂等键 | idempotency key | 两套并存、命名空间独立。账务幂等：管理端发放积分时可选携带，服务端以 `admin:<键>` 写入流水的 `request_id`，命中 `(request_id, entry_type)` 唯一索引即判定为重放并返回首次结果；`admin:` 前缀隔离管理侧与中继侧两个标识命名空间。建对象幂等：建用户、建部门、签发 Key 时可选携带，落 `idempotency_records`（键按 `(idempotency_key, scope, COALESCE(integration_id,0))` 唯一），重放时按首次创建的对象 id 回查并标明「重放」。字符集 `[A-Za-z0-9_-]`、1-64 位。 |
| 按日聚合表 | usage daily rollup | 用量日志按 `(日期, 用户, 部门, 模型, 渠道)` 预聚合的报表数据源，只汇总已结算记录。报表读已汇总日期的聚合表加当日原始日志；原始日志按保留期清理，清理前校验该日期已完成汇总。 |

## 枚举定义

### Role（用户角色）`users.role`

等级序：`user(1) < managed(5) < admin(10) < root(100)`，门禁按「最低等级 ≥」放行，更高等级自动通过。

| 取值 | 含义 |
|------|------|
| `user` | 普通用户，访问 Portal 与 /v1 API |
| `managed` | 托管管理员：接入方服务账号身份，仅持服务令牌、不经会话登录。可管理本接入方作用域内的用户、部门、Key、积分、用量与流水，看不到渠道、定价、系统设置与采购成本 |
| `admin` | 管理员，访问 Admin 管理端（运营桶全量 + 托管桶） |
| `root` | 超级管理员，独占提权、系统级设置、接入方与服务令牌管理 |

### UserStatus（用户状态）`users.status`

| 取值 | 含义 |
|------|------|
| `enabled` | 正常 |
| `disabled` | 已禁用，拒绝登录与 API 调用 |

**首次登录强制改密**（`users.must_change_password`）：管理员建号、批量导入与为他人重置密码时置位，表示当前密码是一次性初始凭证。置位期间 `/api/me`、`/api/dept`、`/api/admin` 下的接口一律 403，只有 `/api/auth` 下的自身操作放行；`/v1` 不受约束，那里的凭证是 API Key。本人调用改密接口是清除该标志的唯一途径。管理员留空密码时由系统生成初始密码，明文只在建号响应中返回一次，不落库也不进审计。

**托管账号**（`password_hash` 为空）：由接入方经服务令牌代管的账号，不用于登录（无口令，登录一律按「用户名或密码错误」拒绝，不泄露账号存在性），其调用走 API Key、与口令无关。建号时不生成口令、不置 `must_change_password`，其余（余额、日限额、模型策略、部门归属、用量与流水）与普通账号一致。给托管账号重置密码会破坏「不用于登录」的不变式，返回 400。`password_hash` 列可空（`users.password_hash DROP NOT NULL`）。

### 接入方与作用域（integration）

**接入方**（`integrations` 表）：一个第三方系统对应一个接入方，是对象作用域的载体。每个接入方在创建时配套一个 `role=managed` 的服务账号用户（用户名 `svc:<slug>`，无口令）与若干服务令牌。`integration_id` 列标注对象归属：`users / departments / api_keys` 直接持有；`usage_logs / credit_ledger / usage_daily_rollup / audit_logs` 持有记账/操作时点的快照（0 或 NULL = 运营方内部对象，与 `department_id=0`「未分配」同口径）。

托管视角（`role=managed`）的全部读写都限定在本接入方作用域内，跨作用域的对象一律按「不存在」处理（404，不区分无权与不存在，避免借端点探测对象 ID）；运营 `admin/root` 不受作用域限制，看得见全部接入方与内部对象。停用一个接入方级联停用其全部服务令牌并禁用其全部用户（不删数据），停用后其用户 Key 调 `/v1` 返回 403 `user_disabled`。

### 服务令牌（service token）

接入方后端进程的管理端机器凭据，明文以 `tzs-` 前缀签发，哈希（SHA-256）落库 `service_tokens.token_hash`，明文仅在签发响应中返回一次。与下游 `/v1` 的用户 API Key（`tzl-` 前缀）彻底分离：服务令牌只走管理端 `mw.AdminAuth`，不经会话、不受 `must_change_password` 与会话过期约束。每个令牌认证其接入方的服务账号用户，故 `auth.CurrentUser` / `canManage` / 审计链路对服务令牌零特殊代码。

### 外部标识（external_ref）`users.external_ref` / `departments.external_ref`

接入方侧的对象标识，写入后不可变更，用于两侧对象长期对应（用户名会改名、数字 ID 单向不可反查）。按 `(integration_id, external_ref)` 部分唯一（仅非空值参与，运营方内部对象默认空串不占位）。检索走 `GET /admin/users/external/{ref}` 与 `GET /admin/departments/external/{ref}`，托管视角自动叠加本接入方作用域。不可变由数据库触发器强制（应用层 `UpdateFields` 白名单亦不含）。字符集 `[A-Za-z0-9_\-.:]`、1-64 位。

账号退役一律使用 `disabled`，不使用删除。删除仅适用于尚未产生任何积分流水与用量日志的误建账号：积分流水是对账唯一事实源，用量日志是成本分摊依据，销毁其中任一都会使已发生的消费无法复算。已产生记录的账号调用删除端点返回 409。数据库侧 `credit_ledger.user_id` 为 `ON DELETE RESTRICT` 兜底；`api_keys.user_id` 保持 `ON DELETE CASCADE`——密钥不是账务记录，账号消失时必须同时失效，否则会留下仍可通过认证的悬空密钥。

### DepartmentStatus（部门状态）`departments.status`

| 取值 | 含义 |
|------|------|
| `enabled` | 正常，可作为归属选项分配成员 |
| `disabled` | 已停用，不能再分配新成员；现有成员的归属与调用能力不变 |

部门停用不连带禁用成员账号——部门是成本归属维度，不决定成员能否调用 API。

**部门负责人**（`departments.owner_user_id`）是唯一由部门归属派生出的权限：担任负责人的账号可经 `/api/dept` 查看本部门的消费、预算进度与成员用量，不含任何写操作，也不含网关的采购成本与差额。该资格不写入 `users.role`——角色一经授予即脱离部门归属，转岗后仍保留原部门的查账能力，且无法表达同一人负责多个部门。因此负责人可以是任意角色的账号，且撤销资格的方式是改部门的负责人字段，不是改用户角色。删除部门要求成员数为 0；已产生的用量日志与流水中的部门快照保留原 ID，报表显示为"已删除部门 #N"。数据库侧 `users.department_id` 为 `ON DELETE RESTRICT`，`departments.owner_user_id` 为 `ON DELETE SET NULL`。

### ProjectStatus（项目状态）`projects.status`

| 取值 | 含义 |
|------|------|
| `enabled` | 正常，可作为归属选项分配密钥 |
| `disabled` | 已停用，不能再分配新密钥；现有密钥的归属与调用能力不变 |

项目停用不连带禁用密钥——项目是成本归属维度，不决定密钥能否调用 API，停用项目只表示"不再作为归属选项"。

项目与部门正交：同一密钥可同时归属一个部门与一个项目，两个维度在报表中各自独立聚合。项目负责人（`projects.owner_user_id`）仅作元数据，不派生查账资格（项目无独立的 `/api` 负责人视图，与部门负责人派生 `/api/dept` 的范式不同）。删除项目时，归属它的密钥 `project_id` 由 `ON DELETE SET NULL` 置空、密钥记录本身保留；已产生的用量日志与按日汇总中的项目快照保留原 ID，报表显示为"已删除项目 #N"。

### KeyStatus（API Key 状态）`api_keys.status`

| 取值 | 含义 | 写入方与时机 |
|------|------|--------------|
| `enabled` | 正常 | 创建时的初始状态；用户手工启用；系统在额度上限被上调（高于已用额度）或清除时从 `depleted` 自动恢复 |
| `disabled` | 手工禁用 | 用户在密钥管理界面手工设置 |
| `expired` | 已过期（expires_at 已过） | 系统惰性写入：过期后第一次 API 调用认证时写入，列表中的状态可能滞后于实际过期时间 |
| `depleted` | 独立额度已耗尽 | 系统写入：请求预扣命中密钥额度上限时写入；上限被上调或清除后自动恢复为 `enabled` |

### LedgerEntryType（积分流水类型）`credit_ledger.entry_type`

| 取值 | 含义 | 金额符号 |
|------|------|---------|
| `grant` | 管理员分配 | 正 |
| `revoke` | 管理员扣回 | 负 |
| `redeem` | 兑换码充值 | 正 |
| `consume` | 请求预扣费 | 负 |
| `refund` | 请求失败全额退款 | 正 |
| `settle_adjust` | 结算差额（多退少补） | 正或负 |

幂等约束：同一 `request_id` + `entry_type` 的流水最多一条（部分唯一索引）。结算补扣超出余额时截断到 0，欠扣金额记入流水备注。

流水另带两个归属字段：`operator_id` 记录发起该笔调整的管理员（0 表示消费、退款、兑换等非管理动作），`department_id` 记录记账时点用户所属部门的快照（0 表示未分配）。管理端发放积分时可携带幂等键，服务端以 `admin:<键>` 写入 `request_id` 参与上述唯一索引。

### RedemptionStatus（兑换码状态）`redemptions.status`

| 取值 | 含义 | 是否入库 |
|------|------|---------|
| `unused` | 未使用 | 是 |
| `used` | 已核销（不可再改状态） | 是 |
| `disabled` | 已禁用 | 是 |
| `expired` | 已过期 | 否，展示态 |

`expired` 只出现在接口返回的 `effective_status` 字段，由 `status = unused` 且 `expires_at` 已到点推导得出，不写回 `redemptions.status`：过期不是一次状态变更，而是时间到点后的持续判定，落库会引入「谁来改、何时改」的额外机制，且管理员延后过期时间后无法退回未使用。已核销与已禁用的码不受过期时间影响——那两个状态本身已说明它为何不可用。管理端列表与状态筛选一律按 `effective_status`：`unused` 与 `expired` 是「未使用」的两个互斥子集，两者相加等于库中全部 `status = unused` 的记录。状态变更接口只接受 `unused` 与 `disabled`。

核销失败时后端按具体原因给出提示（不存在、已被使用、已被作废、已过期），四种原因在服务层都归入「兑换码不可用」，只判断可用与否的调用方不受影响。

### Modality（模型形态）`models.modality`

`text`（文本对话）/ `embedding`（向量嵌入）/ `image`（图像生成）。模态是端点级语义，决定 `/v1` 端点路由与 `ProtocolSupportsModality` 协议矩阵，不随模型是否多模态而扩展；模型是否支持视觉/音频/推理等能力由 capabilities 表达（见下），不影响本枚举取值。

### CallType（调用类型，派生维度）`GET /api/admin/stats/cost-by-calltype`

调用类型是面向运营报表的派生维度，不是 `usage_logs` 的物理列。由模型形态与是否流式组合得出：`embedding` 模型 → `embedding`；`image` 模型 → `image`；`text` 模型且 `is_stream=true` → `stream`；`text` 模型且 `is_stream=false` → `non_stream`。`usage_logs` 只存 `model_name`，派生时与 `models` 表按 `name` LEFT JOIN 取 `modality`；模型已删除或不在目录中时 `modality` 为 NULL，统一落入 `other`。代码入口 `store.CallTypeEmbedding/Image/Stream/NonStream/Other`，前端展示标签 `CallTypeLabel`。

### 模型能力属性

以下字段落在 `models` 表；`context_window/max_output/capabilities` 为展示用，不参与计费。

- **context_window**（上下文窗口，`models.context_window`）：模型可接受的最大输入 token 数。是否支持 1M 由数值判定（≥1,000,000），不另设标志位；0 表示未知。
- **max_output**（最大输出，`models.max_output`）：模型单次输出的最大 token 数，0 表示未知或沿用上游默认。
- **capabilities**（能力标签，`models.capabilities`）：模型有区分度的能力标签集合（JSONB 数组），只标注非常配能力，取值 `vision`（图像输入）/ `video`（视频输入）/ `audio`（音频输入输出）/ `reasoning`（深度推理）；工具调用、流式等全系标配不标注。多模态能力在此表达，不扩展 Modality 枚举。
- **model alias**（对外别名，`models.alias`）：把对外短名（如 `opus`）映射到真实模型名（如 `claude-opus-5`）的全局声明，对外名全局唯一（部分唯一索引 `models_alias_uniq WHERE alias <> ''` 保证，空串表示无别名）；中继解析顺序为「请求 model → 全局别名 → 渠道 model_mapping → 上游型号」，全局别名先于渠道映射。

### BillingMode（计费方式）`models.billing_mode`

`per_token`（按 token 单价）/ `per_call`（按次单价，图像生成等）。

端点匹配校验：/v1 各端点在模型准备阶段校验模型形态与计费方式和端点匹配——对话端点（/v1/chat/completions、/v1/messages）要求 `text` + `per_token`，/v1/embeddings 要求 `embedding` + `per_token`，/v1/images/generations 要求 `image` + `per_call`；不匹配返回 400，错误码 `model_endpoint_mismatch`，消息中指明应使用的端点。管理端保存定价与变更计费方式时校验两者一致：按 token 计费至少一项 token 单价非零，按次计费必须配置非零按次单价。

### ModelStatus（模型状态）`models.status`

`enabled` / `disabled`（下架后公开目录与 /v1 不再可见）。

### ImportAction（批量导入的单条处理结果）

批量导入模型与定价时，响应中每条记录的处理结果取值。

| 取值 | 含义 |
|------|------|
| `created` | 模型不存在，已按提交内容新建（模型与定价同事务写入） |
| `updated` | 模型已存在且请求要求覆盖，已更新展示信息与定价 |
| `skipped` | 模型已存在且请求未要求覆盖，站点原有配置保持不变 |
| `failed` | 该条未通过校验或写入失败，同批次其余条目不受影响 |

### UsageSemantic（上游 usage 语义）`usage_logs.usage_semantic`

| 取值 | 语义 |
|------|------|
| `openai` | `prompt_tokens` 含缓存命中，计费时需减去 `cached_tokens` |
| `anthropic` | `input_tokens` 不含缓存，`cache_read/cache_creation` 为独立字段 |
| `gemini` | `promptTokenCount` 含缓存，需减去 `cachedContentTokenCount` |

### 用量日志字段语义（usage_logs token 字段）

用量日志对外展示（Portal/Admin 用量页、CSV 导出）的 token 字段统一为以下语义，与上游各协议的原始语义（见上表 UsageSemantic）解耦：

| 字段 | 语义 |
|------|------|
| `prompt_tokens` | 输入 token 的含缓存合计 = 基础输入 + 缓存读 + 缓存写 |
| `cache_read_tokens` | 缓存读 token，是 `prompt_tokens` 的其中明细，不另行相加 |
| `cache_write_tokens` | 缓存写 token，是 `prompt_tokens` 的其中明细，不另行相加 |
| `completion_tokens` | 输出 token |

计费按明细分项单价计算（基础输入、缓存读、缓存写各按对应单价），`prompt_tokens` 仅是展示口径的合计，不直接参与计价。

### AuditAction（审计动作）`audit_logs.action`

动作命名为 `对象.动作`，与 `target_type` 配套。新增管理侧写操作时必须同步登记本表。

| 分组 | 取值 |
|------|------|
| 认证 | `auth.login`、`auth.logout`、`auth.password_change`、`auth.register` |
| 用户 | `user.create`、`user.update`、`user.delete`、`user.status_change`、`user.status_batch_change`、`user.password_reset`、`user.credit_grant`、`user.import`、`user.credit_batch_grant`、`user.policy_change` |
| 部门 | `department.create`、`department.update`、`department.delete`、`department.member_change` |
| 项目 | `project.create`、`project.update`、`project.delete`（与部门同构的第二层成本归属维度） |
| 模型 | `model.create`、`model.update`、`model.delete`、`model.price_change`、`model.peak_rules_change`、`model.import` |
| 渠道 | `channel.create`、`channel.update`、`channel.delete`、`channel.status_change`、`channel.cost_change`、`channel.test` |
| 兑换码 | `redemption.batch_create`、`redemption.status_change` |
| API 密钥 | `api_key.create`、`api_key.update`、`api_key.delete`（员工自助维护自己的密钥，同样留痕） |
| 系统 | `setting.update`、`alert.test`、`audit.purge` |

`auth.login` 的成功与失败通过 `result` 区分，不设独立动作。

### AuditTargetType（审计对象类型）`audit_logs.target_type`

`user` / `department` / `project` / `model` / `channel` / `redemption` / `setting` / `session` / `audit` / `api_key`。无具体对象时 `target_id` 为 0。

### AuditResult（审计结果）`audit_logs.result`

| 取值 | 含义 |
|------|------|
| `success` | 操作已生效 |
| `failure` | 操作被拒绝或执行失败，原因记入 `message` |

审计记录只追加。不可变性由数据库触发器强制而不只靠应用层：任何 UPDATE 一律拒绝，DELETE 只允许清理写入满 30 天的记录——审计约束的对象往往正是具备数据库权限的内部人员，仅靠「代码里没有更新路径」对其不构成约束。`before` / `after` 中的密码哈希、上游渠道密钥、SMTP 密码、Webhook 密钥一律记为 `"***"`，只表达该字段被修改过，不记录任何值（含旧值）。脱敏按「对象类型 + 字段名」判定，不按字段名一刀切：`code` 在兑换码上是凭据、必须脱敏，在部门上是成本中心编码、必须留痕（那是财务对账的关键标识）。API 密钥的审计快照只记前缀与限制条件，不含明文与哈希。保留期由 `audit_log_retention_days` 控制，清理动作本身写一条 `audit.purge`。

### AlertType（告警事件类型）`alert_events.alert_type`

| 取值 | 触发条件 | 去重键 |
|------|---------|--------|
| `channel_auto_disabled` | 渠道因连续致命错误被自动禁用 | `channel_auto_disabled:<渠道 ID>` |
| `reconcile_failed` | 对账发现余额与流水不一致 | `reconcile_failed` |
| `usage_log_dropped` | 用量日志队列丢弃计数由 0 变为非零 | `usage_log_dropped` |
| `orphan_precharge_found` | 孤儿预扣回收单轮处理条数非零 | `orphan_precharge_found` |
| `department_over_budget` | 部门当月消费超过月度预算 | `department_over_budget:<部门 ID>:<年月>` |
| `backup_failed` | 备份脚本执行失败 | `backup_failed` |
| `user_low_balance` | 有启用用户的余额低于 `low_balance_threshold_credits` | `user_low_balance:<日期>:<人数>` |
| `user_balance_notice` | 给用户本人的余额提醒。只投递到该用户邮箱，不进 Webhook 群通道；抑制窗口固定 24 小时，不随 `alert_dedup_window_sec` 变化 | `user_balance_notice:<用户 ID>` |
| `monthly_grant_failed` | 按月自动发放积分时有账号失败 | `monthly_grant_failed:<年月>` |
| `error_rate_high` | 最近一小时中继失败率超过 `alert_error_rate_percent`，且窗口内请求数达到 `alert_error_rate_min_requests` | `error_rate_high:<年月日时>` |
| `latency_degraded` | 最近一小时中继总耗时的 95 分位超过 `alert_latency_p95_ms` | `latency_degraded:<年月日时>` |
| `policy_malformed` | 模型策略或来源 IP 白名单无法解析，相关调用已被拒绝 | `policy_malformed:<层>:<主体 ID>` |
| `alert_test` | 管理员手工测试告警通道，不参与去重抑制 | 无 |

### AlertSeverity（告警严重度）`alert_events.severity`

`critical`（需立即处理，影响计费正确性或服务可用性）/ `warning`（需知晓并择机处理）。

### AlertStatus（告警投递状态）`alert_events.status`

| 取值 | 含义 |
|------|------|
| `pending` | 已落库，尚未投递 |
| `sent` | 至少一条通道投递成功 |
| `failed` | 本次投递失败（全部已配置通道失败，或未配置任何通道）。后台异步投递路径在指数退避重试期间也以此状态标记每次失败 |
| `suppressed` | 命中抑制窗口，本次不投递 |
| `dead_letter` | 后台重试耗尽仍未送达。需要管理员介入，区别于一次普通失败 |

异步投递（`Raise`）在通道全部失败时按指数退避重试（默认 2s/8s/30s，共 4 次尝试），每次失败回写 `failed` 并记 WARNING；任一次成功即改写 `sent`；全部耗尽转 `dead_letter`。同步投递（`RaiseSync`，用于管理端通道测试与运维 CLI）只尝试一次，失败即 `failed`，不重试。抑制窗口由 `alert_dedup_window_sec` 控制：同一去重键在窗口内只投递一次。面向用户本人的通知自带更长的固定窗口，不受该设置影响——管理员愿意每小时被提醒一次「有人余额不足」，员工不愿意每小时收到同样内容的邮件。

投递范围由事件是否指定收件人决定：未指定时投递到全部已配置通道（Webhook 与管理员邮箱），指定时只发该收件人的邮件，不进 Webhook。

### MonthlyGrantMode（按月自动发放口径）`monthly_grant_mode`

| 取值 | 含义 |
|------|------|
| `topup` | 补足到额度。余额已达额度的账号本月不再发放，未用完的额度不累积到下月 |
| `add` | 增发固定额度。不看当前余额，未用完的部分累积 |

发放由每小时一轮的维护循环驱动，幂等键为「月份 + 用户」：每月首次执行完成发放，同月后续轮次全部命中重放而不记账；服务在月初停机时，恢复后的第一轮补上。

### WebhookFormat（告警 Webhook 报文格式）`alert_webhook_format`

| 取值 | 目标 |
|------|------|
| `generic` | 任意接收端，发送原始告警 JSON |
| `dingtalk` | 钉钉群机器人（markdown 消息；密钥非空时按加签规则追加 `timestamp` 与 `sign` 查询参数） |
| `feishu` | 飞书群机器人（post 富文本；密钥非空时填充 `timestamp` 与 `sign` 字段） |
| `wecom` | 企业微信群机器人（markdown 消息） |
| `slack` | Slack Incoming Webhook（`text` + `blocks`） |

### SetupCheck（首次配置引导检查项）`GET /api/admin/setup-status`

新装系统在管理员配置完成前，任何 `/v1` 调用都必然被拒绝。引导逐项指出未完成的配置，前六项为必需，缺任一项即无法完成一次成功调用。

| 取值 | 完成判定 |
|------|---------|
| `channel` | 至少一条渠道处于启用状态 |
| `model` | 至少一个模型处于上架状态 |
| `model_servable` | 上架模型中至少一个被启用渠道承载（渠道的模型清单含该模型名） |
| `member` | 至少一个角色为 `user` 的启用账号 |
| `credits` | 上述账号中至少一个余额为正 |
| `server_address` | 系统设置 `server_address` 非空 |
| `alert_channel` | 已配置 Webhook 或 SMTP 通道。非必需：缺它告警事件只落库不投递 |

## 系统设置键（settings 表）

| 键 | 类型 | 默认值 | 含义 |
|----|------|--------|------|
| `site_name` | string | Token Zen | 站点名称 |
| `exchange_rate_credits_per_cny` | int64 | 1000000 | 1 货币单位兑换的积分数（全局兑换率，与货币符号联用决定界面金额刻度） |
| `currency_symbol` | string | ¥ | 界面金额展示符号。仅影响展示，不进行汇率换算；积分始终为内部计费单位，对外统一以此符号呈现。修改时管理端弹确认框告警 |
| `usd_cny_rate_milli` | int64 | 7200 | 美元兑人民币汇率千分数（7200 = 7.200），渠道成本折算用 |
| `register_enabled` | bool | false | 是否开放自助注册。默认关闭：企业内部部署的账号由管理员建立 |
| `low_balance_threshold_credits` | int64 | 100000 | 余额预警阈值（积分）：用户余额低于该值时门户展示预警并向管理员告警（0 = 关闭预警） |
| `user_balance_notice_enabled` | bool | true | 余额低于预警阈值时，是否额外向用户本人邮箱发送提醒（需已配置告警邮件通道，且该用户填了邮箱）。管理员侧的低余额告警不受此项影响 |
| `profile_display_name_editable` | bool | true | 是否允许用户自助修改显示名称。关闭后 `PUT /auth/profile` 携带 `display_name` 返回 403，管理员侧仍可改 |
| `profile_email_editable` | bool | true | 是否允许用户自助修改邮箱。关闭后 `PUT /auth/profile` 携带 `email` 返回 403，管理员侧仍可改 |
| `monthly_grant_credits` | int64 | 0 | 按月自动发放给每个启用普通用户的积分额度（0 = 关闭自动发放） |
| `monthly_grant_mode` | string | topup | 按月发放口径，取值见 MonthlyGrantMode |
| `relay_max_retries` | int64 | 3 | 中继失败时最多尝试的渠道数 |
| `precharge_min_tokens` | int64 | 500 | 预扣费最小估算 token 数 |
| `precharge_default_max_tokens` | int64 | 8192 | 请求未带 max_tokens 时预扣采用的输出上限 |
| `rate_limit_per_key_rpm` | int64 | 120 | 单个 API Key 每分钟请求上限（取值 0-100000，0 = 不限流） |
| `rate_limit_per_user_rpm` | int64 | 240 | 单个用户每分钟请求上限（全部 API Key 合并计数，与按密钥上限同时生效取更严者；取值 0-100000，0 = 不限流） |
| `max_concurrent_requests_per_user` | int64 | 10 | /v1 单个用户并发请求子配额（一级闸门，防单一用户占满全站槽位；取值 0-1000，0 = 不限制） |
| `max_keys_per_user` | int64 | 20 | 单个用户可持有的 API Key 数量上限（创建时校验；取值 0-1000，0 = 不限制） |
| `max_concurrent_requests` | int64 | 40 | /v1 并发请求总上限（二级保护，约束全部在途请求；取值 0-1000，0 = 不限制；与内存预算的换算依据见 docs/deployment.md） |
| `max_concurrent_large_requests` | int64 | 2 | /v1 大请求（请求体超过 1 MiB 或长度未知）并发上限，计入总并发（取值 0-1000，0 = 不限制） |
| `channel_disable_failure_threshold` | int64 | 3 | 渠道连续致命错误达到该次数时自动禁用（0 = 不自动禁用；计数为进程内存态） |
| `channel_probe_interval_sec` | int64 | 60 | 自动禁用渠道的半开探测间隔秒数（0 = 关闭探测） |
| `orphan_cleanup_interval_sec` | int64 | 300 | 孤儿预扣回收的执行间隔秒数（0 = 关闭定时回收，仅在服务启动时执行一次） |
| `server_address` | string | 空 | 对外的 API 基址（如 `https://api.example.com`，末尾不带斜杠），随 `/api/site/config` 下发给用户端接入指引作为 Base URL；留空时用户端按浏览器当前站点地址推断 |
| `audit_log_retention_days` | int64 | 180 | 操作审计记录保留天数（0 = 不清理，其余取值不少于 30 天）。默认值覆盖等保二级对安全审计记录留存六个月的要求；下限 30 天与数据库层的不可删除保护期一致 |
| `usage_log_retention_days` | int64 | 90 | 原始用量日志保留天数（0 = 不清理）。清理前校验该日期已完成按日汇总，报表不受影响，仅明细查询与导出受限于保留期 |
| `usage_rollup_enabled` | bool | true | 是否启用用量按日汇总任务（关闭后报表退回直接聚合原始日志） |
| `alert_error_rate_percent` | int64 | 20 | 中继失败率告警阈值（百分数），0 = 关闭。统计窗口为最近一小时 |
| `alert_error_rate_min_requests` | int64 | 50 | 触发失败率告警所需的最小请求数，窗口内不足该值时不判定 |
| `alert_latency_p95_ms` | int64 | 0 | 中继总耗时 95 分位的告警阈值（毫秒），0 = 关闭。默认关闭：合理耗时随模型与请求长度差异巨大 |
| `alert_dedup_window_sec` | int64 | 3600 | 告警抑制窗口秒数：同一去重键在窗口内只投递一次（0 = 不抑制） |
| `alert_webhook_url` | string | 空 | 告警 Webhook 地址（http/https），留空表示不启用该通道 |
| `alert_webhook_format` | string | generic | Webhook 报文格式，取值见 WebhookFormat |
| `alert_webhook_secret` | string | 空 | Webhook 加签密钥（钉钉、飞书），AES-GCM 加密存储，读取接口返回掩码 |
| `alert_smtp_host` | string | 空 | 告警邮件 SMTP 服务器地址，留空表示不启用该通道 |
| `alert_smtp_port` | int64 | 587 | SMTP 端口（取值 1-65535） |
| `alert_smtp_username` | string | 空 | SMTP 登录账号 |
| `alert_smtp_password` | string | 空 | SMTP 登录密码，AES-GCM 加密存储，读取接口返回掩码 |
| `alert_smtp_tls` | string | starttls | SMTP 加密方式：`starttls` / `tls` / `none` |
| `alert_smtp_from` | string | 空 | 告警邮件发件地址，留空时取 `alert_smtp_username` |
| `alert_email_to` | string | 空 | 告警邮件收件地址，多个以逗号分隔 |

### Provider（上游厂商）`channels.provider`

`openai` / `anthropic` / `gemini` / `zhipu` / `qwen` / `deepseek` / `minimax` / `xai` / `moonshot`(Kimi) / `custom`。厂商是业务归属标识，与协议解耦（如智谱渠道可配 anthropic 协议端点）。

模型归属厂商由 `models.provider` 字段记录；厂商元数据（官网、定价页、默认接入地址、支持协议等）以前端预置目录 `ProviderCatalog` 形式提供，只读展示，不入库。

provider 前缀路由（`/{provider}/v1/*`）把候选渠道收窄到同 provider，URL slug 接受品牌/产品/厂商任一常见别名并归一到 `Provider` 枚举值（`relay.SlugToProvider`）：`openai`、`anthropic`、`gemini`/`google`、`glm`/`zhipu`/`chatglm`、`kimi`/`moonshot`、`deepseek`、`qwen`/`tongyi`、`minimax`、`xai`/`grok`、`custom`。未命中的 slug 返回 404 `provider_not_found`（不经认证与限流，不占限流配额）；URL provider 与请求体 model 的归属厂商（`models.provider`）不一致返回 400 `model_provider_mismatch`。该 provider 全部渠道不可用时返回 503 `no_channel`，不回退其他 provider。`/v1/*`（无前缀）入口的跨 provider 行为保持不变。

### ChannelProtocol（渠道协议）`channels.protocol`

| 取值 | 含义 |
|------|------|
| `openai_compat` | OpenAI 兼容协议（覆盖智谱/阿里/DeepSeek/MiniMax/xAI 等） |
| `anthropic` | Anthropic 原生协议（/v1/messages） |
| `gemini` | Google Gemini 原生协议 |

下游端点 × 上游协议支持矩阵（权威定义，代码入口 `domain.ProtocolSupportsModality`）：

| 下游端点 | 要求的模型形态 | `openai_compat` | `anthropic` | `gemini` |
|------|------|------|------|------|
| /v1/chat/completions、/v1/messages | `text` | 支持（直通或跨协议转换） | 支持 | 支持 |
| /v1/embeddings | `embedding` | 支持（直通） | 不支持 | 不支持 |
| /v1/images/generations | `image` | 支持（直通） | 不支持 | 不支持 |

矩阵在两处强制执行：管理端保存渠道时，模型清单中已进入模型目录的向量/图像模型只允许配置在 `openai_compat` 协议渠道上，违反返回 400；中继侧向量/图像端点按矩阵过滤候选渠道，启用渠道存在但协议均不支持时返回 503 `channel_protocol_unsupported`（区别于完全无启用渠道的 `no_channel`）。

### Canonical 规范字段（跨协议转换中枢）

跨协议路径（`canonicalConduit`，下游与上游协议不同）经 canonical 模型中转：`decodeXxxRequest` 把下游请求解析为 `CanonRequest`，`encodeXxxRequest` 再编码为目标上游协议请求体。同协议直通路径不经 canonical，所有字段原样透传，canonical 字段建模对直通零影响。

各协议对 canonical 字段的表达能力矩阵（代码入口 `relay.UpstreamCodec.EncodeBody`）：

| Canonical 字段 | OpenAI 上游 | Anthropic 上游 | Gemini 上游 |
|---|---|---|---|
| `Thinking`（扩展思考） | `reasoning_effort`（按 BudgetTokens 映射 low/medium/high，有损） | `thinking:{type:enabled,budget_tokens}`（原生） | `thinkingConfig:{thinkingBudget}`（原生） |
| `Metadata.user_id` | `user`（平铺字符串，原生） | `metadata:{user_id}`（原生） | 无原生支持，降级丢弃（不拒绝） |
| `ResponseFormat` | `response_format`（原生） | 不支持 → 拒绝 | `responseMimeType`+`responseSchema`（原生） |
| `Logprobs` | `logprobs`+`top_logprobs`（原生） | 不支持 → 拒绝 | 不支持 → 拒绝 |
| `Seed` | `seed`（原生） | 不支持 → 拒绝 | 不支持 → 拒绝 |
| `TopK` | chat 端点不支持 → 拒绝 | `top_k`（原生） | `topK`（原生） |
| `ToolChoice.DisableParallel` | 不支持 → 拒绝 | `disable_parallel_tool_use`（原生） | 不支持 → 拒绝 |

「不支持 → 拒绝」表示目标上游协议无法表达该字段，跨协议路径下 `EncodeBody` 返回 `unsupported_feature`（HTTP 400），由上层透传给客户端，不静默丢弃、不换渠道重试。`Metadata` 在目标协议无原生支持时（Gemini）降级丢弃，不拒绝。

### ChannelStatus（渠道状态）`channels.status`

| 取值 | 含义 |
|------|------|
| `enabled` | 正常参与路由 |
| `manual_disabled` | 管理员手工禁用 |
| `auto_disabled` | 因连续致命错误达到阈值自动禁用（原因记录在 disabled_reason）；由定时半开探测探活，成功后自动恢复为 enabled |

### ChannelTestStatus（渠道连通测试结果）`channels.last_test_status`

| 取值 | 含义 |
|------|------|
| `success` | 最近一次连通测试通过 |
| `failure` | 最近一次连通测试失败（原因见测试响应或审计日志） |
| NULL | 该渠道尚未做过连通测试 |

管理端手工测试与半开探测均写入此字段，配合 `last_test_at` / `last_test_latency_ms` 一起反映渠道最近的健康检查结果。

### CostCurrency（渠道成本币种）`channel_costs.currency`

| 取值 | 含义 | 计量单位 |
|------|------|---------|
| `credits` | 以积分记账 | 与模型售价同单位：积分 / 1M tokens；按次计费模型为 积分 / 次 |
| `usd` | 以美元记账 | 微美元 / 1M tokens（1 美元 = 1,000,000 微美元）；按次计费模型为 微美元 / 次 |

`usd` 记账的成本在利润分析时折算为积分：微美元 × `usd_cny_rate_milli` / 1000 = 微人民币，微人民币 × `exchange_rate_credits_per_cny` / 1,000,000 = 积分。

### UsageStatus（请求计费终态）`usage_logs.status`

| 取值 | 含义 |
|------|------|
| `settled` | 已按真实用量结算 |
| `refunded` | 请求失败，预扣已全额退款 |
| `failed` | 失败：无预扣（如余额不足被拒），或结算写入失败（预扣保留，待孤儿预扣清理补偿，补偿退款后状态改写为 `refunded`） |

### ErrorClass（上游错误分类）`usage_logs.error_class`

| 取值 | 判定 | 渠道动作 | 请求动作 |
|------|------|---------|---------|
| `auth_fatal` | 401/403/invalid_api_key | 计入连续失败 | 换渠道重试 |
| `quota_fatal` | 402，或非 429 的 4xx 响应体含明确余额类文案 | 计入连续失败 | 换渠道重试 |
| `rate_limited` | 429（一律视为上游限流，不做文本细分） | 不计入 | 换渠道重试 |
| `transient` | 5xx/超时/网络错误 | 不计入 | 换渠道重试 |
| `bad_request` | 400/内容策略 | 不计入 | 不重试，透传给用户 |
| `stream_aborted` | 流式响应中途中断（上游流读取失败，或单个事件超出缓冲上限） | 不计入 | 响应已开始输出，无法改换渠道重发 |

`stream_aborted` 与其余取值的区别：它标记的调用照常结算（已产生的 token 要计费），`status` 仍是 `settled`，只是这次的回答不完整。员工反映「回答被截断」时，管理员据此在用量日志里认出这类调用。

渠道禁用与恢复：致命错误类（`auth_fatal`/`quota_fatal`）按渠道累计连续失败次数（成功响应清零），达到 `channel_disable_failure_threshold` 阈值时渠道置为 `auto_disabled`；自动禁用的渠道由后台按 `channel_probe_interval_sec` 周期做半开探测，探活成功自动恢复为 `enabled`、清除禁用原因并记录事件日志。连续失败计数为进程内存态，重启后清零。

### 计费会话（BillingSession）

一次 /v1 请求的三阶段状态机：预扣（consume）→ 结算（settle_adjust 多退少补）→ 或失败退款（refund）。原则："扣了没退"可通过流水修复，"用了没扣"不可逆，因此结算补扣超出余额时截断到 0 而非拒绝。
