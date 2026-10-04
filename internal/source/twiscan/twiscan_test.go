package twiscan

import (
	"os"
	"testing"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
)

func TestFixture(t *testing.T) {
	body, err := os.ReadFile("../../../testdata/twiscan.html")
	if err != nil {
		t.Fatal(err)
	}
	items, err := Parse(body)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v error=%v", items, err)
	}
	for _, item := range items {
		if item.CanonicalAuthor != "thsottiaux" || item.Source.Kind != domain.FirstPartyDerived || item.PublishedAt.IsZero() {
			t.Fatal("invalid provenance")
		}
	}
}
func TestChangedLayoutIsNotSuccess(t *testing.T) {
	for _, body := range []string{"<html>Login</html>", "<div id='clamp-123-0'>Reset</div>", ""} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatal("silent parser failure")
		}
	}
}
func FuzzParse(f *testing.F) {
	f.Add([]byte("<html></html>"))
	f.Fuzz(func(_ *testing.T, body []byte) {
		if len(body) < 65536 {
			_, _ = Parse(body)
		}
	})
}
