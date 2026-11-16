package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mailBatch() *BatchWithMessages {
	score := 96.0
	return &BatchWithMessages{Batch: Batch{ID: "batch-1", Channel: ChannelCrypto, Status: BatchApproved, Score: &score}, Messages: []StoredMessage{{ID: "1", Channel: ChannelCrypto, AuthorName: "Terry", SentAt: "2026-11-16T12:00:00Z", Text: "买入 BTC <script>alert(1)</script>", Images: []StoredImage{{FileName: "one.jpg", MIMEType: "image/jpeg", SourceURL: "https://cdn.example.com/order.png", SourceMIME: "image/png", Width: 800, Height: 600, Alt: "买入截图"}}}}}
}
func TestFixedMailEscapesOriginalTextAndUsesCID(t *testing.T) {
	content := RenderAlertEmail(mailBatch(), "https://example.com/unsubscribe/signed")
	if !strings.Contains(content.HTML, "买入 BTC &lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(content.HTML, "<script>alert(1)</script>") {
		t.Fatal("原文转义失败")
	}
	if !strings.Contains(content.HTML, `cid:post-batch-1-0-0`) || !strings.Contains(content.HTML, "退订全部频道") {
		t.Fatal("图片或退订链接缺失")
	}
	if content.Subject != "【加密货币】Terry 新发言提醒" {
		t.Fatal("固定主题错误")
	}
}
func TestResendRemoteCIDAttachmentAvoidsServerUpload(t *testing.T) {
	var payload map[string]any
	var idempotency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idempotency = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"email-1"}`))
	}))
	defer server.Close()
	config := testConfig(t)
	config.ResendBaseURL = server.URL
	mailer := NewResendMailer(config)
	id, err := mailer.SendAlert(AlertMail{Batch: mailBatch(), Recipient: Subscriber{ID: 7, Email: "user@example.com"}, UnsubscribeURL: "https://example.com/unsubscribe/token"})
	if err != nil || id != "email-1" {
		t.Fatalf("发送失败：%v", err)
	}
	attachments := payload["attachments"].([]any)
	attachment := attachments[0].(map[string]any)
	if attachment["path"] != "https://cdn.example.com/order.png" || attachment["content_id"] != "post-batch-1-0-0" {
		t.Fatalf("远程 CID 配置错误：%+v", attachment)
	}
	if _, exists := attachment["content"]; exists {
		t.Fatal("远程模式仍上传 base64 图片")
	}
	if idempotency != "blogger-alert-batch-1-7" {
		t.Fatal("幂等键错误")
	}
}
func TestJevMultipartScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" {
			t.Error("缺少图片实验头")
		}
		raw := r.FormValue("request")
		if raw == "" {
			t.Error("缺少 request multipart 字段")
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Error(err)
		}
		if len(payload["assets"].(map[string]any)) != 1 {
			t.Error("图片资产未进入请求")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-test","answers":{"subscription_value":{"score":4.1,"confidence":0.9,"probabilities":{"4":1}}}}`))
	}))
	defer server.Close()
	config := testConfig(t)
	config.TypeSafeBaseURL = server.URL
	if err := os.MkdirAll(config.ImageDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.ImageDir, "one.jpg"), []byte("fake-jpeg"), 0640); err != nil {
		t.Fatal(err)
	}
	scorer := NewJevScorer(config)
	result, err := scorer.Score(mailBatch())
	if err != nil {
		t.Fatal(err)
	}
	if result.Score < 81.999 || result.Score > 82.001 || result.Model != "jev-test" {
		t.Fatalf("评分归一化错误：%+v", result)
	}
}
