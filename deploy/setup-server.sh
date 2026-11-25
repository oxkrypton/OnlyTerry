#!/usr/bin/env bash
# 在云服务器上执行：bash setup-server.sh
# 作用：创建 /opt/tg-bot 目录结构及全部运行时配置文件
set -euo pipefail

BASE=${TG_DEPLOY_DIR:-/opt/tg-bot}
mkdir -p "$BASE/mihomo" "$BASE/data/service"

# ── docker-compose.yml ──────────────────────────────────
cat > "$BASE/docker-compose.yml" <<'YAML'
services:
  mihomo:
    image: metacubex/mihomo:latest
    container_name: tg-mihomo
    restart: unless-stopped
    volumes:
      - ./mihomo/config.yaml:/root/.config/mihomo/config.yaml:ro
    networks:
      - tg-internal
    healthcheck:
      test: ["CMD-SHELL", "nc -z 127.0.0.1 11305 || exit 1"]
      interval: 5s
      timeout: 3s
      retries: 12
      start_period: 10s

  tg-service:
    image: tg-service:latest
    container_name: tg-service
    restart: unless-stopped
    ports:
      - "127.0.0.1:3000:3000"
    env_file:
      - .env
    volumes:
      - ./data/service:/app/data/service
    depends_on:
      mihomo:
        condition: service_healthy
    networks:
      - tg-internal

  cloudflared:
    image: cloudflare/cloudflared:latest
    container_name: tg-cloudflared
    restart: unless-stopped
    command: tunnel --no-autoupdate run
    environment:
      TUNNEL_TOKEN: ${TUNNEL_TOKEN}
      TUNNEL_TRANSPORT_PROTOCOL: http2
    depends_on:
      - tg-service
    networks:
      - tg-internal

networks:
  tg-internal:
    driver: bridge
YAML

# ── .env（模板，需填写真实值）──────────────────────────
if [[ ! -f "$BASE/.env" ]]; then
  cat > "$BASE/.env" <<'ENV'
PROXY=socks5h://mihomo:11305
PUBLIC_URL=https://alerts.example.com
PAGES_URL=https://yourname.github.io
HOST=0.0.0.0
PORT=3000
TUNNEL_TOKEN=your_cloudflare_tunnel_token
DATABASE_PATH=data/service/service.db
IMAGE_DIR=data/service/images
TOKEN_SECRET=replace_with_at_least_32_random_characters
TYPESAFE_API_KEY=your_typesafe_api_key
RESEND_API_KEY=re_your_resend_api_key
EMAIL_FROM=Terry Alerts <alerts@example.com>
TG_ENABLED=true
TG_API_ID=12345678
TG_API_HASH=your_32_char_api_hash
TG_SESSION=your_string_session
TG_SESSION_FILE=data/service/tg.session
TG_STATE_FILE=data/service/tg.updates.json
TG_CHANNEL_MAP_JSON={"1234567890_1":"us_stocks","1234567890_2":"crypto"}
TARGET_AUTHOR_IDS=123456789
ENV
  chmod 600 "$BASE/.env"
  echo "已生成 .env（权限 600），请先填写真实值再启动"
fi

# ── mihomo 配置模板 ─────────────────────────────────────
if [[ ! -f "$BASE/mihomo/config.yaml" ]]; then
  cat > "$BASE/mihomo/config.yaml" <<'YAML'
# 请用 Clash Verge 导出的【已展开完整配置】替换本文件全部内容，
# 保留下列顶层参数，并删除任何 tun: 配置块。
mixed-port: 11305
allow-lan: true
bind-address: "*"
mode: rule
log-level: warning

# 兜底规则必须走代理组，例如：
# rules:
#   - MATCH,PROXY
YAML
  echo "已生成 mihomo/config.yaml 模板，请用导出的完整配置替换"
fi

echo ""
echo "配置已就绪：$BASE"
echo "下一步："
echo "  1. 编辑 $BASE/.env 填写真实值"
echo "  2. 用导出的完整配置替换 $BASE/mihomo/config.yaml"
echo "  3. 导入镜像：docker load < mihomo.tar.gz && docker load < tg-service.tar.gz"
echo "  4. 启动：cd $BASE && docker compose up -d"
