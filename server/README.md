# Go 版博主原始发言邮件订阅服务

这是面向 2C2G、低带宽云服务器重写的版本。JEV 只负责决策，邮件固定模板只包含原始文字、时间和原图。订阅前端托管在 GitHub Pages（由 `PAGES_URL` 指定，服务端仅对其放行跨域并把根路径重定向过去），服务器只保留 API 与邮件链接落点页，不需要 Node、npm、Go、Git 或外部静态文件。

## 功能

- 内置 Telegram MTProto 采集器（gotd/td）：复用 Telethon StringSession 登录 userbot，实时监听目标群聊、断线重连、目标博主过滤、两个频道映射和消息去重。图片走 MTProto 下载后转存，无需外部 WebSocket 数据源。
- 每个频道独立的两分钟静默窗口，新发言顺延；批次另设最长 15 分钟、最多 20 条的硬上限，高频发言下也会强制封箱打分。SQLite 持久化并支持重启恢复。
- JEV 0–100 分价值判断，严格大于 80 才发送，不生成摘要。
- Resend 固定模板邮件、CID 原图、每收件人幂等键和失败重试。
- 邮箱确认订阅、单频道或双频道、签名退订和 `List-Unsubscribe` 一键退订。
- 图片单并发解码、5 MB 默认下载上限、2500 万像素上限和 SSRF 防护。
- 内置定时清理，不依赖 crontab。
- 纯 Go SQLite 驱动，`CGO_ENABLED=0` 可交叉编译成无动态依赖的 Linux 二进制。

## 本地构建，服务器只接收产物

> 完整的部署、HTTPS、systemd、升级与备份流程见 [DEPLOYMENT.md](DEPLOYMENT.md)。

需要 Go 1.26 或更新版本的只是**构建机器**，不是云服务器。

```bash
cd server
go test ./...
make release
```

生成：

```text
release/terry-alerts-linux-amd64.tar.gz
release/terry-alerts-linux-arm64.tar.gz
```

查看服务器 CPU 架构：

```bash
uname -m
# x86_64 选择 amd64；aarch64 选择 arm64
```

通过支持断点续传的 rsync 上传，例如：

```bash
rsync -az --partial --progress \
  release/terry-alerts-linux-amd64.tar.gz user@server:/tmp/
```

服务器仅需：

```bash
sudo mkdir -p /opt/terry-alerts
sudo tar -xzf /tmp/terry-alerts-linux-amd64.tar.gz -C /opt/terry-alerts
cd /opt/terry-alerts
sudo mv terry-alerts-linux-amd64 terry-alerts
sudo chmod +x terry-alerts
# 将 server/.env.example 内容保存为 /opt/terry-alerts/.env 并填写真实配置
./terry-alerts
```

不需要 `git clone`、`go install`、`npm install` 或服务器端编译。

## 定时清理

服务启动时清理一次，之后按 `CLEANUP_INTERVAL` 周期执行：

```env
CLEANUP_INTERVAL=1h
IMAGE_RETENTION=168h
DB_RETENTION=720h
```

每次依次处理：

1. 删除超过 `DB_RETENTION` 的 `sent`、`skipped`、`failed` 批次及其消息、投递记录和图片引用。
2. 删除超过 48 小时仍未确认的订阅请求。
3. 删除超过 `IMAGE_RETENTION` 且不再被任何消息引用的图片。
4. 删除同期限、未登记到 SQLite 的孤儿 `.jpg` 文件。

仍在收集、评分、重试或发送中的批次不会被删除；仍被引用的图片不会被删除。默认图片保留 7 天，旧数据库批次保留 30 天。

## 低带宽图片策略

默认：

```env
RESEND_REMOTE_IMAGES=true
```

Telegram 图片由采集器通过 MTProto 下载到服务器、压缩转存为 JPEG 后再供 JEV 评分与邮件使用。这类图片没有公开 URL（`SourceURL` 为空），因此**始终从服务器上传处理后的 JPEG**，不受 `RESEND_REMOTE_IMAGES` 影响。多订阅者时会消耗相应上行带宽，这是 TG 数据源的固有行为。

`RESEND_REMOTE_IMAGES=true` 仅对带公开原图 URL 的图片生效（如未来接入其它上游）：服务器压缩一份供评分，发给 Resend 的附件只带远程 `path` 和 CID，由 Resend 从图片源拉取原图，避免“订阅者人数 × 图片大小”的服务器上行流量。

## Telegram 数据源（内置采集器）

服务启动时用 `TG_SESSION`（Telethon StringSession）以 userbot 身份登录 Telegram MTProto，实时监听 `TG_CHANNEL_MAP_JSON` 中配置的群聊/频道，过滤出 `TARGET_AUTHOR_IDS` 里的博主发言，规范化后进入聚合流水线。断线重连、心跳、DC 迁移由 gotd/td 处理。

- **凭证**：`TG_API_ID`/`TG_API_HASH` 来自 my.telegram.org；`TG_SESSION` 由仓库根目录的 `scripts/gen_session.py` 生成。**建议为邮件服务单独生成一个全新 session**，与 `scripts/fetch_tg.py` 抓取脚本用的 session 互不影响（两者共用同一组 API_ID/API_HASH）。session 等价于账号完全控制权，请用专用小号。
- **session 持久化**：gotd 刷新后的 session 写入 `TG_SESSION_FILE`（默认 `data/service/tg.session`），纳入备份；首次启动时由 `TG_SESSION` 字符串初始化。
- **断线补包（防漏消息）**：gotd 内建的 `updates.Manager` 把每条消息按 `pts` 编号记账到 `TG_STATE_FILE`（默认 `data/service/tg.updates.json`）。断线或重启期间博主发的消息不会丢——恢复连接后按断点用 `getDifference` 自动补齐，再走同一条去重/聚合流水线。该文件与 session 文件一样纳入备份；空洞过大（服务端截断）时日志会告警。**禁止多实例共享此文件**。
- **群聊 ID**：`TG_CHANNEL_MAP_JSON` 的 key 是群聊/频道的**纯数字 ID**。超级群（`-100…`）填去掉 `-100` 前缀的部分，普通频道填带符号完整 ID；与 gotd update 里的 `PeerChannel.ChannelID` 对齐。
- **博主 ID**：`TARGET_AUTHOR_IDS` 填博主的纯数字 user ID，留空则处理该群所有作者。
- **图片**：消息中的 photo 与 `image/*` document 通过 MTProto 下载后转 base64，交给现有 `ImageStore` 压缩转存，遵守 `MAX_IMAGES_PER_MESSAGE` 与 `MAX_IMAGE_BYTES` 限制。

相关实现在 `internal/app/tg_source.go`；消息字段映射与过滤是纯函数，单测在 `tg_source_test.go`。

## HTTP 入口

- `GET /`：302 重定向到 `PAGES_URL`（GitHub Pages 订阅前端）；未配置时返回 404。
- `POST /api/subscriptions`：`{"email":"...","channels":["us_stocks","crypto"]}`；仅对 `PAGES_URL` 来源返回 CORS 头并应答 OPTIONS 预检。
- `GET /confirm/{token}`：确认最新订阅请求，48 小时有效。
- `GET /unsubscribe/{token}`：退订确认页。
- `POST /unsubscribe/{token}`：退订全部频道，支持一键退订。
- `GET /healthz`：健康检查。

生产环境用 Nginx/Caddy 提供 HTTPS，并传 `Host`、`X-Forwarded-Proto`、`X-Forwarded-For`。`PUBLIC_URL` 必须是公网 HTTPS 根地址。

## systemd

参考 `deploy/terry-alerts.service`。创建专用用户、写权限和数据目录后启用：

```bash
sudo useradd --system --home /opt/terry-alerts terryalerts
sudo chown -R terryalerts:terryalerts /opt/terry-alerts
sudo cp deploy/terry-alerts.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now terry-alerts
```

备份 `data/service/`，不要跨机器直接复制正在写入的 SQLite 文件；先停止服务或使用 SQLite 在线备份。单台服务器只运行一个实例。

## 验证

```bash
go test ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/server
```

测试覆盖窗口延长、去重、严格分数边界、JEV 失败重试、双频道、确认和退订、图片尺寸、JEV multipart、Resend 远程 CID、HTTP 页面和清理安全性。测试 API 均为本地模拟，不消耗真实额度。

> 如果运行过此前的 Node/TypeScript 测试版，请为 Go 版使用新的 `DATABASE_PATH`。两版图片存储结构不同，不自动迁移旧测试数据库。
