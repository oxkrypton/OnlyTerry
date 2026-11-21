# Terry Alerts 邮件订阅服务 · 开发文档

> **⚠️ 强制规则：修改代码必须同步修改本文档。**
>
> 本文档是 `server/internal/app` 模块的唯一权威开发文档。任何改变模块行为的代码修改（新增/修改/删除配置项、数据库表结构、HTTP 接口、状态机流转、外部 API 调用、文件职责划分、默认值、阈值等），**都必须在同一个提交中更新本文档的对应章节**。评审时若发现代码与文档不一致，一律以文档为准要求回退或补文档。
>
> 文档最后更新：2026-11-21（切换为内置 Telegram MTProto 采集器；接入 updates.Manager 实现断线补包防消息空洞）。

---

## 1. 模块概述

Terry Alerts 是一个「博主原始发言邮件订阅服务」。核心流水线：

```
Telegram 群聊 ──▶ 内置 MTProto 采集器(gotd/td) ──▶ 过滤/规范化 ──▶ 按频道聚合(静默窗口) ──▶ JEV 价值打分
                                                                                              │ ≥ 75 分（默认）
                                                                                              ▼
                                                                             SQLite 批次/投递记录 ◀── Resend 邮件群发(原文+原图)
```

- 监听目标博主在 **美股（us_stocks）** 和 **加密货币（crypto）** 两个频道的实时发言。
- 每个频道独立聚合一个静默窗口（默认 120 秒）内的发言为一个批次（Batch）；批次另有最长等待 15 分钟、最多 20 条消息的硬上限，任一触发即强制封箱，防止高频发言导致窗口被无限顺延。
- 用 TypeSafe JEV 决策模型对批次打 0–100 分，**大于等于 `SCORE_THRESHOLD`（默认 75）** 才投递。
- 不做摘要：邮件只包含博主原文、发送时间（Asia/Shanghai）和原图（CID 内嵌）。
- 订阅采用邮箱确认制（double opt-in），每封邮件带签名退订链接和 `List-Unsubscribe` 头。
- 订阅前端托管在 GitHub Pages：服务端不内嵌订阅页，只对 `PAGES_URL` 来源放行跨域、把根路径重定向过去；保留的页面仅有确认/退订结果页（内联 HTML，是状态变更的落点，不可删除）。
- 面向 2C2G 低配置云服务器：纯 Go SQLite 驱动（`modernc.org/sqlite`），`CGO_ENABLED=0` 可交叉编译出无动态依赖的 Linux 二进制。

## 2. 运行拓扑与入口

入口：`server/cmd/server/main.go`，装配顺序：

1. `LoadDotEnv(".env")` → `LoadConfig()` → `config.Validate()`，校验失败直接退出。
2. 依次构造：`Store`（SQLite）→ `Tokens`（HMAC 签名）→ `ResendMailer` → `JevScorer` → `Coordinator` → `ImageStore` → `TgSource`。
3. 消息回调流水线（与上游无关）：`store.HasMessage` 去重 → `images.Prepare` 下载转存图片 → 空消息丢弃 → `coordinator.Accept` 入库并入窗。
4. `coordinator.Recover()` 从数据库恢复未完成批次（重启不丢窗口）。
5. 后台 goroutine：`Cleaner.Run`（定时清理）、`TgSource.Run`（内置 MTProto 监听，`TG_ENABLED` 可关）。
6. HTTP 服务监听 `HOST:PORT`；收到 `SIGINT/SIGTERM` 后停止 Coordinator、10 秒内优雅关闭。

部署：`server/deploy/terry-alerts.service`（systemd unit）。构建与发布见 `server/Makefile`（`make release` 产出 Linux amd64/arm64 tar 包）。

## 3. 文件职责一览

| 文件 | 类型/导出物 | 职责 |
|---|---|---|
| `types.go` | `Channel`、`IncomingMessage`、`StoredMessage`、`Batch`、`BatchWithMessages`、`Subscriber`、`ScoreResult`、`Scorer` 接口、`Mailer` 接口、`AlertMail`、`RandomID()` | 全部领域模型与状态枚举；`Scorer`/`Mailer` 是可替换的依赖接口 |
| `config.go` | `Config`、`LoadConfig()`、`Validate()`、`LoadDotEnv()` | 环境变量解析（含默认值）、启动校验、`.env` 加载（不覆盖已有环境变量） |
| `coordinator.go` | `Coordinator` | 核心调度器：窗口定时、打分、重试、投递编排（见 §5） |
| `store.go` | `Store` | SQLite 持久化与全部 SQL（见 §6） |
| `tg_source.go` | `TgSource`、`tgProcessor`、`channelOf`、`authorOf`、`largestPhotoSize` | 内置 Telegram MTProto 采集器：StringSession 登录、updates.Manager 断线补包、群聊/作者过滤、消息规范化、MTProto 图片下载（见 §7） |
| `images.go` | `ImageStore` | 图片下载、校验、转码、落盘，SSRF 防护（见 §8） |
| `jev.go` | `JevScorer` | 调用 TypeSafe JEV 模型打分（见 §9） |
| `mail.go` | `ResendMailer`、`RenderAlertEmail()` | 确认邮件与告警邮件渲染、Resend API 发送（见 §10） |
| `tokens.go` | `Tokens` | HMAC-SHA256 签名 token：确认链接（48h 有效）、退订链接（见 §11） |
| `http.go` | `HTTPHandler` | HTTP 路由、限流、Pages 跨域放行与预检、确认/退订结果页、安全头（见 §12） |
| `cleanup.go` | `Cleaner` | 定时清理过期数据库记录与图片（见 §13） |
| `*_test.go` | — | 单元测试（store/coordinator/http/images/mail/tg_source/cleanup） |

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
| `SCORE_THRESHOLD` | `75` | 投递阈值，批次得分须**大于等于**该值（0–100） |
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
| `TG_ENABLED` | `true` | 是否启用内置 Telegram MTProto 采集器 |
| `TG_API_ID` | （启用时必填） | my.telegram.org 应用的 API ID |
| `TG_API_HASH` | （启用时必填） | my.telegram.org 应用的 API Hash |
| `TG_SESSION` | （启用时必填） | Telethon StringSession（`scripts/gen_session.py` 生成），建议为邮件服务单独生成 |
| `TG_SESSION_FILE` | `data/service/tg.session` | gotd 刷新后 session 的落盘路径（登录凭证），纳入备份 |
| `TG_STATE_FILE` | `data/service/tg.updates.json` | 断线补包断点文件（每群 pts 进度），重启后补回空洞消息，纳入备份；禁止多实例共享 |
| `PROXY` | 空（可选） | TG 采集与 JEV 评分共用的 SOCKS5 出口，仅支持 `socks5://` / `socks5h://`；gotd 与 JEV 的 HTTP 请求都走这里，留空则都直连；TypeSafe 返回 451 时需指向可用的海外出口 |
| `TG_CHANNEL_MAP_JSON` | 两频道同名映射 | 群聊 → 内部 `Channel` 的映射，未映射的消息丢弃。**固定格式 `群号_话题ID`**（如 `3989060419_1`，群号是 gotd 的裸 `PeerChannel.ChannelID`，即 Web 链接 `web.telegram.org/a/#-1003989060419_1` 完整 chat_id 去掉 `-100` 前缀；下划线后为话题 ID），不同话题可映射到不同频道；其他写法一律不识别。代码不做任何前缀/格式转换。见 `channelOf` / `topicIDOf` |
| `TARGET_AUTHOR_IDS` | 空 | 逗号分隔的目标博主数字 user ID；非空时只处理这些作者 |

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
- **大于等于阈值才投递**：`Score < ScoreThreshold` 即跳过（`skipped`）；代码默认阈值 75，部署 `.env` 当前覆盖为 70。灰区批次直接从数据库查 `batches.score` 与 `messages.text`，不再单独打日志。
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

## 7. 数据源接入：TgSource（内置 MTProto）

方案 C：把外部 WebSocket 数据源改为服务内置的 Telegram MTProto 采集器（gotd/td）。`main.go` 中 `TgSource` 与旧 `GroupSocket` 使用**同一个消息回调**，聚合之后的流水线完全不变。

- **登录**：`session.TelethonSession(TG_SESSION)` 解码 Telethon StringSession → 存入 `session.StorageMemory`（未配置文件）或 `session.FileStorage`（配置 `TG_SESSION_FILE`）。文件不存在时由字符串初始化，之后 gotd 自行刷新文件内容。`client.Run` 内用 `Auth().Status` 校验已授权，未授权直接报错提示重新生成 session（无交互环境不做验证码登录）。
- **断线补包（防消息空洞）**：update 不直接交给 `tg.NewUpdateDispatcher`，而是先经 `updates.Manager`（gotd 内建 gap 管理器）：
  - `updates.NewFileStorage(TG_STATE_FILE)` 持久化每个群的 `pts` 断点，**Storage 与 AccessHasher 必须用同一实例**，否则重启后 channel 的 pts 无法恢复；父目录由 `stateStorage()` 提前创建（0700）。
  - 恢复的 update 先按 pts 检测空洞，命中则调 `updates.getDifference` / `getChannelDifference` 补齐，再把**实时 + 补包**消息统一喂给 dispatcher → `onMessage`。`Forget=false`（默认）表示从本地断点续传，而非用服务端状态覆盖。
  - `client.Run` 内授权成功后调用 `gaps.Run(ctx, client.API(), userID, AuthOptions{IsBot:false})`；`IsBot=false` 因为 userbot 依赖频道 difference 能力。
  - `OnTooLong` / `OnChannelTooLong` 回调仅打告警日志（空洞过大时服务端会截断，无法自动补齐）。补包消息走同一 handler，重复投递由 `store.HasMessage` 去重挡掉。
  - **部署约束**：`FileStorage` 禁止多进程共享同一文件，本服务按单实例部署（幂等键同样假设单实例）。`TG_STATE_FILE` 与 `TG_SESSION_FILE` 职责不同：前者存消息进度，后者存登录凭证，均需纳入备份。
- **重连**：`TgSource.Run` 外层退避重启（1s 起步 ×2，30s 封顶）；gotd 客户端内部另有自动重连、心跳、DC 迁移。断线期间的消息由上述 gap 管理器补齐。
- **消息过滤**（`tgProcessor.onMessage`）：
  - 只处理 `*tg.Message` 且 `Out == false`（忽略服务消息与自己发的消息）；
  - `channelOf(peer, TGChannelMap)` 从 `PeerChannel.ChannelID` / `PeerChat.ChatID` 取纯数字 ID 映射频道，未映射丢弃；
  - `authorOf` 从 `FromID`（`PeerUser`/`PeerChannel`）取作者 ID 与名字，`TARGET_AUTHOR_IDS` 非空时仅放行目标作者；
  - 无文本无图片的消息丢弃；`IncomingMessage.ID` 为 `<群ID>:<消息ID>`。
- **图片**（`imagesOf`）：处理 `MessageMediaPhoto` 与 `image/*` 的 `MessageMediaDocument`，`largestPhotoSize` 选最大尺寸，`client.Download(...).Stream` 下载到受限 buffer（`limitedWriter` 按 `MAX_IMAGE_BYTES` 截断），转 base64 交给现有 `ImageStore.Prepare`。下载失败只跳过该图，不阻塞消息。
- 改动群聊/作者过滤或图片提取时：改 `tg_source.go`，补 `tg_source_test.go`（过滤与尺寸选择均为纯函数，不依赖真实连接），并在此节登记。

## 8. 图片处理：ImageStore

- `Prepare(sources, limit)`：逐张下载（**单并发信号量** `sem`，保护低带宽服务器），失败的图片跳过不阻塞消息。
- 安全约束（`checkURL`/`download`）：仅 http/https、禁止 URL 携带用户信息、**不跟随重定向**、默认拒绝回环/私网/链路本地/组播/CGNAT 地址（SSRF 防护，`ALLOW_PRIVATE_IMAGE_URLS` 可放开，仅用于测试）。
- `convert`：解码（jpeg/png/gif/webp）→ 像素上限校验 → 统一转 JPEG 落盘，文件名为内容 SHA-256（天然去重）。
- 送评分与发邮件均从 `IMAGE_DIR` 按文件名读图。

## 9. 价值评分：JevScorer

- 请求构造：批次消息序列化为 `{author, sent_at, original_text, original_images}` 数组；`original_images` 无图时通过 `omitempty` 省略；全文上限 **48000 rune**（超限从头截断），图片上限 `MAX_SCORING_IMAGES`；图片以 multipart `file*` part 上传，`assets` 中以 `image-xxx` ref 关联。无图片时**必须完全省略 `assets` 字段**：TypeSafe 规定 `assets` 至少 1 项，传 `{}` 会返回 `400 Invalid request`。
- 评分标准：6 档中文价值等级（`subscriptionCriteria`，从「无关闲聊」到「带核心依据的完整操作信号」）；问题文案为 `subscriptionQuestion`，明确「极简表达不扣分」。第 4 档涵盖「买入TSLA」这类极简动作，第 5 档要求附带核心依据或可执行细节。输出 0–100 分、置信度与各档概率。
- HTTP 超时 45s；任何错误由 Coordinator 走重试。
- HTTP 出口与 TG 采集共用 `PROXY`：配置后通过 `x/net/proxy` 的 SOCKS5 拨号器连接，`socks5h` 由代理端解析域名；同时清空 `Transport.Proxy`，避免再叠加进程的 `HTTP_PROXY` 环境变量。未配置则用默认 Transport 直连。TypeSafe 对被判定为不支持的地区返回 `HTTP 451`，此时需检查该出口的归属地。
- **改评分提示词/档位/上下文字段必须同步本节**，并评估对阈值 75 语义的影响。

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
- [ ] 改 TG 登录、群聊/作者过滤、消息规范化、图片提取 → §7 与 §4 对应项
- [ ] 改图片限制、格式、SSRF 策略 → §8 与 §4 对应项
- [ ] 改评分提示词、档位、上下文 → §9
- [ ] 改邮件模板、头、幂等策略 → §10
- [ ] 改 token 格式、有效期、action → §11
- [ ] 增删路由、改限流/安全头 → §12
- [ ] 改清理周期、保留策略 → §13 与 §4 对应项
- [ ] 文件拆分/合并/改名 → §3 职责表
