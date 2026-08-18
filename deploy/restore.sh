#!/usr/bin/env bash
# 从备份还原数据库，还原后自动执行积分对账。
#
# 用法：
#   restore.sh <备份文件> <目标数据库 URL>
# 例（先在演练库上验证，确认无误后再对生产库执行）：
#   ./restore.sh /opt/tzl/backup/tzl-20260806-033000.dump \
#       postgres://tzl:PASS@localhost:5432/tzl_restore_drill
#
# 恢复顺序要求：先恢复 /etc/tzl/env（其中的 TZL_ENCRYPT_KEY 不可变更），
# 再恢复数据库。顺序颠倒时服务会以新密钥启动，渠道密钥全部解不开。
#
# 本脚本会先停止 tzl 服务再还原（还原期间有写入会导致数据不一致），
# 还原完成后不自动启动，由操作者确认对账结果后手工启动。
set -euo pipefail

BACKUP_FILE="${1:-}"
TARGET_URL="${2:-}"
TZL_BIN="${TZL_BIN:-/opt/tzl/bin/tzl}"

if [ -z "$BACKUP_FILE" ] || [ -z "$TARGET_URL" ]; then
    echo "用法: $0 <备份文件> <目标数据库 URL>" >&2
    exit 1
fi
if [ ! -r "$BACKUP_FILE" ]; then
    echo "备份文件不可读: $BACKUP_FILE" >&2
    exit 1
fi

# 校验完整性：有 .sha256 伴随文件时必须匹配，避免还原一个已损坏的备份。
if [ -r "$BACKUP_FILE.sha256" ]; then
    echo "校验备份完整性..."
    sha256sum -c "$BACKUP_FILE.sha256"
else
    echo "警告: 未找到 $BACKUP_FILE.sha256，跳过完整性校验" >&2
fi
pg_restore --list "$BACKUP_FILE" >/dev/null

echo "目标库: $TARGET_URL"
read -r -p "还原会覆盖目标库中的同名对象，确认继续？(yes/no) " confirm
if [ "$confirm" != "yes" ]; then
    echo "已取消"
    exit 1
fi

if systemctl is-active --quiet tzl 2>/dev/null; then
    echo "停止 tzl 服务..."
    systemctl stop tzl
    STOPPED_SERVICE=1
else
    STOPPED_SERVICE=0
fi

echo "开始还原..."
# --clean --if-exists：先删除同名对象再重建，使还原结果与备份时刻一致，
# 不会残留备份之后新建的表或行。
pg_restore --clean --if-exists --no-owner --no-privileges \
    --dbname="$TARGET_URL" "$BACKUP_FILE"

echo "还原完成，执行积分对账..."
if [ -x "$TZL_BIN" ]; then
    TZL_DATABASE_URL="$TARGET_URL" "$TZL_BIN" reconcile
else
    echo "警告: 未找到可执行的 $TZL_BIN，跳过对账；请手工执行 tzl reconcile" >&2
fi

if [ "$STOPPED_SERVICE" = "1" ]; then
    echo "tzl 服务仍处于停止状态。确认对账结果与 /etc/tzl/env 中的 TZL_ENCRYPT_KEY"
    echo "与备份时一致后，执行 systemctl start tzl 启动。"
fi
