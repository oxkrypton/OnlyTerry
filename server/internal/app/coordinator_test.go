package app

import (
	"errors"
	"testing"
)

func runCoordinatorCase(t *testing.T, score float64) (*Store, *fakeMailer, *fakeScorer, *Coordinator, string) {
	t.Helper()
	config := testConfig(t)
	store, err := NewStore(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	mailer, scorer := &fakeMailer{}, &fakeScorer{value: score}
	tokens, _ := NewTokens(config.TokenSecret)
	coordinator := NewCoordinator(config, store, scorer, mailer, tokens)
	subscribeForTest(t, store, "us@example.com", []Channel{ChannelUS})
	subscribeForTest(t, store, "both@example.com", Channels)
	subscribeForTest(t, store, "crypto@example.com", []Channel{ChannelCrypto})
	batch, err := coordinator.Accept(testMessage())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.db.Exec(`UPDATE batches SET due_at=0 WHERE id=?`, batch.ID)
	coordinator.Process(batch.ID)
	return store, mailer, scorer, coordinator, batch.ID
}
func TestStrictThresholdAndChannelRecipients(t *testing.T) {
	store, mailer, scorer, coordinator, id := runCoordinatorCase(t, 82)
	defer store.Close()
	defer coordinator.Stop()
	if len(mailer.alerts) != 2 || mailer.alerts[0].Recipient.Email != "us@example.com" || mailer.alerts[1].Recipient.Email != "both@example.com" {
		t.Fatalf("收件人错误：%+v", mailer.alerts)
	}
	batch, _ := store.GetBatch(id)
	if batch.Status != BatchSent {
		t.Fatalf("批次状态错误：%s", batch.Status)
	}
	coordinator.Process(id)
	if len(mailer.alerts) != 2 || scorer.calls != 1 {
		t.Fatal("重复处理或重复评分")
	}
}
func TestThresholdBoundarySends(t *testing.T) {
	store, mailer, _, coordinator, id := runCoordinatorCase(t, 75)
	defer store.Close()
	defer coordinator.Stop()
	if len(mailer.alerts) != 2 {
		t.Fatalf("75 分应发送，实际收件人 %d 人", len(mailer.alerts))
	}
	batch, _ := store.GetBatch(id)
	if batch.Status != BatchSent {
		t.Fatalf("批次状态错误：%s", batch.Status)
	}
}
func TestJevFailureRetriesRatherThanSkipping(t *testing.T) {
	config := testConfig(t)
	store, _ := NewStore(config.DatabasePath)
	defer store.Close()
	mailer := &fakeMailer{}
	scorer := &fakeScorer{err: errors.New("API unavailable")}
	tokens, _ := NewTokens(config.TokenSecret)
	coordinator := NewCoordinator(config, store, scorer, mailer, tokens)
	defer coordinator.Stop()
	subscribeForTest(t, store, "us@example.com", []Channel{ChannelUS})
	batch, _ := coordinator.Accept(testMessage())
	_, _ = store.db.Exec(`UPDATE batches SET due_at=0 WHERE id=?`, batch.ID)
	coordinator.Process(batch.ID)
	stored, _ := store.GetBatch(batch.ID)
	if stored.Status != BatchRetrying || stored.Attempts != 1 || len(mailer.alerts) != 0 {
		t.Fatalf("失败重试状态错误：%+v", stored)
	}
}
