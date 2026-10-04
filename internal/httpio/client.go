package httpio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type StatusError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *StatusError) Error() string   { return fmt.Sprintf("HTTP status %d", e.Code) }
func (e *StatusError) Transient() bool { return e.Code == 429 || e.Code >= 500 }

type Client struct {
	HTTP     *http.Client
	MaxBody  int64
	Attempts int
	Tracer   trace.Tracer
}

func New(timeout time.Duration, maxBody int64, tracer trace.Tracer) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}, MaxBody: maxBody, Attempts: 3, Tracer: tracer}
}

func RetryAfter(header string) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil {
		return min(time.Duration(max(seconds, 0))*time.Second, time.Hour)
	}
	if date, err := http.ParseTime(header); err == nil {
		return max(time.Until(date), 0)
	}
	return 0
}

func Pause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func Backoff(attempt int) time.Duration {
	return min(time.Second*time.Duration(1<<min(attempt, 10)), 30*time.Minute) + time.Duration(rand.IntN(1000))*time.Millisecond
}

// Get retries only transient failures; response and transport errors never expose URL secrets.
func (c *Client) Get(ctx context.Context, address string, headers http.Header) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid source URL")
	}
	if c.Tracer != nil {
		var span trace.Span
		ctx, span = c.Tracer.Start(ctx, "source.http")
		defer span.End()
	}
	for attempt := 0; attempt < c.Attempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, errors.New("create source request")
		}
		request.Header = headers.Clone()
		if request.Header == nil {
			request.Header = make(http.Header)
		}
		request.Header.Set("User-Agent", "codex-reset-watcher/0.2.0 (+https://github.com/golosoman/codex-reset-watcher)")
		response, callErr := c.HTTP.Do(request)
		delay := Backoff(attempt)
		if callErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, c.MaxBody+1))
			closeErr := response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				if readErr != nil || closeErr != nil {
					return nil, errors.New("read source response")
				}
				if int64(len(body)) > c.MaxBody {
					return nil, errors.New("source response exceeds size limit")
				}
				return body, nil
			}
			statusErr := &StatusError{Code: response.StatusCode, RetryAfter: RetryAfter(response.Header.Get("Retry-After"))}
			if !statusErr.Transient() || attempt == c.Attempts-1 {
				return nil, statusErr
			}
			delay = max(delay, statusErr.RetryAfter)
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		} else if attempt == c.Attempts-1 {
			return nil, errors.New("source transport failure")
		}
		if err := Pause(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("source retries exhausted")
}
