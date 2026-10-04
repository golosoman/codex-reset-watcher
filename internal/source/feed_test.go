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

func TestRSSRepostProvenanceWithoutTrustUpgrade(t *testing.T) {
	items, err := ParseFeed([]byte(`<rss><channel><item><guid>reddit-post</guid><link>https://www.reddit.com/r/codex/comments/one</link><title>Codex reset tomorrow</title><pubDate>Sun, 04 Oct 2026 10:00:00 GMT</pubDate><description><![CDATA[<a href="https://x.com/thsottiaux/status/123">Original announcement</a>]]></description></item></channel></rss>`), "rss", domain.SourceInfo{Kind: domain.Community})
	if err != nil || len(items) != 1 || items[0].CanonicalOriginID != "123" || items[0].Source.Kind != domain.Community {
		t.Fatalf("items=%+v error=%v", items, err)
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
