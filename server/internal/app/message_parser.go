package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type object map[string]any

func asObject(value any) object { result, _ := value.(map[string]any); return result }
func firstString(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	return ""
}
func unwrap(root object) object {
	if message := asObject(root["message"]); message != nil {
		return message
	}
	data := asObject(root["data"])
	if message := asObject(data["message"]); message != nil {
		return message
	}
	if payload := asObject(root["payload"]); payload != nil {
		return payload
	}
	if data != nil {
		return data
	}
	return root
}
func imageValues(candidate object) []any {
	result := []any{}
	for _, key := range []string{"images", "image_urls", "imageUrls", "media", "attachments", "image"} {
		value := candidate[key]
		if list, ok := value.([]any); ok {
			result = append(result, list...)
		} else if value != nil {
			result = append(result, value)
		}
	}
	return result
}
func parseImages(candidate object) []IncomingImage {
	result := []IncomingImage{}
	for _, value := range imageValues(candidate) {
		if url, ok := value.(string); ok {
			result = append(result, IncomingImage{URL: url})
			continue
		}
		item := asObject(value)
		if item == nil {
			continue
		}
		mime := firstString(item["mime_type"], item["mimeType"], item["content_type"])
		if mime != "" && !strings.HasPrefix(mime, "image/") {
			continue
		}
		url := firstString(item["url"], item["src"], item["image_url"], item["imageUrl"], item["download_url"])
		data := firstString(item["data_base64"], item["dataBase64"], item["base64"], item["data"])
		if url == "" && data == "" {
			continue
		}
		result = append(result, IncomingImage{URL: url, DataBase64: data, MIMEType: mime, Alt: firstString(item["alt"], item["caption"])})
	}
	return result
}
func parseDate(value any) string {
	raw := firstString(value)
	if raw != "" {
		if number, err := strconv.ParseFloat(raw, 64); err == nil {
			if number < 10000000000 {
				number *= 1000
			}
			return time.UnixMilli(int64(number)).UTC().Format(time.RFC3339)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return time.Now().UTC().Format(time.RFC3339)
}

func NormalizeSocketMessage(payload []byte, channelMap map[string]Channel, targetAuthors map[string]bool) (*IncomingMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	root := asObject(raw)
	if root == nil {
		return nil, nil
	}
	event := strings.ToLower(firstString(root["type"], root["event"]))
	allowed := map[string]bool{"message": true, "new_message": true, "message.created": true, "message_created": true, "chat_message": true}
	if event != "" && !allowed[event] {
		return nil, nil
	}
	candidate := unwrap(root)
	author := asObject(candidate["author"])
	if author == nil {
		author = asObject(candidate["sender"])
	}
	if author == nil {
		author = asObject(candidate["user"])
	}
	authorID := firstString(candidate["author_id"], candidate["authorId"], candidate["sender_id"], candidate["senderId"], author["id"], author["user_id"], author["username"])
	if len(targetAuthors) > 0 && !targetAuthors[authorID] {
		return nil, nil
	}
	rawChannel := firstString(candidate["channel"], candidate["channel_id"], candidate["channelId"], root["channel"], root["channel_id"])
	channel, ok := channelMap[rawChannel]
	if !ok && ValidChannel(Channel(rawChannel)) {
		channel, ok = Channel(rawChannel), true
	}
	if !ok {
		return nil, nil
	}
	images := parseImages(candidate)
	body := firstString(candidate["text"], candidate["content"], candidate["message"], candidate["body"], candidate["caption"])
	if body == "" && len(images) == 0 {
		return nil, nil
	}
	id := firstString(candidate["id"], candidate["message_id"], candidate["messageId"], root["id"])
	if id == "" {
		canonical, _ := json.Marshal(root)
		sum := sha256.Sum256(canonical)
		id = hex.EncodeToString(sum[:])
	}
	if authorID == "" {
		authorID = "unknown"
	}
	name := firstString(candidate["author_name"], candidate["authorName"], author["name"], author["display_name"], author["username"])
	if name == "" {
		name = "博主"
	}
	sent := candidate["sent_at"]
	if sent == nil {
		sent = candidate["sentAt"]
	}
	if sent == nil {
		sent = candidate["timestamp"]
	}
	if sent == nil {
		sent = candidate["created_at"]
	}
	return &IncomingMessage{ID: id, Channel: channel, AuthorID: authorID, AuthorName: name, SentAt: parseDate(sent), Text: body, Images: images}, nil
}
