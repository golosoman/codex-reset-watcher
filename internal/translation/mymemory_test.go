package translation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type memoryCache map[string]string

func (m memoryCache) Translation(_ context.Context, key string) (string, bool, error) {
	value, ok := m[key]
	return value, ok, nil
}

func TestTranslationTimeoutHasShortCooldownAndSafeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	m := &MyMemory{HTTP: server.Client(), BaseURL: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := m.Translate(ctx, "A public Codex reset announcement.")
	if err == nil || err.Error() != "translation request timed out" || time.Until(m.retryAt) > 2*time.Minute {
		t.Fatalf("wrong timeout error or long cooldown: %v", err)
	}
}
func (m memoryCache) SaveTranslation(_ context.Context, key, text string) error {
	m[key] = text
	return nil
}

func TestTranslationCachesAndExpandsBankedTerm(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("langpair") != "en|ru" || !strings.Contains(r.URL.Query().Get("q"), "saved manual quota reset") || r.URL.Query().Has("key") {
			t.Errorf("invalid request: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"responseStatus":200,"quotaFinished":false,"responseData":{"translatedText":"Сохранённый ручной сброс для Codex &amp; ChatGPT."}}`)
	}))
	defer server.Close()
	cache := memoryCache{}
	for range 2 {
		// A fresh adapter shares the durable cache, as it would after a restart.
		m := &MyMemory{HTTP: server.Client(), BaseURL: server.URL, Cache: cache}
		got, err := m.Translate(context.Background(), "A banked reset for Codex & ChatGPT.")
		if err != nil || got != "Сохранённый ручной сброс для Codex & ChatGPT." {
			t.Fatalf("translation=%q error=%v", got, err)
		}
	}
	if calls != 1 || len(cache) != 1 {
		t.Fatalf("calls=%d cache=%d", calls, len(cache))
	}
}

func TestTranslationRejectsFailuresAndDoesNotRetryImmediately(t *testing.T) {
	for _, body := range []string{
		`broken`, `{}`, `{"responseStatus":403,"responseData":{"translatedText":"Ошибка"}}`,
		`{"responseStatus":200,"quotaFinished":true,"responseData":{"translatedText":"Лимит"}}`,
		`{"responseStatus":200,"responseData":{"translatedText":""}}`,
		`{"responseStatus":200,"responseData":{"translatedText":"MYMEMORY WARNING"}}`,
		strings.Repeat("x", 64*1024+1),
	} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cache := memoryCache{}
			m := &MyMemory{HTTP: server.Client(), BaseURL: server.URL, Cache: cache}
			for range 2 {
				if value, err := m.Translate(context.Background(), "A reset is coming."); err == nil || value != "" {
					t.Fatalf("value=%q err=%v", value, err)
				}
			}
			if len(cache) != 0 || calls != 1 {
				t.Fatalf("failed response cached or retried: calls=%d cache=%d", calls, len(cache))
			}
		})
	}
}

func TestTranslationHTTPFailureCancellationAndRussianInput(t *testing.T) {
	for _, code := range []int{403, 429, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		m := &MyMemory{HTTP: server.Client(), BaseURL: server.URL}
		if _, err := m.Translate(context.Background(), "The reset is available."); err == nil {
			t.Fatal("accepted HTTP failure")
		}
		server.Close()
	}
	m := &MyMemory{HTTP: &http.Client{}, BaseURL: "http://127.0.0.1:1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Translate(ctx, "The reset is available."); err == nil {
		t.Fatal("accepted cancellation")
	}
	got, err := m.Translate(context.Background(), "Лимиты Codex восстановлены.")
	if err != nil || got != "Лимиты Codex восстановлены." {
		t.Fatalf("Russian input: %q %v", got, err)
	}
}

func TestTranslationChunksRespectBytesAndPreserveText(t *testing.T) {
	for _, input := range []string{strings.Repeat("hello world. ", 100), strings.Repeat("🙂", 300), strings.Repeat("word", 300), strings.Repeat("a", 479) + "🙂word", strings.Repeat("Ж ", 500)} {
		parts := chunks(input)
		if strings.Join(parts, "") != input {
			t.Fatal("input lost during chunking")
		}
		for _, part := range parts {
			if len(part) > 500 || !utf8.ValidString(part) {
				t.Fatalf("invalid segment: bytes=%d", len(part))
			}
		}
	}
}

func TestTranslationNeverReturnsPartialResult(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(429)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"responseStatus": 200, "responseData": map[string]string{"translatedText": "Часть перевода"}})
	}))
	defer server.Close()
	cache := memoryCache{}
	m := &MyMemory{HTTP: server.Client(), BaseURL: server.URL, Cache: cache}
	text, err := m.Translate(context.Background(), strings.Repeat("Public reset news. ", 40))
	if err == nil || text != "" || len(cache) != 0 {
		t.Fatalf("partial translation leaked: %q %v", text, err)
	}
}
