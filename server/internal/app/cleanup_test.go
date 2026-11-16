package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupKeepsReferencedImagesThenDeletesExpiredData(t *testing.T) {
	config := testConfig(t)
	now := time.UnixMilli(1000000)
	store, err := newStore(config.DatabasePath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = os.MkdirAll(config.ImageDir, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ImageDir, "old.jpg")
	if err = os.WriteFile(path, []byte("image"), 0640); err != nil {
		t.Fatal(err)
	}
	message := testMessage()
	message.Images = []StoredImage{{FileName: "old.jpg", MIMEType: "image/jpeg", Width: 800, Height: 600}}
	batch, err := store.AppendMessage(message, config.Window)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	cleaner := NewCleaner(config, store)
	cleaner.CleanupOnce()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("清理任务删除了仍被引用的图片")
	}
	if err = store.MarkSkipped(batch.ID, 20, "jev-test"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(22 * 24 * time.Hour)
	old := now.Add(-31 * 24 * time.Hour)
	_ = os.Chtimes(path, old, old)
	cleaner.CleanupOnce()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("过期图片未删除")
	}
	stored, _ := store.GetBatch(batch.ID)
	if stored != nil {
		t.Fatal("旧批次未删除")
	}
}

func TestCleanupDeletesOrphanFiles(t *testing.T) {
	config := testConfig(t)
	store, _ := NewStore(config.DatabasePath)
	defer store.Close()
	_ = os.MkdirAll(config.ImageDir, 0750)
	path := filepath.Join(config.ImageDir, "orphan.jpg")
	_ = os.WriteFile(path, []byte("orphan"), 0640)
	old := time.Now().Add(-8 * 24 * time.Hour)
	_ = os.Chtimes(path, old, old)
	NewCleaner(config, store).CleanupOnce()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("孤儿图片未清理")
	}
}
