# Terry Alerts 邮件订阅服务 · 开发文档

> **⚠️ 强制规则：修改代码必须同步修改本文档。**
>
> 本文档是 `server/internal/app` 模块的唯一权威开发文档。任何改变模块行为的代码修改（新增/修改/删除配置项、数据库表结构、HTTP 接口、状态机流转、外部 API 调用、文件职责划分、默认值、阈值等），**都必须在同一个提交中更新本文档的对应章节**。评审时若发现代码与文档不一致，一律以文档为准要求回退或补文档。
>
> 文档最后更新：2026-11-16（与代码基线同步）。

---

## 1. 模块概述

Terry Alerts 是一个「博主原始发言邮件订阅服务」。核心流水线：

```
群聊 WebSocket ──▶ 消息解析/过滤 ──▶ 按频道聚合(静默窗口) ──▶ JEV 价值打分
                                                                   │ > 80 分
                                                                   ▼
                                              SQLite 批次/投递记录 ◀── Resend 邮件群发(原文+原图)
```

- 监听目标博主在 **美股（us_stocks）** 和 **加密货币（crypto）** 两个频道的实时发言。
- 每个频道独立聚合一个静默窗口（默认 120 秒）内的发言为一个批次（Batch）；批次另有最长等待 15 分钟、最多 20 条消息的硬上限，任一触发即强制封箱，防止高频发言导致窗口被无限顺延。
- 用 TypeSafe JEV 决策模型对批次打 0–100 分，**严格大于 `SCORE_THRESHOLD`（默认 80）** 才投递。
- 不做摘要：邮件只包含博主原文、发送时间（Asia/Shanghai）和原图（CID 内嵌）。
- 订阅采用邮箱确认制（double opt-in），每封邮件带签名退订链接和 `List-Unsubscribe` 头。
- 订阅前端托管在 GitHub Pages：服务端不内嵌订阅页，只对 `PAGES_URL` 来源放行跨域、把根路径重定向过去；保留的页面仅有确认/退订结果页（内联 HTML，是状态变更的落点，不可删除）。
- 面向 2C2G 低配置云服务器：纯 Go SQLite 驱动（`modernc.org/sqlite`），`CGO_ENABLED=0` 可交叉编译出无动态依赖的 Linux 二进制。

## 2. 运行拓扑与入口

入口：`server/cmd/server/main.go`，装配顺序：

1. `LoadDotEnv(".env")` → `LoadConfig()` → `config.Validate()`，校验失败直接退出。
2. 依次构造：`Store`（SQLite）→ `Tokens`（HMAC 签名）→ `ResendMailer` → `JevScorer` → `Coordinator` → `ImageStore` → `GroupSocket`。
3. WS 消息回调流水线：`store.HasMessage` 去重 → `images.Prepare` 下载转存图片 → 空消息丢弃 → `coordinator.Accept` 入库并入窗。
4. `coordinator.Recover()` 从数据库恢复未完成批次（重启不丢窗口）。
5. 后台 goroutine：`Cleaner.Run`（定时清理）、`GroupSocket.Run`（WS 监听，`WS_ENABLED` 可关）。
6. HTTP 服务监听 `HOST:PORT`；收到 `SIGINT/SIGTERM` 后停止 Coordinator、10 秒内优雅关闭。

部署：`server/deploy/terry-alerts.service`（systemd unit）。构建与发布见 `server/Makefile`（`make release` 产出 Linux amd64/arm64 tar 包）。

## 3. 文件职责一览

| 文件 | 类型/导出物 | 职责 |
|---|---|---|
| `types.go` | `Channel`、`IncomingMessage`、`StoredMessage`、`Batch`、`BatchWithMessages`、`Subscriber`、`ScoreResult`、`Scorer` 接口、`Mailer` 接口、`AlertMail`、`RandomID()` | 全部领域模型与状态枚举；`Scorer`/`Mailer` 是可替换的依赖接口 |
| `config.go` | `Config`、`LoadConfig()`、`Validate()`、`LoadDotEnv()` | 环境变量解析（含默认值）、启动校验、`.env` 加载（不覆盖已有环境变量） |
| `coordinator.go` | `Coordinator` | 核心调度器：窗口定时、打分、重试、投递编排（见 §5） |
| `store.go` | `Store` | SQLite 持久化与全部 SQL（见 §6） |
| `websocket.go` | `GroupSocket` | 上游群聊 WS 连接、鉴权、订阅、断线重连（见 §7） |
| `message_parser.go` | `NormalizeSocketMessage()` | 容错解析上游多种嵌套消息格式 → `IncomingMessage` |
| `images.go` | `ImageStore` | 图片下载、校验、转码、落盘，SSRF 防护（见 §8） |
| `jev.go` | `JevScorer` | 调用 TypeSafe JEV 模型打分（见 §9） |
| `mail.go` | `ResendMailer`、`RenderAlertEmail()` | 确认邮件与告警邮件渲染、Resend API 发送（见 §10） |
| `tokens.go` | `Tokens` | HMAC-SHA256 签名 token：确认链接（48h 有效）、退订链接（见 §11） |
| `http.go` | `HTTPHandler` | HTTP 路由、限流、Pages 跨域放行与预检、确认/退订结果页、安全头（见 §12） |
| `cleanup.go` | `Cleaner` | 定时清理过期数据库记录与图片（见 §13） |
| `*_test.go` | — | 单元测试（store/coordinator/http/images/mail/parser/cleanup） |

## 4. 配置项（环境变量）

所有配置经 `LoadConfig()` 读取，`Validate()` 在启动时校验。新增配置项必须：① 加入 `Config` 结构体；② 在 `LoadConfig` 中解析；③ 需要时加入 `Validate`；④ 更新本表与 `.env.example`。

| 变量 | 默认值 | 说明 |
|---|---|---|
| `HOST` | `0.0.0.0` | HTTP 监听地址 |
| `PORT` | `3000` | HTTP 端口 |
| `PUBLIC_URL` | `http://127.0.0.1:3000` | 对外完整 URL，用于拼接确认/退订链接（必须是 http/https） |
| `PAGES_URL` | 空（可选） | GitHub Pages 前端地址：设置后 `GET /` 302 到该地址，且仅对该来源（scheme://host）放行 CORS 并应答预检；确认/退订结果页的「重新订阅」链接也指向它 |
| `DATABASE_PATH` | `data/service/service.db` | SQLite 文件路径 |
| `IMAGE_DIR` | `data/service/images` | 图片存储目录 |
| `TOKEN_SECRET` | （必填） | HMAC 签名密钥，**至少 32 字符** |
| `TYPESAFE_API_KEY` | （必填） | TypeSafe API 密钥 |
| `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` | TypeSafe API 地址 |
| `JEV_MODEL` | `jev-latest` | JEV 模型名 |
| `RESEND_API_KEY` | （必填） | Resend API 密钥 |
| `RESEND_BASE_URL` | `https://api.resend.com` | Resend API 地址 |
| `EMAIL_FROM` | `Terry Alerts <onboarding@resend.dev>` | 发件人 |
| `MESSAGE_WINDOW_MS` | `120000` | 每频道聚合静默窗口（毫秒，≥1000） |
| `MAX_BATCH_WAIT_MS` | `900000` | 批次自创建起的最长等待（毫秒），须大于窗口；到点强制封箱 |
| `MAX_BATCH_MESSAGES` | `20` | 批次最多容纳的消息数（≥1），达到即封箱，后续消息另起一批 |
| `SCORE_THRESHOLD` | `80` | 投递阈值，批次得分须**严格大于**该值（0–100） |
| `RETRY_MS` | `60000` | 批次失败重试间隔（毫秒） |
| `MAX_BATCH_ATTEMPTS` | `8` | 批次最大处理尝试次数（≥1），超限标记 `failed` |
| `MAX_IMAGE_BYTES` | `5242880` | 单图下载上限（5 MB） |
| `MAX_IMAGE_PIXELS` | `25000000` | 单图像素上限 |
| `MAX_IMAGES_PER_MESSAGE` | `6` | 每条消息最多保存图片数 |
| `MAX_SCORING_IMAGES` | `6` | 送评分时每批次最多携带图片数 |
| `ALLOW_PRIVATE_IMAGE_URLS` | `false` | 是否允许内网图片 URL（SSRF 开关，生产必须 `false`） |
| `RESEND_REMOTE_IMAGES` | `true` | 告警邮件中是否内嵌图片附件 |
| `CLEANUP_INTERVAL` | `1h` | 清理周期 |
| `IMAGE_RETENTION` | `168h` | 图片保留时长（7 天） |
| `DB_RETENTION` | `720h` | 批次/消息等记录保留时长（30 天） |
| `WS_ENABLED` | `true` | 是否启用上游 WebSocket 监听 |
| `WS_URL` | （启用时必填） | 上游 WS 地址，必须 ws/wss |
| `WS_AUTH_TOKEN` | 空 | WS `Authorization: Bearer` 令牌 |
| `WS_HEADERS_JSON` | `{}` | 额外 WS 请求头（JSON 对象） |
| `WS_SUBSCRIBE_MESSAGES_JSON` | `[]` | 连接后发送的订阅消息数组（JSON） |
| `WS_CHANNEL_MAP_JSON` | 两频道同名映射 | 上游频道标识 → 内部 `Channel` 的映射 |
| `TARGET_AUTHOR_IDS` | 空 | 逗号分隔的目标博主 ID；非空时只处理这些作者 |

## 5. 核心调度：Coordinator 与批次状态机

### 5.1 状态机

```
collecting ──窗口到期──▶ scoring ──成功且 ≤ 阈值──▶ skipped
    ▲                      │
    │                      ├─成功且 > 阈值─▶ approved ──▶ sending ──▶ sent
    │                      │                    ▲            │
    │                      └─失败──▶ retrying ──┘            │ 失败
    │                              （保留 approved 状态）     ▼
    └────────────── 失败未超次数 ◀── ScheduleRetry ◀── 超过 MAX_BATCH_ATTEMPTS → failed
```

关键不变量：

- **静默窗口顺延（带硬上限）**：窗口期内每来一条新消息，批次 `due_at` 顺延到「该消息时间 + Window」，但不超过「批次创建时间 + MaxBatchWait」；同时消息数达到 `MaxMessages` 时 `due_at` 立即置为当前时间。任一硬上限触发后批次封箱，后续消息另起一批——避免持续发言把静默窗口无限顺延导致批次永不打分。
- **严格大于阈值**：`Score <= ScoreThreshold` 即跳过（`skipped`）。
- **已批准不重打分**：批次进入 `approved` 后，后续重试只重发邮件（`deliver`），不再调用评分。
- **幂等投递**：`deliveries` 表按 `(batch_id, subscriber_id)` 预占（`ReserveDelivery`），重试/重启不会给同一收件人重复发信。
- **重启恢复**：`Recover()` 找出所有 `collecting/retrying/approved` 且未完成的批次重新排入定时器。
- **并发保护**：`running` map 防止同一批次并发处理；`Stop()` 停掉所有定时器。

### 5.2 修改指南

- 改状态流转：同步改 `BatchStatus` 常量（`types.go`）、`store.go` 的 `Mark*`/`ScheduleRetry`、`coordinator.go` 的 `Process`，并更新上面的状态图。
- 改打分通过条件：改 `coordinator.go:Process` 中的比较逻辑，并同步 §4 的 `SCORE_THRESHOLD` 描述与 README。
- 打分事件以 JSON 结构日志输出（`event: jev_scored`，含 score/rawScore/confidence/probabilities），改字段需同步排查日志消费方。

## 6. 数据层：Store 与表结构

SQLite（WAL 模式，`foreign_keys = ON`，`busy_timeout = 5000`，单连接 `SetMaxOpenConns(1)`）。

| 表 | 关键点 |
|---|---|
| `subscribers` | `email` 唯一（NOCASE）；`status` ∈ pending/active/unsubscribed；`pending_request_id` 防止旧确认链接生效 |
| `subscriber_channels` | `(subscriber_id, channel)` 主键，级联删除 |
| `batches` | 见状态机；`batches_recovery(status, due_at)` 索引服务重启恢复 |
| `messages` | `UNIQUE(channel, external_id)` 消息级去重 |
| `images` | 文件元数据（尺寸、来源 URL/MIME） |
| `message_images` | 消息-图片多对多（保序 `image_index`） |
| `deliveries` | `(batch_id, subscriber_id)` 幂等键；记录 `resend_id`/error |

规则：

- **改表结构必须同步**：`newStore` 中的建表语句、对应 scan/组装代码、相关测试、本表。当前无迁移框架， schema 变更需同时给出对存量数据库的处理方案（手动 SQL 或升级步骤）。
- 所有时间戳列为 Unix 毫秒。
- `Store.now` 可注入时钟用于测试（`newStore(path, now)`），新写时间相关逻辑必须走 `s.now()`，禁止直接调 `time.Now()`（Coordinator/Cleaner 等非 Store 代码除外）。

## 7. 上游接入：GroupSocket 与消息解析

- `GroupSocket.Run`：断线后指数退避重连（1s 起步 ×2，30s 封顶）；连接后依次发送 `WS_SUBSCRIBE_MESSAGES_JSON` 中的订阅消息；WS 读限为 `MaxImageBytes * 2`。
- `NormalizeSocketMessage(payload, channelMap, targetAuthors)`：
  - 容错解包 `message` / `data.message` / `payload` / `data` 等嵌套层；
  - 图片字段识别 `images`、`image_urls`、`imageUrls`、`media`、`attachments`、`image`；
  - 频道经 `WS_CHANNEL_MAP_JSON` 映射，未映射的消息丢弃；
  - `TARGET_AUTHOR_IDS` 非空时仅放行目标作者；
  - 时间字段统一解析为 RFC3339。
- 新增上游字段/格式时：改 `message_parser.go`，补 `message_parser_test.go` 用例，并在此节登记。

## 8. 图片处理：ImageStore

- `Prepare(sources, limit)`：逐张下载（**单并发信号量** `sem`，保护低带宽服务器），失败的图片跳过不阻塞消息。
- 安全约束（`checkURL`/`download`）：仅 http/https、禁止 URL 携带用户信息、**不跟随重定向**、默认拒绝回环/私网/链路本地/组播/CGNAT 地址（SSRF 防护，`ALLOW_PRIVATE_IMAGE_URLS` 可放开，仅用于测试）。
- `convert`：解码（jpeg/png/gif/webp）→ 像素上限校验 → 统一转 JPEG 落盘，文件名为内容 SHA-256（天然去重）。
- 送评分与发邮件均从 `IMAGE_DIR` 按文件名读图。

## 9. 价值评分：JevScorer

- 请求构造：批次消息序列化为 `{author, sent_at, original_text, original_images}` 数组；全文上限 **48000 rune**（超限从头截断），图片上限 `MAX_SCORING_IMAGES`；图片以 multipart `file*` part 上传，`assets` 中以 `image-xxx` ref 关联。
- 评分标准：6 档中文价值等级（`valueLevels`，从「无关闲聊」到「可核对的高价值交易信息」），输出 0–100 分、置信度与各档概率。
- HTTP 超时 45s；任何错误由 Coordinator 走重试。
- **改评分提示词/档位/上下文字段必须同步本节**，并评估对阈值 80 语义的影响。

## 10. 邮件：ResendMailer

- `SendConfirmation`：订阅确认邮件（含 48h 有效确认链接）。
- `SendAlert`：告警邮件，`RenderAlertEmail` 渲染固定模板——作者、时间（Asia/Shanghai `2006-01-02 15:04:05`）、原文（`pre-wrap`）、原图 CID 内嵌（`post-<batchID>-<msgIdx>-<imgIdx>`）；附纯文本备选与 `List-Unsubscribe` 头。
- 幂等：以 `(batchID, subscriberID)` 派生幂等键调用 Resend；返回 Resend 消息 ID 落库。
- 模板改动只动 HTML 结构/文案，**不得引入外链追踪或摘要生成**（产品定位是转发原始发言）；改动需同步本节。

## 11. 令牌：Tokens

- 格式：`base64url(json) + "." + base64url(HMAC-SHA256)`。
- 两种 action：`confirm`（含 requestID、频道列表，48h 过期）与 `unsubscribe`（不过期）。
- 校验逐项失败原因：签名不符 / 内容无效 / action 或 ID 不符 / 过期。
- 密钥 < 32 字符时 `NewTokens` 拒绝构造。
- 确认链接携带 `requestID`：同一邮箱重新订阅会使旧确认链接失效（`ConfirmSubscription` 比对 `pending_request_id`）。

## 12. HTTP 接口

统一安全头：`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`、CSP `default-src 'self'`。

跨域（CORS）：仅当请求 `Origin` 严格等于 `PAGES_URL` 的 `scheme://host` 时返回 `Access-Control-Allow-Origin`（带 `Vary: Origin`）；匹配的 `OPTIONS` 预检在进入路由前直接应答 204（`Allow-Methods: POST, OPTIONS`、`Allow-Headers: Content-Type`、`Max-Age: 86400`）。其他来源一律不放行。**禁止改成 `*`。**

| 路由 | 说明 |
|---|---|
| `GET /healthz` | 健康检查，返回 `{"ok":true}` |
| `GET /` | 配置了 `PAGES_URL` 时 302 到该地址；否则 404 JSON |
| `POST /api/subscriptions` | 提交订阅：JSON `{email, channels[]}`；IP 限流 **10 分钟 5 次**（超限 429）；成功返回 202 并发送确认邮件；Resend 故障返回 502 |
| `GET /confirm/{token}` | 确认订阅（HTML 结果页）；链接失效/被新请求替代返回 400 |
| `GET /unsubscribe/{token}` | 退订确认页（表单） |
| `POST /unsubscribe/{token}` | 执行退订（全部频道） |

请求体限制 20 KB，邮箱规范化（小写、去空格、`mail.ParseAddress`），频道去重且至少一个。

服务端**没有订阅落地页**：落地页在 GitHub Pages 前端仓库维护；确认/退订结果页（`resultPage` 内联 HTML）必须保留，因为邮件链接落点在本服务，其上的「重新订阅」链接经 `pagesLink()` 指向 `PAGES_URL`。

## 13. 清理：Cleaner

每 `CLEANUP_INTERVAL`（默认 1h）执行一次 `CleanupOnce`：

1. `DeleteOldRecords`：删除 `DB_RETENTION`（默认 30 天）前的批次级联记录；48h 前的 pending 订阅请求。
2. `ExpiredUnreferencedImages` + `DeleteImage`：删除超过 `IMAGE_RETENTION`（默认 7 天）且不被消息引用的图片文件与记录。
3. 孤儿文件兜底：目录中超过保留期、数据库无记录的 `.jpg` 直接删除。

## 14. 测试与构建

```bash
cd server
go test ./...        # 全部单元测试
make release         # 产出 release/terry-alerts-linux-{amd64,arm64}.tar.gz
```

- 测试约定：`test_helpers_test.go` 提供共享夹具；Store 测试用注入时钟；HTTP/Coordinator 测试用内存库（`:memory:`）。
- 新增行为必须带测试；改动现有行为必须先改测试再改实现。

## 15. 变更同步清单（提交前自查）

修改代码时，逐项核对本文档需要同步的位置：

- [ ] 增删配置项 / 改默认值 → §4 表格 + `.env.example`
- [ ] 改批次状态、阈值语义、重试策略 → §5 状态机图与不变量
- [ ] 改表结构 / SQL → §6 表清单 + 迁移说明
- [ ] 改 WS 协议、订阅消息、消息解析 → §7
- [ ] 改图片限制、格式、SSRF 策略 → §8 与 §4 对应项
- [ ] 改评分提示词、档位、上下文 → §9
- [ ] 改邮件模板、头、幂等策略 → §10
- [ ] 改 token 格式、有效期、action → §11
- [ ] 增删路由、改限流/安全头 → §12
- [ ] 改清理周期、保留策略 → §13 与 §4 对应项
- [ ] 文件拆分/合并/改名 → §3 职责表
