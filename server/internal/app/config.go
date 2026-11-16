package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                  string
	Port                  int
	PublicURL             string
	DatabasePath          string
	ImageDir              string
	TokenSecret           string
	TypeSafeAPIKey        string
	TypeSafeBaseURL       string
	JevModel              string
	ResendAPIKey          string
	ResendBaseURL         string
	EmailFrom             string
	Window                time.Duration
	ScoreThreshold        float64
	Retry                 time.Duration
	MaxBatchAttempts      int
	MaxImageBytes         int64
	MaxImagePixels        int
	MaxImagesPerMessage   int
	MaxScoringImages      int
	AllowPrivateImageURLs bool
	ResendRemoteImages    bool
	CleanupInterval       time.Duration
	ImageRetention        time.Duration
	DatabaseRetention     time.Duration
	WSEnabled             bool
	WSURL                 string
	WSAuthToken           string
	WSHeaders             map[string]string
	WSSubscribeMessages   []json.RawMessage
	WSChannelMap          map[string]Channel
	TargetAuthorIDs       map[string]bool
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
func envInt64(key string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
func envFloat(key string, fallback float64) float64 {
	value, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil {
		return fallback
	}
	return value
}
func envBool(key string, fallback bool) bool {
	value := strings.ToLower(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
func envDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
func jsonEnv[T any](key string, fallback T) T {
	if value := os.Getenv(key); value != "" {
		var parsed T
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	}
	return fallback
}

func LoadConfig() Config {
	channels := jsonEnv("WS_CHANNEL_MAP_JSON", map[string]Channel{string(ChannelUS): ChannelUS, string(ChannelCrypto): ChannelCrypto})
	authors := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("TARGET_AUTHOR_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			authors[id] = true
		}
	}
	return Config{
		Host: env("HOST", "0.0.0.0"), Port: envInt("PORT", 3000),
		PublicURL:    strings.TrimRight(env("PUBLIC_URL", "http://127.0.0.1:3000"), "/"),
		DatabasePath: env("DATABASE_PATH", "data/service/service.db"), ImageDir: env("IMAGE_DIR", "data/service/images"),
		TokenSecret: os.Getenv("TOKEN_SECRET"), TypeSafeAPIKey: os.Getenv("TYPESAFE_API_KEY"),
		TypeSafeBaseURL: env("TYPESAFE_BASE_URL", "https://api.typesafe.ai"), JevModel: env("JEV_MODEL", "jev-latest"),
		ResendAPIKey: os.Getenv("RESEND_API_KEY"), ResendBaseURL: env("RESEND_BASE_URL", "https://api.resend.com"),
		EmailFrom:      env("EMAIL_FROM", "Terry Alerts <onboarding@resend.dev>"),
		Window:         time.Duration(envInt("MESSAGE_WINDOW_MS", 120000)) * time.Millisecond,
		ScoreThreshold: envFloat("SCORE_THRESHOLD", 80), Retry: time.Duration(envInt("RETRY_MS", 60000)) * time.Millisecond,
		MaxBatchAttempts: envInt("MAX_BATCH_ATTEMPTS", 8), MaxImageBytes: envInt64("MAX_IMAGE_BYTES", 5*1024*1024),
		MaxImagePixels: envInt("MAX_IMAGE_PIXELS", 25000000), MaxImagesPerMessage: envInt("MAX_IMAGES_PER_MESSAGE", 6),
		MaxScoringImages: envInt("MAX_SCORING_IMAGES", 6), AllowPrivateImageURLs: envBool("ALLOW_PRIVATE_IMAGE_URLS", false),
		ResendRemoteImages: envBool("RESEND_REMOTE_IMAGES", true),
		CleanupInterval:    envDuration("CLEANUP_INTERVAL", time.Hour), ImageRetention: envDuration("IMAGE_RETENTION", 168*time.Hour),
		DatabaseRetention: envDuration("DB_RETENTION", 720*time.Hour),
		WSEnabled:         envBool("WS_ENABLED", true), WSURL: os.Getenv("WS_URL"), WSAuthToken: os.Getenv("WS_AUTH_TOKEN"),
		WSHeaders:           jsonEnv("WS_HEADERS_JSON", map[string]string{}),
		WSSubscribeMessages: jsonEnv("WS_SUBSCRIBE_MESSAGES_JSON", []json.RawMessage{}), WSChannelMap: channels,
		TargetAuthorIDs: authors,
	}
}

func (c Config) Validate() error {
	if len(c.TokenSecret) < 32 {
		return fmt.Errorf("TOKEN_SECRET 至少需要 32 个字符")
	}
	if c.TypeSafeAPIKey == "" || c.ResendAPIKey == "" {
		return fmt.Errorf("缺少 TYPESAFE_API_KEY 或 RESEND_API_KEY")
	}
	if c.WSEnabled {
		u, err := url.Parse(c.WSURL)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
			return fmt.Errorf("WS_URL 必须是有效的 ws/wss URL")
		}
	}
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("PUBLIC_URL 必须是完整 HTTP(S) URL")
	}
	if c.Window < time.Second || c.CleanupInterval <= 0 || c.ImageRetention <= 0 || c.DatabaseRetention <= 0 {
		return fmt.Errorf("窗口和清理周期配置无效")
	}
	if c.ScoreThreshold < 0 || c.ScoreThreshold > 100 || c.MaxBatchAttempts < 1 {
		return fmt.Errorf("评分阈值或重试配置无效")
	}
	for _, channel := range c.WSChannelMap {
		if !ValidChannel(channel) {
			return fmt.Errorf("未知频道：%s", channel)
		}
	}
	return nil
}

func LoadDotEnv(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
