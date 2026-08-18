# CLAUDE.md

本项目是 Token Zen Lite —— 自建的 AI 模型 API 网关与积分计费系统，Go 后端 + React 双前端同仓（monorepo）。

## 核心业务

管理员统一配置上游渠道并给成员分配积分（或兑换码充值）→ 成员创建 API Key → 调用 /v1 下游 API → 系统路由到上游渠道 → 按模型直接单价 × 时段倍率扣积分。全局唯一兑换率默认 1 人民币 = 1,000,000 积分。术语与枚举的权威定义在 `docs/glossary.md`，API 契约在 `docs/api-contract.md`，部门与审计的数据模型在 `docs/design/组织与审计模型.md`。

## 技术栈与环境要求

- 后端：Go 1.25+（chi + GORM + PostgreSQL，golang-migrate 纯 SQL 迁移 embed 进二进制）；无 Redis（内存限流/缓存）
- 前端：React 18 + TypeScript + Antd 5 + Vite，pnpm workspace（apps/admin、apps/portal、packages/shared）；Node 18+、pnpm 8+
- 数据库：PostgreSQL 16
- 会话：alexedwards/scs（postgres store）；/v1 用 API Key（SHA-256 哈希存储）

## 常用命令

```bash
./setup.sh                          # 新环境一键引导（前置检查 + 建库 + 装依赖 + 构建）
bash scripts/start.sh               # 启动全部（后端 + 前端 dev）
bash scripts/start.sh --backend-only
bash scripts/stop.sh / restart.sh / status.sh [--logs backend|admin|portal]

make test        # Go 全量测试（需 TZL_TEST_DATABASE_URL）+ 前端类型检查与单测
make build       # Go 二进制 + 前端构建
cd server && go test -p 1 ./...   # 集成测试共用测试库，必须 -p 1 串行
cd server && go run ./cmd/tzl reconcile   # 积分对账（未通过时主动告警）
cd server && go run ./cmd/tzl alert <类型> <标题> [正文]   # 运维脚本复用告警通道

docker compose --env-file .env.docker up -d --build   # 容器方式整套起（db + backend + web）
```

## 端口与账号（开发环境）

| 服务 | 端口 |
|------|------|
| 后端 API | 19030 |
| Admin dev | 19073 |
| Portal dev | 19074 |
| PostgreSQL | 5433（`.env.example` 默认连接串；库 tzl_dev / tzl_test，用户 tzl） |

root 初始密码在首次启动时打印到后端日志且仅打印一次（`bash scripts/status.sh --logs backend` 搜索「初始 root」）；也可在 `.env` 中预设 `TZL_ROOT_PASSWORD`。

## 架构要点

> 完整的分层架构、模块清单与依赖关系见 [`docs/overview/architecture.md`](docs/overview/architecture.md)。以下为本仓日常作业需牢记的要点。

- `server/internal/domain`：全部业务枚举（字符串值），与前端 `packages/shared/src/constants` 同步，新增枚举先改 `docs/glossary.md`。
- `server/internal/pricing`：积分计算纯函数（int64 整数运算，一次性向上取整）；时段倍率求值；美元官价到积分的折算（`convert.go`，大整数运算避免中间积溢出）与内置预置价目（`presets.json`，采集时点的厂商公开价，随二进制嵌入）。
- `server/internal/billing`：余额调整唯一入口（行锁 + 流水同事务 + (request_id, entry_type) 幂等索引）。不变式：用户余额 == 流水之和，`tzl reconcile` 校验。
- `server/internal/relay`：中继核心。conduit 抽象"下游协议 × 上游协议"：同协议直通（透传 + model 改写 + usage 嗅探），跨协议经 canonical 中间模型转换（codec_*.go + stream_codec.go）。计费三阶段：预扣 → 结算（多退少补，补扣截断到 0）→ 失败退款，defer 兜底。`/v1/messages/count_tokens` 不消耗上游 token，因此不计费不写日志（count_tokens.go）：优先转发给 anthropic 协议渠道，失败则回落本地估算，且不计入渠道失败计数。
- 渠道上游密钥 AES-GCM 加密存储（TZL_ENCRYPT_KEY 派生，不可变更）。
- `server/internal/audit`：管理侧写操作与认证事件的只追加记录，敏感字段只记「已变更」不记取值；由各处理函数在业务成功后显式调用（中间件拿不到对象名称与变更前后的字段）。不可变性由数据库触发器强制：UPDATE 一律拒绝，DELETE 只允许清理写入满 30 天的记录。
- `server/internal/alerting`：告警事件落库 + 去重抑制 + 投递（Webhook 报文与 SMTP 邮件）。业务模块依赖 `alerting.Notifier` 接口，未配置通道时可传 nil。事件带 `EmailTo` 时为定向通知（如给员工本人的余额提醒）：只走邮件、收件人取该字段、不投 Webhook；`SuppressFor` 可覆盖该条事件的抑制窗口。
- `server/internal/maintenance`：每小时一轮的数据维护——用量按日汇总、审计与用量日志的保留期清理、部门超预算检查、用户低余额检查（含给本人的邮件提醒）、按月自动发放积分（幂等键为「月份 + 用户」）、中继失败率与耗时分位的连续量告警。
- `server/internal/obs`：结构化日志、request_id、访问日志与运行指标。指标为进程内存态的计数器/直方图/仪表，经 `/metrics` 以 Prometheus 文本格式导出（令牌或 root 会话鉴权）；中继侧的埋点收口在 `relay.finishLog`，与用量日志同一口径。
- 部门是扁平单层的成本归属维度，不持有积分余额、不参与扣费；用量日志与积分流水记录记账时点的部门快照，报表按快照聚合（用户转部门后历史口径不变）。
- 有效模型集合 = 部门策略 ∩ 用户策略 ∩ 密钥白名单，各层只能收窄；策略解析失败一律拒绝并告警，不放行。
- 响应信封 `{success, message, data}`、分页 `{page, page_size, total, items}` 由 `api/respond` 统一输出（用量日志 CSV 导出例外，直接输出流）。
- 前端金额展示：用户界面不直接展示积分（积分仅为内部计费单位），统一经 `useMoney()`（各 app 的 site store 提供）取得 `MoneyFormatters`，用 `money.format(credits)`（总额 2 位）/ `money.formatDetail(credits)`（明细 6 位，默认兑换率下无损）展示；符号由系统设置 `currency_symbol` 决定（默认 ¥，仅换符号不换算汇率），兑换率与符号均从 `GET /api/site/config` 动态获取，禁止硬编码。货币输入框用 `money.fromCredits` / `money.toCredits` 在积分与货币间换算（API 契约仍以积分为单位）。

## 部署

非容器化单机：见 `docs/deployment.md` 与 `deploy/`（systemd 单元、Nginx 配置、备份脚本）。容器化：`.env.docker.example` + `docker-compose.yml`。生产必填 TZL_SESSION_SECRET、TZL_ENCRYPT_KEY。

## 测试纪律

- Go 集成测试依赖 `TZL_TEST_DATABASE_URL`，未设置时自动跳过；共用测试库，跨包必须 `-p 1` 串行。
- 计费相关改动必须跑 `internal/billing`、`internal/api`（中继场景）与 reconcile。
- `make test` 含接口层覆盖率下限门禁（`scripts/coverage-gate.sh`，起步 70%）；新增端点须配套用例，否则门禁失败。
- 提交门禁由 `.github/workflows/ci.yml` 执行（go vet、gofmt、全量测试、前端类型检查/单测/构建、镜像构建）。
