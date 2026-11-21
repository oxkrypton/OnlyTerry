# 部署文档

Terry Alerts 的完整部署流程。目标机器是 2C2G、低带宽的 Linux 云服务器；构建在你的本地开发机（Mac/Linux）完成，服务器只接收二进制，**不需要安装 Go、Node、Git 等任何工具链**。

> 本文档与代码同仓库维护：部署流程、路径、端口、配置项发生变化时，必须同步更新本文档。

## 架构总览

```
GitHub Pages（订阅前端）
   │  fetch（跨域，仅放行 PAGES_URL 来源）
   ▼
https://api.你的域名.com   ← Caddy：自动 HTTPS、反代
   │  127.0.0.1:3000
   ▼
terry-alerts（systemd 常驻，terryalerts 用户）
   ├─ HTTP API：订阅 / 确认 / 退订 / healthz
   ├─ 内置 MTProto 采集器监听 TG 群聊 → 聚合 → JEV 打分 → Resend 发邮件
   └─ 内置定时清理（SQLite + 图片，存 data/service/）
```

## 1. 本地构建

```bash
cd server
go test ./...     # 先跑测试
make release      # 需要 Go 1.26+，仅在构建机上需要
```

产物：

```text
release/terry-alerts-linux-amd64.tar.gz
release/terry-alerts-linux-arm64.tar.gz
```

交叉编译 `CGO_ENABLED=0`，二进制无动态依赖，任意发行版可跑。

## 2. 上传

在**服务器**上确认 CPU 架构：

```bash
uname -m    # x86_64 → amd64；aarch64 → arm64
```

在**本地**用 rsync 上传（支持断点续传，适合小带宽）：

```bash
rsync -az --partial --progress \
  release/terry-alerts-linux-amd64.tar.gz user@服务器IP:/tmp/
```

## 3. 服务器安装

```bash
# 专用系统用户 + 安装目录
sudo useradd --system --home /opt/terry-alerts terryalerts
sudo mkdir -p /opt/terry-alerts
sudo tar -xzf /tmp/terry-alerts-linux-amd64.tar.gz -C /opt/terry-alerts
cd /opt/terry-alerts
sudo mv terry-alerts-linux-amd64 terry-alerts
sudo chmod +x terry-alerts
```

### 配置 .env

把仓库里 `server/.env.example` 的内容保存为 `/opt/terry-alerts/.env`，逐项填写（每项都有中文注释，带 `*` 的为必填）。关键项：

```env
PUBLIC_URL=https://api.你的域名.com        # 必须公网 HTTPS，用于邮件链接
PAGES_URL=https://你的用户名.github.io     # Pages 前端，CORS 只放行它
TOKEN_SECRET=$(openssl rand -hex 32)       # 至少 32 字符的随机串
TYPESAFE_API_KEY=...
RESEND_API_KEY=...
TG_API_ID=...                          # my.telegram.org 应用凭证
TG_API_HASH=...
TG_SESSION=...                         # scripts/gen_session.py 生成的 StringSession
TG_STATE_FILE=data/service/tg.updates.json  # 断线补包断点（默认值，可不写）；禁止多实例共享
TG_CHANNEL_MAP_JSON={"1234567890":"us_stocks","987654321":"crypto"}
TARGET_AUTHOR_IDS=123456789
```

权限收紧：

```bash
sudo chown -R terryalerts:terryalerts /opt/terry-alerts
sudo chmod 600 /opt/terry-alerts/.env
```

数据目录 `data/service/`（SQLite + 图片 + `tg.session` 登录凭证 + `tg.updates.json` 补包断点）由服务首次启动时自动创建。

## 4. systemd 常驻

```bash
sudo cp deploy/terry-alerts.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now terry-alerts
```

检查状态与日志：

```bash
systemctl status terry-alerts       # 应为 active (running)
journalctl -u terry-alerts -f       # 应输出“订阅服务已启动：…”
```

单元文件说明：

- `Restart=always`：崩溃 5 秒后自动拉起；
- 加固：`NoNewPrivileges`、`ProtectSystem=strict`，仅 `data/` 目录可写；
- 配置经 `EnvironmentFile` 注入，优先级高于服务自带的 `.env` 读取逻辑。

## 5. HTTPS 反代（Caddy）

GitHub Pages 是 HTTPS，浏览器禁止它调用 HTTP 接口，所以必须套一层 HTTPS。推荐 Caddy（自动签发/续期证书）。

1. DNS：把 `api.你的域名.com` 的 A 记录指向服务器 IP。
2. 安装 Caddy（Debian/Ubuntu）：

   ```bash
   sudo apt install caddy
   ```

3. `/etc/caddy/Caddyfile`：

   ```caddyfile
   api.你的域名.com {
       reverse_proxy 127.0.0.1:3000
   }
   ```

4. 生效：

   ```bash
   sudo systemctl reload caddy
   ```

Caddy 默认传递 `Host`、`X-Forwarded-For`，订阅接口的 IP 限流会按真实访客 IP 计数。

用 Nginx 亦可，需自行申请证书并传递同样的请求头。

## 6. 防火墙

只需对外开放 80/443：

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

3000 端口只供 Caddy 本机回源，不要对外放行。

## 7. 验证

```bash
curl https://api.你的域名.com/healthz     # {"ok":true}
curl -I https://api.你的域名.com/         # 302 到 PAGES_URL
```

端到端流程：

1. 打开 GitHub Pages 订阅页，提交邮箱；
   浏览器 Network 应看到 204 的 OPTIONS 预检 + 202 的 POST。
2. 邮箱收到确认邮件，点链接显示「订阅已确认」。
3. 上游博主发言后，日志出现 `{"event":"jev_scored",...}` 打分记录；
   分数超过阈值时订阅者收到原文邮件。

## 8. 升级

```bash
# 本地：重新 make release 并上传新包，然后服务器上：
sudo systemctl stop terry-alerts
sudo tar -xzf /tmp/新包.tar.gz -C /opt/terry-alerts
sudo mv /opt/terry-alerts/terry-alerts-linux-amd64 /opt/terry-alerts/terry-alerts
sudo chown terryalerts:terryalerts /opt/terry-alerts/terry-alerts
sudo systemctl start terry-alerts
```

数据库无迁移框架：未完成的收集/重试批次会在重启后自动恢复，不丢消息。

## 9. 备份与注意事项

- 备份整个 `data/service/` 目录即可（SQLite + 图片 + session 凭证 + 补包断点）。
- **不要**在服务运行中直接复制 SQLite 文件：先 `systemctl stop`，或使用 SQLite 在线备份。
- 单台服务器只运行一个实例（session 与投递幂等键均假设单实例）。
- 日志排查：`journalctl -u terry-alerts --since "1 hour ago"`。

## 10. 前端（GitHub Pages）

订阅落地页在仓库 `pages/` 目录（与服务端解耦，可推到任意 Pages 仓库）：

1. 修改 `pages/subscribe.js` 第一行 `API_BASE` 为 `https://api.你的域名.com`；
2. 把 `pages/` 三个文件推到 Pages 仓库并开启 Pages；
3. 服务端 `.env` 配置 `PAGES_URL=https://你的用户名.github.io` 并重启。

项目页（URL 带 `/仓库名/`）也能正常工作：`PAGES_URL` 只取 `scheme://host` 做来源比对，路径不参与。
