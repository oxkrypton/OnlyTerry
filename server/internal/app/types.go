package app

import (
	"crypto/rand"
	"encoding/hex"
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

type IncomingMessage struct {
	ID         string
	Channel    Channel
	AuthorID   string
	AuthorName string
	SentAt     string
	Text       string
	Images     []IncomingImage
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
