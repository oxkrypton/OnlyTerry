package app

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed public/*
var publicFiles embed.FS

type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func (l *rateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := []time.Time{}
	for _, value := range l.attempts[ip] {
		if value.After(cutoff) {
			recent = append(recent, value)
		}
	}
	if len(recent) >= 5 {
		l.attempts[ip] = recent
		return false
	}
	l.attempts[ip] = append(recent, time.Now())
	return true
}

type HTTPHandler struct {
	config  Config
	store   *Store
	tokens  *Tokens
	mailer  Mailer
	limiter *rateLimiter
	mux     *http.ServeMux
}

func NewHTTPHandler(config Config, store *Store, tokens *Tokens, mailer Mailer) *HTTPHandler {
	h := &HTTPHandler{config: config, store: store, tokens: tokens, mailer: mailer, limiter: &rateLimiter{attempts: map[string][]time.Time{}}, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	h.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/subscribe", http.StatusFound) })
	h.mux.HandleFunc("GET /subscribe", h.subscribePage)
	h.mux.HandleFunc("GET /subscribe.css", h.asset("public/subscribe.css", "text/css; charset=utf-8"))
	h.mux.HandleFunc("GET /subscribe.js", h.asset("public/subscribe.js", "text/javascript; charset=utf-8"))
	h.mux.HandleFunc("POST /api/subscriptions", h.subscribe)
	h.mux.HandleFunc("GET /confirm/{token}", h.confirm)
	h.mux.HandleFunc("GET /unsubscribe/{token}", h.unsubscribePage)
	h.mux.HandleFunc("POST /unsubscribe/{token}", h.unsubscribe)
	return h
}
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	h.mux.ServeHTTP(w, r)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *HTTPHandler) asset(path, contentType string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := publicFiles.ReadFile(path)
		if err != nil {
			http.NotFound(w, nil)
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}
}
func (h *HTTPHandler) subscribePage(w http.ResponseWriter, _ *http.Request) {
	data, _ := publicFiles.ReadFile("public/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func resultPage(title, body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#f4f4f1;color:#18201b;margin:0;display:grid;place-items:center;min-height:100vh}.card{background:white;max-width:540px;margin:24px;padding:36px;border-radius:16px;box-shadow:0 12px 40px #0001}h1{font-size:26px}p{line-height:1.8;color:#5b625d}a,button{display:inline-block;background:#1e5b3c;color:white;padding:12px 20px;border:0;border-radius:8px;text-decoration:none;font-size:16px;cursor:pointer}</style></head><body><main class="card"><h1>` + html.EscapeString(title) + `</h1>` + body + `</main></body></html>`
}
func validEmail(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	return value, err == nil && address.Address == value && len(value) <= 254 && strings.Contains(value, "@")
}
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return r.RemoteAddr
}
func (h *HTTPHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r)) {
		writeJSON(w, 429, map[string]string{"message": "请求过于频繁，请稍后再试。"})
		return
	}
	var input struct {
		Email    string    `json:"email"`
		Channels []Channel `json:"channels"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 20*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		writeJSON(w, 400, map[string]string{"message": "请求格式无效"})
		return
	}
	email, ok := validEmail(input.Email)
	if !ok {
		writeJSON(w, 400, map[string]string{"message": "请填写有效邮箱"})
		return
	}
	unique := map[Channel]bool{}
	channels := []Channel{}
	for _, channel := range input.Channels {
		if !ValidChannel(channel) {
			writeJSON(w, 400, map[string]string{"message": "频道无效"})
			return
		}
		if !unique[channel] {
			unique[channel] = true
			channels = append(channels, channel)
		}
	}
	if len(channels) == 0 {
		writeJSON(w, 400, map[string]string{"message": "请至少选择一个频道"})
		return
	}
	subscriber, requestID, err := h.store.CreateSubscriptionRequest(email, channels)
	if err != nil {
		writeJSON(w, 500, map[string]string{"message": "服务暂时不可用"})
		return
	}
	token := h.tokens.Confirmation(subscriber.ID, requestID, channels)
	confirmationURL := h.config.PublicURL + "/confirm/" + url.PathEscape(token)
	if err = h.mailer.SendConfirmation(email, confirmationURL); err != nil {
		writeJSON(w, 502, map[string]string{"message": "暂时无法发送确认邮件，请稍后重试。"})
		return
	}
	writeJSON(w, 202, map[string]string{"message": "确认邮件已发送，请查收邮箱并完成确认。"})
}
func (h *HTTPHandler) confirm(w http.ResponseWriter, r *http.Request) {
	payload, err := h.tokens.Verify(r.PathValue("token"), "confirm")
	if err == nil {
		var confirmed bool
		confirmed, err = h.store.ConfirmSubscription(payload.SubscriberID, payload.RequestID, payload.Channels)
		if err == nil && !confirmed {
			err = fmt.Errorf("链接已使用或已被更新的订阅请求替代")
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(resultPage("无法确认订阅", `<p>`+html.EscapeString(err.Error())+`</p><a href="/subscribe">重新订阅</a>`)))
		return
	}
	_, _ = w.Write([]byte(resultPage("订阅已确认", `<p>你已确认订阅。高分原始发言会发送到你的邮箱，每封邮件都包含退订链接。</p><a href="/subscribe">管理订阅</a>`)))
}
func (h *HTTPHandler) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	_, err := h.tokens.Verify(r.PathValue("token"), "unsubscribe")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(resultPage("退订链接无效", `<p>请使用最近一封邮件中的退订链接。</p>`)))
		return
	}
	body := `<p>点击下方按钮将退订全部频道，之后不会再收到博主发言提醒。</p><form method="post" action="/unsubscribe/` + url.PathEscape(r.PathValue("token")) + `"><button type="submit">确认退订</button></form>`
	_, _ = w.Write([]byte(resultPage("退订邮件提醒", body)))
}
func (h *HTTPHandler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	payload, err := h.tokens.Verify(r.PathValue("token"), "unsubscribe")
	if err == nil {
		err = h.store.Unsubscribe(payload.SubscriberID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(resultPage("退订链接无效", `<p>请使用最近一封邮件中的退订链接。</p>`)))
		return
	}
	_, _ = w.Write([]byte(resultPage("已退订", `<p>你的全部频道订阅已取消。你可以随时重新订阅。</p><a href="/subscribe">重新订阅</a>`)))
}
