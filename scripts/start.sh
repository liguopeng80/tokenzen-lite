#!/usr/bin/env bash
# 启动服务。用法：
#   bash scripts/start.sh                  # 启动后端（前端就绪后含前端 dev server）
#   bash scripts/start.sh --backend-only   # 仅后端
#   bash scripts/start.sh --frontend-only  # 仅前端 dev server
set -e
. "$(dirname "$0")/_common.sh"

MODE="${1:-all}"

start_backend() {
  if is_running backend; then
    echo "[backend] 已在运行 (pid $(cat "$(pid_file backend)"))"
    return 0
  fi
  # 残留的旧后端仍会响应健康检查，wait_port 会误判为"新进程已就绪"。
  release_port backend
  load_env
  echo "[backend] 编译..."
  (cd "$ROOT_DIR/server" && go build -o "$BIN_DIR/tzl" ./cmd/tzl)
  echo "[backend] 启动 (port $BACKEND_PORT)..."
  # setsid 让服务独占进程组，stop.sh 才能整组终止（见 _common.sh 的 kill_spec）。
  TZL_PORT="$BACKEND_PORT" setsid nohup "$BIN_DIR/tzl" serve \
    >>"$LOG_DIR/backend.log" 2>&1 &
  echo $! >"$(pid_file backend)"
  if wait_port "$BACKEND_PORT"; then
    echo "[backend] 就绪: http://localhost:$BACKEND_PORT"
  else
    echo "[backend] 启动失败，最近日志："
    tail -20 "$LOG_DIR/backend.log"
    exit 1
  fi
}

start_frontend() {
  for app in admin portal; do
    local port_var
    [ "$app" = admin ] && port_var="$ADMIN_PORT" || port_var="$PORTAL_PORT"
    if [ ! -d "$ROOT_DIR/apps/$app" ]; then
      echo "[$app] 目录不存在，跳过"
      continue
    fi
    if is_running "$app"; then
      echo "[$app] 已在运行"
      continue
    fi
    # 端口被上一次运行的残留进程占着时，vite 会静默改用其它端口，
    # 表现为"启动成功但页面还是旧的"。先清掉再起。
    release_port "$app"
    echo "[$app] 启动 dev server (port $port_var)..."
    # setsid：pnpm 会派生 vite 子进程，独占进程组后 stop.sh 才能连子进程一起终止。
    # setsid 必须是子 shell 里被后台化的那条简单命令——写成 `cd … && setsid …&`
    # 会让 bash 保留一层子 shell，$! 记下的是那层 shell 而非服务本身，
    # 既杀不掉 vite，也会因为它继续持有继承来的标准输出而让调用方读不到 EOF。
    (
      cd "$ROOT_DIR" || exit 1
      setsid nohup pnpm --filter "@token-zen/$app" dev --port "$port_var" \
        >>"$LOG_DIR/$app.log" 2>&1 </dev/null &
      echo $! >"$(pid_file "$app")"
    )
  done
}

case "$MODE" in
  all)            start_backend; start_frontend ;;
  --backend-only) start_backend ;;
  --frontend-only) start_frontend ;;
  *) echo "未知参数: $MODE"; exit 2 ;;
esac
