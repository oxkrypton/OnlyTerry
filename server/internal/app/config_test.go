package app

import (
	"testing"
	"time"
)

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
