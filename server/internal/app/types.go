package app

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Channel string

const (
	ChannelUS     Channel = "us_stocks"
	ChannelCrypto Channel = "crypto"
)

var Channels = []Channel{ChannelUS, ChannelCrypto}

func ValidChannel(c Channel) bool { return c == ChannelUS || c == ChannelCrypto }
func ChannelLabel(c Channel) string {
	if c == ChannelCrypto {
		return "加密货币"
	}
	return "美股"
}

type IncomingImage struct {
	URL        string
	DataBase64 string
	MIMEType   string
	Alt        string
}

// ReplyContext 是被回复消息的上下文。MessageID 对应 Telegram 原始消息 ID；
// Quote 是博主回复时选中的片段（可能为空），Text 是被回复消息的全文。
type ReplyContext struct {
	MessageID  string
	Quote      string
	Text       string
	AuthorName string
	SentAt     string
}

type IncomingMessage struct {
	ID         string
	Channel    Channel
	AuthorID   string
	AuthorName string
	SentAt     string
	Text       string
	Images     []IncomingImage
	Reply      *ReplyContext
}

type StoredImage struct {
	FileName   string
	MIMEType   string
	SourceURL  string
	SourceMIME string
	Width      int
	Height     int
	Alt        string
}

type StoredMessage struct {
	ID         string
	Channel    Channel
	AuthorID   string
	AuthorName string
	SentAt     string
	Text       string
	Images     []StoredImage
	Reply      *ReplyContext
}

type BatchStatus string

const (
	BatchCollecting BatchStatus = "collecting"
	BatchScoring    BatchStatus = "scoring"
	BatchRetrying   BatchStatus = "retrying"
	BatchApproved   BatchStatus = "approved"
	BatchSending    BatchStatus = "sending"
	BatchSent       BatchStatus = "sent"
	BatchSkipped    BatchStatus = "skipped"
	BatchFailed     BatchStatus = "failed"
)

type Batch struct {
	ID        string
	Channel   Channel
	Status    BatchStatus
	DueAt     int64
	Score     *float64
	Model     string
	Attempts  int
	Error     string
	CreatedAt int64
	UpdatedAt int64
}

// BatchWindow 控制批次封箱的窗口策略。
// Silence 为静默窗口；MaxWait 为批次自创建起的最长存活时间；MaxMessages 为批次最多容纳的消息数。
// 后两者为硬上限，任一触发即强制封箱，防止高频消息流下静默窗口被无限顺延。
type BatchWindow struct {
	Silence     time.Duration
	MaxWait     time.Duration
	MaxMessages int
}

type BatchWithMessages struct {
	Batch
	Messages []StoredMessage
}

type Subscriber struct {
	ID     int64
	Email  string
	Status string
}

type ScoreResult struct {
	Score         float64
	RawScore      float64
	Model         string
	Confidence    float64
	Probabilities map[string]float64
}

type Scorer interface {
	Score(*BatchWithMessages) (ScoreResult, error)
}

type AlertMail struct {
	Batch          *BatchWithMessages
	Recipient      Subscriber
	UnsubscribeURL string
}

type Mailer interface {
	SendConfirmation(email, confirmationURL string) error
	SendAlert(AlertMail) (string, error)
}

func RandomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
