# 技术架构

本文档记录 Token Zen Lite 的系统边界、分层架构、模块划分与模块间依赖关系。术语与枚举的权威定义见 `docs/glossary.md`，API 契约见 `docs/api-contract.md`，部门与审计的数据模型见 `docs/design/组织与审计模型.md`，部署拓扑见 `docs/deployment.md`。

## 1. 系统定位与技术栈

Token Zen Lite 是用 Go 从零编写的 AI 模型 API 网关与积分计费系统，定位为中小型企业内部自用与个人自用的轻量网关。核心业务：管理员分配积分（或兑换码充值）→ 用户创建 API Key → 调用 `/v1` 下游 API → 系统路由到上游渠道 → 按模型单价 × 时段倍率扣积分。

- 后端：Go + chi + GORM + PostgreSQL（golang-migrate 纯 SQL 迁移，embed 进二进制）；无 Redis（内存限流/缓存）。
- 前端：React 18 + TypeScript + Antd 5 + Vite，pnpm workspace（`apps/admin`、`apps/portal`、`packages/shared`）。
- 会话：alexedwards/scs（postgres store）；`/v1` 用 API Key（SHA-256 哈希存储）。

系统边界：对外暴露管理端（admin）、用户门户（portal）两套前端，与 `/v1` 下游兼容协议（OpenAI / Anthropic）+ `/{provider}/v1/*` 厂商前缀路由；对内经中继调用上游渠道（openai_compat / anthropic / gemini 协议）。

## 2. 分层架构

后端 15 个 internal 包 + 1 个入口命令，按依赖方向分 5 层，依赖单向朝基础层收敛、无环。

```
┌─────────────────────────────────────────────────────────────┐
│ L4 装配/传输  api（HTTP 路由+中间件+handler，13 sub-controller）│
├─────────────────────────────────────────────────────────────┤
│ L3 编排/运行时  relay（中继核心）  maintenance（每小时后台）    │
├─────────────────────────────────────────────────────────────┤
│ L2 领域服务  billing  alerting  audit  auth                   │
├─────────────────────────────────────────────────────────────┤
│ L1 计算/持久化  pricing（纯函数）  store（GORM 仓储）          │
├─────────────────────────────────────────────────────────────┤
│ L0 基础  domain  obs  strutil  config  secrets  ratelimit     │
└─────────────────────────────────────────────────────────────┘
        cmd/tzl：装配根（buildDeps → api.NewRouter + 后台调度）
```

规则：上层可依赖下层，同层尽量不互相依赖（例外见各模块说明）；`api` 是唯一装配根；`billing` 是余额变更唯一入口；`store` 是共享持久化骨干。

## 3. 模块清单

按层列出每个模块：路径、职责、依赖、关键设计。

### L0 基础（叶子，零内部依赖）

| 模块 | 职责 | 关键设计 |
|---|---|---|
| `internal/domain` | 业务枚举与值对象，前后端单一事实源 | 枚举以 `glossary.md` 为权威；`AllProtocols()`/`AuditActions()` 等「全部合法值」切片供启动覆盖检查与 `Valid()` 校验；跨协议 `UsageSemantic→NormalizedUsage` 归一化 |
| `internal/obs` | 结构化日志、request_id 跨服务传播、访问日志、Prometheus 指标（进程内存态，经 `/metrics` 导出） | 中继埋点单一收口在 `relay.finishLog`，保证指标与用量日志同口径；暴露 `Recorder` 接口供测试注入 |
| `internal/config` | 环境变量加载，启动期 fail-fast 校验 | 静态配置只在装配根消费，不下沉到服务包；运行时旋钮另走 `store.SettingsRepo` |
| `internal/secrets` | AES-GCM 密钥盒（TZL_ENCRYPT_KEY 派生，不可变更） | 渠道上游密钥加密存储 |
| `internal/ratelimit` | 内存限流 + 并发闸（ConcurrencyGate）+ 登录失败锁（FailureLocker） | `Limiter` 接口（可替换）；时钟可注入；单机前提，扩多副本需 Redis 化 |
| `internal/strutil` | `Truncate` 等字符串工具 | — |

### L1 计算 / 持久化

| 模块 | 职责 | 依赖 | 关键设计 |
|---|---|---|---|
| `internal/pricing` | 积分计算纯函数：单价 × 倍率、时段倍率求值、美元官价→积分折算、内置预置价目 | domain | 全程 `big.Int` 防溢出，超限饱和到 `MaxInt64`；成本/收入侧共用取整函数保证同向；无 I/O 无状态 |
| `internal/store` | GORM repository，约一文件一实体；纯 SQL 迁移 embed 进二进制 | domain, pricing | 18 个 Repo，契约一致（`ErrNotFound` 哨兵、`NormalizePagination`、白名单 `UpdateFields`、软删）；`store→pricing` 仅用于 `Model.Price.ToPricing()` DTO 转换 |

### L2 领域服务

| 模块 | 职责 | 依赖 | 关键设计 |
|---|---|---|---|
| `internal/auth` | 会话(scs) + APIKey(SHA-256) + 密码(bcrypt) + 中间件双源认证（运营会话/托管令牌） | domain, obs, store | 双源认证在 `AdminAuth` 中间件统一注入；密码用 bcrypt（不可反查），APIKey/服务令牌用 SHA-256（认证需反查）；认证失败有结构化日志 |
| `internal/audit` | 管理侧写操作与认证事件的只追加记录 | auth, domain, obs, store, strutil | 不可变性由 DB 触发器强制（UPDATE 一律拒绝、DELETE 仅清理满 30 天记录）；敏感字段按 target-type 脱敏，只记「已变更」不记取值 |
| `internal/alerting` | 告警事件落库 + 去重抑制 + 投递（Webhook 五格式 + SMTP） | domain, obs, secrets, store, strutil | 暴露 `Notifier` 端口（业务模块依赖窄接口、nil-safe）；定向邮件（EmailTo）只走邮件；`deliverWithRetry` 把投递/回写/退避参数化 |
| `internal/billing` | 余额调整唯一入口 | alerting, auth, domain, obs, store | `Service.Apply`/`applyTx`：行锁串行化 + 流水同事务 + `(request_id,entry_type)` 幂等；`api_keys.credit_used` 同事务维护（消除双轨账本）；`tzl reconcile` 校验不变式「用户余额 == Σ流水」与密钥额度 |

### L3 编排 / 运行时

| 模块 | 职责 | 依赖 | 关键设计 |
|---|---|---|---|
| `internal/relay` | 下游↔上游中继核心，承载"AI API 网关"职能 | alerting, billing, domain, obs, pricing, secrets, store, strutil | **conduit 抽象**："下游协议 × 上游协议"——同协议直通（透传 + model 改写 + usage 嗅探），跨协议经 canonical 中间模型 + `codec_{anthropic,gemini,openai}` 转换；**三阶段计费**：预扣 → 结算（多退少补，补扣截断到 0）→ 失败退款，`BillingSession` 状态机 + `defer EnsureFinal` 兜底；`Engine` 退薄壳，协作对象 `ChannelSelector`（provider 过滤+亲和+加权）、`ChannelHealth`（连续失败计数+自动禁用+半开探活）、`UsageSink`（finishLog 收口+异步队列）；`count_tokens` 不计费不写日志 |
| `internal/maintenance` | 每小时一轮数据维护 | alerting, audit, billing, domain, obs, store | `Scheduler.RunOnce` 9 子任务（用量按日汇总、保留期清理、部门超预算、用户低余额、按月发放积分、中继健康告警），`runTask` 横切包装统一计时/计数/异常日志与独立超时；月度发放幂等键「月份+用户」经 `billing.Grant`→唯一索引 |

### L4 装配 / 传输

| 模块 | 职责 | 依赖 | 关键设计 |
|---|---|---|---|
| `internal/api` | HTTP 路由 + 中间件 + 全部 handler，唯一装配根 | 全部 | 依赖容器 `Deps` 由 `cmd/tzl` 构造；`NewRouter(d Deps)` 内部按 feature 拆给 13 个 sub-controller（auth/me/user-admin/catalog/org/integrations/billing/reports/relay/dept/external/public/system），各 controller 仅持本 feature 所需依赖；响应信封 `{success,message,data}` + 分页 `{page,page_size,total,items}` 由 `api/respond` 统一；管理端三桶（admin/managed/root）按角色隔离 |

### 入口

| 模块 | 职责 | 关键设计 |
|---|---|---|
| `cmd/tzl` | `serve`：`migrate.Up` → `store.Open` → `buildDeps`（构造全部 repo/service）→ `api.NewRouter` + `startBackground`（中继探活、孤儿预扣清理、maintenance 调度）；CLI 子命令：`reconcile`（对账）、`alert`（运维告警）等 | `buildDeps` 是唯一装配点（纯装配、无 I/O 副作用）；静态配置在装配根拆给组件，运行时旋钮走 `SettingsRepo` |

## 4. 依赖关系

依赖图（→ 表示依赖，朝基础层收敛）：

```
domain  obs  strutil  config  secrets  ratelimit     ← 叶子
  ↑       ↑    ↑        ↑       ↑        ↑
pricing──┘    │                        │
  ↑           │                        │
store──┘      │                        │
  ↑↑↑↑        │                        │
auth ─────────┘                        │
audit ────────┘                        │
alerting ──────────────────────────────┘
billing ──────┘
  ↑
relay ─────────────────────────────────┘
maintenance ──┘
api ───────────────────────────────────┘ （依赖全部）
  ↑
cmd/tzl
```

关键关系：

- **`api` 是装配根**：`cmd/tzl` 构造 `api.Deps`，调 `api.NewRouter`；路由直接绑各 sub-controller 方法。
- **`billing` 是余额变更唯一入口**：`relay`（中继计费三阶段）、`maintenance`（月度发放）、`api`（管理员发放/兑换码核销）的所有扣费/退款都经 `billing.Service.Apply`；不变式由 `tzl reconcile` 校验。
- **`alerting.Notifier` 是唯一的刻意接口缝**：`relay`/`maintenance`/孤儿清理依赖该窄接口，未配置通道时传 `nil`（nil-safe）。
- **`store` 是共享持久化骨干**：10 个包依赖它；跨实体报表聚合（如 `RollupRepo.Aggregate`）属持久化层职责。
- **`store→pricing` 反向依赖**：仅用于 `Model.Price.ToPricing()` 这类模型→定价 DTO 转换，可接受。

## 5. 装配与运行

启动序列（`cmd/tzl serve`）：

1. `config.Load()` + `obs.InitLogger()`
2. `migrate.Up(databaseURL)`（纯 SQL 迁移，embed）
3. `store.Open()` → `*gorm.DB`
4. `bootstrapRoot`（首次启动创建 root 账号）
5. `buildDeps()`：构造全部 repo + service（`billing.Service`、`relay.Engine` 含三个协作对象、`alerting.Service`、`audit.Recorder`、`auth.SessionManager` 等）
6. `startBackground`：中继探活循环、孤儿预扣清理调度、maintenance 每小时调度、用量日志丢弃监控
7. `api.NewRouter(deps)` → HTTP 服务监听

停机：`shutdown` 经 `shutdownFlusher` 接口刷盘中继用量日志/录制队列；ctx 到期放弃等待。

## 6. 部署拓扑（简）

非容器化单机：systemd 单元 + Nginx 反代 + 备份脚本（见 `docs/deployment.md`）。生产必填 `TZL_SESSION_SECRET`、`TZL_ENCRYPT_KEY`（后者不可变更，丢失需重新获取全部上游密钥）。容器化：`docker compose --env-file .env.docker up -d --build`（db + backend + web）。

**单机前提**：`ratelimit`、`relay` 亲和表/健康计数、`obs` 指标均为进程内态；扩多副本需将这些换为共享存储（如 Redis）。
