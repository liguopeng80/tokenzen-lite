#!/usr/bin/env bash
# 查看服务状态与日志。用法：
#   bash scripts/status.sh                 # 状态总览
#   bash scripts/status.sh --logs backend  # 跟随查看某服务日志 (backend|admin|portal)
set -e
. "$(dirname "$0")/_common.sh"

if [ "${1:-}" = "--logs" ]; then
  name="${2:-backend}"
  exec tail -f "$LOG_DIR/$name.log"
fi

# 同时看 pid 与端口：只看 pid 会把"进程已停但子进程仍占端口"报成 stopped，
# 而浏览器此时访问到的正是那个残留进程。
for name in backend admin portal; do
  port="$(service_port "$name")"
  listeners="$(port_listeners "$port")"
  listeners="${listeners% }"
  if is_running "$name"; then
    echo "[$name] running (pid $(cat "$(pid_file "$name")"), 端口 $port 监听 pid ${listeners:-无})"
  elif [ -n "$listeners" ]; then
    echo "[$name] stopped，但端口 $port 仍被 pid $listeners 占用（残留进程，运行 stop.sh 清理）"
  else
    echo "[$name] stopped"
  fi
done
