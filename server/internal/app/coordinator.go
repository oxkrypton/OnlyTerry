package app

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type Coordinator struct {
	config  Config
	store   *Store
	scorer  Scorer
	mailer  Mailer
	tokens  *Tokens
	mu      sync.Mutex
	timers  map[string]*time.Timer
	running map[string]bool
	stopped bool
}

func NewCoordinator(config Config, store *Store, scorer Scorer, mailer Mailer, tokens *Tokens) *Coordinator {
	return &Coordinator{config: config, store: store, scorer: scorer, mailer: mailer, tokens: tokens, timers: map[string]*time.Timer{}, running: map[string]bool{}}
}
func (c *Coordinator) Accept(message StoredMessage) (*Batch, error) {
	batch, err := c.store.AppendMessage(message, c.config.batchWindow())
	if err != nil || batch == nil {
		return batch, err
	}
	c.schedule(*batch)
	return batch, nil
}
func (c *Coordinator) Recover() error {
	batches, err := c.store.RecoverableBatches()
	if err != nil {
		return err
	}
	for _, batch := range batches {
		c.schedule(batch)
	}
	return nil
}
func (c *Coordinator) schedule(batch Batch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	if previous := c.timers[batch.ID]; previous != nil {
		previous.Stop()
	}
	delay := time.Until(time.UnixMilli(batch.DueAt))
	if delay < 0 {
		delay = 0
	}
	c.timers[batch.ID] = time.AfterFunc(delay, func() { c.mu.Lock(); delete(c.timers, batch.ID); c.mu.Unlock(); c.Process(batch.ID) })
}
func (c *Coordinator) Process(id string) {
	c.mu.Lock()
	if c.stopped || c.running[id] {
		c.mu.Unlock()
		return
	}
	c.running[id] = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.running, id); c.mu.Unlock() }()
	current, err := c.store.GetBatch(id)
	if err != nil || current == nil {
		return
	}
	if current.Status != BatchCollecting && current.Status != BatchRetrying && current.Status != BatchApproved {
		return
	}
	if current.DueAt > time.Now().UnixMilli() {
		c.schedule(*current)
		return
	}
	if current.Attempts >= c.config.MaxBatchAttempts {
		_ = c.store.MarkFailed(id, "超过最大重试次数")
		return
	}
	approved := current.Status == BatchApproved
	if !approved {
		if err = c.store.MarkScoring(id); err != nil {
			return
		}
		batch, getErr := c.store.GetBatchWithMessages(id)
		if getErr != nil || batch == nil || len(batch.Messages) == 0 {
			c.retry(id, fmt.Errorf("批次中没有消息"), false, current.Attempts)
			return
		}
		result, scoreErr := c.scorer.Score(batch)
		if scoreErr != nil {
			c.retry(id, scoreErr, false, current.Attempts)
			return
		}
		logScore(id, batch.Channel, result)
		if result.Score <= c.config.ScoreThreshold {
			if err := c.store.MarkSkipped(id, result.Score, result.Model); err != nil {
				log.Printf("更新批次失败：%v", err)
			}
			return
		}
		if err = c.store.MarkApproved(id, result.Score, result.Model); err != nil {
			c.retry(id, err, false, current.Attempts)
			return
		}
		approved = true
	}
	if err = c.deliver(id); err != nil {
		c.retry(id, err, approved, current.Attempts)
	}
}
func logScore(id string, channel Channel, result ScoreResult) {
	data, _ := json.Marshal(map[string]any{"event": "jev_scored", "batchId": id, "channel": channel, "score": result.Score, "rawScore": result.RawScore, "model": result.Model, "confidence": result.Confidence, "probabilities": result.Probabilities})
	log.Print(string(data))
}
func (c *Coordinator) retry(id string, problem error, approved bool, attempts int) {
	log.Printf("批次 %s 处理失败：%v", id, problem)
	if attempts+1 >= c.config.MaxBatchAttempts {
		_ = c.store.MarkFailed(id, problem.Error())
		return
	}
	batch, err := c.store.ScheduleRetry(id, problem.Error(), c.config.Retry, approved)
	if err == nil {
		c.schedule(*batch)
	}
}
func (c *Coordinator) deliver(id string) error {
	batch, err := c.store.GetBatchWithMessages(id)
	if err != nil {
		return err
	}
	if batch == nil || batch.Score == nil {
		return fmt.Errorf("已批准批次缺少评分")
	}
	if err = c.store.MarkSending(id); err != nil {
		return err
	}
	recipients, err := c.store.ActiveRecipients(batch.Channel)
	if err != nil {
		return err
	}
	failures := []string{}
	for _, recipient := range recipients {
		needed, reserveErr := c.store.ReserveDelivery(id, recipient.ID)
		if reserveErr != nil {
			failures = append(failures, reserveErr.Error())
			continue
		}
		if !needed {
			continue
		}
		token := c.tokens.Unsubscribe(recipient.ID)
		resendID, sendErr := c.mailer.SendAlert(AlertMail{Batch: batch, Recipient: recipient, UnsubscribeURL: c.config.PublicURL + "/unsubscribe/" + token})
		if sendErr != nil {
			_ = c.store.DeliveryFailed(id, recipient.ID, sendErr.Error())
			failures = append(failures, sendErr.Error())
		} else if err := c.store.DeliverySucceeded(id, recipient.ID, resendID); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("部分邮件发送失败：%s", strings.Join(failures, "；"))
	}
	log.Printf("批次 %s 已处理，收件人 %d 人", id, len(recipients))
	return c.store.MarkSent(id)
}
func (c *Coordinator) Stop() {
	c.mu.Lock()
	c.stopped = true
	for _, timer := range c.timers {
		timer.Stop()
	}
	c.mu.Unlock()
}
