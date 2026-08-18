#!/usr/bin/env bash
# 重启服务。用法：
#   bash scripts/restart.sh                  # 全部
#   bash scripts/restart.sh --backend-only | --frontend-only
#   bash scripts/restart.sh --force          # 停止阶段直接 SIGKILL
set -e
DIR="$(dirname "$0")"

# --force 只对停止阶段有意义，不能透传给 start.sh（会被当成未知参数拒绝）。
START_MODE="all"
for arg in "$@"; do
  case "$arg" in
    --backend-only|--frontend-only) START_MODE="$arg" ;;
  esac
done

bash "$DIR/stop.sh" "$@"
bash "$DIR/start.sh" "$START_MODE"
