#!/usr/bin/env bash
# 实时跟随远端服务的 Docker Compose 日志。
# 配置优先级：命令行/环境变量 > server/.env > 占位符默认值。
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$SCRIPT_DIR"

usage() {
  cat <<'EOF'
用法: bash scripts/logs.sh [选项]
  --once          只打印最近 N 行后退出（默认持续跟随）
  --tail N        最近 N 行，默认 200
  --since TIME    只看某时间点之后的日志，如 1h / 2026-11-23T12:00:00
  --grep PATTERN  仅显示匹配行（实时过滤，支持正则）
  --service NAME  覆盖服务名（默认取 .env 的 SERVICE）
  -h, --help      显示帮助

示例:
  bash scripts/logs.sh
  bash scripts/logs.sh --since 1h --grep jev_scored
  bash scripts/logs.sh --once --tail 50
EOF
}

# 只读取需要的键，不 source 整个 .env，避免执行其中任意内容。
env_value() {
  local key="$1" default="$2" value=""
  if [[ -f .env ]]; then
    value=$(sed -n "s/^${key}=//p" .env | head -n 1)
  fi
  printf '%s' "${value:-$default}"
}

ONCE=0
TAIL=200
SINCE=""
FILTER=""
SERVICE_OVERRIDE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --once) ONCE=1; shift ;;
    --tail) TAIL="${2:?--tail 需要参数}"; shift 2 ;;
    --since) SINCE="${2:?--since 需要参数}"; shift 2 ;;
    --grep) FILTER="${2:?--grep 需要参数}"; shift 2 ;;
    --service) SERVICE_OVERRIDE="${2:?--service 需要参数}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数: $1" >&2; usage >&2; exit 1 ;;
  esac
done

REMOTE_SSH=${REMOTE_SSH:-$(env_value REMOTE_SSH my-vps)}
REMOTE_DEPLOY=${REMOTE_DEPLOY:-$(env_value REMOTE_DEPLOY /opt/my-app)}
SERVICE=${SERVICE_OVERRIDE:-${SERVICE:-$(env_value SERVICE my-service)}}

if [[ -z "$REMOTE_SSH" ]]; then
  echo "请指定 REMOTE_SSH（SSH 别名或 user@host）" >&2
  exit 1
fi

remote="cd '$REMOTE_DEPLOY' && docker compose logs"
if [[ "$ONCE" -eq 0 ]]; then
  remote+=" -f"
fi
remote+=" --tail=$TAIL"
if [[ -n "$SINCE" ]]; then
  remote+=" --since '$SINCE'"
fi
remote+=" '$SERVICE'"

echo "→ ssh $REMOTE_SSH :: $remote" >&2

if [[ -n "$FILTER" ]]; then
  ssh "$REMOTE_SSH" "$remote" | grep --line-buffered -E -- "$FILTER"
else
  ssh "$REMOTE_SSH" "$remote"
fi
