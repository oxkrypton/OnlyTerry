package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
)

func TestChannelOf(t *testing.T) {
	channelMap := map[string]Channel{"1234567890": ChannelUS, "-1009876543210": ChannelCrypto}
	tests := []struct {
		name   string
		peer   tg.PeerClass
		wantID string
		wantCh Channel
		wantOK bool
	}{
		{"supergroup", &tg.PeerChannel{ChannelID: 1234567890}, "1234567890", ChannelUS, true},
		{"channel with prefix", &tg.PeerChannel{ChannelID: -1009876543210}, "-1009876543210", ChannelCrypto, true},
		{"basic group", &tg.PeerChat{ChatID: 1234567890}, "1234567890", ChannelUS, true},
		{"unmapped", &tg.PeerChannel{ChannelID: 111}, "", "", false},
		{"user peer", &tg.PeerUser{UserID: 1234567890}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, ok := channelOf(tt.peer, channelMap)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && (rule.id != tt.wantID || rule.channel != tt.wantCh) {
				t.Fatalf("rule = %+v, want id=%s channel=%s", rule, tt.wantID, tt.wantCh)
			}
		})
	}
}

func TestAuthorOf(t *testing.T) {
	user := &tg.User{}
	user.SetFirstName("Terry")
	user.SetLastName("Lin")
	channel := &tg.Channel{Title: "公开频道"}
	entities := tg.Entities{
		Users:    map[int64]*tg.User{42: user},
		Channels: map[int64]*tg.Channel{99: channel},
	}

	id, name := authorOf(&tg.Message{FromID: &tg.PeerUser{UserID: 42}}, entities)
	if id != "42" || name != "Terry Lin" {
		t.Fatalf("user author = %s / %s", id, name)
	}

	id, name = authorOf(&tg.Message{FromID: &tg.PeerUser{UserID: 777}}, entities)
	if id != "777" || name != "777" {
		t.Fatalf("unknown user author = %s / %s", id, name)
	}

	id, name = authorOf(&tg.Message{FromID: &tg.PeerChannel{ChannelID: 99}}, entities)
	if id != "99" || name != "公开频道" {
		t.Fatalf("channel author = %s / %s", id, name)
	}

	id, name = authorOf(&tg.Message{FromID: &tg.PeerChannel{ChannelID: 55}}, entities)
	if id != "55" || name != "55" {
		t.Fatalf("unknown channel author = %s / %s", id, name)
	}

	id, _ = authorOf(&tg.Message{}, entities)
	if id != "" {
		t.Fatalf("no FromID should yield empty id, got %s", id)
	}
}

func TestLargestPhotoSize(t *testing.T) {
	sizes := []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "s", W: 100, H: 100, Size: 1000},
		&tg.PhotoSize{Type: "y", W: 1280, H: 960, Size: 90000},
		&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 8000},
	}
	if got := largestPhotoSize(sizes); got != "y" {
		t.Fatalf("largest size type = %s, want y", got)
	}
	if got := largestPhotoSize(nil); got != "" {
		t.Fatalf("nil sizes should yield empty, got %s", got)
	}
	if got := photoSizeArea(&tg.PhotoSizeProgressive{Type: "x", W: 800, H: 600}); got != 480000 {
		t.Fatalf("progressive area = %d", got)
	}
	if got := photoSizeArea(&tg.PhotoSizeEmpty{Type: "j"}); got != 0 {
		t.Fatalf("empty area = %d, want 0", got)
	}
}

// testStringSession 构造一个格式合法的 Telethon StringSession：
// 前缀 '1' + base64.URLEncoding(1 字节 DC + 4 字节 IP + 2 字节端口 + 256 字节 authKey)。
func testStringSession(t *testing.T) string {
	t.Helper()
	payload := bytes.Repeat([]byte{0x61}, 263) // 1 + 4 + 2 + 256
	payload[0] = 2                             // DC id
	return "1" + base64.URLEncoding.EncodeToString(payload)
}

func TestSessionStorageMemory(t *testing.T) {
	config := testConfig(t)
	config.TGSessionFile = ""
	config.TGSession = testStringSession(t)
	source := NewTgSource(config, func(IncomingMessage) error { return nil })
	data, err := session.TelethonSession(config.TGSession)
	if err != nil {
		t.Fatalf("解析 session 失败：%v", err)
	}
	storage, err := source.sessionStorage(context.Background(), data)
	if err != nil {
		t.Fatalf("内存 session 存储失败：%v", err)
	}
	if _, ok := storage.(*session.StorageMemory); !ok {
		t.Fatalf("期望内存存储，实际 %T", storage)
	}
}

func TestSessionStorageFileInitAndReuse(t *testing.T) {
	config := testConfig(t)
	path := filepath.Join(t.TempDir(), "nested", "tg.session")
	config.TGSessionFile = path
	source := NewTgSource(config, func(IncomingMessage) error { return nil })
	data, err := session.TelethonSession(testStringSession(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := source.sessionStorage(ctx, data); err != nil {
		t.Fatalf("首次初始化 session 文件失败：%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session 文件应已创建：%v", err)
	}
	// 文件已存在时再次初始化不应报错（复用 gotd 刷新后的 session）。
	if _, err := source.sessionStorage(ctx, data); err != nil {
		t.Fatalf("复用 session 文件失败：%v", err)
	}
}

func TestConfigValidatesTGRequirements(t *testing.T) {
	config := testConfig(t)
	config.TGEnabled = true
	config.TGAPIID = 0
	if err := config.Validate(); err == nil {
		t.Fatal("启用 TG 但缺少 API_ID 应被拒绝")
	}
	config.TGAPIID = 123
	config.TGAPIHash = "hash"
	config.TGSession = ""
	if err := config.Validate(); err == nil {
		t.Fatal("启用 TG 但缺少 SESSION 应被拒绝")
	}
	config.TGSession = testStringSession(t)
	config.TGStateFile = ""
	if err := config.Validate(); err == nil {
		t.Fatal("启用 TG 但缺少 STATE_FILE（断线补包断点）应被拒绝")
	}
	config.TGStateFile = filepath.Join(t.TempDir(), "tg.updates.json")
	if err := config.Validate(); err != nil {
		t.Fatalf("完整 TG 配置应合法：%v", err)
	}
}

func TestStateStorageCreatesParentDir(t *testing.T) {
	config := testConfig(t)
	config.TGStateFile = filepath.Join(t.TempDir(), "nested", "tg.updates.json")
	source := NewTgSource(config, func(IncomingMessage) error { return nil })
	if err := source.stateStorage(); err != nil {
		t.Fatalf("创建 state 目录失败：%v", err)
	}
	if info, err := os.Stat(filepath.Dir(config.TGStateFile)); err != nil || !info.IsDir() {
		t.Fatalf("state 父目录应已创建：%v", err)
	}
	config.TGStateFile = ""
	source = NewTgSource(config, func(IncomingMessage) error { return nil })
	if err := source.stateStorage(); err == nil {
		t.Fatal("未配置 STATE_FILE 应报错")
	}
}
