#!/usr/bin/env bash
# 远程 SQLite 快照同步
#   pull（默认 online）：服务不中断，远端用 SQLite 在线备份生成一致性快照 → 下载到本地
#   pull PULL_MODE=stop：停服后按文件拉取（兼容旧方式，适合远端没有 python3 的情况）
#   push：必须停服 → 备份远端原库 → 上传本地 service.db* → 恢复服务
# 只动 service.db / service.db-wal / service.db-shm，不碰 images、tg.session 等文件。
set -euo pipefail

ACTION=${1:?用法: db-sync.sh pull|push}

# 脚本位于 server/scripts/；按脚本位置定位本地数据目录，不依赖当前工作目录
SERVER_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# 默认走 ~/.ssh/config 里的别名；也可用 REMOTE_SSH=user@host 覆盖
REMOTE_SSH=${REMOTE_SSH:-my-vps}
REMOTE_DEPLOY=${REMOTE_DEPLOY:-/opt/my-app}
REMOTE_DIR=${REMOTE_DIR:-$REMOTE_DEPLOY/data/service}
LOCAL_DIR=${LOCAL_DIR:-$SERVER_DIR/data/service}
PULL_MODE=${PULL_MODE:-online}   # online | stop
SERVICE=${SERVICE:-my-service}

# 远端部署形态；默认按远端 Docker Compose 部署。
# systemd 部署可覆盖（例如在 .env 里设置）：
#   REMOTE_STOP='sudo systemctl stop my-service'
#   REMOTE_START='sudo systemctl start my-service'
REMOTE_STOP=${REMOTE_STOP:-"cd $REMOTE_DEPLOY && docker compose stop $SERVICE"}
REMOTE_START=${REMOTE_START:-"cd $REMOTE_DEPLOY && docker compose start $SERVICE"}

if [[ -z "$REMOTE_SSH" ]]; then
  echo "请指定 REMOTE_SSH（SSH 别名或 user@host）" >&2
  exit 1
fi
if [[ "$PULL_MODE" != "online" && "$PULL_MODE" != "stop" ]]; then
  echo "PULL_MODE 只能是 online 或 stop" >&2
  exit 1
fi

SSH_CMD=(ssh "$REMOTE_SSH")
RSYNC_CMD=(
  rsync -avz --partial --delete
  --include=service.db
  --include=service.db-wal
  --include=service.db-shm
  --exclude='*'
)

mkdir -p "$LOCAL_DIR"

# 停服模式下，无论中途成功失败都把远端服务拉起来
restore_remote() {
  echo "→ 恢复远端服务…" >&2
  "${SSH_CMD[@]}" "$REMOTE_START" >&2 || echo "⚠️ 远端服务启动失败，请手动检查" >&2
}

# 在线备份产生的远端临时快照，脚本退出时清理
SNAP_PATH=""
cleanup_snap() {
  if [[ -n "$SNAP_PATH" ]]; then
    "${SSH_CMD[@]}" "rm -f '$SNAP_PATH'" >/dev/null 2>&1 || true
    SNAP_PATH=""
  fi
}

pull_online() {
  echo "⚠️  如果本地 Navicat 正打开着 $LOCAL_DIR/service.db，请先关闭连接。"
  SNAP_PATH="/tmp/service-snap.$(date +%s).$$.db"
  trap cleanup_snap EXIT

  echo "→ 远端在线备份（服务不中断，正在跑的任务照常执行）…"
  "${SSH_CMD[@]}" "python3 -c 'import sqlite3,sys; s=sqlite3.connect(sys.argv[1]); d=sqlite3.connect(sys.argv[2]); s.backup(d); d.close(); s.close()' '$REMOTE_DIR/service.db' '$SNAP_PATH'"

  echo "→ 下载快照到 $LOCAL_DIR/service.db …"
  rsync -avz --partial "$REMOTE_SSH:$SNAP_PATH" "$LOCAL_DIR/service.db"
  # 快照已自包含，清掉本地旧 WAL/SHM，避免被重放到新库上
  rm -f "$LOCAL_DIR/service.db-wal" "$LOCAL_DIR/service.db-shm"

  cleanup_snap
  trap - EXIT
  echo "✓ 拉取完成（一致性时间点快照，远端服务全程在线）"
}

pull_stopped() {
  trap restore_remote EXIT
  echo "→ 停止远端服务…"
  "${SSH_CMD[@]}" "$REMOTE_STOP"
  echo "→ 拉取数据库到 $LOCAL_DIR/ …"
  "${RSYNC_CMD[@]}" "$REMOTE_SSH:$REMOTE_DIR/" "$LOCAL_DIR/"
  echo "✓ 拉取完成（停服模式）"
}

case "$ACTION" in
  pull)
    if [[ "$PULL_MODE" == "online" ]]; then
      pull_online
    else
      pull_stopped
    fi
    ;;
  push)
    if [[ ! -f "$LOCAL_DIR/service.db" ]]; then
      echo "本地没有 $LOCAL_DIR/service.db，请先执行 make db-pull" >&2
      exit 1
    fi
    echo "⚠️  上传前请确认已关闭 Navicat 连接，WAL 已合并进主文件。"
    trap restore_remote EXIT
    echo "→ 停止远端服务…"
    "${SSH_CMD[@]}" "$REMOTE_STOP"
    echo "→ 备份远端原数据库…"
    "${SSH_CMD[@]}" "cd '$REMOTE_DIR' && for f in service.db service.db-wal service.db-shm; do if [ -f \"\$f\" ]; then cp -p \"\$f\" \"\$f.bak.\$(date +%Y%m%d-%H%M%S)\"; fi; done"
    echo "→ 上传本地数据库…"
    "${RSYNC_CMD[@]}" "$LOCAL_DIR/" "$REMOTE_SSH:$REMOTE_DIR/"
    echo "✓ 上传完成（远端服务已重启）"
    ;;
  *)
    echo "用法: db-sync.sh pull|push" >&2
    exit 1
    ;;
esac
