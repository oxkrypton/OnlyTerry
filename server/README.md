# Go 版博主原始发言邮件订阅服务

这是面向 2C2G、低带宽云服务器重写的版本。JEV 只负责决策，邮件固定模板只包含原始文字、时间和原图。订阅前端托管在 GitHub Pages（由 `PAGES_URL` 指定，服务端仅对其放行跨域并把根路径重定向过去），服务器只保留 API 与邮件链接落点页，不需要 Node、npm、Go、Git 或外部静态文件。

## 功能

- WebSocket 监听、断线重连、目标博主过滤、两个频道映射和消息去重。
- 每个频道独立的两分钟静默窗口，新发言顺延，SQLite 持久化并支持重启恢复。
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

WebSocket 图片有公开 URL 时，服务器仍会下载并压缩一份供 JEV 评分，但发给 Resend 的附件只包含远程 `path` 和 CID。Resend 从图片源拉取原图，避免“订阅者人数 × 图片大小”的服务器上行流量。

- 群聊直接提供 base64 图片时会自动回退；
- 原图 URL 需要鉴权、不是公网地址或很快过期时，应设置 `RESEND_REMOTE_IMAGES=false`，此时会从服务器上传处理后的 JPEG。

如果原图 URL 需要鉴权或很快过期，应使用对象存储保存处理图并提供长期可访问 URL，或关闭远程模式。关闭远程模式时，多订阅者会消耗较多上行带宽。

## WebSocket 输入

默认接受规范结构及常见字段别名：

```json
{
  "type": "message",
  "id": "provider-message-id",
  "channel": "room-us",
  "author": {"id": "blogger-id", "name": "Terry"},
  "sent_at": "2026-11-16T12:00:00Z",
  "text": "NVDA 100 买入，止损 95",
  "images": [
    {"url": "https://cdn.example.com/order.jpg"},
    {"data_base64": "/9j/4AAQSkZJRg..."}
  ]
}
```

`WS_CHANNEL_MAP_JSON` 映射外部频道 ID；`TARGET_AUTHOR_IDS` 是逗号分隔的博主 ID。连接成功后按顺序发送 `WS_SUBSCRIBE_MESSAGES_JSON` 中的订阅帧。实际协议不同只需调整 `internal/app/message_parser.go`。

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
