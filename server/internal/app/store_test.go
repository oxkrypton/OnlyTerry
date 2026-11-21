package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testWindow() BatchWindow {
	return BatchWindow{Silence: 2 * time.Minute, MaxWait: 10 * time.Minute, MaxMessages: 20}
}

func TestWindowExtendsAndDeduplicates(t *testing.T) {
	now := time.UnixMilli(1000000)
	store, err := newStore(":memory:", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.AppendMessage(testMessage(), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if first.DueAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("首个窗口截止时间错误")
	}
	now = now.Add(time.Minute)
	secondMessage := testMessage()
	secondMessage.ID, secondMessage.Text = "m2", "第二条"
	second, err := store.AppendMessage(secondMessage, testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.DueAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("窗口没有正确延长")
	}
	duplicate, err := store.AppendMessage(testMessage(), testWindow())
	if err != nil || duplicate != nil {
		t.Fatal("重复消息未去重")
	}
	batch, err := store.GetBatchWithMessages(first.ID)
	if err != nil || len(batch.Messages) != 2 {
		t.Fatal("消息未合并到同一批")
	}
}

func TestMaxWaitCapsDueAtUnderContinuousMessages(t *testing.T) {
	now := time.UnixMilli(1000000)
	start := now
	store, err := newStore(":memory:", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	policy := BatchWindow{Silence: 2 * time.Minute, MaxWait: 10 * time.Minute, MaxMessages: 1000}

	var batch *Batch
	for i := 0; i < 6; i++ {
		batch, err = store.AppendMessage(testMessageWithID(fmt.Sprintf("m%d", i)), policy)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(90 * time.Second)
	}
	if want := start.Add(9*time.Minute + 30*time.Second).UnixMilli(); batch.DueAt != want {
		t.Fatalf("静默窗口顺延异常：due_at=%d, want=%d", batch.DueAt, want)
	}
	// 第 7 条在 t=9min 到达，理论 due=11min，应被 10min 硬上限截断
	batch, err = store.AppendMessage(testMessageWithID("m6"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if want := start.Add(10 * time.Minute).UnixMilli(); batch.DueAt != want {
		t.Fatalf("最长等待时长未截断 due_at：due_at=%d, want=%d", batch.DueAt, want)
	}
}

func TestMaxMessagesSealsBatchAndStartsNewOne(t *testing.T) {
	now := time.UnixMilli(1000000)
	store, err := newStore(":memory:", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	policy := BatchWindow{Silence: 2 * time.Minute, MaxWait: 10 * time.Minute, MaxMessages: 3}

	first, err := store.AppendMessage(testMessageWithID("m1"), policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendMessage(testMessageWithID("m2"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.DueAt != now.Add(2*time.Minute).UnixMilli() {
		t.Fatal("未满上限时窗口应正常顺延")
	}
	third, err := store.AppendMessage(testMessageWithID("m3"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != first.ID || third.DueAt != now.UnixMilli() {
		t.Fatalf("达到条数上限应立即封箱：due_at=%d, want=%d", third.DueAt, now.UnixMilli())
	}
	filled, err := store.GetBatchWithMessages(third.ID)
	if err != nil || len(filled.Messages) != 3 {
		t.Fatalf("封箱批次应含 3 条消息，实际 %d", len(filled.Messages))
	}
	fourth, err := store.AppendMessage(testMessageWithID("m4"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.ID == first.ID {
		t.Fatal("封箱后新消息应另起批次")
	}
	fresh, err := store.GetBatchWithMessages(fourth.ID)
	if err != nil || len(fresh.Messages) != 1 {
		t.Fatalf("新批次应含 1 条消息，实际 %d", len(fresh.Messages))
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
