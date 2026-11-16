package app

import (
	"testing"
)

func TestNormalizeSocketMessage(t *testing.T) {
	channels := map[string]Channel{"room-us": ChannelUS, "room-btc": ChannelCrypto}
	authors := map[string]bool{"blogger": true}
	message, err := NormalizeSocketMessage([]byte(`{"type":"message","data":{"id":42,"channel_id":"room-us","text":"买入 NVDA","timestamp":1800000000,"author":{"id":"blogger","display_name":"Terry"},"images":["https://cdn.example.com/order.jpg"]}}`), channels, authors)
	if err != nil || message == nil {
		t.Fatalf("解析失败：%v", err)
	}
	if message.Channel != ChannelUS || message.Text != "买入 NVDA" || message.AuthorName != "Terry" || message.Images[0].URL != "https://cdn.example.com/order.jpg" {
		t.Fatalf("解析内容错误：%+v", message)
	}
	filtered, err := NormalizeSocketMessage([]byte(`{"type":"message","id":"1","channel":"room-us","text":"hello","author_id":"other"}`), channels, authors)
	if err != nil || filtered != nil {
		t.Fatal("非目标博主未过滤")
	}
	nested, err := NormalizeSocketMessage([]byte(`{"event":"new_message","data":{"message":{"message_id":"btc-1","channelId":"room-btc","content":"BTC 止损上移","sender":{"user_id":"blogger","username":"trader"},"media":[{"mime_type":"image/png","data_base64":"aGVsbG8="}]}}}`), channels, authors)
	if err != nil || nested == nil || nested.Channel != ChannelCrypto || nested.Images[0].DataBase64 != "aGVsbG8=" {
		t.Fatalf("嵌套消息解析失败：%+v %v", nested, err)
	}
}
