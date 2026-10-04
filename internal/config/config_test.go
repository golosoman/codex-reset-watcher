package config

import (
	"testing"
)

func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		valid  bool
	}{{"no Telegram", nil, false}, {"optional credentials missing", map[string]string{"TELEGRAM_BOT_TOKEN": "fixture", "TELEGRAM_CHAT_ID": "1"}, true}, {"LLM enabled without key", map[string]string{"TELEGRAM_BOT_TOKEN": "fixture", "TELEGRAM_CHAT_ID": "1", "LLM_ENABLED": "true"}, false}, {"bad duration", map[string]string{"TELEGRAM_BOT_TOKEN": "fixture", "TELEGRAM_CHAT_ID": "1", "CHECK_INTERVAL": "-1s"}, false}, {"feed trust escalation", map[string]string{"TELEGRAM_BOT_TOKEN": "fixture", "TELEGRAM_CHAT_ID": "1", "EXTRA_FEEDS_JSON": `[{"name":"spoof","url":"https://example.com/feed","kind":"official","format":"rss"}]`}, false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(func(key string) string { return tc.values[key] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
