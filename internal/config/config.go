package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

type Feed struct {
	Name   string            `json:"name"`
	URL    string            `json:"url"`
	Kind   domain.SourceKind `json:"kind"`
	Format string            `json:"format"`
}
type Config struct {
	Interval, Jitter, SourceTimeout, Lookback, MaxAge                                                              time.Duration
	Database, Listen, TelegramToken, ChatID, AdminChatID, XToken, XUsername, LLMKey, LLMModel, OTLPEndpoint, Level string
	NotifySignals, LLMEnabled                                                                                      bool
	Concurrency, FailureThreshold                                                                                  int
	Feeds                                                                                                          []Feed
	HelpURLs                                                                                                       []string
}

func Load(get func(string) string) (Config, error) {
	c := Config{Database: value(get, "DATABASE_PATH", "/data/watcher.db"), Listen: value(get, "HTTP_LISTEN_ADDR", ":8080"), TelegramToken: get("TELEGRAM_BOT_TOKEN"), ChatID: get("TELEGRAM_CHAT_ID"), AdminChatID: get("ADMIN_CHAT_ID"), XToken: get("X_BEARER_TOKEN"), XUsername: value(get, "X_USERNAME", "thsottiaux"), LLMKey: get("LLM_API_KEY"), LLMModel: get("LLM_MODEL"), OTLPEndpoint: get("OTEL_EXPORTER_OTLP_ENDPOINT"), Level: value(get, "LOG_LEVEL", "info")}
	for key, target := range map[string]*string{"TELEGRAM_BOT_TOKEN": &c.TelegramToken, "X_BEARER_TOKEN": &c.XToken, "LLM_API_KEY": &c.LLMKey} {
		if path := get(key + "_FILE"); path != "" {
			if *target != "" {
				return c, fmt.Errorf("%s and %s_FILE are mutually exclusive", key, key)
			}
			secret, err := os.ReadFile(path)
			if err != nil {
				return c, fmt.Errorf("read %s secret: %w", key, err)
			}
			*target = strings.TrimSpace(string(secret))
		}
	}
	if c.TelegramToken == "" || c.ChatID == "" {
		return c, errors.New("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}
	if _, err := strconv.ParseInt(c.ChatID, 10, 64); err != nil {
		return c, errors.New("TELEGRAM_CHAT_ID must be an integer")
	}
	if c.AdminChatID != "" {
		if _, err := strconv.ParseInt(c.AdminChatID, 10, 64); err != nil {
			return c, errors.New("ADMIN_CHAT_ID must be an integer")
		}
	}
	durations := []struct {
		key, def  string
		target    *time.Duration
		allowZero bool
	}{{"CHECK_INTERVAL", "1h", &c.Interval, false}, {"CHECK_JITTER", "5m", &c.Jitter, true}, {"SOURCE_TIMEOUT", "30s", &c.SourceTimeout, false}, {"INITIAL_LOOKBACK", "48h", &c.Lookback, false}, {"MAX_EVENT_AGE", "72h", &c.MaxAge, false}}
	for _, d := range durations {
		parsed, err := time.ParseDuration(value(get, d.key, d.def))
		if err != nil || parsed < 0 || parsed == 0 && !d.allowZero {
			return c, fmt.Errorf("invalid %s", d.key)
		}
		*d.target = parsed
	}
	if c.Jitter > c.Interval {
		return c, errors.New("CHECK_JITTER exceeds CHECK_INTERVAL")
	}
	for key, target := range map[string]*bool{"NOTIFY_SIGNALS": &c.NotifySignals, "LLM_ENABLED": &c.LLMEnabled} {
		def := "false"
		if key == "NOTIFY_SIGNALS" {
			def = "true"
		}
		parsed, err := strconv.ParseBool(value(get, key, def))
		if err != nil {
			return c, fmt.Errorf("invalid %s", key)
		}
		*target = parsed
	}
	if c.LLMEnabled && (c.LLMKey == "" || c.LLMModel == "") {
		return c, errors.New("LLM_API_KEY and LLM_MODEL required when LLM_ENABLED=true")
	}
	c.Concurrency = 3
	c.FailureThreshold = 3
	for key, target := range map[string]*int{"SOURCE_CONCURRENCY": &c.Concurrency, "ADMIN_FAILURE_THRESHOLD": &c.FailureThreshold} {
		if raw := get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 16 {
				return c, fmt.Errorf("invalid %s", key)
			}
			*target = n
		}
	}
	if c.OTLPEndpoint != "" {
		if err := validURL(c.OTLPEndpoint); err != nil {
			return c, fmt.Errorf("invalid OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
		}
	}
	if raw := get("EXTRA_FEEDS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Feeds); err != nil {
			return c, errors.New("invalid EXTRA_FEEDS_JSON")
		}
	}
	names := map[string]bool{"openai-status": true, "openai-help": true, "openai-docs": true, "tibo-x": true}
	for _, f := range c.Feeds {
		if f.Name == "" || names[f.Name] {
			return c, errors.New("feed names must be unique and nonempty")
		}
		names[f.Name] = true
		if f.Kind != domain.Community {
			return c, errors.New("extra feeds must be community; trust cannot be upgraded through configuration")
		}
		if f.Format != "rss" && f.Format != "json" {
			return c, errors.New("feed format must be rss or json")
		}
		if err := validURL(f.URL); err != nil {
			return c, fmt.Errorf("invalid feed URL: %w", err)
		}
	}
	c.HelpURLs = []string{"https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan"}
	if raw := get("OPENAI_HELP_URLS"); raw != "" {
		c.HelpURLs = strings.Split(raw, ",")
	}
	for _, u := range c.HelpURLs {
		if err := validURL(u); err != nil {
			return c, err
		}
		parsed, _ := url.Parse(u)
		if parsed.Hostname() != "help.openai.com" && parsed.Hostname() != "developers.openai.com" && parsed.Hostname() != "learn.chatgpt.com" {
			return c, errors.New("official documentation host not allowed")
		}
	}
	return c, nil
}

func value(get func(string) string, key, def string) string {
	if s := get(key); s != "" {
		return s
	}
	return def
}
func validURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" {
		return errors.New("HTTPS URL without credentials required")
	}
	return nil
}
