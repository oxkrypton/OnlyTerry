package app

import (
	"path/filepath"
	"sync"
	"testing"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	directory := t.TempDir()
	return Config{PublicURL: "http://127.0.0.1:3000", PagesURL: "https://pages.example.com", DatabasePath: filepath.Join(directory, "service.db"), ImageDir: filepath.Join(directory, "images"),
		TokenSecret: "test-secret-test-secret-test-secret-32", TypeSafeAPIKey: "test", ResendAPIKey: "test", TypeSafeBaseURL: "http://127.0.0.1", ResendBaseURL: "http://127.0.0.1",
		EmailFrom: "Test <test@example.com>", Window: 120000000000, ScoreThreshold: 80, Retry: 60000000000, MaxBatchAttempts: 8,
		MaxImageBytes: 5 * 1024 * 1024, MaxImagePixels: 25000000, MaxImagesPerMessage: 6, MaxScoringImages: 6,
		ResendRemoteImages: true, CleanupInterval: 3600000000000, ImageRetention: 168 * 36000000000, DatabaseRetention: 720 * 36000000000,
		WSChannelMap: map[string]Channel{"room-us": ChannelUS, "room-crypto": ChannelCrypto}, TargetAuthorIDs: map[string]bool{"blogger": true}}
}

type fakeScorer struct {
	value float64
	err   error
	calls int
}

func (s *fakeScorer) Score(*BatchWithMessages) (ScoreResult, error) {
	s.calls++
	if s.err != nil {
		return ScoreResult{}, s.err
	}
	return ScoreResult{Score: s.value, RawScore: s.value / 20, Model: "jev-test", Confidence: .9, Probabilities: map[string]float64{"4": 1}}, nil
}

type fakeMailer struct {
	mu               sync.Mutex
	confirmations    []string
	confirmationURLs []string
	alerts           []AlertMail
	fail             bool
}

func (m *fakeMailer) SendConfirmation(email, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirmations = append(m.confirmations, email)
	m.confirmationURLs = append(m.confirmationURLs, url)
	return nil
}
func (m *fakeMailer) SendAlert(mail AlertMail) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return "", &testError{}
	}
	m.alerts = append(m.alerts, mail)
	return "resend-id", nil
}

type testError struct{}

func (*testError) Error() string { return "mail unavailable" }

func subscribeForTest(t *testing.T, store *Store, email string, channels []Channel) int64 {
	t.Helper()
	sub, request, err := store.CreateSubscriptionRequest(email, channels)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.ConfirmSubscription(sub.ID, request, channels)
	if err != nil || !ok {
		t.Fatalf("确认订阅失败：%v", err)
	}
	return sub.ID
}
func testMessage() StoredMessage {
	return StoredMessage{ID: "m1", Channel: ChannelUS, AuthorID: "blogger", AuthorName: "Terry", SentAt: "2026-11-16T12:00:00Z", Text: "NVDA 100 买入，止损 95"}
}
