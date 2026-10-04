package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
)

func TestMetrics(t *testing.T) {
	telemetry, err := New(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer telemetry.Close(context.Background())
	telemetry.Source(context.Background(), domain.SourceInfo{Name: "fixture"}, time.Second, 1, 1, 1, nil)
	telemetry.Cycle(context.Background(), monitor.Run{Duration: time.Second})
	telemetry.Delivery(context.Background(), "sent")
	telemetry.Event(context.Background(), domain.Event{Source: domain.SourceInfo{Name: "fixture"}, Type: domain.ResetPropagating})
	telemetry.Health("fixture", "stale")
	recorder := httptest.NewRecorder()
	telemetry.Handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, name := range []string{"watcher_checks_total", "watcher_source_requests_total", "watcher_items_received_total", "watcher_events_detected_total", "watcher_notifications_total", "watcher_last_success_timestamp", "watcher_check_duration_seconds", "watcher_source_stale", `event_type="reset_propagating"`} {
		if !strings.Contains(recorder.Body.String(), name) {
			t.Errorf("missing metric %s", name)
		}
	}
}
