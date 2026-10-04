package httpio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponses(t *testing.T) {
	for _, status := range []int{200, 400, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "fixture")
			}))
			defer server.Close()
			client := New(time.Second, 4096, nil)
			client.Attempts = 1
			body, err := client.Get(context.Background(), server.URL, nil)
			if status == 200 {
				if err != nil || string(body) != "fixture" {
					t.Fatalf("%s %v", body, err)
				}
			} else {
				var failure *StatusError
				if !errors.As(err, &failure) || failure.Code != status {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestTransientRetryAndSizeLimit(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := New(time.Second, 4096, nil)
	body, err := client.Get(context.Background(), server.URL, nil)
	if err != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
	client.MaxBody = 1
	if _, err := client.Get(context.Background(), server.URL, nil); err == nil {
		t.Fatal("oversized body accepted")
	}
}
func TestTimeoutAnd429AreBounded(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == 200 {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := New(time.Second, 4096, nil).Get(ctx, server.URL, nil)
			if err == nil {
				t.Fatal("deadline ignored")
			}
		})
	}
}
func TestNoSecretsInTransportErrors(t *testing.T) {
	_, err := New(time.Millisecond, 4096, nil).Get(context.Background(), "http://127.0.0.1:1/?token=secret", nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}
