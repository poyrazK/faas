package api_test

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type readRetryTransport func(*http.Request) (*http.Response, error)

func (f readRetryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func readRetryResponse(r *http.Request, status int, header http.Header, data string) *http.Response {
	return &http.Response{Request: r, StatusCode: status, Header: header, ContentLength: int64(len(data)), Body: io.NopCloser(strings.NewReader(data))}
}

func TestReadRetriesTransientResponsesAndRefusals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body, after string
		retry       bool
	}{
		{"gateway", 502, `gateway unavailable`, "", true},
		{"service", 503, `{"status":503,"code":"service_unavailable"}`, "", true},
		{"gateway timeout", 504, ``, "", true},
		{"rate limit", 429, `{"code":"api_rate_limited"}`, "0", true},
		{"missing rate delay", 429, `{"code":"api_rate_limited"}`, "", false},
		{"long delay", 503, ``, "31", false},
		{"bad delay", 503, ``, "secret-invalid-delay", false},
		{"quota", 429, `{"code":"quota_exceeded"}`, "0", false},
		{"disabled", 503, `{"code":"tenant_surfaces_not_enabled"}`, "0", false},
		{"authentication", 401, `{"code":"http_error","status":503}`, "0", false},
		{"authorization", 403, ``, "0", false},
		{"not found", 404, ``, "0", false},
		{"validation", 400, ``, "0", false},
		{"unknown failure", 503, `{"code":"operator_configuration_invalid"}`, "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := api.NewClient("https://example.test", "private-token")
			client := base.WithReadRetries(1)
			calls := 0
			client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				info, ok := api.ReadRetryAttemptFromContext(r.Context())
				if !ok || info.Attempt != calls || info.MaxAttempts != 2 || calls > 1 && info.Delay <= 0 {
					t.Errorf("bad attempt metadata: %+v, present=%t", info, ok)
				}
				if r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("read retry changed authentication or added an idempotency key")
				}
				if calls > 1 {
					return readRetryResponse(r, 200, nil, `{}`), nil
				}
				h := make(http.Header)
				if tc.after != "" {
					h.Set("Retry-After", tc.after)
				}
				return readRetryResponse(r, tc.status, h, tc.body), nil
			})
			_, err := client.UsageSummary(t.Context(), "2026-11")
			want := 1
			if tc.retry {
				want = 2
			}
			if calls != want || tc.retry && err != nil || !tc.retry && err == nil {
				t.Fatalf("calls=%d want=%d error=%v", calls, want, err)
			}
		})
	}
}

func TestReadRetriesBudgetDefaultAndClientIsolation(t *testing.T) {
	base := api.NewClient("https://example.test", "")
	for _, tc := range []struct{ count, want int }{{0, 1}, {-1, 1}, {2, 3}, {99, 6}} {
		client := base.WithReadRetries(tc.count)
		calls := 0
		client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return readRetryResponse(r, 503, nil, ``), nil
		})
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		_, err := client.UsageSummary(ctx, "")
		cancel()
		if calls != tc.want || err == nil {
			t.Fatalf("count=%d calls=%d want=%d err=%v", tc.count, calls, tc.want, err)
		}
	}
	var calls int
	base.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if _, ok := api.ReadRetryAttemptFromContext(r.Context()); ok {
			t.Error("retry policy mutated the base client")
		}
		return readRetryResponse(r, 503, nil, ``), nil
	})
	_, _ = base.UsageSummary(t.Context(), "")
	if calls != 1 {
		t.Fatalf("base client retried %d times", calls)
	}
}

func TestReadRetriesTransportClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: io.ErrUnexpectedEOF}, true},
		{"deadline", context.DeadlineExceeded, true},
		{"missing dns", &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"certificate", &net.OpError{Op: "read", Net: "tcp", Err: x509.UnknownAuthorityError{}}, false},
		{"cancelled", context.Canceled, false},
		{"unknown", errors.New("private failure text"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := api.NewClient("https://example.test", "").WithReadRetries(1)
			calls := 0
			client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, tc.err
				}
				return readRetryResponse(r, 200, nil, `{}`), nil
			})
			_, err := client.UsageSummary(t.Context(), "")
			want := 1
			if tc.retry {
				want = 2
			}
			if calls != want || tc.retry && err != nil || !tc.retry && err == nil {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestReadRetriesCancelledBeforeRequest(t *testing.T) {
	client := api.NewClient("https://example.test", "").WithReadRetries(1)
	calls := 0
	client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return readRetryResponse(r, 200, nil, `{}`), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client.UsageSummary(ctx, "")
	if calls != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read sent %d requests: %v", calls, err)
	}
}

func TestReadRetriesScopeAndBodyOwnership(t *testing.T) {
	for _, operation := range []string{"mutation", "upload", "stream", "unknown read", "malformed success", "duplicate delay", "allowed read"} {
		t.Run(operation, func(t *testing.T) {
			client := api.NewClient("https://example.test", "").WithReadRetries(1)
			calls := 0
			var closed atomic.Bool
			client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 && !closed.Load() {
					t.Error("previous response body remained open")
				}
				if operation != "allowed read" && operation != "malformed success" && operation != "duplicate delay" {
					if _, ok := api.ReadRetryAttemptFromContext(r.Context()); ok {
						t.Error("retry metadata on an excluded request")
					}
				}
				status, data := 503, ``
				if operation == "malformed success" {
					status, data = 200, `{bad json`
				}
				if calls > 1 {
					status, data = 200, `{}`
				}
				response := readRetryResponse(r, status, make(http.Header), data)
				if operation == "duplicate delay" {
					response.Header["Retry-After"] = []string{"0", "30"}
				}
				response.Body = &readRetryBody{Reader: strings.NewReader(data), closed: &closed}
				return response, nil
			})
			var err error
			switch operation {
			case "mutation":
				_, err = client.Rollback(t.Context(), "app")
			case "upload":
				_, err = client.DeployMultipart(t.Context(), "app", strings.NewReader("source"), "source.tgz", "node22", "index.handler", false, api.DeployAnnotations{})
			case "stream":
				var body io.ReadCloser
				body, err = client.StreamDeploymentLogs(t.Context(), "deployment", nil, 10, true)
				if body != nil {
					_ = body.Close()
				}
			case "unknown read":
				_, err = client.GetDeploymentURL(t.Context(), "deployment")
			default:
				_, err = client.UsageSummary(t.Context(), "")
			}
			want := 1
			if operation == "allowed read" {
				want = 2
			}
			if calls != want || operation == "allowed read" && err != nil || operation != "allowed read" && err == nil {
				t.Fatalf("calls=%d want=%d error=%v", calls, want, err)
			}
			if !closed.Load() {
				t.Fatal("response body was not closed")
			}
		})
	}
}

type readRetryBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b *readRetryBody) Close() error { b.closed.Store(true); return nil }

func TestReadRetriesRateDelayAndCancellation(t *testing.T) {
	var calls atomic.Int32
	var first atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			first.Store(time.Now().UnixNano())
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"api_rate_limited"}`)
			return
		}
		if time.Since(time.Unix(0, first.Load())) < time.Second {
			t.Error("retry occurred before Retry-After")
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	if _, err := api.NewClient(server.URL, "").WithReadRetries(1).UsageSummary(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("rate-limit attempts=%d", calls.Load())
	}
	client := api.NewClient("https://example.test", "").WithReadRetries(5)
	var closed atomic.Bool
	sent := make(chan struct{})
	var cancelCalls atomic.Int32
	client.HTTPClient().Transport = readRetryTransport(func(r *http.Request) (*http.Response, error) {
		response := readRetryResponse(r, 503, http.Header{"Retry-After": {"30"}}, ``)
		response.Body = &readRetryBody{Reader: strings.NewReader(""), closed: &closed}
		if cancelCalls.Add(1) == 1 {
			close(sent)
		} else {
			t.Error("unexpected replay during cancellation")
		}
		return response, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.UsageSummary(ctx, ""); done <- err }()
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	time.AfterFunc(100*time.Millisecond, cancel)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backoff ignored cancellation")
	}
	cancel()
	if !closed.Load() {
		t.Fatal("response body open while waiting")
	}
}

func TestReadRetryAfterAndRoutes(t *testing.T) {
	now := time.Date(2026, 11, 13, 13, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"0", 0, true}, {"2", 2 * time.Second, true}, {"30", 30 * time.Second, true}, {"31", 31 * time.Second, false}, {"-1", 0, false}, {"+1", 0, false}, {"invalid", 0, false}, {"18446744073709551615", 0, false},
		{now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second, true}, {now.Add(-time.Second).Format(http.TimeFormat), 0, true}, {now.Add(time.Minute).Format(http.TimeFormat), time.Minute, false},
	} {
		got, ok := api.ReadRetryAfter(tc.value, now)
		if ok != tc.ok || ok && got != tc.want {
			t.Errorf("Retry-After %q = %s,%t", tc.value, got, ok)
		}
	}
	for _, path := range []string{"/v1/apps", "/v1/apps/app?environment=preview", "/v1/apps/app/deployments", "/v1/apps/app/deployments/latest", "/v1/apps/app/deployments/id/summary", "/v1/apps/app/status", "/v1/apps/app/usage", "/v1/deployments/id", "/v1/deployments/id/stages", "/v1/usage/summary?month=2026-11"} {
		if !api.RetryableReadRoute("GET", path) || api.RetryableReadRoute("POST", path) {
			t.Errorf("wrong route eligibility: %s", path)
		}
	}
	for _, path := range []string{"/v1/apps/app/invoke", "/v1/apps/app/logs", "/v1/apps/%2e%2e", "/v1/apps/app%2finvoke", "/v1/deployments/id/logs", "/v1/queues/q/pull", "/v1/account/export", "/v1/usage/summary/", "https://example.test/v1/apps"} {
		if api.RetryableReadRoute("GET", path) {
			t.Errorf("unsafe route accepted: %s", path)
		}
	}
}
