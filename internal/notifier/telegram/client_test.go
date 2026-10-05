package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

func TestSendResponses(t *testing.T) {
	cases := []struct {
		code      int
		body      string
		success   bool
		ambiguous bool
	}{{200, `{"ok":true,"result":{"message_id":42}}`, true, false}, {429, `{"ok":false,"error_code":429,"parameters":{"retry_after":12}}`, false, false}, {500, `error`, false, false}, {403, `{"ok":false,"error_code":403}`, false, false}, {200, `broken`, false, true}, {200, `{"ok":true,"result":{}}`, false, true}, {200, `{}`, false, true}}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.code)+tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			id, err := (Client{HTTP: server.Client(), Token: "fixture-secret", BaseURL: server.URL}).Send(context.Background(), monitor.Notification{ChatID: "1", Message: "test"})
			if (err == nil) != tc.success {
				t.Fatalf("id=%d error=%v", id, err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("token leaked")
			}
			var ambiguous monitor.AmbiguousDelivery
			if errors.As(err, &ambiguous) != tc.ambiguous {
				t.Fatalf("ambiguous=%v error=%v", tc.ambiguous, err)
			}
		})
	}
}

type stubTranslator struct {
	text string
	err  error
	seen string
}

func (s *stubTranslator) Translate(_ context.Context, text string) (string, error) {
	s.seen = text
	return s.text, s.err
}

func TestSendIncludesTranslationOrSafeFallback(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "translated", true: "fallback"}[failure], func(t *testing.T) {
			translator := &stubTranslator{text: "Сброс <b>лимитов</b> скоро."}
			if failure {
				translator.text = ""
				translator.err = errors.New("translation unavailable")
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload struct {
					Text string `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if calls == 2 {
					if payload.Text != "Проверка сервиса" {
						t.Error("operational message was altered")
					}
					_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":43}}`)
					return
				}
				if !strings.Contains(payload.Text, "A reset is coming.") || !strings.Contains(payload.Text, "https://example.com/post") {
					t.Error("lost original or source link")
				}
				if failure && !strings.Contains(payload.Text, "Перевод сейчас недоступен") {
					t.Error("missing honest fallback")
				}
				if !failure && (!strings.Contains(payload.Text, "Сброс &lt;b&gt;лимитов&lt;/b&gt; скоро.") || !strings.Contains(payload.Text, "Перевод на русский")) {
					t.Error("missing translation or HTML escaped incorrectly")
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
			}))
			defer server.Close()
			notification := monitor.Notification{ChatID: "1", Event: &domain.Event{Type: domain.ResetAnnounced, Evidence: "A reset is coming.", Summary: "Объявлен будущий сброс.", SourceURL: "https://example.com/post", Source: domain.SourceInfo{Kind: domain.Aggregator}}}
			client := Client{HTTP: server.Client(), Token: "fixture-secret", BaseURL: server.URL, Translator: translator}
			id, err := client.Send(context.Background(), notification)
			if err != nil || id != 42 || calls != 1 || translator.seen != notification.Event.Evidence {
				t.Fatalf("id=%d err=%v calls=%d evidence=%q", id, err, calls, translator.seen)
			}
			if _, err := client.Send(context.Background(), monitor.Notification{ChatID: "1", Message: "Проверка сервиса"}); err != nil {
				t.Fatal(err)
			}
			if translator.seen != notification.Event.Evidence {
				t.Fatal("operational message sent to translator")
			}
		})
	}
}

func TestTranslatedMessageFitsTelegramLimitAndPreservesTrust(t *testing.T) {
	e := &domain.Event{Type: domain.ResetPropagating, Scope: "unknown", Evidence: strings.Repeat("A & B ", 500), Summary: strings.Repeat("Текст ", 500), ExpectedWindow: strings.Repeat("soon ", 500), Source: domain.SourceInfo{Name: strings.Repeat("source", 50), Kind: domain.Aggregator}, SourceURL: "https://example.com/post", CanonicalOriginURL: "https://x.com/example/status/123"}
	for range 100 {
		e.Observations = append(e.Observations, domain.AccountObservation{Plan: "Pro20x", Status: "received"})
	}
	formatted := format(monitor.Notification{Event: e}, strings.Repeat("Переведённый текст & ", 500))
	plain := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(formatted, ""))
	if length := len(utf16.Encode([]rune(plain))); length > 4096 {
		t.Fatalf("message too large: %d", length)
	}
	if !strings.Contains(formatted, "не официальное подтверждение") || !strings.Contains(formatted, "Машинный перевод") {
		t.Fatal("translation changed source trust")
	}
}
func TestFormatEscapesHTML(t *testing.T) {
	text := Format(monitor.Notification{Event: &domain.Event{Type: domain.ResetAnnounced, Summary: "<b>spoof</b>", Evidence: "A & B", Source: domain.SourceInfo{Name: "<source>"}, SourceURL: "javascript:alert(1)", DetectedAt: time.Now()}})
	if !strings.Contains(text, "&lt;b&gt;spoof&lt;/b&gt;") || strings.Contains(text, "javascript:") {
		t.Fatal(text)
	}
}
