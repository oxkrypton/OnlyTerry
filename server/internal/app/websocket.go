package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type GroupSocket struct {
	config  Config
	handler func(IncomingMessage) error
}

func NewGroupSocket(config Config, handler func(IncomingMessage) error) *GroupSocket {
	return &GroupSocket{config: config, handler: handler}
}
func (s *GroupSocket) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("群聊 WebSocket 断开：%v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}
func (s *GroupSocket) connect(ctx context.Context) error {
	headers := http.Header{}
	for key, value := range s.config.WSHeaders {
		headers.Set(key, value)
	}
	if s.config.WSAuthToken != "" {
		headers.Set("Authorization", "Bearer "+s.config.WSAuthToken)
	}
	connection, _, err := websocket.Dial(ctx, s.config.WSURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return err
	}
	defer connection.Close(websocket.StatusInternalError, "connection ended")
	limit := s.config.MaxImageBytes * 2
	if limit < 16*1024*1024 {
		limit = 16 * 1024 * 1024
	}
	connection.SetReadLimit(limit)
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-connectionCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(connectionCtx, 10*time.Second)
				_ = connection.Ping(pingCtx)
				pingCancel()
			}
		}
	}()
	for _, message := range s.config.WSSubscribeMessages {
		if err := connection.Write(connectionCtx, websocket.MessageText, message); err != nil {
			return err
		}
	}
	log.Print("群聊 WebSocket 已连接")
	for {
		_, data, err := connection.Read(connectionCtx)
		if err != nil {
			return err
		}
		message, err := NormalizeSocketMessage(data, s.config.WSChannelMap, s.config.TargetAuthorIDs)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			continue
		}
		if message != nil {
			if err := s.handler(*message); err != nil {
				log.Printf("处理群聊消息失败：%v", err)
			}
		}
	}
}
