# Token Zen Lite API 契约

后端全部 web 端点的权威清单。统一信封 `{success, message, data}`；分页 `{page, page_size, total, items}`；时间字段为 ISO 8601 字符串；金额内部记账单位为"积分"（整数）。认证为 session cookie（无自定义请求头）。成功响应恒含三个顶层键（无返回体的端点 `data` 为 `null`，而非省略该键）；失败响应仅含 `success`（false）与 `message`，不含 `data` 键。`/v1` 下游端点不套用本信封，见对应章节。

**金额字段双形式约定**：凡响应中含积分整数字段（如 `credit_balance`、`credits_charged`、`credits_cost`、`amount`、`monthly_budget_credits` 等），同时附带同名 `_money` 字段（如 `credit_balance_money`）——货币定点字符串，默认兑换率（1 货币单位 = 1,000,000 积分）下为 6 位小数、无符号、逐行可精确汇总。积分整数字段保留不变（向后兼容），`_money` 为纯增量。货币符号与兑换率为元数据，见 `GET /site/config` 的 `currency_symbol` 与 `exchange_rate_credits_per_cny`。该约定覆盖全部 `/api/admin/*`（托管可达）与 `/api/me/*` 响应；`/api/dept/*` 与运营内部仪表盘（overview/profit/redemptions）暂不附带 `_money`。

## 运维端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /healthz | `{status, usage_log_dropped}`。数据库不可用时 503 |
| GET | /metrics | Prometheus 文本格式的运行指标，**不套用信封**（消费者是抓取端）。访问控制：配置了 `TZL_METRICS_TOKEN` 时接受 `Authorization: Bearer <令牌>`，或 root 会话；其余一律 401。指标清单见 `docs/deployment.md` |

## 认证 /api/auth

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /auth/login | `{username, password}` → data: User |
| POST | /auth/logout | 登出 |
| POST | /auth/register | `{username, password, display_name?}`（register_enabled 控制） |
| GET | /auth/me | 当前用户（需登录） |
| PUT | /auth/password | `{original_password, password}`；成功后作废该用户全部登录会话，当前会话重新签发（本次登录保持有效）。本人改密是清除 `must_change_password` 的唯一途径 |
| PUT | /auth/profile | `{display_name?, email?}` |

## 公开接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /site/config | `{site_name, exchange_rate_credits_per_cny, currency_symbol, register_enabled, server_address, low_balance_threshold_credits, profile_display_name_editable, profile_email_editable}`。`server_address` 为管理员配置的对外 API 基址，空串表示未配置，用户端按当前站点地址推断；`low_balance_threshold_credits` 为余额预警阈值，0 表示关闭预警；`currency_symbol` 为界面/账单金额展示符号（默认 ¥，仅影响展示不进行汇率换算），`exchange_rate_credits_per_cny` 为 1 货币单位兑换的积分数；`profile_display_name_editable` / `profile_email_editable` 为用户自助修改资料的字段级开关（默认 true，关闭后 `PUT /auth/profile` 携带对应字段返回 403，门户对应输入框置灰，管理员侧不受影响） |

## 用户端 /api/me（UserAuth）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /me/balance | `{credit_balance, credit_used, request_count, exchange_rate_credits_per_cny, currency_symbol, credit_balance_money, credit_used_money}`（`*_money` 为货币定点字符串，见文首双形式约定） |
| POST | /me/redeem | `{code}` → data: LedgerEntry |
| GET | /me/models | ModelInfo[]（含 price、peak_rules，仅 enabled；每项附 `available` 布尔标记——是否存在启用渠道承载，false 表示当前调用必然失败，前端标注"暂不可用"并置灰，不从目录剔除）。需登录：已上架的模型清单属于内部信息 |
| GET | /me/ledger | 分页账目；query: entry_type, view, page, page_size。默认按调用合并：同一 `request_id` 的预扣、结算差额、退款合并为一条净额，形态为 `{id, request_id, entry_type, amount, balance_after, note, created_at, entries[]}`，`entries` 是构成该行的原始流水（升序）。`entry_type=consume` 在合并视角下表示「只看调用扣费」。`view=raw` 返回未合并的 LedgerEntry 分页 |
| GET | /me/service-status | 本站服务可用性：`{status, checked_at, models_total, models_available, unavailable_models[], providers[], recent_hour, recent_day}`。`status` 取值 operational/degraded/outage；`providers` 每项为 `{provider, total, enabled, auto_disabled, status}`，只给厂商级数量，不含渠道名称与地址；两个窗口为 `{window_minutes, requests, failed, failure_rate_percent, p95_latency_ms}` |
| GET | /me/usage-logs | 分页用量日志；query: model, status, api_key_id, start_timestamp, end_timestamp(Unix 秒), request_id。token 字段语义：`prompt_tokens` 为含缓存合计，`cache_read_tokens`/`cache_write_tokens` 为其中明细（见 glossary.md 用量日志字段语义节） |
| GET | /me/usage-summary | query: group_by=day\|model\|key, start_timestamp, end_timestamp(Unix 秒，缺省时回退 days)，days(默认 30，上限 365) → SummaryRow[]。时间范围按自然日对齐（服务器时区）。走按日汇总表与原始日志合并查询，原始日志按保留期清理后结果仍保留。SummaryRow 只含 `{group_key, requests, credits_charged, total_tokens}`，不暴露网关采购成本与差额 |
| GET | /me/usage-daily | query: start_timestamp, end_timestamp(Unix 秒，缺省时回退 days)，days(默认 30，上限 365) → DailyStat[]。口径与 /me/usage-summary 一致（汇总表 + 原始日志合并），`credits_cost` 固定为 0（用户侧不暴露网关成本） |
| GET | /me/cache-report | query: group_by=day\|model(默认 day), start_timestamp, end_timestamp(Unix 秒，缺省时回退 days)，days(默认 30，上限 365) → `{from, to, overall:{requests, prompt_tokens, cache_read_tokens, cache_write_tokens, cache_hit_rate}, groups:[{group_key, requests, prompt_tokens, cache_read_tokens, cache_write_tokens, cache_hit_rate, credits_charged}]}`。缓存命中率 `cache_hit_rate = cache_read_tokens / max(1, cache_read_tokens + prompt_tokens)`（0–1，输入侧 token 中缓存命中占比）。口径与 /me/usage-summary 一致（汇总表 + 原始日志合并，保留期安全）；不暴露网关采购成本与差额。group_by=day 时 groups 按日期升序 |
| GET | /me/heatmap | query: start_timestamp, end_timestamp(Unix 秒，缺省时回退 days)，days(默认 30，上限 365)，model(可选) → `{from, to, cells:[{day_of_week(0=周日..6=周六), hour(0..23), requests, credits_charged}]}`。cells 只含产生数据的格子，前端补零。数据源是原始 usage_logs（按日汇总表不含小时维度），因此受保留期约束。weekday/hour 按服务器本地时区换算。只统计本人 settled 日志，不暴露网关采购成本 |
| GET | /me/token-report | query: group_by=day\|model(默认 model), start_timestamp, end_timestamp(Unix 秒，缺省时回退 days)，days(默认 30，上限 365) → `{from, to, overall:{prompt_tokens, cache_read_tokens, cache_write_tokens, completion_tokens, total_tokens}, groups:[{group_key, requests, prompt_tokens, cache_read_tokens, cache_write_tokens, completion_tokens, total_tokens, credits_charged}]}`。token 结构：输入(不含缓存)/缓存命中读/缓存写入/输出四类 billed token 的合计与占比，`total_tokens` 为四类之和。口径与 /me/usage-summary 一致（汇总表 + 原始日志合并，保留期安全）；不暴露网关采购成本与差额。group_by=day 时 groups 按日期升序，否则按 total_tokens 降序 |
| GET | /me/keys/ | 分页 ApiKey；query: keyword, status |
| POST | /me/keys/ | `{name, credit_limit?, expires_at?, allowed_models?, allowed_ips?}` → CreatedApiKey（key 明文仅此一次） |
| GET | /me/keys/{id} | 单个 Key |
| PUT | /me/keys/{id} | `{name?, status?(enabled/disabled), credit_limit?, clear_limit?, expires_at?, clear_expires?, allowed_models?, allowed_ips?}` |
| DELETE | /me/keys/{id} | 删除 |

用户对象带 `must_change_password`：密码由管理员设定、本人尚未改过。为真时 `/api/me`、`/api/dept`、`/api/admin` 下的全部接口返回 403 `请先修改初始密码`，只有 `/api/auth` 下的自身操作（读取自身、改密、更新资料、登出）放行；`/v1` 下游调用不受影响——那里的凭证是 API Key，与密码无关。

`GET /auth/me` 在用户字段之外附带 `managed_department_ids`：当前账号担任负责人的部门 ID 列表，空数组表示不是任何部门的负责人。前端据此决定是否显示部门费用入口。

## 部门负责人视图 /api/dept（会话认证，最低角色 user）

访问资格不来自角色，而来自 `departments.owner_user_id` 与当前登录用户的归属关系：任何角色的用户只要是某部门的负责人，即可查看该部门；不是负责人的管理员在本组端点下同样被拒（管理员另有 `/admin/stats` 下的全站报表）。除 `/dept/departments` 外，每个端点都按请求中的 `department_id` 逐次校验归属，不通过返回 403 `无权查看该部门`；该 403 对「部门不存在」与「部门存在但无权」不作区分，避免借本组端点探测部门 ID。

返回口径只含本部门的消费额与用量，不含网关的采购成本（`credits_cost`）与差额（`margin`）——那是网关运营方的数据，与部门的费用分摊无关。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /dept/departments | 当前账号负责的部门 `[{id, name, code, status, monthly_budget_credits}]`；非负责人返回空数组而非 403 |
| GET | /dept/budget | query: department_id(必填), month(YYYY-MM，省略取当月)。→ `{month, department_id, department_name, requests, prompt_tokens, completion_tokens, credits_charged, monthly_budget_credits, budget_used_percent, over_budget}`。当月无消费时返回各项为 0 的行，不返回空对象 |
| GET | /dept/cost-report | query: department_id(必填), group_by=user\|model\|day(默认 user，其余取值回落为 user), start_timestamp, end_timestamp(Unix 秒，省略取最近 30 天)。→ `{group_by, department_id, department_name, from, to, rows: [{group_id, group_key, requests, prompt_tokens, completion_tokens, credits_charged}]}`。部门范围以校验通过的 `department_id` 为准，请求中的其它部门筛选参数一律被覆盖 |
| GET | /dept/members | query: department_id(必填), page, page_size。分页 `{user_id, username, display_name, status, credit_balance, month_credits_charged, month_requests}`；当月未产生消费的成员也列出，消费字段为 0 |

## 管理端 /api/admin（AdminAuth；设置为 root 独占）

用户：
- GET/POST `/admin/users/`（query: keyword, role, status, department_id）。`department_id=0` 表示只看未分配部门的用户，与不传该参数（不按部门筛选）语义不同。POST 支持 `department_id`——指向已停用部门时返回 400。POST 的 `password` 可留空，留空时由系统生成一次性初始密码，明文在响应的 `initial_password` 中返回一次（不落库、不进审计，此后无法再次取得）；无论密码由谁设定，新账号一律带 `must_change_password`。POST 支持 `initial_credits`（建号即发放的积分，0 = 不发放，负数返回 400），与批量导入的同名字段口径一致——余额为零的账号即使密钥正确也会被拒绝调用。`email` 非空时校验形态，不合格返回 400（PUT 与 `/auth/profile` 同）。POST 另支持：`passwordless`（建无口令托管账号，跳过口令生成与 `must_change_password`；托管管理员强制为 true）、`external_ref`（接入方侧标识，写入后不可变）、`idempotency_key`（建对象幂等，重放返回首次结果并标明）
- GET/PUT/DELETE `/admin/users/{id}`（PUT: display_name/email/role/department_id/allowed_models/daily_spend_limit，role 为 root 独占）。`department_id` 传 0 表示转为未分配，不传表示不变更归属；`allowed_models` 为用户级模型策略（空数组 = 本层不限制）；`daily_spend_limit` 为单自然日累计扣费积分上限（0 = 不限制），负数返回 400。DELETE 为物理删除，仅允许用于尚未产生积分流水与用量日志的误建账号；已产生任一记录时返回 409（`该用户已产生积分流水…请改用禁用账号`），账号与记录均不变。账号退役请改用 `POST /admin/users/{id}/status`。删除成功时该用户全部登录会话立即失效，其 API Key 随账号一并销毁
- POST `/admin/users/{id}/status` `{status}`
- POST `/admin/users/{id}/reset-password` `{password}`；成功后作废该用户全部登录会话，message 提示"该用户全部登录已失效"（root 重置自己密码时当前会话重新签发）。为他人重置时置位 `must_change_password`，本人重置自己的密码不置位
- POST `/admin/users/{id}/credits` `{amount(正分配/负扣回), note, idempotency_key?}` → LedgerEntry。`amount` 为 0 时 400；负数扣回超过用户当前余额时 400（`扣回金额超过用户当前余额`）且余额与流水均不变（区别于中继结算补扣场景的截断到 0）。携带 `idempotency_key`（1-64 位字母、数字、下划线或连字符，越界 400）后重复提交只记一次账，返回首次记账结果且 message 为"该发放已记账，本次未重复调整"；不传该字段时行为与既有调用方一致——各自记账，需调用方自行防重
- GET `/admin/users/{id}/keys` 该用户全部 Key
- POST `/admin/users/{id}/keys` `{name, credit_limit?, expires_at?, allowed_models?, allowed_ips?, idempotency_key?}` → CreatedApiKey（代签发，明文仅此一次）。`idempotency_key` 命中重放时返回首次创建的密钥对象（不含明文，明文仅首次返回）并标明重放
- PUT `/admin/users/{id}/keys/{key_id}` `{name?, status?, credit_limit?, clear_limit?, expires_at?, clear_expires?, allowed_models?, allowed_ips?}`（owner 锁定 {id}）
- DELETE `/admin/users/{id}/keys/{key_id}` 吊销
- GET `/admin/users/external/{ref}` 按 `external_ref` 精确检索用户；托管视角自动叠加本接入方作用域，跨作用域或不存在一律 404
- POST `/admin/users/import` 批量创建用户。请求 `{items: [{username, password?, display_name?, email?, department_id?, initial_credits?}], default_department_id?}`——`password` 留空时由系统生成一次性初始密码，单次上限 500 条，超出或 items 为空返回 400。逐条独立处理：新建记 created，用户名已存在记 skipped（不改动既有账号的密码与归属），校验或写入失败记 failed 且不影响同批其余条目。`initial_credits` 在建号后单独发放；账号已建但发放失败时该条记 failed 并说明需单独补发，账号保留。响应 `{created, skipped, failed, results: [{username, action, user_id, message, initial_password?}]}`，整体仍为 200。`initial_password` 只对系统代为生成的记录出现，只返回这一次；导入的账号一律带 `must_change_password`
- POST `/admin/users/batch-status` 批量启用或禁用用户（离职与调岗的集中处置）。请求 `{user_ids[] | department_id, status}`——`department_id` 指定时对该部门当前全部成员操作，与 `user_ids` 二选一；两者都缺省或 `status` 取值非法返回 400（整批拒绝，不部分执行）。逐条独立处理：状态确实改变记入 succeeded，本就是目标状态记入 unchanged，用户不存在、无权管理、禁用自己记入 failed 且不影响同批其余账号。被禁用的账号立即无法登录，其 API Key 调用返回 403 `user_disabled`。响应 `{succeeded, unchanged, failed, results: [{user_id, username, ok, message}]}`，整体仍为 200
- POST `/admin/credits/batch-grant` 批量发放积分。请求 `{user_ids[] | department_id, amount, note?, idempotency_key?}`——`department_id` 指定时对该部门当前全部成员发放，与 `user_ids` 二选一；两者都缺省返回 400。每个用户一条独立流水（合并记账会使"谁拿到多少"无法从流水复算）。`idempotency_key` 内部按用户拼接，同批不同用户不会互相命中。响应 `{succeeded, replayed, failed, results: [{user_id, ok, replay, message}]}`

部门：
- GET/POST `/admin/departments/`（query: keyword, status）。列表项附 `member_count` 与 `owner_username`（负责人未指定、或负责人已不是本部门成员时为空串）。`owner_user_id` 必须是本部门成员，否则 400；新建部门时尚无成员，只能先建部门再指定负责人。POST 载荷：`{name, code?, owner_user_id?, monthly_budget_credits?, allowed_models?, status?, note?}`；名称或成本中心编码重复返回 409
- GET/PUT/DELETE `/admin/departments/{id}`。DELETE 要求成员数为 0，否则 409（`该部门仍有成员，请先把成员转出或改分到其他部门`）；已产生的用量日志与流水中的部门快照保留原 ID，报表显示为"已删除部门 #N"
- POST `/admin/departments/{id}/members` `{user_ids[], remove?}` → `{affected}`。`remove` 为 true 表示把这些用户转为未分配部门；单次上限 500 个用户。向已停用部门划入新成员返回 400
- GET `/admin/departments/external/{ref}` 按 `external_ref` 精确检索部门；托管视角叠加作用域，跨作用域或不存在一律 404

渠道：
- GET/POST `/admin/channels/`（query: keyword, status, model）
- GET/PUT/DELETE `/admin/channels/{id}`（payload: name, provider, protocol, base_url, api_key(明文，编辑留空=不换), models[], model_mapping{}, priority, weight, param_override{}, header_override{}, test_model）。PUT 中 priority、weight、param_override、header_override 为指针语义：字段缺席（或 null）保持原值；param_override/header_override 显式传 `{}` 才清空。创建与编辑均校验协议与模型形态匹配：models[] 中已进入模型目录的向量/图像模型只能配置在 `openai_compat` 协议渠道（支持矩阵见 glossary.md 的 ChannelProtocol 节），违反返回 400 并指明模型与协议；未进入模型目录的名称不校验
- PUT `/admin/channels/{id}/status` `{status: enabled|manual_disabled}`
- POST `/admin/channels/{id}/test`（?model= 可选）→ `{ok, latency_ms, message}`
- GET/PUT `/admin/channels/{id}/costs`（PUT: `{costs: ChannelCost[]}` 全量替换）。ChannelCost 字段：`model_name`、`currency`（`credits`/`usd`，缺省 `credits`）、`input_cost`、`output_cost`、`cache_read_cost`、`cache_write_cost`（token 类单价）、`per_call_cost`（按次单价），全部为非负整数。单位由 `currency` 决定：`credits` 为 积分 / 1M tokens（按次为 积分 / 次）；`usd` 为 微美元 / 1M tokens（按次为 微美元 / 次），1 美元 = 1,000,000 微美元。定义见 glossary.md 的 CostCurrency 节

模型：
- GET/POST `/admin/models/`（query: keyword, status, modality；列表含 price/peak_rules，每项附 `channel_count`——启用渠道承载数，0 表示模型上架后用户调用必然失败，供上架前自检）
- GET/PUT/DELETE `/admin/models/{id}`（payload: name, display_name, description, modality, billing_mode, status, tags；name 创建后不可修改——渠道路由、成本、密钥白名单与用量日志均按名称字符串关联，PUT 传入不同 name 返回 400，改名须新建模型并下架旧模型）
- PUT `/admin/models/{id}/price`（ModelPrice 各字段，积分/1M tokens 或积分/次；校验定价与计费方式一致：per_token 至少一项 token 单价非零，per_call 必须配置非零按次单价；变更 billing_mode 时同样校验既有定价）
- PUT `/admin/models/{id}/peak-rules` `{rules: [{timezone, start_minute, end_minute, days_of_week[], multiplier_percent(≥100), enabled}]}` 全量替换。规则语义：时段为左闭右开区间 `[start_minute, end_minute)`（当日分钟数，0-1440），`start_minute` ≥ `end_minute` 返回 400，跨午夜时段须拆成两条规则；`timezone` 缺省 `Asia/Shanghai`，不合法返回 400；`days_of_week` 取值 1-7（ISO 星期），空数组表示全周（保存时展开为 1-7）；`multiplier_percent` 低于 100 返回 400
- GET `/admin/models/{id}/channel-costs` 跨渠道成本比价 → ChannelCost[]（字段与单位见渠道成本端点说明）
- POST `/admin/models/import` 批量导入模型与定价。请求 `{items: [{name, display_name, description, modality, billing_mode, status, tags, price{...}}], overwrite}`，单次上限 200 条，超出或 items 为空返回 400。`price` 必填——导入的模型直接进入对外目录，无定价会被零扣费调用。逐条处理、逐条独立事务：模型不存在则新建（created），已存在且 `overwrite` 为 false 则跳过（skipped），已存在且为 true 则覆盖展示信息与定价（updated），单条校验或写入失败记 failed 且不影响同批其余条目。响应 `{created, updated, skipped, failed, results: [{name, action, message}]}`，整体仍为 200——失败信息在 results 中逐条给出
- GET `/admin/models/pricing-presets`（query: markup_percent，取值 100-1000，缺省 100 = 按厂商官价平价折算，越界返回 400）内置预置价目 → `{priced_at, note, markup_percent, usd_cny_rate_milli, exchange_rate_credits_per_cny, providers: [{id, name, pricing_url, models: [{name, display_name, modality, billing_mode, *_usd(微美元/1M tokens), price{...积分单价}}]}]}`。折算在服务端完成，管理端预览到的积分单价与直接提交导入后落库的值一致

计费：
- GET `/admin/redemptions/`（query: keyword, status, batch_id）。`status` 按展示态筛选，可取 `unused`（未使用且未过期）、`expired`（未使用但已过期）、`used`、`disabled`。列表项在存储字段之外附 `effective_status`，界面按它显示状态与是否提供作废入口，取值定义见 `docs/glossary.md` 的 RedemptionStatus 一节
- POST `/admin/redemptions/batch` `{count(1-1000), credits, name, expires_at?}` → `{batch_id, codes[]}`（明文仅此一次）
- PUT `/admin/redemptions/{id}/status` `{status: unused|disabled}`。不接受 `expired`：那是推导出的展示态，没有对应的落库字段
- GET `/admin/ledger`（query: user_id, entry_type）

日志与统计：
- GET `/admin/usage-logs`（query 同 /me/usage-logs 外加 user_id, channel_id, username）。`username` 按用户名模糊匹配，与 `user_id` 同时给出时两个条件叠加；该参数仅在管理端视角生效，员工侧的用户维度已强制限定为本人。列表项在 UsageLog 之外附 `username` 与 `channel_name`，用户或渠道已删除时为空串
- GET `/admin/usage-logs/detail?request_id=` 单条含 price_snapshot
- GET `/admin/setup-status` → `{completed, pending_count, checks: SetupCheckItem[], counts, server_address}`。新装系统的配置完整性检查，检查项取值与完成判定见 `docs/glossary.md` 的 SetupCheck 一节；`completed` 只看必需项，可选项未完成不影响该字段
- GET `/admin/stats/overview` → StatsOverview
- GET `/admin/stats/usage-daily?days=` → DailyStat[]
- GET `/admin/stats/profit?group_by=channel|model&from=&to=(Unix 秒)` → ProfitRow[]
- GET `/admin/usage-logs/export` 按当前筛选条件导出全部记录（query 同 `/admin/usage-logs`）。响应为 UTF-8 BOM 开头的 CSV 流，**不套用 `{success, message, data}` 信封**——消费者是浏览器与表格软件，不经过前端的统一解析。单次上限 20 万行，超出时截断并在服务端日志记录
- GET `/admin/stats/cost-report?group_by=user|department|model|channel|day|key&start_timestamp=&end_timestamp=&user_id=&department_id=&model=&channel_id=&api_key_id=` → `{group_by, from, to, rows: CostReportRow[]}`。时间范围按自然日对齐（服务器时区），缺省为最近 30 天；`department_id=0` 表示只看未分配部门。`group_by=key` 按密钥聚合（关联 `api_keys.name`），`api_key_id` 按密钥筛选。0015 迁移前已汇总的历史日期按 `api_key_id=0` 存在，在密钥维度下标记为「历史汇总（按密钥不可拆）」，不回填。只统计已结算记录，失败与已退款请求不计入
- GET `/admin/stats/department-budget?month=YYYY-MM` → `{month, rows: DepartmentBudgetRow[]}`。缺省为当月，格式非法返回 400。未产生消费的部门同样出现在结果中（否则"预算全部未用"与"部门不存在"无法区分）
- GET `/admin/stats/heatmap?start_timestamp=&end_timestamp=&user_id=&model=&channel_id=&department_id=` → `{from, to, cells:[{day_of_week(0=周日..6=周六), hour(0..23), requests, credits_charged}]}`。缺省为最近 30 天，按自然日对齐。cells 只含产生数据的格子，前端补零。数据源是原始 usage_logs（按日汇总表不含小时维度），受保留期约束。weekday/hour 按服务器本地时区换算。托管视角叠加接入方作用域（与 cost-report 一致）。只统计 settled 日志 |
- GET `/admin/stats/calendar?days=` → DailyStat[]（与 usage-daily 同形，按日升序）。供首页年度日历热力图：走 `usage_daily_rollup`（RollupRepo.Aggregate 按日维度），原始日志按保留期清理后结果仍保留（retention-safe），支持 365 天回看（默认 90、上限 365）。`/admin/stats/usage-daily` 读原始 usage_logs、不 retention-safe，保留为趋势图数据源不动。托管视角叠加接入方作用域
- GET `/admin/stats/cache-report?group_by=day\|model\|project\|channel&start_timestamp=&end_timestamp=&user_id=&department_id=&project_id=&channel_id=` → `{from, to, overall: {requests, prompt_tokens, cache_read_tokens, cache_write_tokens, cache_hit_rate}, groups: [{group_key, requests, prompt_tokens, cache_read_tokens, cache_write_tokens, cache_hit_rate, credits_charged, credits_charged_money, credits_cost, credits_cost_money}]}`。管理端缓存命中率统计：走 rollup（保留期安全），命中率口径 `cache_read / max(1, cache_read + prompt)`，与 `/me/cache-report` 一致；group 行额外暴露 `credits_cost`（成本），overall 不含成本。托管视角叠加接入方作用域
- GET `/admin/stats/runtime` → `{generated_at, gauges:[{name,value}], counters:[{name,labels,value}], histograms:[{name,labels,count,sum,p50,p95,p99}]}`。进程级运行指标的结构化快照，供运维大屏实时层（10s 轮询）。gauge 含 `tzl_relay_in_flight`/`tzl_usage_log_dropped`；counter 含 `tzl_relay_requests_total{model,status}`、`tzl_relay_errors_total{model,error_class}`、`tzl_channel_attempts_total{channel,outcome}`、`tzl_http_requests_total{group,method,status}`；histogram 分位由累计桶线性插值。计数器为进程内累计态，进程重启归零；RPM/失败率等速率由前端两次采样差推算。与 `/metrics`（Prometheus 文本、root+token）互不影响

审计与告警：
- GET `/admin/audit-logs/`（query: action, target_type, target_id, operator_id, result, keyword, start_timestamp, end_timestamp）→ 分页 AuditLog[]。审计只提供读取，没有更新与删除端点
- GET `/admin/audit-logs/actions` → 全部审计动作取值（string[]），供管理端筛选下拉直接使用
- GET `/admin/alerts/`（query: alert_type, severity, status, start_timestamp, end_timestamp）→ 分页 AlertEvent[]
- POST `/admin/alerts/test` 向当前配置的通道同步发一条测试消息 → AlertEvent。未配置任何通道时 400；通道全部投递失败时 502 并在 message 中给出各通道的失败原因

设置（root）：
- GET `/admin/settings` → SettingItem[]。以密文存储的项（`alert_webhook_secret`、`alert_smtp_password`）附 `secret: true`，`value` 为掩码 `********`（未配置时为空串），`default` 不回显。枚举型设置项（`monthly_grant_mode`、`alert_webhook_format`、`alert_smtp_tls`）附 `options`，为该项的全部合法取值，管理端据此渲染下拉选择
- PUT `/admin/settings` `{key, value}`（逐项提交）。密文项提交明文即可，服务端 AES-GCM 加密后存储；提交掩码占位值返回 400（避免把掩码当成新密码存进去），提交空串表示清除

接入方与服务令牌（root 独占）—— 第三方系统经服务令牌程序化管理其下属用户的运营入口：
- POST `/admin/integrations/` `{name, slug}` → Integration。事务内建接入方（status=enabled）与其服务账号用户（`svc:<slug>`，role=managed，无口令）。slug 或 `svc:<slug>` 用户名冲突返回 409
- GET `/admin/integrations/`（query: page, page_size）分页接入方
- GET `/admin/integrations/{id}` 详情，附 `service_account_user_id` 与 `token_count`
- PUT `/admin/integrations/{id}` `{name}` 改名（slug 不可变）
- POST `/admin/integrations/{id}/disable` 级联停用：接入方置 disabled + 其全部服务令牌 disabled + 其全部用户 disabled，不删数据。停用后其用户 Key 调 `/v1` 返回 403 `user_disabled`
- POST `/admin/integrations/{id}/service-tokens` `{name}` → `{...ServiceToken, token}`（`tzs-` 前缀明文仅此一次）
- GET `/admin/integrations/{id}/service-tokens` 该接入方令牌列表（hash 不返回）
- PUT `/admin/integrations/{id}/service-tokens/{token_id}/status` `{status: enabled|disabled}`
- DELETE `/admin/integrations/{id}/service-tokens/{token_id}` 软删令牌

管理端认证（`mw.AdminAuth`）双源：运营方会话 cookie 或接入方服务令牌（`Authorization: Bearer tzs-…`），二者都把识别出的用户注入上下文。管理端按角色分三桶：托管桶（`requireManaged`，managed/admin/root）含用户/部门/Key/积分/流水/用量/成本口径报表/审计/external 检索；运营桶（`requireAdmin`，admin/root）含渠道/模型/兑换码/告警/利润与总览/批量运营；root 桶（`requireRoot`）含系统设置与接入方管理。托管服务令牌访问运营桶端点返回 403（已认证后按角色拒，非 401）。托管视角（role=managed）的全部读写限定在本接入方作用域内，跨作用域对象一律 404；运营 admin/root 不受作用域限制。

## 下游 /v1（API Key 认证）

面向第三方应用的 LLM 中继 API。响应不使用 `{success, message, data}` 信封，而是各下游协议的原生格式；错误格式见下文错误码表。

### 端点与协议范围

| 方法 | 路径 | 下游协议 | 可路由的上游渠道协议 | 流式 |
|---|---|---|---|---|
| POST | /v1/chat/completions | OpenAI Chat Completions | `openai_compat`（直通）、`anthropic`、`gemini`（跨协议转换） | 支持 |
| POST | /v1/messages | Anthropic Messages | `anthropic`（直通）、`openai_compat`、`gemini`（跨协议转换） | 支持 |
| POST | /v1/messages/count_tokens | Anthropic Token 计数 | `anthropic`（转发取上游计数）；无此类渠道或上游未实现时由本站估算 | — |
| POST | /v1/embeddings | OpenAI Embeddings | 仅 `openai_compat`（其余协议渠道不参与候选） | 不支持 |
| POST | /v1/images/generations | OpenAI Images | 仅 `openai_compat`（同上） | 不支持 |
| GET | /v1/models | OpenAI 模型清单格式 | 不路由上游，返回本站上架、落在有效模型集合内（部门策略 ∩ 用户策略 ∩ 密钥白名单）且存在启用渠道承载的模型（无渠道承载的模型调用必然失败，故从清单剔除） | — |
| GET | /v1/key/info | 本站自有格式 | 不路由上游，返回 Key 限额用量与用户积分余额 | — |

- 直通 = 请求体透传，仅改写 `model`、注入鉴权与渠道参数/请求头覆盖；跨协议 = 经 canonical 中间模型转换（详见 CLAUDE.md relay 节）。
- 下游端点 × 上游协议支持矩阵的权威定义见 glossary.md 的 ChannelProtocol 节：/v1/embeddings 与 /v1/images/generations 无跨协议转换，`anthropic`/`gemini` 协议渠道即使配置了对应模型也不参与候选；该模型的启用渠道全部因协议被排除时返回 503 `channel_protocol_unsupported`，与完全无启用渠道的 `no_channel` 区分。
- 上游返回的 `model` 字段一律改写回本站公开模型名，不暴露渠道映射后的上游模型名。
- 各中继端点校验模型形态与计费方式和端点匹配（对应关系见 glossary.md 的 BillingMode 节）：/v1/chat/completions 与 /v1/messages 要求 `text` + `per_token`，/v1/embeddings 要求 `embedding` + `per_token`，/v1/images/generations 要求 `image` + `per_call`。不匹配返回 400 `model_endpoint_mismatch`，消息中指明该模型应使用的端点。
- /v1/messages/count_tokens 不消耗上游 token，因此不计费、不写用量日志、不参与积分对账；但仍走完整的身份、有效模型集合与上架校验（放宽校验会让该端点变成探测本站模型清单的旁路），并同样受限流与并发闸门约束。转发失败（无 `anthropic` 协议渠道承载该模型、上游未实现该端点、上游报错）时回落到本站估算并返回 200，估算口径与预扣费一致（字节数 ÷ 4，计入 `messages` 与 `system`），中文场景偏差偏大。转发失败不计入渠道连续失败计数、不触发自动禁用——上游代理未实现计数端点是常见情况，据此禁用会连带切断该渠道正常的对话流量。
- 请求体上限 20 MiB；超限导致 JSON 解析失败，返回 400 `invalid_request_error`。
- 四个 POST 中继端点经过双维度限流（按 Key `rate_limit_per_key_rpm` 与按用户 `rate_limit_per_user_rpm` 同时生效，任一超限即 429）与分层并发闸门（用户子配额 `max_concurrent_requests_per_user` → 全局总上限 `max_concurrent_requests` / 大请求配额 `max_concurrent_large_requests`，请求体 > 1 MiB 或长度未知视为大请求；任一层拒绝即 503）；GET /v1/models 与 GET /v1/key/info 仅做认证，不限流。

### provider 前缀路由 `/{provider}/v1/*`

除 `/v1/*` 外，全部端点另在 `/{provider}/v1/*` 下镜像挂载（如 `/anthropic/v1/messages`、`/deepseek/v1/chat/completions`）。provider 前缀锁定候选渠道的 `channels.provider`：

- URL slug 接受品牌/产品/厂商任一常见别名并归一到 `Provider` 枚举（`openai`、`anthropic`、`gemini`/`google`、`glm`/`zhipu`/`chatglm`、`kimi`/`moonshot`、`deepseek`、`qwen`/`tongyi`、`minimax`、`xai`/`grok`、`custom`）。未命中的 slug 返回 404 `provider_not_found`，且在认证与限流之前返回，不占用限流配额。
- provider 与请求体 model 归属厂商（`models.provider`）不一致返回 400 `model_provider_mismatch`，消息指明两侧 provider 取值；判定发生在 model 别名解析之后、有效模型集合校验同源。
- 该 provider 全部渠道不可用时返回 503 `no_channel`（或上游错误透传），**不回退其他 provider**；同 provider 的多个渠道照常负载均衡与容错。
- provider 前缀是额外约束，**不绕过** Key 权限：有效模型集合（部门策略 ∩ 用户策略 ∩ 密钥白名单）、形态/计费方式校验照常生效。
- `/{provider}/v1/models` 只返回 `model.provider == URL provider` 且存在同 provider 启用渠道承载的模型；`/{provider}/v1/key/info` 挂载但 provider 前缀对该端点 no-op（响应与 `/v1/key/info` 一致，兼容把 base_url 设为 `/{provider}/v1` 的客户端）。
- `/v1/*`（无前缀）入口的跨 provider 容错行为保持不变。

### 认证

两种请求头等价，都携带本站签发的 API Key 明文（`tzl-` 前缀）：

| 请求头 | 说明 |
|---|---|
| `Authorization: Bearer <key>` | OpenAI 风格。`Bearer ` 前缀可省略（直接放明文亦可） |
| `x-api-key: <key>` | Anthropic 风格。仅当 Authorization 头为空时读取 |

两种头对全部 /v1 端点等价，与端点的下游协议无关（用 x-api-key 调 /v1/chat/completions、用 Bearer 调 /v1/messages 均可）。`anthropic-version` 请求头不校验、不要求。同时携带两种头时以 Authorization 为准。

### 错误格式与错误码表

错误响应体按下游协议二选一：

- OpenAI 格式（/v1/chat/completions、/v1/embeddings、/v1/images/generations、/v1/models、/v1/key/info）：`{"error": {"message", "type", "code"}}`，`type` 与 `code` 同值，即下表业务码。
- Anthropic 格式（/v1/messages、/v1/messages/count_tokens）：`{"type": "error", "error": {"type", "message"}}`，`error.type` 为下表业务码。

例外：认证、限流、并发闸门阶段（进入端点业务逻辑之前）的错误一律为 OpenAI 格式，包括 /v1/messages；未匹配任何路由的路径返回 404 站点信封 `{success: false, message: "接口不存在"}`。

| HTTP | 业务码 | 含义 | 建议客户端动作 |
|---|---|---|---|
| 401 | `invalid_api_key` | 缺少 API Key、格式非法或哈希无匹配 | 检查 Key 明文与请求头写法，不重试 |
| 403 | `key_disabled` | Key 被禁用 | 联系管理员或换用其他 Key |
| 403 | `key_expired` | Key 已过期（首次触发时状态同步置为 expired） | 换用新 Key |
| 403 | `key_unavailable` | Key 处于其他不可用状态 | 换用新 Key |
| 403 | `ip_not_allowed` | 来源 IP 不在 Key 的白名单内；白名单内容无法解析时同码返回并提示"配置有误"（不放行，服务端另发一条策略告警） | 从白名单 IP 发起，或调整 Key 白名单 |
| 403 | `user_disabled` | Key 所属账号被禁用 | 联系管理员 |
| 403 | `model_not_allowed` | 请求的模型不在有效模型集合内（部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄）；任一层策略无法解析时同码返回并提示"配置有误"（不放行，服务端另发一条策略告警） | 换模型；策略由管理员维护的两层需联系管理员调整 |
| 400 | `invalid_request_error` | 请求体不是合法 JSON、缺少 model 字段，或跨协议转换时请求结构无法解析 | 修正请求后重发，不重试 |
| 400 | `unsupported_feature` | 跨协议路由时请求体携带目标上游协议无法表达的字段（如 logprobs/seed 路由到 Anthropic、response_format 路由到 Anthropic、top_k 路由到 OpenAI chat 端点等）；错误消息指明具体字段与目标协议。同协议直通不触发此错误 | 移除该字段，或改用支持该字段的同协议渠道；不重试 |
| 400 | `model_endpoint_mismatch` | 模型形态或计费方式与端点不匹配 | 按错误消息改用正确端点 |
| 400 | `model_provider_mismatch` | `/{provider}/v1/*` 入口：URL 前缀锁定的 provider 与请求体 model 的归属厂商（`models.provider`）不一致 | 改用一致的前缀或模型，不重试；消息指明两侧 provider 取值 |
| 404 | `model_not_found` | 模型不存在或未上架 | 用 GET /v1/models 核对可用模型 |
| 404 | `provider_not_found` | `/{provider}/v1/*` 入口：URL 前缀的 provider slug 未命中任何已知厂商别名（在认证与限流之前返回，不占限流配额） | 核对 provider 前缀拼写 |
| 402 | `insufficient_credits` | 用户积分余额不足以完成预扣 | 充值后重试 |
| 402 | `key_quota_exceeded` | Key 剩余额度不足以完成预扣 | 调整 Key 限额或换 Key；余额可查 GET /v1/key/info |
| 429 | `daily_spend_limit_exceeded` | 当日累计扣费已达该用户的每日花费上限 | 次日自动恢复；需要更高额度请联系管理员上调 |
| 429 | `key_daily_spend_limit_exceeded` | 当日累计扣费已达该 API Key 的每日花费上限 | 次日自动恢复；换用其他密钥，或由管理员/本人上调该 Key 上限 |
| 429 | `rate_limited` | 触发每分钟请求数限流（按 Key 与按用户两个维度同时生效，任一超限即拒绝；被拒绝的请求同样计入两个窗口） | 退避后重试 |
| 502 | `upstream_error` | 上游连接失败、上游 5xx/限流经换渠道重试后仍失败，或上游 200 响应体无法解析 | 退避后重试 |
| 503 | `overloaded` | 并发闸门拒绝：可能是本用户并发子配额用尽（`max_concurrent_requests_per_user`），也可能是全站总并发或大请求配额已满。三种原因统一返回 503 `overloaded`，不在响应中区分（区分依据在服务端拒绝日志的 reason 字段）；客户端不应据此判定服务不健康 | 退避后重试 |
| 503 | `no_channel` | 没有启用中的渠道承载该模型 | 稍后重试或联系管理员 |
| 503 | `channel_protocol_unsupported` | 该模型有启用中的渠道，但协议均不被当前端点支持（/v1/embeddings、/v1/images/generations 仅支持 `openai_compat` 渠道） | 联系管理员调整渠道协议或模型配置 |
| 503 | `channel_error` | 渠道配置异常（如密钥解密失败）且无其他候选 | 联系管理员 |
| 503 | `model_not_priced` | 模型未配置定价（或按次模型未配置按次单价） | 联系管理员 |
| 500 | `internal_error` | 计费系统异常、构建上游请求失败等服务端错误 | 退避后重试，持续出现联系管理员 |
| 上游原状态码 | 上游原业务码 | 上游返回不可换渠道重试的 4xx（非 401/402/403/429，如参数校验错误）时转发上游错误：状态码原样，响应体经解析后语义等价转发（JSON 字段不变，键序可能重排）；响应体无 `error` 字段时包装为下游格式的 `upstream_error`。例外：响应体含 `invalid_api_key`、`account_deactivated` 或余额不足类文案的 4xx 判定为渠道级致命错误，换渠道重试，候选耗尽后返回 502 `upstream_error`，不透传 | 按上游错误语义处理 |

失败请求的计费语义：预扣发生在选择渠道之前，凡最终以错误响应结束的请求，预扣积分全额退还（用量日志状态 `refunded`）；预扣前失败（401/403/400/404/402 等）不产生任何扣费。

### 流式响应

请求体 `"stream": true` 开启。开启后成功响应为 `200`，`Content-Type: text/event-stream`；HTTP 状态码在首帧前确定，预扣失败、无渠道等错误仍以上表 JSON 错误返回。流开始后上游中断的，不再发错误帧，流以收尾帧结束；例外：/v1/messages 走 `anthropic` 直通时收尾的 `message_stop` 事件来自上游透传，上游中断则流直接截止，无收尾帧。客户端中途断连不中止计费：上游请求的生命周期与下游连接解耦（总时长受服务端上游超时 `TZL_UPSTREAM_TIMEOUT_SEC` 约束），服务端继续读完上游流并按实际用量结算。

OpenAI 下游（/v1/chat/completions）：

- 帧格式 `data: {chat.completion.chunk JSON}\n\n`，无 `event:` 行，流末发送 `data: [DONE]`。
- 上游为 `openai_compat` 时逐 chunk 透传（改写 model）；转发上游请求时自动注入 `stream_options.include_usage = true`，用量位于上游发来的末尾 usage chunk。
- 上游为 `anthropic` / `gemini` 时由本站重建 chunk 流，结束序列固定为：带 `finish_reason` 的终止 chunk → `choices` 为空数组、携带 `usage`（`prompt_tokens` / `completion_tokens` / `total_tokens`）的用量 chunk → `data: [DONE]`。

Anthropic 下游（/v1/messages）：

- 帧格式 `event: <type>\ndata: {事件 JSON}\n\n`，无 `[DONE]` 标记，以 `message_stop` 事件结束（`anthropic` 直通且上游中断时无此事件，见上文例外）。
- 事件序列：`message_start` → `content_block_start` / `content_block_delta` / `content_block_stop`（文本与 tool_use 块）→ `message_delta` → `message_stop`。
- 用量位置：上游为 `anthropic` 时透传上游事件，输入 token 在 `message_start` 的 `message.usage`，输出 token 在 `message_delta` 的 `usage`；跨协议重建时 `message_start` 的 usage 为占位零值，完整用量（`input_tokens`、`output_tokens`、`cache_read_input_tokens`、`cache_creation_input_tokens`）在 `message_delta` 的 `usage`。

流式用量缺失（上游未上报）时按请求体与输出字节数估算计费，用量日志标记 `usage_estimated`。
