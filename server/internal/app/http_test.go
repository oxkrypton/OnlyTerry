package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPSubscriptionConfirmationAndOneClickUnsubscribe(t *testing.T) {
	config := testConfig(t)
	store, _ := NewStore(config.DatabasePath)
	defer store.Close()
	tokens, _ := NewTokens(config.TokenSecret)
	mailer := &fakeMailer{}
	server := httptest.NewServer(NewHTTPHandler(config, store, tokens, mailer))
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"email": "Reader@Example.com", "channels": Channels})
	response, err := http.Post(server.URL+"/api/subscriptions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 202 || len(mailer.confirmationURLs) != 1 || mailer.confirmations[0] != "reader@example.com" {
		t.Fatalf("订阅请求失败：%d", response.StatusCode)
	}
	confirmation, _ := url.Parse(mailer.confirmationURLs[0])
	response, err = http.Get(server.URL + confirmation.Path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(data), "订阅已确认") {
		t.Fatal("确认页面失败")
	}
	recipients, _ := store.ActiveRecipients(ChannelCrypto)
	if len(recipients) != 1 {
		t.Fatal("确认后未生效")
	}
	unsubscribe := "/unsubscribe/" + tokens.Unsubscribe(recipients[0].ID)
	request, _ := http.NewRequest(http.MethodPost, server.URL+unsubscribe, nil)
	request.Header.Set("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("一键退订失败")
	}
	recipients, _ = store.ActiveRecipients(ChannelUS)
	if len(recipients) != 0 {
		t.Fatal("退订后仍接收邮件")
	}
	page, _ := http.Get(server.URL + "/subscribe")
	pageData, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(pageData), "重要发言") {
		t.Fatal("嵌入订阅页面不可用")
	}
}
