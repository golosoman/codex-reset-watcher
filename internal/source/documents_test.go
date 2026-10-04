package source

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/httpio"
)

func TestDocumentIgnoresNavigationAndScripts(t *testing.T) {
	paragraphs, err := ParseDocument([]byte(`<html><nav>Codex reset confirmed</nav><main><script>reset everyone</script><p>Usage limits for Codex reset every week.</p></main></html>`))
	if err != nil || len(paragraphs) != 1 || paragraphs[0] != "Usage limits for Codex reset every week." {
		t.Fatalf("paragraphs=%v err=%v", paragraphs, err)
	}
}

func TestEmptyMarkdownIsNotSuccessfulFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "\n") }))
	defer server.Close()
	_, err := (Documents{Client: httpio.New(time.Second, 4096, nil), URLs: []string{server.URL + "/empty.md"}}).Fetch(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("empty document accepted as successful fetch")
	}
}

func TestStatusMissingDateRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"incidents":[{"incident_updates":[{"id":"one","body":"Codex reset confirmed"}]}]}`)
	}))
	defer server.Close()
	_, err := (Status{Client: httpio.New(time.Second, 4096, nil), URL: server.URL}).Fetch(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("missing date accepted and watermark could advance")
	}
}
