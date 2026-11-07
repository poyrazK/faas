package livequery_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery"
)

type deadlineTransport func(*http.Request) (*http.Response, error)

func (f deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRemoteCompilerDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		parent, client, want time.Duration
	}{
		{"fallback", 0, 0, 30 * time.Second},
		{"long caller deadline", time.Minute, 0, time.Minute},
		{"short caller deadline", time.Second, 0, time.Second},
		{"client deadline", 0, 5 * time.Second, 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.parent > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.parent)
				defer cancel()
			}
			var remaining time.Duration
			hc := &http.Client{Timeout: tc.client, Transport: deadlineTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("compiler request has no deadline")
				}
				remaining = time.Until(deadline)
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			})}
			_, err := livequery.RemoteCompiler("https://compiler.example.test", hc)(ctx, "SELECT 1")
			if !errors.Is(err, livequery.ErrCompilerUnavailable) {
				t.Fatalf("unexpected compiler result: %v", err)
			}
			if remaining <= tc.want-time.Second/2 || remaining > tc.want {
				t.Fatalf("compiler budget = %s, want approximately %s", remaining, tc.want)
			}
			if hc.Timeout != tc.client || ctx.Err() != nil {
				t.Fatal("compiler changed a shared client or canceled its caller")
			}
		})
	}
}
