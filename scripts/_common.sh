# 共享配置与工具函数，供 scripts/ 下各控制脚本 source 使用。

set -u

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_DIR="$ROOT_DIR/.scratch/pids"
LOG_DIR="$ROOT_DIR/.scratch/logs"
BIN_DIR="$ROOT_DIR/.scratch/bin"

# 端口约定
BACKEND_PORT=19030
ADMIN_PORT=19073
PORTAL_PORT=19074

mkdir -p "$PID_DIR" "$LOG_DIR" "$BIN_DIR"

# 加载 .env（存在时），为后端进程提供 TZL_* 环境变量
load_env() {
  if [ -f "$ROOT_DIR/.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$ROOT_DIR/.env"
    set +a
  fi
}

pid_file() { echo "$PID_DIR/$1.pid"; }

# service_port 返回服务约定的监听端口，未知服务返回空串。
service_port() {
  case "$1" in
    backend) echo "$BACKEND_PORT" ;;
    admin)   echo "$ADMIN_PORT" ;;
    portal)  echo "$PORTAL_PORT" ;;
    *)       echo "" ;;
  esac
}

is_running() {
  local name="$1" pf
  pf="$(pid_file "$name")"
  [ -f "$pf" ] && kill -0 "$(cat "$pf")" 2>/dev/null
}

# port_listeners 返回当前用户占用该端口的监听进程 pid（空格分隔）。
port_listeners() {
  local port="$1"
  command -v lsof >/dev/null 2>&1 || return 0
  lsof -a -u "$(id -u)" -ti tcp:"$port" -sTCP:LISTEN 2>/dev/null | tr '\n' ' '
}

# kill_spec 决定终止目标：进程自身是进程组组长时返回负的进程组号，
# 使信号送达整个进程组。前端 dev server 由 pnpm 派生 vite 子进程，
# 只终止记录在案的 pid 会留下 vite 继续占用端口，随后的 restart 看似
# 成功、浏览器实际仍连在旧进程上加载旧代码。start.sh 用 setsid 让每个
# 服务独占进程组，这里据此整组终止；拿不到进程组时回退为单进程终止。
kill_spec() {
  local pid="$1" pgid
  pgid="$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')"
  if [ -n "$pgid" ] && [ "$pgid" = "$pid" ]; then
    echo "-$pid"
  else
    echo "$pid"
  fi
}

stop_proc() {
  local name="$1" force="${2:-}" pf pid spec
  pf="$(pid_file "$name")"
  if ! [ -f "$pf" ]; then
    echo "[$name] 未在运行"
    release_port "$name"
    return 0
  fi
  pid="$(cat "$pf")"
  if kill -0 "$pid" 2>/dev/null; then
    spec="$(kill_spec "$pid")"
    if [ "$force" = "--force" ]; then
      kill -9 -- "$spec" 2>/dev/null || true
    else
      kill -- "$spec" 2>/dev/null || true
      for _ in $(seq 1 20); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.5
      done
      kill -0 "$pid" 2>/dev/null && kill -9 -- "$spec" 2>/dev/null || true
    fi
    echo "[$name] 已停止 (pid $pid)"
  else
    echo "[$name] PID 文件残留，已清理"
  fi
  rm -f "$pf"
  release_port "$name"
}

# release_port 兜底清理仍在占用端口的残留进程：进程组终止已覆盖正常路径，
# 这里处理 pid 文件丢失、进程被手工启动等场景。端口不放出来的后果是下一次
# start 静默走偏——vite 会自行改用其它端口，页面仍由旧进程提供。
release_port() {
  local name="$1" port pids
  port="$(service_port "$name")"
  [ -z "$port" ] && return 0
  pids="$(port_listeners "$port")"
  [ -z "${pids// /}" ] && return 0
  echo "[$name] 端口 $port 仍被占用 (pid ${pids% })，清理残留进程"
  # shellcheck disable=SC2086
  kill $pids 2>/dev/null || true
  for _ in $(seq 1 10); do
    pids="$(port_listeners "$port")"
    [ -z "${pids// /}" ] && return 0
    sleep 0.5
  done
  # shellcheck disable=SC2086
  kill -9 $pids 2>/dev/null || true
  sleep 0.5
  pids="$(port_listeners "$port")"
  [ -n "${pids// /}" ] && echo "[$name] 警告：端口 $port 仍被 pid ${pids% } 占用，需手工处理"
  return 0
}

wait_port() {
  local port="$1" tries="${2:-30}"
  for _ in $(seq 1 "$tries"); do
    if curl -sf -o /dev/null "http://localhost:$port/healthz" 2>/dev/null \
       || curl -sf -o /dev/null "http://localhost:$port/" 2>/dev/null; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}
