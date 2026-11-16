# OnlyTerry

Terry 频道发言的归档与邮件订阅系统，由四部分组成：

| 目录 | 说明 |
|---|---|
| `scripts/` | 抓取 Telegram 群聊原始发言（`fetch_tg.py`）、构建归档站点数据（`build_view.py`） |
| `frontend/` | 发言归档站点源码：按频道/时间浏览、搜索历史消息 |
| `dist/` | 归档站点构建产物（`make build` 生成，可直接静态托管） |
| `pages/` | 邮件订阅落地页（部署到 GitHub Pages，调用服务端订阅 API） |
| `server/` | 邮件订阅服务（Go 单二进制）：监听发言 → JEV 价值评分 → 高分原文邮件推送 |

## 文档索引

- **服务概览与构建**：[server/README.md](server/README.md)
- **部署上线（服务器、HTTPS、systemd、升级备份）**：[server/DEPLOYMENT.md](server/DEPLOYMENT.md)
- **开发文档（改代码前必读，含文档同步规则）**：[server/internal/app/DEVELOPMENT.md](server/internal/app/DEVELOPMENT.md)
- **配置项说明（带逐项注释）**：[server/.env.example](server/.env.example)

## 常用命令

```bash
make help      # 查看全部命令
make fetch     # 抓取真实发言数据（需根目录 .env）
make build     # 重建归档站点 dist/
make server    # 本地预览归档站点

cd server && make release   # 构建邮件服务的 Linux 发布包
```
