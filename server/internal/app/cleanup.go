package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Cleaner struct {
	config Config
	store  *Store
}

func NewCleaner(config Config, store *Store) *Cleaner { return &Cleaner{config: config, store: store} }
func (c *Cleaner) Run(ctx context.Context) {
	c.CleanupOnce()
	ticker := time.NewTicker(c.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CleanupOnce()
		}
	}
}
func (c *Cleaner) CleanupOnce() {
	now := time.Now()
	batches, err := c.store.DeleteOldRecords(now.Add(-c.config.DatabaseRetention).UnixMilli(), now.Add(-48*time.Hour).UnixMilli())
	if err != nil {
		log.Printf("清理旧数据库记录失败：%v", err)
	}
	images, err := c.store.ExpiredUnreferencedImages(now.Add(-c.config.ImageRetention).UnixMilli())
	if err != nil {
		log.Printf("查询过期图片失败：%v", err)
		return
	}
	removed := 0
	for _, image := range images {
		if err := os.Remove(filepath.Join(c.config.ImageDir, image.FileName)); err != nil && !os.IsNotExist(err) {
			log.Printf("删除图片失败：%v", err)
			continue
		}
		if err := c.store.DeleteImage(image.FileName); err == nil {
			removed++
		}
	}
	// 清理下载成功但消息未入库的孤儿文件。
	entries, err := os.ReadDir(c.config.ImageDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jpg") {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || now.Sub(info.ModTime()) < c.config.ImageRetention {
				continue
			}
			known, queryErr := c.store.ImageKnown(entry.Name())
			if queryErr != nil || known {
				continue
			}
			if os.Remove(filepath.Join(c.config.ImageDir, entry.Name())) == nil {
				removed++
			}
		}
	}
	if batches > 0 || removed > 0 {
		log.Printf("定时清理完成：旧批次 %d，图片 %d", batches, removed)
	}
}
