// tzl 是 Token Zen Lite 后端的唯一二进制入口。
// 子命令：serve（默认，启动 HTTP 服务）、migrate up|down、version。
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/liguopeng80/tokenzen-lite/server/internal/alerting"
	"github.com/liguopeng80/tokenzen-lite/server/internal/api"
	"github.com/liguopeng80/tokenzen-lite/server/internal/audit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/auth"
	"github.com/liguopeng80/tokenzen-lite/server/internal/billing"
	"github.com/liguopeng80/tokenzen-lite/server/internal/config"
	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/maintenance"
	"github.com/liguopeng80/tokenzen-lite/server/internal/obs"
	"github.com/liguopeng80/tokenzen-lite/server/internal/ratelimit"
	"github.com/liguopeng80/tokenzen-lite/server/internal/relay"
	"github.com/liguopeng80/tokenzen-lite/server/internal/secrets"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store/migrate"
)

// startupCleanupTimeout serve 启动时孤儿预扣清理的上限时长：
// 超时放弃本轮清理并降级继续启动，避免数据库锁等待把启动无限期阻塞。
const startupCleanupTimeout = 30 * time.Second

// usageLogFlushTimeout 停机时用量日志队列刷盘的上限时长。
const usageLogFlushTimeout = 10 * time.Second

// bootstrapRoot 在系统无任何用户时创建初始 root 账号。
// 密码来自 TZL_ROOT_PASSWORD；未提供时生成随机密码并打印到日志（仅此一次）。
func bootstrapRoot(cfg *config.Config, users *store.UserRepo) error {
	ctx := context.Background()
	password := cfg.RootPassword
	generated := false
	if password == "" {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("生成初始密码失败: %w", err)
		}
		password = hex.EncodeToString(raw)
		generated = true
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("初始 root 密码不合规: %w", err)
	}
	created, err := users.EnsureRootUser(ctx, cfg.RootUsername, hash)
	if err != nil {
		return fmt.Errorf("创建初始 root 账号失败: %w", err)
	}
	if created {
		if generated {
			slog.Warn("已创建初始 root 账号（随机密码，请立即修改）",
				"username", cfg.RootUsername, "password", password)
		} else {
			slog.Info("已创建初始 root 账号", "username", cfg.RootUsername)
		}
	}
	return nil
}

var version = "dev" // 构建时通过 -ldflags 注入

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		run(serve)
	case "migrate":
		run(runMigrate)
	case "reconcile":
		run(runReconcile)
	case "cleanup-precharge":
		run(runCleanupPrecharge)
	case "alert":
		run(runAlert)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q，可用: serve | migrate up|down | reconcile | "+
			"cleanup-precharge [分钟] | alert <类型> <标题> [正文] | version\n", cmd)
		os.Exit(2)
	}
}

// runAlert 从命令行投递一条告警，供备份脚本等外部任务复用管理端配置的告警通道。
// 没有这个入口，运维脚本只能各自再配一份 Webhook 地址，改通道时容易漏改其中一处。
func runAlert(cfg *config.Config) error {
	if len(os.Args) < 4 {
		return fmt.Errorf("用法: tzl alert <类型> <标题> [正文]")
	}
	alertType := domain.AlertType(os.Args[2])
	title := os.Args[3]
	message := title
	if len(os.Args) > 4 {
		message = strings.Join(os.Args[4:], " ")
	}
	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	alerts := &alerting.Service{
		Events:   store.NewAlertEventRepo(db),
		Settings: store.NewSettingsRepo(db),
		Secrets:  secrets.New(cfg.EncryptKey),
	}
	event, err := alerts.RaiseSync(context.Background(), alerting.Event{
		Type: alertType, Severity: domain.AlertCritical,
		DedupKey: string(alertType), Title: title, Message: message,
	})
	if err != nil {
		return fmt.Errorf("告警投递失败: %w", err)
	}
	fmt.Printf("告警已记录（#%d），投递状态：%s\n", event.ID, event.Status)
	return nil
}

// runCleanupPrecharge 清理孤儿预扣：进程异常退出遗留的、超过阈值时间
// 且无结算/退款终态的预扣流水，按幂等规则补写退款与追溯日志。
// 可选参数为阈值分钟数，默认 15 分钟。
func runCleanupPrecharge(cfg *config.Config) error {
	threshold := billing.DefaultOrphanPrechargeThreshold
	if len(os.Args) > 2 {
		minutes, err := strconv.Atoi(os.Args[2])
		if err != nil || minutes <= 0 {
			return fmt.Errorf("阈值分钟数必须是正整数，收到 %q", os.Args[2])
		}
		threshold = time.Duration(minutes) * time.Minute
	}
	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	result, err := billing.NewService(db).CleanupOrphanPrecharges(context.Background(), threshold)
	if err != nil {
		return err
	}
	fmt.Printf("孤儿预扣清理完成：扫描 %d 条，退款 %d 条（合计 %d 积分），幂等跳过 %d 条\n",
		result.Scanned, result.Refunded, result.RefundedCredits, result.AlreadyHandled)
	return nil
}

// runReconcile 执行积分对账，含三项独立校验：
//  1. 余额不变式：每个用户的 credit_balance 等于其流水之和。
//  2. 密钥已用额度不变式：每个 api_keys.credit_used 等于其 ref_type='api_key'
//     流水之和的相反数。credit_used 由 relay 计费会话与孤儿预扣清理的 4 处裸 SQL
//     维护、脱离 billing.Service 事务，失败仅 warn；单校第 1 项发现不了密钥账漂移。
//  3. 孤儿预扣积压：只有预扣、无结算也无退款的请求。这类请求天然满足第 1 项，
//     单靠余额不变式发现不了，必须单独扫描，否则用户积分被长期扣住而巡检一直报通过。
//
// 任一项不通过即以非零退出码结束，使 crontab 的 `|| <告警动作>` 能够触发。
func runReconcile(cfg *config.Config) error {
	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	ctx := context.Background()
	ledgerRepo := store.NewLedgerRepo(db)

	mismatches, err := ledgerRepo.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("对账查询失败: %w", err)
	}
	keyMismatches, err := ledgerRepo.ReconcileAPIKeys(ctx)
	if err != nil {
		return fmt.Errorf("密钥额度对账查询失败: %w", err)
	}
	orphans, err := billing.NewService(db).ScanOrphanPrecharges(ctx, billing.DefaultOrphanPrechargeThreshold)
	if err != nil {
		return fmt.Errorf("孤儿预扣扫描失败: %w", err)
	}

	for _, m := range mismatches {
		fmt.Printf("余额不一致 user_id=%d username=%s 余额=%d 流水合计=%d 差额=%d\n",
			m.UserID, m.Username, m.Balance, m.LedgerSum, m.Difference)
	}
	for _, m := range keyMismatches {
		fmt.Printf("密钥额度不一致 key_id=%d name=%s credit_used=%d 流水合计=%d 差额=%d\n",
			m.KeyID, m.Name, m.CreditUsed, m.LedgerSum, m.Difference)
	}
	var orphanCredits domain.Credits
	for _, o := range orphans {
		orphanCredits += o.Amount
		fmt.Printf("孤儿预扣 request_id=%s user_id=%d 预扣积分=%d 预扣时间=%s\n",
			o.RequestID, o.UserID, o.Amount, o.CreatedAt.Format(time.RFC3339))
	}

	if len(mismatches) == 0 && len(keyMismatches) == 0 && len(orphans) == 0 {
		fmt.Println("对账通过：全部用户余额与流水一致，全部密钥已用额度与流水一致，无孤儿预扣积压")
		return nil
	}

	// 三项校验各自独立，按出现的严重度拼装失败消息：余额 > 密钥 > 孤儿。
	var causes []string
	if len(mismatches) > 0 {
		causes = append(causes, fmt.Sprintf("%d 个用户余额与流水不一致", len(mismatches)))
	}
	if len(keyMismatches) > 0 {
		causes = append(causes, fmt.Sprintf("%d 个密钥的已用额度与流水不一致", len(keyMismatches)))
	}
	if len(orphans) > 0 {
		causes = append(causes, fmt.Sprintf("%d 条孤儿预扣未退款（合计 %d 积分），执行 tzl cleanup-precharge 补退",
			len(orphans), orphanCredits))
	}
	failure := fmt.Errorf("对账失败：%s", strings.Join(causes, "，"))

	// 对账通常由 crontab 无人值守执行，非零退出码只有在配了告警动作时才有人看见。
	// 此处主动投递，使已配置告警通道的部署无需额外的 crontab 编排。
	alerts := &alerting.Service{
		Events:   store.NewAlertEventRepo(db),
		Settings: store.NewSettingsRepo(db),
		Secrets:  secrets.New(cfg.EncryptKey),
	}
	if _, err := alerts.RaiseSync(ctx, alerting.Event{
		Type:     domain.AlertReconcileFailed,
		Severity: domain.AlertCritical,
		DedupKey: "reconcile_failed",
		Title:    "积分对账未通过",
		Message: failure.Error() + "。对账不通过意味着账本与余额的对应关系已经断裂，" +
			"在查明原因前，任何按余额出具的费用报表都不可信。",
		Payload: map[string]any{
			"balance_mismatches": len(mismatches),
			"key_mismatches":     len(keyMismatches),
			"orphan_precharges":  len(orphans),
			"orphan_credits":     orphanCredits,
		},
	}); err != nil {
		slog.Error("对账失败告警投递失败", "error", err)
	}
	return failure
}

func run(fn func(*config.Config) error) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	obs.InitLogger(cfg.LogLevel)
	if err := fn(cfg); err != nil {
		slog.Error("命令执行失败", "error", err)
		os.Exit(1)
	}
}

func runMigrate(cfg *config.Config) error {
	dir := "up"
	if len(os.Args) > 2 {
		dir = os.Args[2]
	}
	switch dir {
	case "up":
		if err := migrate.Up(cfg.DatabaseURL); err != nil {
			return err
		}
		slog.Info("迁移完成", "direction", "up")
	case "down":
		if err := migrate.Down(cfg.DatabaseURL); err != nil {
			return err
		}
		slog.Info("迁移完成", "direction", "down")
	default:
		return fmt.Errorf("migrate 方向必须是 up 或 down，收到 %q", dir)
	}
	return nil
}

// usageLogDropWatchInterval 用量日志丢弃计数的巡检周期。
const usageLogDropWatchInterval = time.Minute

// startUsageLogDropWatch 监视用量日志队列的累计丢弃条数，由 0 变为非零时告警。
// 丢弃意味着这些请求的计费明细与成本分摊记录永久缺失，健康接口虽已暴露该计数，
// 但只有管理员主动查看才会发现。
// 每一轮用 obs.RunSafe 包裹：本循环 panic 只记日志，不会杀死巡检循环。
func startUsageLogDropWatch(ctx context.Context, engine *relay.Engine, alerts alerting.Notifier) {
	go func() {
		var lastSeen int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(usageLogDropWatchInterval):
			}
			obs.RunSafe("usage_log_drop_watch", func() {
				dropped := engine.DroppedUsageLogCount()
				if dropped <= lastSeen {
					return
				}
				slog.Error("用量日志出现丢弃", "dropped_total", dropped)
				alerts.Raise(ctx, alerting.Event{
					Type:     domain.AlertUsageLogDropped,
					Severity: domain.AlertCritical,
					DedupKey: "usage_log_dropped",
					Title:    "用量日志出现丢弃",
					Message: fmt.Sprintf("用量日志写入队列累计丢弃 %d 条。"+
						"这些请求的计费明细与成本分摊记录已永久缺失，请检查数据库写入是否积压。", dropped),
					Payload: map[string]any{"dropped_total": dropped},
				})
				lastSeen = dropped
			})
		}
	}()
}

// buildDeps 装配 api.Deps：从 cfg/db/sqlDB 创建全部 store 仓库、计费服务、密钥盒、
// 告警服务、限流三件套与中继引擎。users/settings/billingSvc 由调用方在 bootstrapRoot
// 与启动期孤儿预扣清理阶段先行创建并传入，保证 Deps 与 serve 共用同一份实例。
//
// 纯装配、无 I/O 副作用：不做迁移、不建 root、不触发清理——这些前置副作用留在 serve。
// 抽成独立函数便于在单测中注入 nil db 验证装配正确性与引用一致性。
func buildDeps(cfg *config.Config, db *gorm.DB, sqlDB *sql.DB,
	users *store.UserRepo, settings *store.SettingsRepo, billingSvc *billing.Service) api.Deps {
	channelRepo := store.NewChannelRepo(db)
	costRepo := store.NewChannelCostRepo(db)
	modelRepo := store.NewModelRepo(db)
	usageLogs := store.NewUsageLogRepo(db)
	box := secrets.New(cfg.EncryptKey)
	upstreamClient := &http.Client{Timeout: time.Duration(cfg.UpstreamTimeoutSec) * time.Second}

	auditLogs := store.NewAuditLogRepo(db)
	alertEvents := store.NewAlertEventRepo(db)
	departments := store.NewDepartmentRepo(db)
	spend := store.NewSpendRepo(db)
	rollup := store.NewRollupRepo(db)
	auditRecorder := audit.NewRecorder(auditLogs)
	alerts := &alerting.Service{Events: alertEvents, Settings: settings, Secrets: box}

	return api.Deps{
		Cfg:           cfg,
		DB:            db,
		Sessions:      auth.NewSessionManager(sqlDB, cfg.SessionCookieSecure),
		Users:         users,
		Keys:          store.NewAPIKeyRepo(db),
		Models:        modelRepo,
		Ledger:        store.NewLedgerRepo(db),
		Redemptions:   store.NewRedemptionRepo(db),
		Settings:      settings,
		Billing:       billingSvc,
		Channels:      channelRepo,
		Costs:         costRepo,
		UsageLogs:     usageLogs,
		Secrets:       box,
		Stats:         store.NewStatsRepo(db),
		Limiter:       ratelimit.NewMemoryLimiter(),
		Gate:          ratelimit.NewConcurrencyGate(),
		LoginLock:     ratelimit.NewFailureLocker(),
		Departments:   departments,
		Projects:      store.NewProjectRepo(db),
		AuditLogs:     auditLogs,
		Audit:         auditRecorder,
		AlertEvents:   alertEvents,
		Alerts:        alerts,
		Spend:         spend,
		Rollup:        rollup,
		Integrations:  store.NewIntegrationRepo(db),
		ServiceTokens: store.NewServiceTokenRepo(db),
		Idempotency:   store.NewIdempotencyRepo(db),
		Relay: &relay.Engine{
			DB: db, Channels: channelRepo, Costs: costRepo, Models: modelRepo,
			Billing: billingSvc, UsageLogs: usageLogs, Settings: settings,
			Secrets: box, Client: upstreamClient, Spend: spend, Alerts: alerts,
			UpstreamTimeout: time.Duration(cfg.UpstreamTimeoutSec) * time.Second,
			Usage:           relay.NewUsageSink(usageLogs, settings, cfg.RecordDir, nil),
			Selector:        relay.NewChannelSelector(),
			Health:          relay.NewChannelHealth(channelRepo, box, upstreamClient, settings, alerts),
		},
	}
}

// startBackground 启动常驻后台任务并返回 cancel；调用 cancel 后各任务退出。
// 任务含：自动禁用渠道的半开探活、孤儿预扣定时回收、数据维护（按日汇总/保留期清理/预算检查）、
// 用量日志丢弃监视。各任务的执行间隔由系统设置控制。
// 抽成独立函数便于在测试中显式触发 cancel，验证后台任务的生命周期。
func startBackground(parent context.Context, deps api.Deps) (cancel func()) {
	ctx, cancel := context.WithCancel(parent)
	// 自动禁用渠道的定时半开探测：探活成功自动恢复启用。
	deps.Relay.StartRecoveryProbe(ctx)
	// 孤儿预扣的定时回收：仅靠启动时清理一次，长期不重启的进程等于不回收。
	(&billing.OrphanCleanupScheduler{
		Service: deps.Billing, Settings: deps.Settings, Alerts: deps.Alerts,
	}).Start(ctx)
	// 数据维护：用量按日汇总、审计与用量日志的保留期清理、部门超预算与低余额检查。
	(&maintenance.Scheduler{
		Settings: deps.Settings, Rollup: deps.Rollup, AuditLogs: deps.AuditLogs, Spend: deps.Spend,
		Departments: deps.Departments, Users: deps.Users, Audit: deps.Audit, Alerts: deps.Alerts,
		Billing: deps.Billing, UsageLogs: deps.UsageLogs, RecordDir: deps.Cfg.RecordDir,
	}).Start(ctx)
	// 用量日志丢弃计数由 0 变为非零即告警：丢弃意味着计费明细与成本分摊出现缺口。
	startUsageLogDropWatch(ctx, deps.Relay, deps.Alerts)
	return cancel
}

// shutdownFlusher 停机刷盘依赖的最小接口。*relay.Engine 实现该接口；
// 抽成接口便于在单测中注入桩，验证 shutdown 触发用量日志刷盘。
type shutdownFlusher interface {
	Close(ctx context.Context)
}

var _ shutdownFlusher = (*relay.Engine)(nil)

// shutdown 优雅停机：先在 shutdownTimeout 内停止接收新请求并等待在途请求完成，
// 再用 flushTimeout 刷盘用量日志队列。srv.Shutdown 超时仍会刷盘，避免日志丢失；
// 刷盘超时则放弃等待剩余记录（走丢弃计数分支）。返回停机错误（刷盘错误不返回，
// 与历史语义一致——刷盘失败已由丢弃计数与告警覆盖）。
func shutdown(srv *http.Server, flusher shutdownFlusher,
	shutdownTimeout, flushTimeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(ctx)
	// 无论优雅停机是否超时，都刷盘用量日志队列后再退出；
	// 停机窗口后仍在途请求的日志入队走丢弃计数分支，不会阻塞或崩溃。
	flushCtx, flushCancel := context.WithTimeout(context.Background(), flushTimeout)
	defer flushCancel()
	flusher.Close(flushCtx)
	if shutdownErr != nil {
		return fmt.Errorf("优雅停机超时: %w", shutdownErr)
	}
	slog.Info("服务已停止")
	return nil
}

func serve(cfg *config.Config) error {
	// fail-safe 启动守卫：上游超时加上结算写入窗口后必须严格小于孤儿预扣判定阈值，
	// 否则流式请求可能在结算写入窗口内被孤儿清理先退款，结算再补写 settle_adjust，
	// 使余额异常增加。结算写入窗口以 relay.BackgroundWriteTimeout 为唯一事实源。
	if err := checkOrphanTimeoutGuard(cfg.UpstreamTimeoutSec,
		billing.DefaultOrphanPrechargeThreshold, relay.BackgroundWriteTimeout); err != nil {
		return err
	}
	// 生产环境关闭 Secure cookie 意味着会话凭据在明文链路上传输，内网可被嗅探。
	// 允许该配置是为了让无 TLS 的内网部署可用，但必须在日志中显式留痕。
	if cfg.Env == config.EnvProd && !cfg.SessionCookieSecure {
		slog.Warn("会话 cookie 未启用 Secure 属性，登录凭据将在明文 HTTP 上传输；" +
			"仅在可信内网可接受，站点前置 TLS 后应设 TZL_SESSION_COOKIE_SECURE=true")
	}
	if err := migrate.Up(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("启动前自动迁移失败: %w", err)
	}
	db, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层连接池失败: %w", err)
	}

	users := store.NewUserRepo(db)
	if err := bootstrapRoot(cfg, users); err != nil {
		return err
	}

	settings := store.NewSettingsRepo(db)
	billingSvc := billing.NewService(db)

	// 启动时执行一次孤儿预扣清理：回收上次进程异常退出遗留的在途预扣。
	// 带超时：数据库锁等待时放弃本轮清理并继续启动，不允许启动无限期阻塞；
	// 清理失败不阻塞启动（后续可通过 cleanup-precharge 子命令手工补跑）。
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), startupCleanupTimeout)
	if result, err := billingSvc.CleanupOrphanPrecharges(cleanupCtx,
		billing.DefaultOrphanPrechargeThreshold); err != nil {
		slog.Warn("启动时孤儿预扣清理失败，降级继续启动", "error", err)
	} else if result.Scanned > 0 {
		slog.Info("启动时孤儿预扣清理完成",
			"scanned", result.Scanned, "refunded", result.Refunded,
			"refunded_credits", result.RefundedCredits, "already_handled", result.AlreadyHandled)
	}
	cleanupCancel()
	deps := buildDeps(cfg, db, sqlDB, users, settings, billingSvc)

	// 即时量的指标：在途并发与用量日志丢弃数。两者都取自运行中的组件，
	// 在导出时读一次，不做额外的采样与存储。
	obs.DefaultMetrics().SetGaugeFunc(obs.MetricRelayInFlight,
		"/v1 当前在途请求数（撞上并发上限时请求直接 503 且不排队）",
		func() float64 { return float64(deps.Gate.InFlight()) })
	obs.DefaultMetrics().SetGaugeFunc(obs.MetricUsageLogDropped,
		"用量日志队列累计丢弃条数（非零表示计费明细与成本分摊出现缺口）",
		func() float64 { return float64(deps.Relay.DroppedUsageLogCount()) })

	// 录制目录：配置了 RecordDir 时预创建，避免首条录制请求时并发 MkdirAll 竞争。
	if cfg.RecordDir != "" {
		if err := os.MkdirAll(filepath.Join(cfg.RecordDir, "recordings"), 0o700); err != nil {
			slog.Warn("创建录制目录失败，录制功能将不可用", "dir", cfg.RecordDir, "error", err)
		}
	}

	// 常驻后台任务：cancel 后各任务退出（间隔均由系统设置控制）。
	bgCancel := startBackground(context.Background(), deps)
	defer bgCancel()

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port)),
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("服务启动", "addr", srv.Addr, "env", cfg.Env, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		slog.Info("收到停机信号，开始优雅停机", "signal", sig.String())
	}

	return shutdown(srv, deps.Relay,
		time.Duration(cfg.ShutdownTimeoutSec)*time.Second, usageLogFlushTimeout)
}
