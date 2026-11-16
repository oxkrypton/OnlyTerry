package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type tokenPayload struct {
	Action       string    `json:"action"`
	SubscriberID int64     `json:"subscriberId"`
	RequestID    string    `json:"requestId,omitempty"`
	Channels     []Channel `json:"channels,omitempty"`
	ExpiresAt    int64     `json:"expiresAt,omitempty"`
}

type Tokens struct{ secret []byte }

func NewTokens(secret string) (*Tokens, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("签名密钥至少需要 32 个字符")
	}
	return &Tokens{secret: []byte(secret)}, nil
}
func (t *Tokens) sign(body string) string {
	mac := hmac.New(sha256.New, t.secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (t *Tokens) create(payload tokenPayload) string {
	data, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(data)
	return body + "." + t.sign(body)
}
func (t *Tokens) Confirmation(id int64, requestID string, channels []Channel) string {
	return t.create(tokenPayload{Action: "confirm", SubscriberID: id, RequestID: requestID, Channels: channels, ExpiresAt: time.Now().Add(48 * time.Hour).UnixMilli()})
}
func (t *Tokens) Unsubscribe(id int64) string {
	return t.create(tokenPayload{Action: "unsubscribe", SubscriberID: id})
}
func (t *Tokens) Verify(token, action string) (tokenPayload, error) {
	var payload tokenPayload
	parts := strings.Split(token, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(t.sign(parts[0])), []byte(parts[1])) {
		return payload, fmt.Errorf("链接签名无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payload, fmt.Errorf("链接内容无效")
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Action != action || payload.SubscriberID <= 0 {
		return payload, fmt.Errorf("链接内容或用途无效")
	}
	if payload.ExpiresAt > 0 && payload.ExpiresAt < time.Now().UnixMilli() {
		return payload, fmt.Errorf("链接已过期，请重新订阅")
	}
	if action == "confirm" && (payload.RequestID == "" || len(payload.Channels) == 0) {
		return payload, fmt.Errorf("确认链接缺少订阅信息")
	}
	return payload, nil
}
