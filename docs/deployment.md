# 部署指南（非容器化单机）

目标环境：Ubuntu 24.04 单机（参考生产机 2C/1.6G），已装 Nginx 与 PostgreSQL。

容器方式（Docker Compose，含 PostgreSQL 与 Nginx，不依赖宿主机的 Go 与 Node 工具链）见 `README.md` 的"容器方式安装"，编排文件为仓库根的 `docker-compose.yml`。本文档描述的是把二进制与静态文件直接装在宿主机上的方式，两者在数据库结构、配置项与备份脚本上完全一致。

## 架构

浏览器 → Nginx（443，SSL 终止）→ 静态文件（admin/portal SPA）+ 反代 `/api`、`/v1` → tzl 单二进制（127.0.0.1:19030）→ 本机 PostgreSQL。无 Redis、无容器。

## 并发与内存预算

`deploy/tzl.service` 限制进程内存 512M（MemoryMax）。/v1 的并发准入默认值按该预算换算得出，下表三个量互相制约，只改其一会重新引入内存超限风险：

| 量 | 取值 | 位置 |
|----|------|------|
| 请求体上限 | 20 MiB | `server/internal/relay/pipeline.go`（编译期常量） |
| `max_concurrent_requests`（并发总上限） | 默认 40 | settings 表，管理端系统设置 |
| `max_concurrent_large_requests`（大请求并发上限） | 默认 2 | settings 表，管理端系统设置 |

准入规则：请求体超过 1 MiB（或 Content-Length 未知）的请求判定为大请求，除占用总槽位外还占用大请求配额；两个维度任一占满即返回 503。

换算依据：

- 进程基线（Go 运行时、数据库连接池、进程内缓存）预留约 110 MiB，剩约 400 MiB 供在途请求使用。
- 请求体会解码为 JSON 映射并重新编码转发，峰值内存约为原始字节的 3-4 倍。
- 大请求（1-20 MiB，多模态 base64 场景）：单请求峰值按 20 MiB × 4 = 80 MiB 估算，2 路配额 ≈ 160 MiB。
- 一般请求（≤ 1 MiB）：单请求峰值按 6 MiB 估算（请求体放大 + 非流式响应缓冲），40 路 ≈ 240 MiB。
- 合计 ≈ 400 MiB，与预算相符。
- 数据库连接池上限 10（`server/internal/store/db.go`）：单个请求的数据库操作短暂（认证查询、计费事务），40 路并发下连接排队等待可接受；并发总上限调至百级时须同步调大连接池。

上调 MemoryMax 时按同一比例放大两个并发上限；下调内存或调大请求体上限时反向收紧。

### 按使用人数选配

上表的默认值对应参考机型（2C/1.6G，进程内存上限 512M）。选配的关键在于大模型调用是长连接：单个请求占用槽位的时长以十秒到分钟计，因此约束不是「每秒多少请求」，而是「同一时刻有多少人正在等回答」。编程类客户端（Claude Code、Codex CLI 等）一次任务会连续发起多轮调用，一名活跃使用者在忙时几乎持续占用一个槽位。

| 同时活跃人数 | 机型 | `MemoryMax` | `max_concurrent_requests` | `max_concurrent_large_requests` | 数据库连接池 |
|---|---|---|---|---|---|
| ≤ 10 | 2C / 2G | 512M | 40（默认） | 2（默认） | 10（默认） |
| 10 - 25 | 2C / 4G | 1G | 80 | 4 | 10 |
| 25 - 60 | 4C / 8G | 2G | 160 | 8 | 20 |
| 60 - 120 | 8C / 16G | 4G | 320 | 16 | 40 |

「同时活跃人数」指同一时刻正在等待模型回答的人数，不是账号总数。经验换算：轻度使用（偶尔问答）按总人数的 10% 估算，重度使用（全天开着编程类客户端）按 60% 估算。例如某团队 200 人、其中 30 人重度使用编程客户端，取 30 × 60% + 170 × 10% ≈ 35 人同时活跃，对应第三行。

调整须成组进行，只改其一会重新引入内存超限或连接排队：并发上限翻倍时，`MemoryMax` 同比例放大，并发总上限超过百级时数据库连接池（`server/internal/store/db.go`）也要同步调大。

超限行为必须让使用方知道：并发占满时请求立即返回 503，不排队也不等待。客户端表现为调用失败而非变慢，因此「请求偶发失败」在高峰期的第一嫌疑是并发上限，而不是上游故障。`tzl_relay_in_flight` 指标贴近上限即为扩容信号。

## 首次部署

1. 数据库：
   ```bash
   sudo -u postgres psql -c "CREATE ROLE tzl LOGIN PASSWORD '<强密码>';"
   sudo -u postgres createdb -O tzl tzl
   # 1.6G 内存机型建议调低 PG 内存：postgresql.conf 中 shared_buffers = 128MB
   ```
2. 二进制（本地构建后上传，服务器不需要 Go）：
   ```bash
   cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -ldflags "-X main.version=$(git rev-parse --short HEAD)" -o tzl ./cmd/tzl
   rsync -avz tzl <server>:/opt/tzl/bin/
   ```
3. 配置 `/etc/tzl/env`（属主 root，权限 600）：
   ```
   TZL_ENV=prod
   TZL_BIND_ADDR=127.0.0.1
   TZL_PORT=19030
   TZL_TRUSTED_PROXIES=127.0.0.1,::1
   TZL_DATABASE_URL=postgres://tzl:<密码>@localhost:5432/tzl?sslmode=disable
   TZL_SESSION_SECRET=<32+ 随机字符>
   TZL_ENCRYPT_KEY=<32+ 随机字符，一旦设定不可变更，否则渠道密钥无法解密>
   TZL_ROOT_PASSWORD=<初始 root 密码，首次启动后可从 env 移除>
   ```

   其余可选变量（不配置时取括号内的默认值）：

   | 变量 | 默认值 | 作用 |
   |------|--------|------|
   | `TZL_ROOT_USERNAME` | `root` | 首次启动创建的初始管理员用户名，仅在系统尚无任何用户时生效 |
   | `TZL_REGISTER_ENABLED` | `false` | 自助注册的兜底开关，仅在 settings 表不可读时生效；线上以管理端「系统设置」的 `register_enabled` 为准。企业内部部署应保持关闭，账号由管理员建立 |
   | `TZL_UPSTREAM_TIMEOUT_SEC` | `600` | 单次 /v1 调用访问上游的总超时秒数，覆盖渠道重试在内的整个上游阶段。长思考模型需要调大 |
   | `TZL_SHUTDOWN_TIMEOUT_SEC` | `15` | 优雅停机等待秒数：收到停止信号后等待在途请求完成的时长，超时则强制关闭 |
   | `TZL_LOG_LEVEL` | `info` | 日志级别（`debug`/`info`/`warn`/`error`） |
   | `TZL_METRICS_TOKEN` | 空 | `/metrics` 的抓取令牌（请求头 `Authorization: Bearer <令牌>`）。留空时该端点只接受 root 会话——指标含模型名、渠道 ID 与各接口调用量，不匿名开放 |
   | `TZL_SESSION_COOKIE_SECURE` | `TZL_ENV` 为 `prod` 时是 `true`，否则 `false` | 会话 cookie 是否带 Secure 属性。取值必须与浏览器访问站点的实际协议一致，而非与运行环境一致。本文档的 Nginx 配置以 HTTPS 对外，保持默认即可；以明文 HTTP 对内网提供服务时必须显式设为 `false`，否则浏览器不保存会话 cookie，用户登录后立即掉登录态 |

   绑定与客户端 IP 采信规则：
   - `TZL_BIND_ADDR` 默认 `127.0.0.1`，即只接受本机 Nginx 的反代流量；仅当需要绕过反向代理直接对外提供服务时才改为 `0.0.0.0` 或具体网卡地址，且必须自行配置防火墙。
   - `TZL_TRUSTED_PROXIES` 是可信反向代理的来源地址列表（IP 或 CIDR，逗号分隔）。仅当连接的远端地址命中该列表时，后端才采信 Nginx 传递的 `X-Real-IP` 头作为客户端 IP；其余连接一律以远端地址为准并丢弃该头，防止直连后端伪造来源 IP 绕过 API Key 的来源 IP 白名单、污染用量日志。本机 Nginx 反代场景填 `127.0.0.1,::1`；不配置则不信任任何代理，API Key 来源 IP 白名单将按 Nginx 的地址（而非真实客户端地址）匹配。
4. systemd：`cp deploy/tzl.service /etc/systemd/system/ && systemctl daemon-reload && systemctl enable --now tzl`。启动时自动执行数据库迁移。
5. 前端（本地构建后上传）：
   ```bash
   pnpm build
   rsync -avz --delete apps/admin/dist/  <server>:/var/www/tokenzen/admin/
   rsync -avz --delete apps/portal/dist/ <server>:/var/www/tokenzen/portal/
   ```
6. Nginx：按 `deploy/nginx-tokenzen.conf` 配置三个虚拟主机（portal / admin / api）。SSE 相关配置（proxy_buffering off 等）不可省略。管理端虚拟主机的来源 IP 白名单是部署强制项：
   ```bash
   cp deploy/nginx-admin-allowlist.conf.example /etc/nginx/tokenzen-admin-allowlist.conf
   # 编辑该文件，把示例地址换成本站实际的运维来源 IP 或网段
   nginx -t && systemctl reload nginx
   ```
   admin 虚拟主机以 `include` 引入该文件并紧跟 `deny all`。文件不存在时 `nginx -t` 失败，配置无法生效；文件存在但没有 allow 规则时管理端一律拒绝。确无固定来源 IP 时，在该文件中写 `allow all;` 显式放开——放开后管理端登录仅剩下文安全基线中的应用层限流与账号锁定保护，须同时使用高强度管理员密码。
7. 备份：`cp deploy/backup.sh deploy/backup-secrets.sh deploy/restore.sh /opt/tzl/deploy/`，配置见下文"备份与恢复"。
8. 对账巡检（建议每日）：`10 4 * * * . /etc/tzl/env && /opt/tzl/bin/tzl reconcile || <告警动作>`。

## 发布前必做清单（go-live checklist）

首次部署走完上述步骤后、正式对外提供服务前，逐项确认。任一未完成都可能导致上线即故障或留有安全口子。

**配置与密钥**

- `/etc/tzl/env` 已设 `TZL_SESSION_SECRET`（32+ 随机字符）与 `TZL_ENCRYPT_KEY`（32+ 随机字符）；后者已随 `backup-secrets.sh` 异地备份——一旦丢失，所有渠道上游密钥无法解密，只能向各厂商重新索取录入。
- `TZL_REGISTER_ENABLED` 保持 `false`，且管理端「系统设置」的 `register_enabled` 也为关闭：内部网关账号由管理员建立，自助注册保持关闭，避免网关暴露在内网时被非授权人员建号。
- 初始 root 密码已登录修改，`TZL_ROOT_PASSWORD` 已从 env 移除（仅首次启动建号用）。
- 对外 API 基址 `server_address` 已在管理端「系统设置」配置：门户的接入指引、客户端 base_url 与 `/api/site/config` 都依赖它；留空时门户提示地址靠推断、可能连不通。

**数据与对账**

- 数据库迁移已应用（systemd 启动自动执行；`tzl reconcile` 能跑通即说明 schema 完整）。
- `tzl reconcile` 对账通过（余额不变式 + 孤儿预扣）。
- 备份脚本（`backup.sh` + `backup-secrets.sh`）已部署并手工跑通一次，密钥备份与数据库备份分开异地存放。

**通道与防护**

- 告警通道（Webhook 或邮件）已在管理端配置，并用「告警记录」页的「发送测试消息」确认接收端真的收到——只保存不验证，等于把「发不出去」留到真出故障时才发现。
- Nginx 管理端来源 IP 白名单（`/etc/nginx/tokenzen-admin-allowlist.conf`）已配置为本站实际运维来源——管理端的部署强制项。
- 用真实 API Key 各打一发冒烟：`/v1/chat/completions`（流式 + 非流式）、`/v1/messages`，确认渠道路由、计费扣分、用量日志写入正常。

## 备份与恢复

备份由两个互相独立的部分组成，缺任一都无法还原服务。

| 内容 | 脚本 | 产物 | 为什么必须单独备份 |
|------|------|------|--------------------|
| 数据库 | `deploy/backup.sh` | `/opt/tzl/backup/tzl-<时间戳>.dump` + `.sha256` | 承载全部账务与配置数据 |
| 配置与密钥 | `deploy/backup-secrets.sh` | `/opt/tzl/backup/secrets/tzl-env-<时间戳>.tar.gz` | `TZL_ENCRYPT_KEY` 在 `/etc/tzl/env` 而不在库里，且一旦设定不可变更。缺它则恢复后 `channels.api_key_encrypted` 全部无法解密，必须重新向各上游厂商索取密钥并重新录入 |

crontab（两个脚本都需要读取 `/etc/tzl/env`，以 root 运行）：

```
30 3 * * * . /etc/tzl/env && /opt/tzl/deploy/backup.sh        >> /opt/tzl/backup/backup.log 2>&1
35 3 * * * . /etc/tzl/env && /opt/tzl/deploy/backup-secrets.sh >> /opt/tzl/backup/backup.log 2>&1
```

两个脚本都从 `TZL_DATABASE_URL` 取连接信息，不依赖 PostgreSQL 的 peer 认证；失败时写 stderr，并在配置了 `TZL_ALERT_WEBHOOK` 时上报。数据库备份产出后用 `pg_restore --list` 自检，校验不通过即删除半成品并告警，避免留下无法还原的空文件。

存放要求：密钥备份含明文凭据（权限 600），必须与数据库备份分开存放并异地保存。两者放在同一处时，一次介质损坏或一次泄露就同时失去或暴露全部要素。

### 恢复流程

顺序不可颠倒：先恢复密钥，再恢复数据库。顺序反了服务会以新密钥启动，渠道密钥全部解不开。

1. 恢复 `/etc/tzl/env`：`tar -xzf tzl-env-<时间戳>.tar.gz -C /etc/tzl/`，确认 `TZL_ENCRYPT_KEY` 与备份时一致。
2. 恢复数据库：`/opt/tzl/deploy/restore.sh <备份文件> <目标数据库 URL>`。脚本会校验 `.sha256`、停止 `tzl` 服务、以 `--clean --if-exists` 还原，并在还原后自动执行 `tzl reconcile`。
3. 对账通过后手工 `systemctl start tzl`。脚本刻意不自动启动，留出确认对账结果的环节。

### 恢复演练（建议每季度一次）

在演练库上完整走一遍，不要等真出事才第一次执行恢复：

```bash
sudo -u postgres createdb -O tzl tzl_restore_drill
/opt/tzl/deploy/restore.sh /opt/tzl/backup/<最新备份>.dump \
    postgres://tzl:<密码>@localhost:5432/tzl_restore_drill
# 核对关键表行数与生产一致，确认对账通过后删除演练库
sudo -u postgres dropdb tzl_restore_drill
```

演练需记录耗时，作为恢复时间的实测依据。

## 积分对账巡检

`tzl reconcile` 执行两项独立校验，任一不通过即以退出码 1 结束，供 crontab 的 `|| <告警动作>` 触发：

- 余额不变式：每个用户的 `credit_balance` 等于其积分流水之和。
- 孤儿预扣积压：只有预扣、既无结算也无退款的请求。这类请求天然满足余额不变式，单靠余额校验发现不了；不处理会把用户积分长期扣住不退。发现后执行 `tzl cleanup-precharge` 补退。

孤儿预扣同时由服务内的定时任务回收，间隔由系统设置 `orphan_cleanup_interval_sec` 控制（默认 300 秒，0 = 关闭定时回收、仅在服务启动时执行一次）。巡检报出孤儿预扣通常意味着回收被关闭，或结算写库持续失败，需要查日志确认。

对账未通过时，`tzl reconcile` 除返回非零退出码外，还会主动向管理端配置的告警通道投递一条严重告警。已配置告警通道的部署无需再在 crontab 里编排 `|| <告警动作>`。

## 主动告警

告警通道在管理端「系统设置」中配置，配置完成后到「告警记录」页用「发送测试消息」验证真的能送达——只保存配置不验证，等于把「发不出去」留到真出故障时才发现。

| 设置键 | 说明 |
|---|---|
| `alert_webhook_url` / `alert_webhook_format` / `alert_webhook_secret` | Webhook 地址、报文格式（`generic` / `dingtalk` / `feishu` / `wecom` / `slack`）与加签密钥（钉钉、飞书需要） |
| `alert_smtp_host` / `alert_smtp_port` / `alert_smtp_username` / `alert_smtp_password` / `alert_smtp_tls` / `alert_smtp_from` / `alert_email_to` | 告警邮件的 SMTP 参数与收件地址（多个以逗号分隔） |
| `alert_dedup_window_sec` | 抑制窗口秒数，默认 3600：同一去重键在窗口内只投递一次，避免持续故障把通道刷成噪声 |

密钥与 SMTP 密码以 AES-GCM 加密存储，读取接口只返回掩码。两条通道都配置时并行投递，任一成功即视为已送达；都未配置时事件仍落库，可在「告警记录」页区分「未触发」与「触发了但发不出去」。

触发条件：渠道因连续致命错误自动禁用、积分对账未通过、用量日志出现丢弃、孤儿预扣被回收、部门当月消费超预算、有用户余额低于预警阈值、中继失败率或耗时越过阈值、按月自动发放有账号失败、模型策略或来源 IP 白名单无法解析、备份脚本执行失败。

前八项是离散事件，中继失败率与耗时是连续量：上游劣化时它们逐步爬升，任何单条事件都不越界，但用户已经在受影响。两类判定互补，不互相替代。

面向用户本人的余额提醒（`user_balance_notice`）不走 Webhook：群通道的读者是管理员，把某个员工的余额发进群里既无用也多余。该类事件只投递到用户本人邮箱，抑制窗口固定 24 小时，不随 `alert_dedup_window_sec` 变化。

运维脚本可用 `tzl alert <类型> <标题> [正文]` 复用同一套通道，无需另配一份 Webhook 地址；`deploy/backup.sh` 已按此方式上报备份失败。

## 数据维护任务

服务内的维护循环每小时执行一轮，各项按日期水位判重，不会重复劳动：

| 任务 | 控制设置 | 说明 |
|---|---|---|
| 用量按日汇总 | `usage_rollup_enabled`（默认开启） | 汇总昨日及更早尚未汇总的用量，作为费用报表的数据源。报表按「已汇总日期读聚合表 + 当日读原始日志」两段合并，开启前后结果一致 |
| 审计记录清理 | `audit_log_retention_days`（默认 180） | 默认值覆盖等保二级对安全审计记录留存六个月的要求。清理动作本身写一条 `audit.purge`，使清理可追溯。设为 0 表示不清理，其余取值不得少于 30 天 |
| 原始用量日志清理 | `usage_log_retention_days`（默认 90 天，0 = 不清理） | 清理前校验被清理范围内每一天都已完成汇总；未汇总则跳过并记日志。清理后报表数据仍在，受影响的只有明细查询与导出 |
| 部门超预算检查 | 部门的月度预算字段 | 当月消费超出预算时投递提醒级告警，不拦截调用 |
| 用户低余额检查 | `low_balance_threshold_credits`（默认 100000，0 = 关闭） | 有启用用户的余额低于阈值时投递一条聚合告警，列出人数与名单（最多 20 人）。已禁用的账号、以及从未获得过积分也从未消费过的账号不计入。余额耗尽后该用户全部调用被拒绝，本人未必及时察觉，因此在耗尽前先通知管理员补发 |
| 用户本人余额提醒 | `user_balance_notice_enabled`（默认开启） | 在上一项之外，另向余额不足的用户本人邮箱发一封提醒。三个前置条件缺一不可：本项未关闭、告警邮件通道已配置、该用户填了邮箱；同一用户 24 小时内最多一封 |
| 按月自动发放积分 | `monthly_grant_credits`（默认 0 = 关闭）、`monthly_grant_mode`（默认 `topup`） | 每月首次维护轮次为全部启用的普通用户发放，按「月份 + 用户」幂等。`topup` 为补足到额度（余额已达额度不再发放，未用完不累积），`add` 为增发固定额度（未用完累积）。服务在月初停机时，恢复后的第一轮补上 |
| 中继健康度判定 | `alert_error_rate_percent`（默认 20）、`alert_error_rate_min_requests`（默认 50）、`alert_latency_p95_ms`（默认 0 = 关闭） | 统计最近一小时的失败率与总耗时 95 分位，越过阈值时告警。样本数不足最小请求数时不判定 |

## 运行指标

`GET /metrics` 以 Prometheus 文本格式导出运行指标。访问需要 `TZL_METRICS_TOKEN` 令牌（请求头 `Authorization: Bearer <令牌>`）或 root 会话——指标含模型名、渠道 ID 与各接口的调用量，不匿名开放。

| 指标 | 类型 | 标签 | 回答的问题 |
|---|---|---|---|
| `tzl_http_requests_total` | counter | `group`、`method`、`status` | 各接口分组的调用量与错误比例。`group` 是归并后的分组（`api/admin`、`api/me`、具体的 `/v1` 端点等），资源 ID 不进标签 |
| `tzl_http_request_duration_seconds` | histogram | `group` | 各接口分组的耗时分布，可算任意分位数 |
| `tzl_relay_requests_total` | counter | `model`、`status` | 各模型的调用量与计费终态（`settled` / `failed` / `refunded`）分布 |
| `tzl_relay_errors_total` | counter | `model`、`error_class` | 失败集中在哪个模型、属于哪类错误 |
| `tzl_relay_duration_seconds` | histogram | `model` | 各模型的端到端耗时分布 |
| `tzl_channel_attempts_total` | counter | `channel`、`outcome` | 各渠道的成功与失败次数，即渠道健康度 |
| `tzl_relay_in_flight` | gauge | 无 | 当前在途请求数。撞上 `max_concurrent_requests` 时请求直接 503 且不排队，该值贴近上限即须扩容 |
| `tzl_usage_log_dropped` | gauge | 无 | 用量日志累计丢弃条数。非零表示计费明细与成本分摊出现缺口 |

计数为进程内存态，重启后归零，与限流计数、渠道失败计数的口径一致；抓取端按 counter 语义处理重置即可。

指标解决的是「有没有变坏、从什么时候开始、影响哪部分」，告警解决的是「变坏时有人知道」。两者都配置才闭合：只有指标没有告警，劣化要靠人主动看图才发现；只有告警没有指标，收到告警后无法判断影响范围与起始时间。

## 单实例部署的语义与中断窗口

系统按单实例运行，未做多实例与状态外置。以下状态全部保存在进程内存中，不跨进程共享：

| 状态 | 作用 | 重启后的行为 |
|------|------|-------------|
| 限流计数（按 API Key、按用户、登录与注册防爆破） | 请求频率约束 | 清零，窗口重新开始 |
| 并发闸门占用数（总量、大请求、单用户子配额） | 内存保护 | 清零 |
| 渠道连续失败计数 | 自动禁用判定 | 清零；已落库的自动禁用状态不受影响 |
| 系统设置缓存（30 秒） | 减少设置表查询 | 清空后按需重建 |

由此带来两条部署约束：

- **升级与崩溃期间服务中断。** 重启窗口内全部 `/api` 与 `/v1` 请求失败；`deploy/tzl.service` 配置了 `Restart=on-failure`（3 秒后重启），崩溃自愈时间为秒级，升级中断时间取决于二进制上传与迁移执行。中断期间中继调用会收到连接错误，客户端需自行重试。已预扣但未结算的请求由孤儿预扣回收补偿，不会造成扣费差错。升级应安排在使用低谷，并提前通知使用方。
- **不能直接横向扩容。** 部署第二个实例会让限流额度按实例数翻倍（每个进程各自计数），并发闸门同理，两个实例的设置缓存也可能在 30 秒内不一致。需要多实例时必须先把限流计数与并发闸门迁到共享存储，不能仅靠在 Nginx 上加一个 upstream。

## 审计记录的不可变性

`audit_logs` 的不可变性由数据库触发器强制，不只依赖应用层：任何 UPDATE 一律拒绝；DELETE 只允许清理写入满 30 天的记录。审计约束的对象往往正是具备数据库权限的内部人员，仅靠「应用代码里没有更新与删除路径」对其不构成约束。

触发器不拦截 TRUNCATE——TRUNCATE 需要表属主权限，属于整表重置而非改写单条记录掩盖操作。确需绕过（例如迁移历史数据）时由数据库超级用户临时 `DROP TRIGGER`，该动作本身应纳入运维记录。

## 安全基线：登录与注册防爆破

后端对登录与注册接口内置防爆破限制（编译期常量，进程内存态计数，定义在 `server/internal/api/auth_handlers.go`；有意不做成系统设置，避免被在线调低）：

| 限制 | 取值 | 说明 |
|------|------|------|
| 登录：单来源 IP | 30 次/分钟 | 统计全部尝试，不分成败 |
| 登录：单用户名 | 10 次/分钟 | 统计全部尝试，不分成败 |
| 登录：连续失败锁定 | 连续失败 5 次锁定 10 分钟 | 锁定期内密码正确也拒绝；成功登录清零计数 |
| 注册：单来源 IP | 10 次/小时 | 防批量灌入垃圾账号 |

超限统一返回 429，登录接口的拒绝话术不泄露用户是否存在。计数为进程内存态，重启后清零。这些限制是最后一道防线，管理端仍应保持 Nginx 层来源 IP 白名单（部署强制项，见首次部署第 6 步）；来源 IP 维度的限制依赖 `TZL_TRUSTED_PROXIES` 正确配置，否则全部请求按 Nginx 地址计数，会误伤正常用户。

前端页面的安全响应头由 Nginx 下发（模板已含，不要删）：`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: strict-origin-when-cross-origin`，以及内容安全策略。策略中 `connect-src` 只放行 `'self'`：两个前端的全部数据都经本站后端获取，不直连任何外部站点。`style-src` 必须保留 `'unsafe-inline'`（组件库运行时注入行内样式），`script-src` 不含 `'unsafe-eval'`。

## 升级

```bash
# 后端：上传新二进制 → systemctl restart tzl（启动时自动迁移）
# 前端：重新 rsync dist
# 回滚：换回上一版二进制重启；数据库变更前先跑 backup.sh
```

升级注意：设置项默认值的调整只对 settings 表中无该键的部署生效；管理端保存过的值持久化在 settings 表，升级不会自动改写。历史版本 `max_concurrent_requests` 默认为 100，若部署曾在管理端保存过该值，升级后仍按 100 运行，超出 512M 内存预算。升级后在管理端系统设置中核对 `max_concurrent_requests` 与 `max_concurrent_large_requests`，按上文换算依据调整（默认 40 / 2）。

## 验证清单

- `curl -s https://api.example.com/healthz` 返回 `{"success":true,...}`
- 管理端登录 → 渠道连通测试通过
- 用真实 Key 各打一发：`/v1/chat/completions`（流式 + 非流式）、`/v1/messages`
- `tzl reconcile` 对账通过
- 管理端「系统设置」配置告警通道后，「告警记录」页的「发送测试消息」在接收端收到消息

## 后续容器化（测试稳定后）

代码已按 12-factor 准备（纯环境变量配置、无本地文件状态、日志走 stdout）：补一个多阶段 Dockerfile（构建 → distroless 运行）+ compose（app + postgres）即可，代码零改动。
