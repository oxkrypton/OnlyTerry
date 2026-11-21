package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const jevPurpose = "判断投资博主的发言是否包含值得即时推送的标的趋势、观点或操作信号"

const subscriptionQuestion = "这批消息是否包含值得立即推送给订阅者的具体标的趋势判断、明确看多/看空观点、重点关注提示或交易动作？"

var subscriptionCriteria = []string{
	"0: 完全无关的日常闲聊、广告、寒暄、表情包或无意义水群内容。",
	"1: 泛泛的市场新闻转发、宏观闲聊或情绪宣泄，没有具体标的，也没有明确的方向判断。",
	"2: 提及具体标的或板块，但仅为客观事实/新闻搬运、段子、事后复盘或科普，不含当下投资倾向或关注建议。",
	"3: 讨论宏观大势、大盘整体看法或宽泛行业现象，有一定参考性，但未落脚到具体标的或明确细分赛道的方向判断。",
	"4: 针对具体标的或明确细分赛道，给出了清晰的趋势判断、看多/看空倾向、重点关注提示或明确买卖/调仓动作（例如「TSLA 最近走势挺强的，保持关注」「买入TSLA」），即使表述极简且未附带价格、仓位、详细理由或截图，也属于本档。",
	"5: 针对具体标的给出了明确的操作指令或深度投资判断，且附带了具体的核心依据或可执行信息（如基本面/技术面逻辑、估值分析、入场/离场区间、仓位比例、止损位、订单截图或重大风控预警）。",
}

type JevMessage struct {
	Author         string   `json:"author"`
	SentAt         string   `json:"sent_at"`
	OriginalText   string   `json:"original_text"`
	OriginalImages []string `json:"original_images,omitempty"`
}

type JevScorer struct {
	config Config
	client *http.Client
}

func NewJevScorer(config Config) *JevScorer {
	client := &http.Client{Timeout: 45 * time.Second}
	if config.Proxy != "" {
		// JEV 走标准 HTTP Client，与 gotd 的 MTProto 共用 PROXY 出口，
		// 因此这里需要单独把 SOCKS5 拨号器挂到 HTTP Transport 上。
		// socks5h 由 x/net/proxy 在代理端解析域名，避免本地 DNS 泄漏。
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil {
			log.Printf("JEV 代理地址解析失败，将直连：%v", err)
		} else if dialer, dialErr := proxy.FromURL(proxyURL, proxy.Direct); dialErr != nil {
			log.Printf("JEV 代理配置无效，将直连：%v", dialErr)
		} else {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			// 已显式指定 SOCKS 出口，不再叠加 HTTP_PROXY/HTTPS_PROXY 环境变量。
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
					return contextDialer.DialContext(ctx, network, addr)
				}
				return dialer.Dial(network, addr)
			}
			client.Transport = transport
		}
	}
	return &JevScorer{config: config, client: client}
}

func (s *JevScorer) Score(batch *BatchWithMessages) (ScoreResult, error) {
	var result ScoreResult
	remainingText, remainingImages := 48000, s.config.MaxScoringImages
	messages := []JevMessage{}
	assets := map[string]any{}
	type filePart struct{ name, path string }
	parts := []filePart{}
	for _, message := range batch.Messages {
		text := []rune(message.Text)
		if len(text) > remainingText {
			text = text[:max(remainingText, 0)]
		}
		remainingText -= len(text)
		refs := []string{}
		for _, stored := range message.Images {
			if remainingImages <= 0 {
				break
			}
			ref, part := "image-"+RandomID(), "file"+RandomID()
			refs = append(refs, ref)
			assets[ref] = map[string]any{"type": "image", "part": part}
			parts = append(parts, filePart{name: part, path: filepath.Join(s.config.ImageDir, stored.FileName)})
			remainingImages--
		}
		message := JevMessage{Author: message.AuthorName, SentAt: message.SentAt, OriginalText: string(text)}
		if len(refs) > 0 {
			message.OriginalImages = refs
		}
		messages = append(messages, message)
	}
	payload := map[string]any{
		"model": s.config.JevModel,
		"state": map[string]any{"market": ChannelLabel(batch.Channel), "purpose": jevPurpose, "messages": messages},
		"questions": map[string]any{"subscription_value": map[string]any{"type": "score", "instructions": subscriptionQuestion, "criteria": subscriptionCriteria}},
	}
	// TypeSafe 的 assets 要求至少一项；无图片时必须整个字段缺省，传 {} 会被判为 400 Invalid request。
	if len(assets) > 0 {
		payload["assets"] = assets
	}
	payloadJSON, _ := json.Marshal(payload)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="request"`}
	header["Content-Type"] = []string{"application/json"}
	part, err := writer.CreatePart(header)
	if err == nil {
		_, err = part.Write(payloadJSON)
	}
	for _, file := range parts {
		if err != nil {
			break
		}
		part, err = writer.CreateFormFile(file.name, filepath.Base(file.path))
		if err != nil {
			break
		}
		handle, openErr := os.Open(file.path)
		if openErr != nil {
			err = openErr
			break
		}
		_, err = io.Copy(part, handle)
		handle.Close()
		if err != nil {
			break
		}
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.TypeSafeBaseURL, "/")+"/v1/systemone", &body)
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+s.config.TypeSafeAPIKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("TypeSafe-Danger-Zone-Images", "true")
	response, err := s.client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return result, fmt.Errorf("JEV HTTP %d：%s", response.StatusCode, string(data))
	}
	var decoded struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Score         float64            `json:"score"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return result, err
	}
	answer, ok := decoded.Answers["subscription_value"]
	if !ok {
		return result, fmt.Errorf("JEV 响应缺少 subscription_value")
	}
	return ScoreResult{Score: answer.Score / float64(len(subscriptionCriteria)-1) * 100, RawScore: answer.Score, Model: decoded.Model, Confidence: answer.Confidence, Probabilities: answer.Probabilities}, nil
}
