package app

import (
	"testing"
	"time"
)

func TestProxyConfig(t *testing.T) {
	t.Setenv("PROXY", "socks5h://127.0.0.1:11305")
	if got := LoadConfig().Proxy; got != "socks5h://127.0.0.1:11305" {
		t.Fatalf("PROXY 应写入共用 Proxy，实际为 %q", got)
	}
	config := testConfig(t)
	config.Proxy = "http://127.0.0.1:11305"
	if err := config.Validate(); err == nil {
		t.Fatal("PROXY 使用 http 协议应被拒绝")
	}
	config.Proxy = "socks5h://127.0.0.1:11305"
	if err := config.Validate(); err != nil {
		t.Fatalf("socks5h 代理应合法：%v", err)
	}
}

func TestValidateRejectsInvalidBatchLimits(t *testing.T) {
	config := testConfig(t)
	if err := config.Validate(); err != nil {
		t.Fatalf("默认测试配置应合法：%v", err)
	}
	config.MaxBatchWait = config.Window
	if err := config.Validate(); err == nil {
		t.Fatal("MAX_BATCH_WAIT_MS 等于窗口应被拒绝")
	}
	config.MaxBatchWait = config.Window + time.Minute
	config.MaxBatchMessages = 0
	if err := config.Validate(); err == nil {
		t.Fatal("MAX_BATCH_MESSAGES 为 0 应被拒绝")
	}
}
