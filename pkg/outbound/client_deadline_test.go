package outbound

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientDeadlineDefaultsAndOverrides(t *testing.T) {
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
			hc := &http.Client{Timeout: tc.client, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("outbound request has no deadline")
				}
				remaining = time.Until(deadline)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})}
			client, err := NewClient("https://outbound.example.test", "integration", "token", "app", hc)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(ctx, http.MethodGet, "/resource", nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if remaining <= tc.want-time.Second/2 || remaining > tc.want {
				t.Fatalf("request budget = %s, want approximately %s", remaining, tc.want)
			}
			if hc.Timeout != tc.client || client.HTTP.Timeout != tc.client {
				t.Fatal("request defaults mutated a shared HTTP client")
			}
		})
	}
}
