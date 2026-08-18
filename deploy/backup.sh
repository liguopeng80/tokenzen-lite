#!/usr/bin/env bash
# PostgreSQL 每日备份。crontab 示例（以能读取 /etc/tzl/env 的用户运行，通常是 root）：
#   30 3 * * * . /etc/tzl/env && /opt/tzl/deploy/backup.sh >> /opt/tzl/backup/backup.log 2>&1
#
# 连接信息一律取自 TZL_DATABASE_URL，不依赖 PostgreSQL 的 peer/ident 认证——
# 以 root 身份跑 cron 时 peer 认证会失败，而失败前的旧版脚本没有告警，
# 会造成"以为每天在备份、真出事时目录里全是空文件"。
#
# 注意：本脚本只备份数据库。渠道上游密钥用 TZL_ENCRYPT_KEY 加密存储，
# 该密钥在 /etc/tzl/env 而不在库里，必须另行用 backup-secrets.sh 备份，
# 否则换机恢复后全部渠道密钥无法解密，持有完整数据库备份也起不来服务。
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/opt/tzl/backup}"
KEEP_DAYS="${KEEP_DAYS:-14}"
DB_URL="${TZL_DATABASE_URL:-}"

# alert 在备份失败时上报，优先走管理端配置的告警通道（Webhook 与邮件），
# 使运维脚本与系统本身共用同一份通道配置——各配各的会在换通道时漏改其中一处。
# tzl 不可执行时回退到 TZL_ALERT_WEBHOOK 直连，最后兜底只写日志。
TZL_BIN="${TZL_BIN:-/opt/tzl/tzl}"
alert() {
    local msg="$1"
    echo "$(date -Is) 备份失败: $msg" >&2
    if [ -x "$TZL_BIN" ]; then
        "$TZL_BIN" alert backup_failed "数据库备份失败" \
            "主机 $(hostname)：${msg}" >/dev/null 2>&1 && return
    fi
    if [ -n "${TZL_ALERT_WEBHOOK:-}" ]; then
        curl -fsS -m 10 -X POST "$TZL_ALERT_WEBHOOK" \
            -H 'Content-Type: application/json' \
            -d "{\"event\":\"backup_failed\",\"host\":\"$(hostname)\",\"message\":\"${msg}\"}" \
            >/dev/null 2>&1 && return
    fi
    echo "$(date -Is) 告警上报失败，请检查告警通道配置" >&2
}
trap 'alert "脚本在第 $LINENO 行中止"' ERR

if [ -z "$DB_URL" ]; then
    alert "TZL_DATABASE_URL 未设置，无法连接数据库（cron 中需先 source /etc/tzl/env）"
    exit 1
fi

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$BACKUP_DIR/tzl-$STAMP.dump"

# custom 格式：支持 pg_restore 并行还原与单表挑选，且自带压缩。
pg_dump --format=custom --compress=6 --file="$OUT" "$DB_URL"

# 完整性自检：能列出目录说明文件结构完好，不是被截断的半个文件。
if ! pg_restore --list "$OUT" >/dev/null 2>&1; then
    alert "备份文件校验未通过（pg_restore --list 失败）: $OUT"
    rm -f "$OUT"
    exit 1
fi

sha256sum "$OUT" > "$OUT.sha256"
echo "$(date -Is) 备份完成: $OUT ($(du -h "$OUT" | cut -f1))"

# 清理过期备份及其校验文件
find "$BACKUP_DIR" -name 'tzl-*.dump' -mtime "+$KEEP_DAYS" -delete
find "$BACKUP_DIR" -name 'tzl-*.dump.sha256' -mtime "+$KEEP_DAYS" -delete
