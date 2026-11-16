package app

import (
	"strings"
	"testing"
	"time"
)

func TestWindowExtendsAndDeduplicates(t *testing.T) {
	now := time.UnixMilli(1000000)
	store, err := newStore(":memory:", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.AppendMessage(testMessage(), 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.DueAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("首个窗口截止时间错误")
	}
	now = now.Add(time.Minute)
	secondMessage := testMessage()
	secondMessage.ID, secondMessage.Text = "m2", "第二条"
	second, err := store.AppendMessage(secondMessage, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.DueAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("窗口没有正确延长")
	}
	duplicate, err := store.AppendMessage(testMessage(), 2*time.Minute)
	if err != nil || duplicate != nil {
		t.Fatal("重复消息未去重")
	}
	batch, err := store.GetBatchWithMessages(first.ID)
	if err != nil || len(batch.Messages) != 2 {
		t.Fatal("消息未合并到同一批")
	}
}

func TestSubscriptionBothChannelsAndSignedUnsubscribe(t *testing.T) {
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tokens, _ := NewTokens("test-secret-test-secret-test-secret-32")
	sub, request, err := store.CreateSubscriptionRequest("Reader@Example.com", []Channel{ChannelUS, ChannelCrypto})
	if err != nil {
		t.Fatal(err)
	}
	token := tokens.Confirmation(sub.ID, request, []Channel{ChannelUS, ChannelCrypto})
	payload, err := tokens.Verify(token, "confirm")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.ConfirmSubscription(sub.ID, payload.RequestID, payload.Channels)
	if err != nil || !ok {
		t.Fatal("确认失败")
	}
	for _, channel := range Channels {
		recipients, _ := store.ActiveRecipients(channel)
		if len(recipients) != 1 {
			t.Fatal("双频道订阅未生效")
		}
	}
	if _, err = tokens.Verify(token+"x", "confirm"); err == nil {
		t.Fatal("篡改令牌未拒绝")
	}
	unsubscribe := tokens.Unsubscribe(sub.ID)
	parsed, err := tokens.Verify(unsubscribe, "unsubscribe")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Unsubscribe(parsed.SubscriberID); err != nil {
		t.Fatal(err)
	}
	recipients, _ := store.ActiveRecipients(ChannelUS)
	if len(recipients) != 0 {
		t.Fatal("退订未生效")
	}
	if !strings.EqualFold(sub.Email, "Reader@Example.com") {
		t.Fatal("邮箱存储异常")
	}
}
