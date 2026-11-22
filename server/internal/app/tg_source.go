package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
)

// TgSource 是内置的 Telegram MTProto 采集器。
// 它用 Telethon StringSession 登录 userbot，实时监听目标群聊的新消息，
// 过滤出目标博主发言并规范化为 IncomingMessage 后交给 handler。
// 断线重连、心跳、DC 迁移由 gotd/td 客户端内部处理；Run 只在客户端
// 整体退出时做退避重启。
type TgSource struct {
	config  Config
	handler func(IncomingMessage) error
}

func NewTgSource(config Config, handler func(IncomingMessage) error) *TgSource {
	return &TgSource{config: config, handler: handler}
}

func (s *TgSource) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("TG 采集器断开：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (s *TgSource) connect(ctx context.Context) error {
	if s.config.TGAPIID == 0 || s.config.TGAPIHash == "" {
		return fmt.Errorf("TG_API_ID / TG_API_HASH 未配置")
	}
	data, err := session.TelethonSession(s.config.TGSession)
	if err != nil {
		return fmt.Errorf("TG_SESSION 不是有效的 Telethon StringSession：%w", err)
	}

	storage, err := s.sessionStorage(ctx, data)
	if err != nil {
		return err
	}

	// 消息进度断点：记录每个群聊已处理的 pts，断线或重启后由 updates.Manager
	// 通过 getDifference 补回空洞期间的消息。Storage 与 AccessHasher 必须用同一实例，
	// 否则重启后 channel 的 pts 无法恢复（gotd 库约束）。
	if err := s.stateStorage(); err != nil {
		return err
	}
	state := updates.NewFileStorage(s.config.TGStateFile)

	// processor 持有已登录的 client，用于下载图片；在 client.Run 内赋值。
	processor := &tgProcessor{source: s}
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
		processor.onMessage(ctx, e, update.Message)
		return nil
	})
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, update *tg.UpdateNewMessage) error {
		processor.onMessage(ctx, e, update.Message)
		return nil
	})

	// gaps 接管所有 update：先按 pts 检测空洞并补齐，再把实时+补包的消息
	// 统一交给 dispatcher。这样业务 handler 无需感知补包逻辑。
	gaps := updates.New(updates.Config{
		Handler:          dispatcher,
		Storage:          state,
		AccessHasher:     state,
		UserAccessHasher: state,
		OnTooLong: func() {
			log.Printf("TG 公共更新空洞过大，getDifference 无法补齐，可能有消息丢失")
		},
		OnChannelTooLong: func(channelID int64) {
			log.Printf("TG 群 %d 更新空洞过大，getChannelDifference 无法补齐，可能有消息丢失", channelID)
		},
	})

	opts := telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  gaps,
	}
	// 可选代理：gotd 走原生 MTProto TCP，不读取 HTTP_PROXY 等环境变量。
	// 部署环境若需经 SOCKS5 代理才能访问 Telegram，用 PROXY 指定，
	// 例如 socks5://host:port 或 socks5h://host:port（后者走远端 DNS）。
	// 该出口由 TG 采集与 JEV 评分共用，留空则都直连。
	if s.config.Proxy != "" {
		resolver, err := proxyResolver(s.config.Proxy)
		if err != nil {
			return err
		}
		opts.Resolver = resolver
	}
	client := telegram.NewClient(s.config.TGAPIID, s.config.TGAPIHash, opts)
	processor.client = client

	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("检查 TG 登录状态失败：%w", err)
		}
		if !status.Authorized {
			// 无交互环境不做验证码登录，直接给出可操作的提示。
			return fmt.Errorf("TG_SESSION 未授权，请用 scripts/gen_session.py 重新生成并填入 TG_SESSION")
		}
		// 启动 gap 管理器：Forget=false 表示从本地 state 断点续传（而非用服务端状态覆盖）。
		// IsBot=false，因为这是 userbot——频道更新依赖它的 channel difference 能力。
		if err := gaps.Run(ctx, client.API(), status.User.ID, updates.AuthOptions{IsBot: false}); err != nil {
			return fmt.Errorf("TG 更新断点管理退出：%w", err)
		}
		return ctx.Err()
	})
}

// stateStorage 预留：目前 updates.NewFileStorage 会在首次写入时创建文件，
// 但父目录需提前存在。返回错误以便启动时尽早暴露配置问题。
func (s *TgSource) stateStorage() error {
	if s.config.TGStateFile == "" {
		return fmt.Errorf("TG_STATE_FILE 未配置")
	}
	if dir := filepath.Dir(s.config.TGStateFile); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 TG state 目录失败：%w", err)
		}
	}
	return nil
}

// sessionStorage 构造会话存储：未配置 TG_SESSION_FILE 时用内存；
// 配置时首次由 Telethon 字符串初始化文件，之后 gotd 自行刷新文件内容。
func (s *TgSource) sessionStorage(ctx context.Context, data *session.Data) (session.Storage, error) {
	if s.config.TGSessionFile == "" {
		storage := new(session.StorageMemory)
		loader := session.Loader{Storage: storage}
		if err := loader.Save(ctx, data); err != nil {
			return nil, err
		}
		return storage, nil
	}
	if dir := filepath.Dir(s.config.TGSessionFile); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建 session 目录失败：%w", err)
		}
	}
	storage := &session.FileStorage{Path: s.config.TGSessionFile}
	loader := session.Loader{Storage: storage}
	if _, err := loader.Load(ctx); errors.Is(err, session.ErrNotFound) {
		if err := loader.Save(ctx, data); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return storage, nil
}

// tgProcessor 处理单条更新。它持有已登录的 client 以便通过 MTProto 下载图片。
type tgProcessor struct {
	source *TgSource
	client *telegram.Client
}

func (p *tgProcessor) config() Config { return p.source.config }

// onMessage 过滤并规范化一条消息，然后交给现有 handler。
// 单条消息处理失败只记录日志，不影响后续消息。
func (p *tgProcessor) onMessage(ctx context.Context, e tg.Entities, message tg.MessageClass) {
	msg, ok := message.(*tg.Message)
	if !ok || msg.Out {
		return // 忽略服务消息和自己发出的消息
	}
	channel, ok := channelOf(msg.PeerID, msg, p.config().TGChannelMap)
	if !ok {
		return
	}
	authorID, authorName := authorOf(msg, e)
	if authorID == "" || (len(p.config().TargetAuthorIDs) > 0 && !p.config().TargetAuthorIDs[authorID]) {
		return
	}
	text := msg.Message
	images := p.imagesOf(ctx, e, msg)
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		return
	}
	var reply *ReplyContext
	if msg.ReplyTo != nil {
		if replyID, quote := replyMetaOf(msg.ReplyTo); replyID > 0 {
			reply = p.replyOf(ctx, e, msg, replyID, quote)
		}
	}
	incoming := IncomingMessage{
		ID:         channel.id + ":" + strconv.Itoa(msg.ID),
		Channel:    channel.channel,
		AuthorID:   authorID,
		AuthorName: authorName,
		SentAt:     time.Unix(int64(msg.Date), 0).UTC().Format(time.RFC3339),
		Text:       text,
		Images:     images,
		Reply:      reply,
	}
	if err := p.source.handler(incoming); err != nil {
		log.Printf("处理 TG 消息失败：%v", err)
	}
}

// replyMetaOf 提取被回复消息 ID 与博主选中的引用片段。话题内直接发在话题里的
// 消息 reply_to_msg_id 指向话题根消息，不带引用时只是话题归属，不算回复。
func replyMetaOf(header tg.MessageReplyHeaderClass) (int, string) {
	typed, ok := header.(*tg.MessageReplyHeader)
	if !ok || typed == nil {
		return 0, ""
	}
	id, _ := typed.GetReplyToMsgID()
	quote, _ := typed.GetQuoteText()
	topID, _ := typed.GetReplyToTopID()
	if topID != 0 && id == topID && strings.TrimSpace(quote) == "" {
		return 0, ""
	}
	return id, quote
}

// replyOf 取回被回复消息的上下文。取回失败时保留 ID 与引用片段，
// 邮件里仍会显示「回复某人」，不会因为这条例外丢掉整条博主发言。
func (p *tgProcessor) replyOf(ctx context.Context, e tg.Entities, msg *tg.Message, replyID int, quote string) *ReplyContext {
	reply := &ReplyContext{MessageID: strconv.Itoa(replyID), Quote: quote}
	peer, ok := msg.PeerID.(*tg.PeerChannel)
	if !ok {
		return reply
	}
	channel := e.Channels[peer.ChannelID]
	if channel == nil {
		log.Printf("TG 回复上下文缺少频道实体：%d", peer.ChannelID)
		return reply
	}
	result, err := p.client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: replyID}},
	})
	if err != nil {
		log.Printf("TG 回复上下文获取失败：%v", err)
		return reply
	}
	var messages []tg.MessageClass
	switch typed := result.(type) {
	case *tg.MessagesMessages:
		messages = typed.Messages
	case *tg.MessagesMessagesSlice:
		messages = typed.Messages
	case *tg.MessagesChannelMessages:
		messages = typed.Messages
	}
	if len(messages) == 0 {
		return reply
	}
	original, ok := messages[0].(*tg.Message)
	if !ok {
		return reply
	}
	reply.Text = original.Message
	reply.SentAt = time.Unix(int64(original.Date), 0).UTC().Format(time.RFC3339)
	authorID, authorName := authorOf(original, e)
	if authorID == "" || authorName == authorID || !p.config().TargetAuthorIDs[authorID] {
		authorName = "群友"
	}
	reply.AuthorName = authorName
	return reply
}

// channelRule 描述一个目标群聊到内部频道的映射。
type channelRule struct {
	id      string
	channel Channel
}

// topicIDOf 提取消息所属的话题 ID（论坛话题）。Telegram 话题群内，
// 消息通过 reply_to 携带话题信息：优先 reply_to_top_id（话题起始消息），
// 其次 reply_to_msg_id。非话题消息返回空字符串。
// 与 scripts/fetch_tg.py 的 topic_of 保持一致。
func topicIDOf(msg *tg.Message) string {
	if msg == nil || msg.ReplyTo == nil {
		return ""
	}
	header, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok {
		return ""
	}
	if header.ReplyToTopID != 0 {
		return strconv.Itoa(header.ReplyToTopID)
	}
	if header.ReplyToMsgID != 0 {
		return strconv.Itoa(header.ReplyToMsgID)
	}
	return ""
}

// channelOf 用「群号_话题ID」格式的 key 在映射表里查找内部频道。
// gotd update 里的 PeerChannel.ChannelID 就是群号（如 3989060419），话题 ID 从
// reply_to 提取；两者拼成 key 直接查表，不做任何前缀或格式转换。
// 没有话题 ID 或 key 未映射的消息直接丢弃。channelRule.id 用完整 key，
// 保证不同话题的消息产生不同的幂等 ID。
func channelOf(peer tg.PeerClass, msg *tg.Message, channelMap map[string]Channel) (channelRule, bool) {
	peerChannel, ok := peer.(*tg.PeerChannel)
	if !ok {
		return channelRule{}, false
	}
	topic := topicIDOf(msg)
	if topic == "" {
		return channelRule{}, false
	}
	key := strconv.FormatInt(peerChannel.ChannelID, 10) + "_" + topic
	channel, ok := channelMap[key]
	if !ok {
		return channelRule{}, false
	}
	return channelRule{id: key, channel: channel}, true
}

// authorOf 提取作者的数字 ID 与显示名。频道匿名发帖时 FromID 是 channel。
func authorOf(msg *tg.Message, e tg.Entities) (string, string) {
	switch from := msg.FromID.(type) {
	case *tg.PeerUser:
		id := strconv.FormatInt(from.UserID, 10)
		name := id
		if user := e.Users[from.UserID]; user != nil {
			parts := []string{}
			if first, ok := user.GetFirstName(); ok {
				parts = append(parts, first)
			}
			if last, ok := user.GetLastName(); ok {
				parts = append(parts, last)
			}
			if len(parts) > 0 {
				name = strings.Join(parts, " ")
			} else if username, ok := user.GetUsername(); ok {
				name = username
			}
		}
		return id, name
	case *tg.PeerChannel:
		id := strconv.FormatInt(from.ChannelID, 10)
		name := id
		if ch := e.Channels[from.ChannelID]; ch != nil {
			name = ch.GetTitle()
		}
		return id, name
	default:
		return "", ""
	}
}

// imagesOf 通过 MTProto 下载消息里的图片，转成 base64 交给现有 ImageStore。
// 只处理 photo 与 image/* 的 document，最多取 MaxImagesPerMessage 张。
func (p *tgProcessor) imagesOf(ctx context.Context, e tg.Entities, msg *tg.Message) []IncomingImage {
	result := []IncomingImage{}
	limit := p.config().MaxImagesPerMessage
	maxBytes := p.config().MaxImageBytes
	appendPhoto := func(photo tg.PhotoClass) {
		if len(result) >= limit {
			return
		}
		full, ok := photo.AsNotEmpty()
		if !ok {
			return
		}
		sizeType := largestPhotoSize(full.Sizes)
		if sizeType == "" {
			return
		}
		location := full.AsInputPhotoFileLocation(sizeType)
		data, err := p.download(ctx, location, maxBytes)
		if err != nil {
			log.Printf("TG 图片下载失败：%v", err)
			return
		}
		result = append(result, IncomingImage{
			DataBase64: base64.StdEncoding.EncodeToString(data),
			MIMEType:   "image/jpeg",
			Alt:        "博主原始图片",
		})
	}
	switch media := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		appendPhoto(media.Photo)
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if ok && strings.HasPrefix(doc.MimeType, "image/") && len(result) < limit {
			data, err := p.download(ctx, &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
			}, maxBytes)
			if err != nil {
				log.Printf("TG 文档图片下载失败：%v", err)
			} else {
				result = append(result, IncomingImage{DataBase64: base64.StdEncoding.EncodeToString(data), MIMEType: doc.MimeType, Alt: "博主原始图片"})
			}
		}
	}
	return result
}

// download 通过已登录客户端下载文件到内存，限制最大字节数。
func (p *tgProcessor) download(ctx context.Context, location tg.InputFileLocationClass, maxBytes int64) ([]byte, error) {
	var buffer bytes.Buffer
	_, err := p.client.Download(location).WithSize(maxBytes).Stream(ctx, &limitedWriter{writer: &buffer, max: maxBytes})
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// limitedWriter 限制写入字节数，超出即报错，避免大图占用过多内存。
type limitedWriter struct {
	writer *bytes.Buffer
	max    int64
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if int64(w.writer.Len())+int64(len(data)) > w.max {
		return 0, fmt.Errorf("图片超过大小限制")
	}
	return w.writer.Write(data)
}

// largestPhotoSize 选出最大的图片尺寸类型（Telegram 的 Sizes 通常按从小到大排列）。
func largestPhotoSize(sizes []tg.PhotoSizeClass) string {
	best := ""
	bestArea := -1
	for _, item := range sizes {
		if notEmpty, ok := item.AsNotEmpty(); ok {
			area := photoSizeArea(item)
			if area >= bestArea {
				bestArea, best = area, notEmpty.GetType()
			}
		}
	}
	return best
}

func photoSizeArea(size tg.PhotoSizeClass) int {
	switch typed := size.(type) {
	case *tg.PhotoSize:
		return typed.W * typed.H
	case *tg.PhotoSizeProgressive:
		return typed.W * typed.H
	default:
		return 0
	}
}

func authUserName(user *tg.User) string {
	if user == nil {
		return "unknown"
	}
	if first, ok := user.GetFirstName(); ok {
		return first
	}
	if username, ok := user.GetUsername(); ok {
		return username
	}
	return "unknown"
}

// proxyResolver 把 PROXY（socks5://host:port 或 socks5h://host:port）转成 gotd resolver。
// socks5h 走远端 DNS 解析，适合国内网络环境。HTTP_PROXY 等环境变量对 gotd 无效，
// 必须显式配置这里。
func proxyResolver(raw string) (dcs.Resolver, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("PROXY 格式无效：%w", err)
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("PROXY 仅支持 socks5:// 或 socks5h://，当前为 %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("PROXY 缺少主机地址")
	}
	// FromURL 只认 socks5；socks5h 语义相同（远端解析），统一当 socks5 处理。
	if u.Scheme == "socks5h" {
		u.Scheme = "socks5"
	}
	dialer, err := proxy.FromURL(u, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("创建 TG 代理失败：%w", err)
	}
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("TG 代理不支持 context dial")
	}
	return dcs.Plain(dcs.PlainOptions{Dial: ctxDialer.DialContext}), nil
}
