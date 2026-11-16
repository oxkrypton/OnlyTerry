package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var valueLevels = []string{
	"闲聊、问候、表情、广告或与美股和加密货币市场无关的内容；没有值得订阅者立即阅读的市场信息。",
	"提到市场或某个资产，但只有泛泛评论、情绪或已知事实，没有具体观点、数据、交易信息或风险说明。",
	"包含有一定参考意义的市场背景或方向性看法，但缺少明确标的、价格、时间、依据、仓位或风险边界。",
	"包含明确标的和较具体的市场观点、数据、依据或风险提示，能帮助订阅者理解一个交易想法，但关键细节仍不完整。",
	"包含及时且具体的交易或持仓信息，例如明确标的、方向、买卖动作、订单截图、入场或退出区域、仓位或止损；订阅者能直接核对博主正在做什么。",
	"包含非常及时、清晰且可核对的高价值交易信息：明确标的和方向，并同时给出多项关键细节，例如实际成交或订单截图、价格、仓位、目标、失效条件或风险管理。",
}

type JevScorer struct {
	config Config
	client *http.Client
}

func NewJevScorer(config Config) *JevScorer {
	return &JevScorer{config: config, client: &http.Client{Timeout: 45 * time.Second}}
}

func (s *JevScorer) Score(batch *BatchWithMessages) (ScoreResult, error) {
	var result ScoreResult
	remainingText, remainingImages := 48000, s.config.MaxScoringImages
	messages := []map[string]any{}
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
		messages = append(messages, map[string]any{"author": message.AuthorName, "sent_at": message.SentAt, "original_text": string(text), "original_images": refs})
	}
	payload := map[string]any{
		"model":     s.config.JevModel,
		"state":     map[string]any{"market": ChannelLabel(batch.Channel), "purpose": "评估博主这组连续原始发言对美股或加密货币订阅者的即时阅读价值，不生成摘要。", "messages": messages},
		"assets":    assets,
		"questions": map[string]any{"subscription_value": map[string]any{"type": "score", "instructions": "仅根据 `messages` 中的原始文字和原始截图，评价这组博主发言对关注美股或加密货币交易者的即时订阅价值。", "criteria": valueLevels}},
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
	return ScoreResult{Score: answer.Score / float64(len(valueLevels)-1) * 100, RawScore: answer.Score, Model: decoded.Model, Confidence: answer.Confidence, Probabilities: answer.Probabilities}, nil
}
