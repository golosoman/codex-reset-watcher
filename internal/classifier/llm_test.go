package classifier

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

type memoryCache struct {
	values map[string]domain.Classification
}

func (c *memoryCache) Classification(_ context.Context, key string) (domain.Classification, bool, error) {
	v, ok := c.values[key]
	return v, ok, nil
}
func (c *memoryCache) SaveClassification(_ context.Context, key string, v domain.Classification) error {
	c.values[key] = v
	return nil
}

func TestLLMStructuredOutputTrustAndCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["store"] != false {
			t.Error("request stores publication")
		}
		text := payload["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if format["strict"] != true {
			t.Error("structured output not strict")
		}
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"{\"type\":\"global_reset_confirmed\",\"confidence\":0.99,\"reason\":\"reset\",\"evidence\":\"Codex reset may be happening\",\"scope\":\"global\"}"}]}]}`)
	}))
	defer server.Close()
	composite := Composite{Rules: Rules{}, Model: &LLM{HTTP: server.Client(), URL: server.URL, Model: "test", Key: "fixture"}, Cache: &memoryCache{values: map[string]domain.Classification{}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	item := domain.Item{Text: "Codex reset may be happening", Source: domain.SourceInfo{Kind: domain.Community}}
	for range 2 {
		got, err := composite.Classify(context.Background(), item)
		if err != nil || got.Type != domain.ResetSignal {
			t.Fatalf("classification: %+v %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("LLM called %d times", calls)
	}
}

func TestLLMRejectsInventedEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"{\"type\":\"reset_announced\",\"confidence\":0.9,\"reason\":\"reset\",\"evidence\":\"invented\",\"scope\":\"global\"}"}]}]}`)
	}))
	defer server.Close()
	_, err := (LLM{HTTP: server.Client(), URL: server.URL, Model: "test"}).Classify(context.Background(), domain.Item{Text: "Codex reset", Source: domain.SourceInfo{Kind: domain.FirstParty}})
	if err == nil {
		t.Fatal("invented evidence accepted")
	}
}
