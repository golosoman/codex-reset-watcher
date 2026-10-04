package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

func TestSendResponses(t *testing.T) {
	cases := []struct {
		code      int
		body      string
		success   bool
		ambiguous bool
	}{{200, `{"ok":true,"result":{"message_id":42}}`, true, false}, {429, `{"ok":false,"error_code":429,"parameters":{"retry_after":12}}`, false, false}, {500, `error`, false, false}, {403, `{"ok":false,"error_code":403}`, false, false}, {200, `broken`, false, true}}
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
func TestFormatEscapesHTML(t *testing.T) {
	text := Format(monitor.Notification{Event: &domain.Event{Type: domain.ResetAnnounced, Summary: "<b>spoof</b>", Evidence: "A & B", Source: domain.SourceInfo{Name: "<source>"}, SourceURL: "javascript:alert(1)", DetectedAt: time.Now()}})
	if !strings.Contains(text, "&lt;b&gt;spoof&lt;/b&gt;") || strings.Contains(text, "javascript:") {
		t.Fatal(text)
	}
}
