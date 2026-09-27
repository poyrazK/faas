package outbound

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestHandlerRetriesTransientSafeRequestInsideSingleAdmission(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if providerCalls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	integration := testIntegration(t, server.URL, "secret", []string{"app-1"}, 100, 10, 10)
	integration.MaxRetries = 2
	dailyLimit := int64(1)
	integration.DailyRequestLimit = &dailyLimit
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	backend := NewMemoryBackend()
	handler, err := NewHandler(resolver, backend, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if first.Code != http.StatusOK || first.Body.String() != "ok" {
		t.Fatalf("retried response = %d %q", first.Code, first.Body.String())
	}
	if got := providerCalls.Load(); got != 2 {
		t.Fatalf("provider attempts = %d, want 2", got)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodGet, nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second admission = %d, want daily-budget rejection 429", second.Code)
	}
	if got := providerCalls.Load(); got != 2 {
		t.Fatalf("retry attempts consumed more than one daily admission: provider attempts=%d", got)
	}
}

func TestHandlerDoesNotRetryUnsafeOrUnselectedRequests(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		body      io.Reader
		query     string
		override  string
		firstCode int
		wantCalls int32
	}{
		{name: "post", method: http.MethodPost, body: strings.NewReader("body"), firstCode: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "get body", method: http.MethodGet, body: strings.NewReader("body"), firstCode: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "method override", method: http.MethodGet, override: "POST", firstCode: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "query method override", method: http.MethodGet, query: "?_method=POST", firstCode: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "unselected 500", method: http.MethodGet, firstCode: http.StatusInternalServerError, wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: tc.firstCode,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("retryable response")),
					Request:    req,
				}, nil
			})
			origin, err := url.Parse("https://provider.example")
			if err != nil {
				t.Fatal(err)
			}
			integration := testIntegration(t, origin.String(), "secret", []string{"app-1"}, 100, 10, 10)
			integration.MaxRetries = 2
			resolver, err := NewStaticResolver([]Integration{integration})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHandler(resolver, NewMemoryBackend(), &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}

			req := gatewayRequest(Prefix+integration.ID+"/v1/items"+tc.query, "secret", "app-1", tc.method, tc.body)
			if tc.override != "" {
				req.Header.Set("X-HTTP-Method-Override", tc.override)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if got := calls.Load(); got != tc.wantCalls {
				t.Fatalf("provider attempts = %d, want %d", got, tc.wantCalls)
			}
			if rr.Code != tc.firstCode {
				t.Fatalf("response status = %d, want %d", rr.Code, tc.firstCode)
			}
		})
	}
}

func TestHandlerRetriesTransientNetworkFailure(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})
	integration := testIntegration(t, "https://provider.example", "secret", []string{"app-1"}, 100, 10, 10)
	integration.MaxRetries = 1
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, gatewayRequest(Prefix+integration.ID+"/v1/items", "secret", "app-1", http.MethodHead, nil))
	if rr.Code != http.StatusNoContent || calls.Load() != 2 {
		t.Fatalf("response=%d provider attempts=%d; want 204/2", rr.Code, calls.Load())
	}
}

func TestOutboundRetryDelayHonorsAndCapsRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "backoff", want: initialRetryDelay},
		{name: "numeric", value: "0", want: 0},
		{name: "numeric capped", value: "30", want: maxRetryDelay},
		{name: "date capped", value: now.Add(30 * time.Second).Format(http.TimeFormat), want: maxRetryDelay},
		{name: "past date", value: now.Add(-time.Second).Format(http.TimeFormat), want: 0},
		{name: "invalid falls back", value: "later", want: initialRetryDelay},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outboundRetryDelay(tc.value, 0, now); got != tc.want {
				t.Fatalf("retry delay = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestWaitForOutboundRetryHonorsRequestContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := waitForOutboundRetry(ctx, time.Second)
	if err == nil || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("retry wait error=%v elapsed=%s; want prompt deadline cancellation", err, time.Since(started))
	}
}
