# Token Zen Lite

[English](README.md) | 简体中文

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](server/go.mod)
[![Node](https://img.shields.io/badge/Node-18+-339933.svg)](package.json)

自托管的 AI 模型 API 网关与积分计费系统。管理员一次性配置好上游渠道，给团队成员分配积分；
之后所有人用一把 API Key 即可调用全部可用模型——网关按次计量，并留下完整的用量与成本记录。

它解决的是逐人发放厂商密钥的老问题：不再把各家厂商的 API Key（各自有独立控制台、配额与账单）
直接分发出去，而是由 Token Zen Lite 持有上游密钥、签发自己的 Key，在内部按管理员分配的积分余额计费。

Token Zen Lite 定位为中小型企业内部自用与个人自用的网关，不是公共服务（SaaS）：无在线支付、
无公开定价页、默认关闭自助注册（账号由管理员创建）、密码重置由管理员完成。

## 功能特性

- **多协议中继**。下游端点遵循 OpenAI 与 Anthropic 的请求形态（`/v1/chat/completions`、
  `/v1/messages`、`/v1/embeddings`、`/v1/images/generations`、`/v1/models` 与 `count_tokens`），
  另提供 `/{provider}/v1/*` 厂商前缀路由把请求锁定到单一厂商。上游渠道支持 OpenAI 兼容、
  Anthropic、Gemini 三种协议：同协议流量直通（透传 + model 改写 + usage 嗅探），跨协议流量经
  canonical 中间模型转换，流式响应同样支持。
- **按量积分计费**。模型单价为每百万 token 的积分数（图像模型按次），叠加时段倍率，全程整数
  运算。每个请求按三阶段计费：预扣 → 结算（多退少补，补扣截断到 0）→ 失败退款；幂等流水表
  （`(request_id, entry_type)` 唯一索引）是唯一记账依据，`tzl reconcile` 校验余额 == 流水之和。
- **渠道管理**。同一模型可配多条渠道，按优先级路由并自动故障转移；连续失败的渠道自动禁用，
  半开探测通过后重新回到轮转。
- **部门成本归属**。扁平单层部门作为成本维度；用量日志与积分流水记录记账时点的部门快照，
  用户转部门后历史报表口径不变。可选月度预算与超预算告警。
- **面向内部 AI 服务的托管接入**。为 agent 平台等需要替自己的用户调用模型的服务提供独立的
  密钥管理：每个接入服务获得专属身份与服务令牌——机器凭据，无口令、不经登录会话、不持有
  网关管理员账号；它通过管理 API 为自己的用户建号、签发与回收 API Key、发放积分，关键写操作
  支持幂等键。作用域由服务端强制隔离：一个接入服务只能看到自己创建的用户、密钥与用量，越界
  访问一律按「不存在」应答；渠道、上游密钥、定价与系统设置对接入服务永久不可见。停用接入即
  级联停用其全部令牌与用户。
- **流量录制与复盘**。可对流经网关的中继请求做全量或采样录制：每个请求落盘为独立 JSON
  文件——完整请求体、响应体与 SSE 全流累计——按日期目录组织、以 request_id 命名，与用量
  日志直接对齐，用于事后复盘、审计取证与回放样本积累。默认关闭；开启后由万分位采样率控制
  录制比例，请求体与流式累计均有字节上限（超出置 truncated 标记，绝不影响转发），可选请求
  体脱敏，文件按保留天数自动清理（默认 7 天）。
- **管理端与用户门户双前端**。管理端管理渠道、模型定价、用户与积分、部门、用量日志、成本
  报表、审计记录、告警与系统设置；门户提供余额与用量查看、API Key 自助管理、兑换码充值与
  接入指引，部门负责人另有部门费用视图。
- **内置运维能力**。`/metrics` 输出 Prometheus 指标；Webhook 与 SMTP 双通道告警；审计日志
  只追加，由数据库触发器强制不可篡改；每小时一轮数据维护（用量汇总、保留期清理、低余额
  检查、按月积分发放、失败率与耗时分位告警）。
- **轻量部署**。单个 Go 二进制 + PostgreSQL，无 Redis；限流与缓存均为进程内存态，按单实例部署。

## 组件构成

| 组件 | 说明 |
|------|------|
| 后端 `tzl` | 单个 Go 二进制：HTTP 服务、内嵌 SQL 迁移，附带 `reconcile` 与 `cleanup-precharge` 子命令 |
| 管理端前端 | React 应用：渠道、模型定价、用户与积分、部门、用量日志、成本报表、审计、告警、系统设置 |
| 门户前端 | React 应用：余额与用量、API Key 自助管理、接入指引、兑换码 |
| PostgreSQL | 唯一的外部依赖（PostgreSQL 16） |

## Docker 快速开始

需要 Docker 与 Docker Compose。

```bash
git clone https://github.com/liguopeng80/tokenzen-lite.git
cd tokenzen-lite
cp .env.docker.example .env.docker
# 按注释填写：数据库密码、会话密钥、加密密钥
# （生成方式：openssl rand -hex 32）
chmod 600 .env.docker
docker compose --env-file .env.docker up -d --build
```

首次启动会执行数据库迁移并创建初始管理员账号。未设置 `TZL_ROOT_PASSWORD` 时，
随机初始密码只打印一次：

```bash
docker compose logs backend | grep "初始 root"
```

默认入口：

| 入口 | 地址 | 说明 |
|------|------|------|
| 门户 | `http://<主机>:8080` | 同时提供 `/v1`；客户端工具的 API 基址以此为准 |
| 管理端 | `http://127.0.0.1:8081` | 默认只绑定回环地址；远程访问用 SSH 隧道，或配置带 IP 白名单的反向代理（模板：`deploy/nginx-admin-allowlist.conf.example`） |

上线前必须知道的两件事：

- `TZL_ENCRYPT_KEY` 一经设置不可变更。渠道上游密钥用它派生的密钥加密存储；丢失后所有已存
  渠道密钥都无法解密。请在数据库备份之外、离机保存（`deploy/backup-secrets.sh`）。
- 默认形态是明文 HTTP，因此 `TZL_SESSION_COOKIE_SECURE` 默认 `false`。前置 TLS 之后必须改为
  `true`——否则浏览器会丢弃会话 cookie，登录状态无法保持。

## 源码快速开始

需要 Go 1.25+、Node.js 18+、pnpm 8+ 与 PostgreSQL 16。

```bash
./setup.sh              # 检查前置依赖，准备 .env 与数据库，
                        # 执行迁移、安装前端依赖并构建
bash scripts/start.sh   # 后端 + 双前端 dev server
bash scripts/status.sh  # 状态总览；--logs backend 跟踪日志
bash scripts/stop.sh
```

开发端口：后端 19030，管理端 19073，门户 19074。

## 首次配置

新装系统的管理端仪表盘会列出待完成的配置项，简版流程如下：

1. 用初始 root 账号登录管理端并修改密码。
2. **渠道**：至少添加一条上游渠道（厂商、协议、base URL、上游 API Key、模型名列表）。
3. **模型 → 批量导入**：导入内置预置价目（采集时点的厂商公开价，需对照厂商现行定价核对），
   应用加价比例，确认积分单价后导入。
4. **系统设置**：确认兑换率（默认 1 人民币 = 1,000,000 积分）与门户接入指引使用的对外 API 基址。
5. **用户**：创建账号并分配积分（或发放兑换码）。密码可留空，系统生成一次性初始密码并只显示
   一次，用户首次登录时强制改密。
6. 用户登录门户创建 API Key，按接入指引中的基址配置客户端。

## 架构

后端包在 `server/internal/` 下，依赖严格单向向下（详见
[`docs/overview/architecture.md`](docs/overview/architecture.md)）：

| 层 | 包 | 职责 |
|----|----|------|
| 传输层 | `api` | HTTP 路由、中间件、处理器 |
| 运行层 | `relay`、`maintenance` | 中继核心（每个"下游协议 × 上游协议"组合一个 conduit）；每小时后台任务 |
| 领域服务 | `billing`、`alerting`、`audit`、`auth` | 流水支撑的余额调整；告警去重与投递；只追加审计 |
| 计算/持久层 | `pricing`、`store` | 纯函数积分计算；GORM 仓储、内嵌 SQL 迁移 |
| 基础层 | `domain`、`obs`、`config`、`secrets`、`ratelimit`、`strutil` | 枚举、结构化日志与指标、环境变量配置、AES-GCM 密钥盒、内存限流 |

前端为 pnpm workspace：`apps/admin`、`apps/portal` 与 `packages/shared`
（共享类型与常量，与 `server/internal/domain` 保持同步）。

## 配置

全部配置经环境变量注入，完整清单见 [`.env.example`](.env.example)（源码/开发）与
[`.env.docker.example`](.env.docker.example)（Docker Compose）。生产关键项：

| 变量 | 用途 |
|------|------|
| `TZL_DATABASE_URL` | PostgreSQL 连接串（必填） |
| `TZL_SESSION_SECRET` | 会话 cookie 签名密钥，32 字符以上（生产必填） |
| `TZL_ENCRYPT_KEY` | 渠道上游密钥的加密密钥；一经设置不可变更 |
| `TZL_ROOT_USERNAME` / `TZL_ROOT_PASSWORD` | 初始管理员引导（留空则生成随机密码并打印到日志） |
| `TZL_METRICS_TOKEN` | `/metrics` 的 Bearer 令牌（未设置时接受 root 会话） |
| `TZL_TRUSTED_PROXIES` | 信任其 `X-Real-IP` 的代理 CIDR 列表 |

## 运维

| 任务 | 方式 |
|------|------|
| 积分对账（建议每日） | `tzl reconcile`——余额/流水不匹配或存在孤儿预扣时非零退出 |
| 孤儿预扣清理 | `tzl cleanup-precharge [分钟数]` |
| 数据库备份/恢复 | `deploy/backup.sh`、`deploy/backup-secrets.sh`、`deploy/restore.sh` |
| 指标抓取 | `GET /metrics`（Prometheus 文本格式） |
| 非容器部署 | systemd + Nginx：[`docs/deployment.md`](docs/deployment.md)，模板在 `deploy/` |

## 开发

```bash
make test    # Go 测试（需 TZL_TEST_DATABASE_URL）+ 前端类型检查与单测
make build   # Go 二进制 + 前端构建
```

注意事项：

- Go 集成测试共用一个测试库并反复执行迁移的 up/down，跨包必须串行：在 `server/` 下
  `go test -p 1 ./...`。未设置 `TZL_TEST_DATABASE_URL` 时自动跳过。
- `make test` 含 `server/internal/api` 的覆盖率下限门禁（`scripts/coverage-gate.sh`，
  当前 70%）——新增端点须配套用例。
- CI（`.github/workflows/ci.yml`）执行 `go vet`、gofmt 检查、对接 PostgreSQL 服务容器的
  全量后端测试、覆盖率门禁、前端类型检查/测试/构建，以及两个容器镜像构建。
- `mock-upstream/` 是模拟上游 API 服务，用于在不接真实厂商的情况下验证中继链路。

工作流与约定见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 文档

参考文档在 [`docs/`](docs/) 目录：

| 文档 | 内容 |
|------|------|
| [`docs/glossary.md`](docs/glossary.md) | 术语与枚举的权威定义、设置项清单 |
| [`docs/api-contract.md`](docs/api-contract.md) | 完整 HTTP API 契约 |
| [`docs/overview/architecture.md`](docs/overview/architecture.md) | 分层架构与模块清单 |
| [`docs/deployment.md`](docs/deployment.md) | 部署、备份恢复、安全基线、内存预算 |
| [`docs/design/组织与审计模型.md`](docs/design/组织与审计模型.md) | 部门与审计的数据模型 |

## 许可证

[Apache-2.0](LICENSE)。基于本项目构建或修改后再分发时，须保留 [LICENSE](LICENSE) 与
[NOTICE](NOTICE) 文件，并对修改过的文件作显著变更声明（Apache-2.0 第 4 条）。
