package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(path string) (*Store, error) { return newStore(path, time.Now) }
func newStore(path string, now func() time.Time) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: now}
	statements := []string{
		`PRAGMA journal_mode = WAL`, `PRAGMA foreign_keys = ON`, `PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS subscribers (
			id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL COLLATE NOCASE UNIQUE,
			status TEXT NOT NULL, pending_request_id TEXT, pending_channels_json TEXT,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, confirmed_at INTEGER, unsubscribed_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS subscriber_channels (
			subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
			channel TEXT NOT NULL, PRIMARY KEY(subscriber_id, channel))`,
		`CREATE TABLE IF NOT EXISTS batches (
			id TEXT PRIMARY KEY, channel TEXT NOT NULL, status TEXT NOT NULL, due_at INTEGER NOT NULL,
			score REAL, model TEXT, attempts INTEGER NOT NULL DEFAULT 0, error TEXT,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS batches_recovery ON batches(status, due_at)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT, batch_id TEXT NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
			external_id TEXT NOT NULL, channel TEXT NOT NULL, author_id TEXT NOT NULL, author_name TEXT NOT NULL,
			sent_at TEXT NOT NULL, text TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(channel, external_id))`,
		`CREATE TABLE IF NOT EXISTS images (
			file_name TEXT PRIMARY KEY, mime_type TEXT NOT NULL, source_url TEXT, source_mime TEXT,
			width INTEGER NOT NULL, height INTEGER NOT NULL, alt TEXT, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS message_images (
			message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE, image_index INTEGER NOT NULL,
			file_name TEXT NOT NULL REFERENCES images(file_name) ON DELETE CASCADE, PRIMARY KEY(message_id, image_index))`,
		`CREATE INDEX IF NOT EXISTS message_images_file ON message_images(file_name)`,
		`CREATE TABLE IF NOT EXISTS deliveries (
			batch_id TEXT NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
			subscriber_id INTEGER NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
			status TEXT NOT NULL, resend_id TEXT, error TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY(batch_id, subscriber_id))`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化 SQLite：%w", err)
		}
	}
	return store, nil
}

func (s *Store) Close() error     { return s.db.Close() }
func (s *Store) timestamp() int64 { return s.now().UnixMilli() }

func scanBatch(scanner interface{ Scan(...any) error }) (*Batch, error) {
	var b Batch
	var score sql.NullFloat64
	var model, problem sql.NullString
	err := scanner.Scan(&b.ID, &b.Channel, &b.Status, &b.DueAt, &score, &model, &b.Attempts, &problem, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if score.Valid {
		b.Score = &score.Float64
	}
	b.Model = model.String
	b.Error = problem.String
	return &b, nil
}

const batchColumns = `id, channel, status, due_at, score, model, attempts, error, created_at, updated_at`

func (s *Store) GetBatch(id string) (*Batch, error) {
	b, err := scanBatch(s.db.QueryRow(`SELECT `+batchColumns+` FROM batches WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func (s *Store) CreateSubscriptionRequest(email string, channels []Channel) (Subscriber, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Subscriber{}, "", err
	}
	defer tx.Rollback()
	requestID := RandomID()
	now := s.timestamp()
	channelsJSON, _ := json.Marshal(channels)
	var sub Subscriber
	err = tx.QueryRow(`SELECT id, email, status FROM subscribers WHERE email = ?`, email).Scan(&sub.ID, &sub.Email, &sub.Status)
	if err == sql.ErrNoRows {
		result, insertErr := tx.Exec(`INSERT INTO subscribers(email,status,pending_request_id,pending_channels_json,created_at,updated_at) VALUES(?,'pending',?,?,?,?)`, email, requestID, channelsJSON, now, now)
		if insertErr != nil {
			return sub, "", insertErr
		}
		sub.ID, _ = result.LastInsertId()
		sub.Email = email
		sub.Status = "pending"
	} else if err != nil {
		return sub, "", err
	} else {
		if _, err = tx.Exec(`UPDATE subscribers SET pending_request_id=?,pending_channels_json=?,updated_at=? WHERE id=?`, requestID, channelsJSON, now, sub.ID); err != nil {
			return sub, "", err
		}
	}
	return sub, requestID, tx.Commit()
}

func (s *Store) ConfirmSubscription(id int64, requestID string, channels []Channel) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM subscribers WHERE id=? AND pending_request_id=?`, id, requestID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := s.timestamp()
	if _, err = tx.Exec(`UPDATE subscribers SET status='active',pending_request_id=NULL,pending_channels_json=NULL,confirmed_at=?,updated_at=?,unsubscribed_at=NULL WHERE id=?`, now, now, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`DELETE FROM subscriber_channels WHERE subscriber_id=?`, id); err != nil {
		return false, err
	}
	for _, channel := range channels {
		if _, err = tx.Exec(`INSERT INTO subscriber_channels(subscriber_id,channel) VALUES(?,?)`, id, channel); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) Unsubscribe(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.timestamp()
	if _, err = tx.Exec(`UPDATE subscribers SET status='unsubscribed',pending_request_id=NULL,pending_channels_json=NULL,unsubscribed_at=?,updated_at=? WHERE id=?`, now, now, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM subscriber_channels WHERE subscriber_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ActiveRecipients(channel Channel) ([]Subscriber, error) {
	rows, err := s.db.Query(`SELECT s.id,s.email,s.status FROM subscribers s JOIN subscriber_channels c ON c.subscriber_id=s.id WHERE s.status='active' AND c.channel=? ORDER BY s.id`, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Subscriber{}
	for rows.Next() {
		var sub Subscriber
		if err := rows.Scan(&sub.ID, &sub.Email, &sub.Status); err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, rows.Err()
}
func (s *Store) GetSubscriber(id int64) (Subscriber, error) {
	var sub Subscriber
	err := s.db.QueryRow(`SELECT id,email,status FROM subscribers WHERE id=?`, id).Scan(&sub.ID, &sub.Email, &sub.Status)
	return sub, err
}

func (s *Store) HasMessage(channel Channel, externalID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM messages WHERE channel=? AND external_id=?`, channel, externalID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) AppendMessage(message StoredMessage, window time.Duration) (*Batch, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing int
	err = tx.QueryRow(`SELECT 1 FROM messages WHERE channel=? AND external_id=?`, message.Channel, message.ID).Scan(&existing)
	if err == nil {
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	now := s.timestamp()
	due := s.now().Add(window).UnixMilli()
	var batchID string
	err = tx.QueryRow(`SELECT id FROM batches WHERE channel=? AND status='collecting'`, message.Channel).Scan(&batchID)
	if err == sql.ErrNoRows {
		batchID = RandomID()
		if _, err = tx.Exec(`INSERT INTO batches(id,channel,status,due_at,created_at,updated_at) VALUES(?,?,'collecting',?,?,?)`, batchID, message.Channel, due, now, now); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE batches SET due_at=?,updated_at=?,error=NULL WHERE id=?`, due, now, batchID); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`INSERT INTO messages(batch_id,external_id,channel,author_id,author_name,sent_at,text,created_at) VALUES(?,?,?,?,?,?,?,?)`, batchID, message.ID, message.Channel, message.AuthorID, message.AuthorName, message.SentAt, message.Text, now)
	if err != nil {
		return nil, err
	}
	messageID, _ := result.LastInsertId()
	for index, image := range message.Images {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO images(file_name,mime_type,source_url,source_mime,width,height,alt,created_at) VALUES(?,?,?,?,?,?,?,?)`, image.FileName, image.MIMEType, image.SourceURL, image.SourceMIME, image.Width, image.Height, image.Alt, now); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO message_images(message_id,image_index,file_name) VALUES(?,?,?)`, messageID, index, image.FileName); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetBatch(batchID)
}

func (s *Store) GetBatchWithMessages(id string) (*BatchWithMessages, error) {
	batch, err := s.GetBatch(id)
	if err != nil || batch == nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,external_id,channel,author_id,author_name,sent_at,text FROM messages WHERE batch_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	type rowMessage struct {
		id      int64
		message StoredMessage
	}
	var collected []rowMessage
	for rows.Next() {
		var row rowMessage
		if err = rows.Scan(&row.id, &row.message.ID, &row.message.Channel, &row.message.AuthorID, &row.message.AuthorName, &row.message.SentAt, &row.message.Text); err != nil {
			rows.Close()
			return nil, err
		}
		collected = append(collected, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := &BatchWithMessages{Batch: *batch, Messages: []StoredMessage{}}
	for _, row := range collected {
		images, queryErr := s.db.Query(`SELECT i.file_name,i.mime_type,i.source_url,i.source_mime,i.width,i.height,i.alt FROM images i JOIN message_images m ON m.file_name=i.file_name WHERE m.message_id=? ORDER BY m.image_index`, row.id)
		if queryErr != nil {
			return nil, queryErr
		}
		for images.Next() {
			var image StoredImage
			var sourceURL, sourceMIME, alt sql.NullString
			if err = images.Scan(&image.FileName, &image.MIMEType, &sourceURL, &sourceMIME, &image.Width, &image.Height, &alt); err != nil {
				images.Close()
				return nil, err
			}
			image.SourceURL, image.SourceMIME, image.Alt = sourceURL.String, sourceMIME.String, alt.String
			row.message.Images = append(row.message.Images, image)
		}
		images.Close()
		if err = images.Err(); err != nil {
			return nil, err
		}
		result.Messages = append(result.Messages, row.message)
	}
	return result, nil
}

func (s *Store) RecoverableBatches() ([]Batch, error) {
	now := s.timestamp()
	if _, err := s.db.Exec(`UPDATE batches SET status=CASE WHEN status='sending' THEN 'approved' ELSE 'retrying' END,due_at=?,updated_at=? WHERE status IN('scoring','sending')`, now, now); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT ` + batchColumns + ` FROM batches WHERE status IN('collecting','retrying','approved') ORDER BY due_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Batch{}
	for rows.Next() {
		batch, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *batch)
	}
	return result, rows.Err()
}

func (s *Store) updateBatch(id, status string, score *float64, model, problem string, attempts bool, dueAt *int64) error {
	now := s.timestamp()
	query := `UPDATE batches SET status=?,error=?,updated_at=?`
	args := []any{status, problem, now}
	if score != nil {
		query += `,score=?,model=?`
		args = append(args, *score, model)
	}
	if attempts {
		query += `,attempts=attempts+1`
	}
	if dueAt != nil {
		query += `,due_at=?`
		args = append(args, *dueAt)
	}
	query += ` WHERE id=?`
	args = append(args, id)
	_, err := s.db.Exec(query, args...)
	return err
}
func (s *Store) MarkScoring(id string) error {
	return s.updateBatch(id, "scoring", nil, "", "", false, nil)
}
func (s *Store) MarkSkipped(id string, score float64, model string) error {
	return s.updateBatch(id, "skipped", &score, model, "", false, nil)
}
func (s *Store) MarkApproved(id string, score float64, model string) error {
	return s.updateBatch(id, "approved", &score, model, "", false, nil)
}
func (s *Store) MarkSending(id string) error {
	return s.updateBatch(id, "sending", nil, "", "", false, nil)
}
func (s *Store) MarkSent(id string) error { return s.updateBatch(id, "sent", nil, "", "", false, nil) }
func (s *Store) MarkFailed(id, problem string) error {
	return s.updateBatch(id, "failed", nil, "", truncate(problem, 2000), false, nil)
}
func (s *Store) ScheduleRetry(id, problem string, retry time.Duration, approved bool) (*Batch, error) {
	status := "retrying"
	if approved {
		status = "approved"
	}
	due := s.now().Add(retry).UnixMilli()
	if err := s.updateBatch(id, status, nil, "", truncate(problem, 2000), true, &due); err != nil {
		return nil, err
	}
	return s.GetBatch(id)
}

func (s *Store) ReserveDelivery(batchID string, subscriberID int64) (bool, error) {
	now := s.timestamp()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO deliveries(batch_id,subscriber_id,status,created_at,updated_at) VALUES(?,?,'sending',?,?)`, batchID, subscriberID, now, now); err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE deliveries SET status='sending',error=NULL,updated_at=? WHERE batch_id=? AND subscriber_id=? AND status!='sent'`, now, batchID, subscriberID); err != nil {
		return false, err
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM deliveries WHERE batch_id=? AND subscriber_id=?`, batchID, subscriberID).Scan(&status); err != nil {
		return false, err
	}
	return status != "sent", nil
}
func (s *Store) DeliverySucceeded(batchID string, subscriberID int64, resendID string) error {
	_, err := s.db.Exec(`UPDATE deliveries SET status='sent',resend_id=?,error=NULL,updated_at=? WHERE batch_id=? AND subscriber_id=?`, resendID, s.timestamp(), batchID, subscriberID)
	return err
}
func (s *Store) DeliveryFailed(batchID string, subscriberID int64, problem string) error {
	_, err := s.db.Exec(`UPDATE deliveries SET status='failed',error=?,updated_at=? WHERE batch_id=? AND subscriber_id=?`, truncate(problem, 2000), s.timestamp(), batchID, subscriberID)
	return err
}

func (s *Store) DeleteOldRecords(batchCutoff, pendingCutoff int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM batches WHERE status IN('sent','skipped','failed') AND updated_at<?`, batchCutoff)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	if _, err = tx.Exec(`DELETE FROM subscribers WHERE status='pending' AND updated_at<?`, pendingCutoff); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}
func (s *Store) ExpiredUnreferencedImages(cutoff int64) ([]StoredImage, error) {
	rows, err := s.db.Query(`SELECT file_name,mime_type,width,height FROM images i WHERE created_at<? AND NOT EXISTS(SELECT 1 FROM message_images m WHERE m.file_name=i.file_name)`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []StoredImage{}
	for rows.Next() {
		var image StoredImage
		if err := rows.Scan(&image.FileName, &image.MIMEType, &image.Width, &image.Height); err != nil {
			return nil, err
		}
		result = append(result, image)
	}
	return result, rows.Err()
}
func (s *Store) DeleteImage(fileName string) error {
	_, err := s.db.Exec(`DELETE FROM images WHERE file_name=?`, fileName)
	return err
}
func (s *Store) ImageKnown(fileName string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM images WHERE file_name=?`, fileName).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
