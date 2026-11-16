package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"terry-alerts/internal/app"
)

func main() {
	if err := app.LoadDotEnv(".env"); err != nil {
		log.Fatalf("读取 .env 失败：%v", err)
	}
	config := app.LoadConfig()
	if err := config.Validate(); err != nil {
		log.Fatalf("配置无效：%v", err)
	}
	store, err := app.NewStore(config.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	tokens, err := app.NewTokens(config.TokenSecret)
	if err != nil {
		log.Fatal(err)
	}
	mailer := app.NewResendMailer(config)
	scorer := app.NewJevScorer(config)
	coordinator := app.NewCoordinator(config, store, scorer, mailer, tokens)
	images := app.NewImageStore(config)
	socket := app.NewGroupSocket(config, func(message app.IncomingMessage) error {
		duplicate, err := store.HasMessage(message.Channel, message.ID)
		if err != nil || duplicate {
			return err
		}
		storedImages := images.Prepare(message.Images, config.MaxImagesPerMessage)
		if message.Text == "" && len(storedImages) == 0 {
			return nil
		}
		_, err = coordinator.Accept(app.StoredMessage{ID: message.ID, Channel: message.Channel, AuthorID: message.AuthorID, AuthorName: message.AuthorName, SentAt: message.SentAt, Text: message.Text, Images: storedImages})
		return err
	})
	if err := coordinator.Recover(); err != nil {
		log.Fatalf("恢复消息窗口失败：%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleaner := app.NewCleaner(config, store)
	go cleaner.Run(ctx)
	if config.WSEnabled {
		go socket.Run(ctx)
	}
	httpServer := &http.Server{Addr: fmt.Sprintf("%s:%d", config.Host, config.Port), Handler: app.NewHTTPHandler(config, store, tokens, mailer), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	errorsCh := make(chan error, 1)
	go func() {
		log.Printf("订阅服务已启动：%s", config.PublicURL)
		errorsCh <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP 服务停止：%v", err)
		}
		stop()
	}
	coordinator.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}
