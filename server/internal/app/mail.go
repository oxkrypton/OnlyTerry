package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ResendMailer struct {
	config Config
	client *http.Client
}

func NewResendMailer(config Config) *ResendMailer {
	return &ResendMailer{config: config, client: &http.Client{Timeout: 45 * time.Second}}
}
func imageCID(batchID string, messageIndex, imageIndex int) string {
	return fmt.Sprintf("post-%s-%d-%d", batchID, messageIndex, imageIndex)
}
func displayTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*3600)
	}
	return parsed.In(location).Format("2006-01-02 15:04:05")
}

type EmailContent struct{ Subject, HTML, Text string }

func RenderAlertEmail(batch *BatchWithMessages, unsubscribeURL string) EmailContent {
	author := "博主"
	if len(batch.Messages) > 0 {
		author = batch.Messages[0].AuthorName
	}
	var sections, plain strings.Builder
	for messageIndex, message := range batch.Messages {
		sections.WriteString(`<section style="padding:20px 0;border-bottom:1px solid #e5e7eb">`)
		sections.WriteString(`<div style="color:#6b7280;font-size:13px;margin-bottom:10px">` + html.EscapeString(message.AuthorName+" · "+displayTime(message.SentAt)) + `</div>`)
		if message.Text != "" {
			sections.WriteString(`<div style="white-space:pre-wrap;overflow-wrap:anywhere;font-size:16px;line-height:1.75;color:#111827">` + html.EscapeString(message.Text) + `</div>`)
		}
		plain.WriteString(message.AuthorName + " · " + displayTime(message.SentAt) + "\n" + message.Text + "\n")
		for imageIndex, stored := range message.Images {
			sections.WriteString(fmt.Sprintf(`<div style="margin:12px 0"><img src="cid:%s" alt="%s" width="%d" height="%d" style="display:block;max-width:100%%;height:auto;border-radius:6px;border:1px solid #e5e7eb"></div>`, imageCID(batch.ID, messageIndex, imageIndex), html.EscapeString(stored.Alt), stored.Width, stored.Height))
			plain.WriteString(fmt.Sprintf("[原图 %d，见邮件内嵌图片]\n", imageIndex+1))
		}
		sections.WriteString(`</section>`)
		plain.WriteString("\n────────────\n\n")
	}
	subject := "【" + ChannelLabel(batch.Channel) + "】" + author + " 新发言提醒"
	body := `<!doctype html><html lang="zh-CN"><body style="margin:0;background:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif"><div style="max-width:680px;margin:0 auto;padding:28px 16px"><main style="background:#fff;border-radius:12px;padding:8px 28px"><header style="padding:20px 0;border-bottom:2px solid #111827"><strong style="font-size:20px">` + html.EscapeString(author+" · "+ChannelLabel(batch.Channel)) + `</strong><div style="font-size:13px;color:#6b7280;margin-top:6px">原始发言</div></header>` + sections.String() + `</main><footer style="text-align:center;color:#6b7280;font-size:12px;line-height:1.7;padding:20px">本邮件仅转发博主原始发言，不构成投资建议。<br><a href="` + html.EscapeString(unsubscribeURL) + `" style="color:#4b5563">退订全部频道</a></footer></div></body></html>`
	return EmailContent{Subject: subject, HTML: body, Text: author + " · " + ChannelLabel(batch.Channel) + "\n\n" + plain.String() + "\n本邮件仅转发博主原始发言，不构成投资建议。\n退订全部频道：" + unsubscribeURL}
}

func (m *ResendMailer) send(payload map[string]any, idempotencyKey string) (string, error) {
	data, _ := json.Marshal(payload)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(m.config.ResendBaseURL, "/")+"/emails", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+m.config.ResendAPIKey)
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := m.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("Resend HTTP %d：%s", response.StatusCode, string(body))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", fmt.Errorf("Resend 未返回邮件 ID")
	}
	return result.ID, nil
}
func (m *ResendMailer) SendConfirmation(email, confirmationURL string) error {
	_, err := m.send(map[string]any{"from": m.config.EmailFrom, "to": email, "subject": "确认你的博主发言邮件订阅", "html": `<h2>确认邮件订阅</h2><p>请在 48 小时内打开下面的链接确认订阅：</p><p><a href="` + html.EscapeString(confirmationURL) + `">确认订阅</a></p><p>如果这不是你的操作，请忽略本邮件。</p>`, "text": "请打开以下链接确认订阅（48 小时内有效）：\n" + confirmationURL + "\n\n如果这不是你的操作，请忽略本邮件。"}, "")
	return err
}
func (m *ResendMailer) SendAlert(mail AlertMail) (string, error) {
	content := RenderAlertEmail(mail.Batch, mail.UnsubscribeURL)
	attachments := []map[string]any{}
	for messageIndex, message := range mail.Batch.Messages {
		for imageIndex, stored := range message.Images {
			attachment := map[string]any{"filename": fmt.Sprintf("original-%d-%d.jpg", messageIndex+1, imageIndex+1), "content_id": imageCID(mail.Batch.ID, messageIndex, imageIndex)}
			if m.config.ResendRemoteImages && stored.SourceURL != "" {
				attachment["path"] = stored.SourceURL
				extension := ".jpg"
				switch stored.SourceMIME {
				case "image/png":
					extension = ".png"
				case "image/gif":
					extension = ".gif"
				case "image/webp":
					extension = ".webp"
				}
				attachment["filename"] = fmt.Sprintf("original-%d-%d%s", messageIndex+1, imageIndex+1, extension)
				if stored.SourceMIME != "" {
					attachment["content_type"] = stored.SourceMIME
				}
			} else {
				data, err := os.ReadFile(filepath.Join(m.config.ImageDir, stored.FileName))
				if err != nil {
					return "", err
				}
				attachment["content"] = base64.StdEncoding.EncodeToString(data)
				attachment["content_type"] = "image/jpeg"
			}
			attachments = append(attachments, attachment)
		}
	}
	payload := map[string]any{"from": m.config.EmailFrom, "to": mail.Recipient.Email, "subject": content.Subject, "html": content.HTML, "text": content.Text,
		"headers": map[string]string{"List-Unsubscribe": "<" + mail.UnsubscribeURL + ">", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"},
		"tags":    []map[string]string{{"name": "kind", "value": "blogger_alert"}, {"name": "channel", "value": string(mail.Batch.Channel)}}}
	if len(attachments) > 0 {
		payload["attachments"] = attachments
	}
	return m.send(payload, "blogger-alert-"+mail.Batch.ID+fmt.Sprintf("-%d", mail.Recipient.ID))
}
