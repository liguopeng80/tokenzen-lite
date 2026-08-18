#!/usr/bin/env bash
# 配置与密钥备份。crontab 示例（与数据库备份同频，以 root 运行）：
#   35 3 * * * /opt/tzl/deploy/backup-secrets.sh >> /opt/tzl/backup/backup.log 2>&1
#
# 备份对象是 /etc/tzl/env，其中的 TZL_ENCRYPT_KEY 是渠道上游密钥的解密密钥，
# 一旦设定不可变更。该密钥不在数据库中，因此数据库备份单独存在时无法还原服务：
# 恢复后 channels.api_key_encrypted 全部解不开，只能重新向各上游厂商索取密钥。
#
# 产物含明文凭据，权限 600，且必须与数据库备份分开存放、异地保存——
# 两者放在同一处时，一次介质损坏或一次泄露就同时失去或暴露全部要素。
set -euo pipefail

ENV_FILE="${ENV_FILE:-/etc/tzl/env}"
SECRETS_DIR="${SECRETS_DIR:-/opt/tzl/backup/secrets}"
KEEP_DAYS="${KEEP_DAYS:-90}"

alert() {
    local msg="$1"
    echo "$(date -Is) 密钥备份失败: $msg" >&2
    if [ -n "${TZL_ALERT_WEBHOOK:-}" ]; then
        curl -fsS -m 10 -X POST "$TZL_ALERT_WEBHOOK" \
            -H 'Content-Type: application/json' \
            -d "{\"event\":\"secrets_backup_failed\",\"host\":\"$(hostname)\",\"message\":\"${msg}\"}" \
            >/dev/null 2>&1 || echo "$(date -Is) 告警上报失败" >&2
    fi
}
trap 'alert "脚本在第 $LINENO 行中止"' ERR

if [ ! -r "$ENV_FILE" ]; then
    alert "配置文件不可读: $ENV_FILE（需以 root 运行）"
    exit 1
fi

mkdir -p "$SECRETS_DIR"
chmod 700 "$SECRETS_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$SECRETS_DIR/tzl-env-$STAMP.tar.gz"

tar -czf "$OUT" -C "$(dirname "$ENV_FILE")" "$(basename "$ENV_FILE")"
chmod 600 "$OUT"
sha256sum "$OUT" > "$OUT.sha256"
chmod 600 "$OUT.sha256"

# 明确核对加密密钥确实进了备份：这是恢复能否成功的决定性要素。
if ! tar -xzOf "$OUT" | grep -q '^TZL_ENCRYPT_KEY='; then
    alert "备份中未找到 TZL_ENCRYPT_KEY，恢复后渠道密钥将无法解密"
    exit 1
fi

echo "$(date -Is) 密钥备份完成: $OUT"
echo "$(date -Is) 提醒：请将该文件复制到与数据库备份不同的存储位置"

find "$SECRETS_DIR" -name 'tzl-env-*.tar.gz' -mtime "+$KEEP_DAYS" -delete
find "$SECRETS_DIR" -name 'tzl-env-*.tar.gz.sha256' -mtime "+$KEEP_DAYS" -delete
