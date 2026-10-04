package source

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/httpio"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRSSAtomJSON(t *testing.T) {
	rss, err := os.ReadFile("../../testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, format, body string }{{"RSS", "rss", string(rss)}, {"Atom", "rss", `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>one</id><title>Codex reset tomorrow</title><link href="https://example.com/1"/><published>2026-10-02T10:00:00Z</published><content>Usage reset</content></entry></feed>`}, {"JSON", "json", `{"items":[{"id":"one","url":"https://example.com/1","content_text":"Codex reset tomorrow","date_published":"2026-10-02T10:00:00Z"}]}`}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := ParseFeed([]byte(tc.body), tc.format, domain.SourceInfo{Kind: domain.Community})
			if err != nil || len(items) != 1 || items[0].PublishedAt.IsZero() {
				t.Fatalf("items=%+v error=%v", items, err)
			}
		})
	}
}
func TestInvalidSources(t *testing.T) {
	for _, body := range []string{`{}`, `{"items":[{"text":"no id"}]}`, `<html>not RSS</html>`} {
		format := "json"
		if body[0] == '<' {
			format = "rss"
		}
		if _, err := ParseFeed([]byte(body), format, domain.SourceInfo{}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := ParseDocument([]byte(`<html><body>login required</body></html>`)); err == nil {
		t.Fatal("changed layout accepted")
	}
}
func TestXDisabledAndPagination(t *testing.T) {
	if _, err := (&X{}).Fetch(context.Background(), time.Now()); err != monitor.ErrSourceUnavailable {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/by/username/thsottiaux" {
			_, _ = io.WriteString(w, `{"data":{"id":"42"}}`)
			return
		}
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing credential")
		}
		if calls == 1 {
			_, _ = io.WriteString(w, `{"data":[{"id":"1","text":"Reset tomorrow","created_at":"2026-10-02T10:00:00Z"}],"meta":{"next_token":"next"}}`)
		} else {
			if r.URL.Query().Get("pagination_token") != "next" {
				t.Error("pagination token missing")
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"2","text":"Reset all propagated","created_at":"2026-10-02T11:00:00Z"}],"meta":{}}`)
		}
	}))
	defer server.Close()
	client := httpio.New(time.Second, 4096, noop.NewTracerProvider().Tracer("test"))
	adapter := X{Client: client, BaseURL: server.URL, Token: "fixture", Username: "thsottiaux"}
	items, err := adapter.Fetch(context.Background(), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || len(items) != 2 || calls != 2 {
		t.Fatalf("items=%d calls=%d error=%v", len(items), calls, err)
	}
}
func TestStatusInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer server.Close()
	_, err := (Status{Client: httpio.New(time.Second, 4096, nil), URL: server.URL}).Fetch(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("invalid status accepted")
	}
}
func FuzzParseFeed(f *testing.F) {
	f.Add([]byte(`<rss><channel></channel></rss>`))
	f.Add([]byte(`{"items":[]}`))
	f.Fuzz(func(_ *testing.T, body []byte) {
		if len(body) > 65536 {
			return
		}
		_, _ = ParseFeed(body, "rss", domain.SourceInfo{})
		_, _ = ParseFeed(body, "json", domain.SourceInfo{})
	})
}
