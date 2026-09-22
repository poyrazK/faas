package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdTraceRendersAccountTraceEvidence(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/account/traces/" + traceID:
			_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
				TraceID: traceID,
				Matches: []api.AccountTraceMatch{
					{App: "alpha", Request: api.DebugTelemetryRequestItem{ID: "row-alpha", Route: "/checkout", Method: "GET", Status: 200, LatencyMS: 100}},
					{App: "beta", Request: api.DebugTelemetryRequestItem{ID: "row-beta", Route: "/checkout", Method: "GET", Status: 200, LatencyMS: 40}},
				},
				Invocations: []api.AccountTraceInvocation{
					{App: "alpha", ID: "inv-async", Source: "async_invoke", State: "completed", Attempts: 1, CreatedAt: "2026-09-22T10:00:00.020Z", CompletedAt: "2026-09-22T10:00:00.070Z"},
				},
				Spans: []api.DebugTelemetrySpan{
					{TraceID: traceID, SpanID: "root", Name: "edge.request", Kind: "server", StartTime: "2026-09-22T10:00:00Z", EndTime: "2026-09-22T10:00:00.100Z", DurationNanos: 100_000_000},
					{TraceID: traceID, SpanID: "guest", ParentSpanID: "root", Name: "guest.request", Kind: "server", StartTime: "2026-09-22T10:00:00.010Z", EndTime: "2026-09-22T10:00:00.050Z", DurationNanos: 40_000_000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdTrace([]string{traceID}); code != 0 {
		t.Fatalf("cmdTrace = %d", code)
	}
	got := stdout.String()
	for _, want := range []string{
		"TRACE " + traceID + " · 2 app match(es) · 1 invocation(s) · 2 span(s)",
		"alpha · GET /checkout · HTTP 200 · 100 ms",
		"beta · GET /checkout · HTTP 200 · 40 ms",
		"INVOCATIONS",
		"alpha · async_invoke inv-async · state=completed · attempts=1 · created_at=2026-09-22T10:00:00.020Z · duration=50.00ms",
		"WATERFALL (relative to earliest retained event)",
		"0.00ms → 100.00ms | edge.request [server]",
		"20.00ms → 50.00ms  | alpha · async_invoke inv-async · state=completed",
		"└─ edge.request [server] 100 ms",
		"└─ guest.request [server] 40.00 ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdTraceWatchPollsUntilTerminal(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		state := "pending"
		completedAt := ""
		if calls > 1 {
			state = "completed"
			completedAt = "2026-09-22T10:00:00.070Z"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
			TraceID: traceID,
			Invocations: []api.AccountTraceInvocation{
				{App: "alpha", ID: "inv-async", Source: "async_invoke", State: state,
					CreatedAt: "2026-09-22T10:00:00.020Z", CompletedAt: completedAt},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, stderr, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdTrace([]string{traceID, "--watch", "--interval", "1ms", "--timeout", "100ms"}); code != 0 {
		t.Fatalf("cmdTrace --watch = %d; stderr=%s", code, stderr())
	}
	if calls != 2 {
		t.Fatalf("trace requests=%d want 2", calls)
	}
	got := stdout.String()
	for _, want := range []string{
		"state=pending",
		"--- trace update ---",
		"state=completed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("watch output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdTraceWatchRetriesTransientAPIError(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/problem+json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusServiceUnavailable, Code: "temporarily_unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
			TraceID: traceID,
			Invocations: []api.AccountTraceInvocation{
				{App: "alpha", ID: "inv-async", Source: "async_invoke", State: "completed"},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	_, stderr, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdTrace([]string{traceID, "--watch", "--interval", "1ms", "--timeout", "1s"}); code != 0 {
		t.Fatalf("cmdTrace --watch = %d; stderr=%s", code, stderr())
	}
	if calls != 2 {
		t.Fatalf("trace requests=%d want 2", calls)
	}
	if !strings.Contains(stderr(), "temporary lookup failure") {
		t.Fatalf("stderr=%q missing retry diagnostic", stderr())
	}
}

func TestTraceWatchRetryDelayIsBounded(t *testing.T) {
	hint := int64(120)
	if got := traceWatchRetryDelay(1, &hint); got != traceWatchRetryMax {
		t.Fatalf("traceWatchRetryDelay with Retry-After = %s, want cap %s", got, traceWatchRetryMax)
	}
	if got := traceWatchRetryDelay(4, nil); got != 2*time.Second {
		t.Fatalf("traceWatchRetryDelay attempt 4 = %s, want 2s", got)
	}
}

func TestTraceWatchRetryableError(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, 425, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !traceWatchRetryableError(&APIError{Problem: api.Problem{Status: status}}) {
			t.Errorf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if traceWatchRetryableError(&APIError{Problem: api.Problem{Status: status}}) {
			t.Errorf("status %d should not be retryable", status)
		}
	}
	if !traceWatchRetryableError(errors.New("connection reset by peer")) {
		t.Fatal("transport errors should be retryable")
	}
}

func TestTraceWatchComplete(t *testing.T) {
	tests := []struct {
		name   string
		result api.AccountTraceLookupResponse
		want   bool
	}{
		{name: "pending", result: api.AccountTraceLookupResponse{Invocations: []api.AccountTraceInvocation{{State: "pending"}}}},
		{name: "dispatching", result: api.AccountTraceLookupResponse{Invocations: []api.AccountTraceInvocation{{State: "dispatching"}}}},
		{name: "terminal", result: api.AccountTraceLookupResponse{Invocations: []api.AccountTraceInvocation{{State: "failed"}}}, want: true},
		{name: "span-only", result: api.AccountTraceLookupResponse{Spans: []api.DebugTelemetrySpan{{Name: "edge.request"}}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := traceWatchComplete(tt.result); got != tt.want {
				t.Fatalf("traceWatchComplete=%t want %t", got, tt.want)
			}
		})
	}
}

func TestCmdTraceWatchTimesOut(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
			TraceID: traceID,
			Invocations: []api.AccountTraceInvocation{
				{App: "alpha", ID: "inv-async", Source: "async_invoke", State: "pending", CreatedAt: "2026-09-22T10:00:00.020Z"},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	_, stderr, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdTrace([]string{traceID, "--watch", "--interval", "1ms", "--timeout", "10ms"}); code != 3 {
		t.Fatalf("cmdTrace timeout = %d want 3; stderr=%s", code, stderr())
	}
	if !strings.Contains(stderr(), "watch timed out") {
		t.Fatalf("stderr=%q missing timeout message", stderr())
	}
}
