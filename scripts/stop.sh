#!/usr/bin/env bash
# 停止服务。用法：
#   bash scripts/stop.sh            # 停止全部
#   bash scripts/stop.sh --force    # SIGKILL
#   bash scripts/stop.sh --backend-only | --frontend-only
set -e
. "$(dirname "$0")/_common.sh"

FORCE=""
MODE="all"
for arg in "$@"; do
  case "$arg" in
    --force) FORCE="--force" ;;
    --backend-only) MODE="backend" ;;
    --frontend-only) MODE="frontend" ;;
  esac
done

[ "$MODE" != "frontend" ] && stop_proc backend "$FORCE"
if [ "$MODE" != "backend" ]; then
  stop_proc admin "$FORCE"
  stop_proc portal "$FORCE"
fi
