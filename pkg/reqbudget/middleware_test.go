package reqbudget

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// testClock is a per-test fake clock used by middleware tests. The
// middleware calls cfg.Now() once per request to stamp Budget.Started;
// tests advance the clock and observe how Remaining / cancellation
// respond.
type testClock struct{ v atomicInt64 }

type atomicInt64 struct{ v int64 }

func newTestClock(t time.Time) *testClock { return &testClock{v: atomicInt64{v: t.UnixNano()}} }
func (c *testClock) now() time.Time       { return time.Unix(0, c.v.v) }

// noopHandler is the canonical "happy path" handler used by tests
// that don't care about the inner work — it returns 200 with an
// empty body.
func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// errorEnvelope is the decoded RFC 7807 problem+json payload the
// middleware writes on deadline-exceeded.
type errorEnvelope struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Limit   string `json:"limit,omitempty"`
	DocsURL string `json:"docs_url,omitempty"`
}

func newCfg(t *testing.T, total, ceiling time.Duration) (MiddlewareConfig, *M, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg, "gateway")
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return MiddlewareConfig{
		Default:  total,
		Max:      ceiling,
		Route:    "forward",
		Endpoint: "POST:/payment",
		Metrics:  m,
		Now:      newTestClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)).now,
	}, m, reg
}

func TestMiddleware_OK(t *testing.T) {
	cfg, _, reg := newCfg(t, 3*time.Second, 30*time.Second)
	h := cfg.Middleware(noopHandler())
	req := httptest.NewRequest(http.MethodPost, "/payment", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("ok: status = %d, want 200", rr.Code)
	}
	if body := rr.Body.String(); body != "ok" {
		t.Fatalf("ok: body = %q, want \"ok\"", body)
	}
	// Verify the histogram has the outcome=set observation by
	// gathering the metric directly through the registry.
	if got := gatherSeriesCount(t, reg, "request_budget_seconds"); got != 1 {
		t.Fatalf("ok: histogram series count = %d, want 1", got)
	}
}

func TestMiddleware_Exceeded(t *testing.T) {
	// Total 100ms budget, handler sleeps 250ms — must fire 504.
	cfg, m, _ := newCfg(t, 100*time.Millisecond, 30*time.Second)
	clk := newTestClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	cfg.Now = clk.now

	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(250 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			// Don't write — the middleware will write the 504.
			return
		}
	})
	h := cfg.Middleware(slowHandler)
	req := httptest.NewRequest(http.MethodPost, "/payment", nil)
	req.Header.Set(requestIDHeader, "middleware-req-1")
	rr := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("exceeded: ServeHTTP took %v, want < 200ms (must short-circuit on ctx cancel)", elapsed)
	}
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("exceeded: status = %d, want 504", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
		t.Fatalf("exceeded: Content-Type = %q, want problem+json", got)
	}
	// Decode and check the stable code.
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("exceeded: body unmarshal: %v", err)
	}
	if env.Code != "request_budget_exceeded" {
		t.Fatalf("exceeded: code = %q, want request_budget_exceeded", env.Code)
	}
	if env.Status != http.StatusGatewayTimeout {
		t.Fatalf("exceeded: status = %d, want 504", env.Status)
	}
	if got := rr.Header().Get(requestBudgetErrorCodeHeader); got != "request_budget_exceeded" {
		t.Fatalf("exceeded: error code header = %q, want request_budget_exceeded", got)
	}
	if got := rr.Header().Get(requestIDHeader); got != "middleware-req-1" {
		t.Fatalf("exceeded: request id header = %q, want middleware-req-1", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("exceeded: cache-control = %q, want no-store", got)
	}
	// Counter must increment on exceed.
	if got := testutil.ToFloat64(m.RequestBudgetExceededTotal.WithLabelValues("forward", "POST:/payment", "gateway")); got != 1 {
		t.Fatalf("exceeded: counter = %v, want 1", got)
	}
}

func TestMiddleware_Cancelled(t *testing.T) {
	// Client cancels before deadline; middleware records
	// outcome=cancelled and does NOT write the 504 (cancellation
	// isn't an internal problem to surface to a dead client).
	cfg, _, reg := newCfg(t, 3*time.Second, 30*time.Second)
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	h := cfg.Middleware(slowHandler)

	ctx, cancel := contextWithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/payment", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// outcome=cancelled observation must be recorded.
	if got := gatherSeriesCount(t, reg, "request_budget_seconds"); got != 1 {
		t.Fatalf("cancelled: histogram series count = %d, want 1", got)
	}
	// exceeded counter must NOT have incremented — cancellation isn't
	// a budget-exceeded event.
	if got := gatherSeriesCount(t, reg, "request_budget_exceeded_total"); got != 0 {
		t.Fatalf("cancelled: exceeded counter series count = %d, want 0", got)
	}
}

func TestMiddleware_ValidationRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     MiddlewareConfig
		wantErr string
	}{
		{"default_zero", MiddlewareConfig{Default: 0, Max: time.Second, Route: "x"}, "Default must be > 0"},
		{"max_lt_default", MiddlewareConfig{Default: time.Second, Max: 500 * time.Millisecond, Route: "x"}, "Max must be >= Default"},
		{"route_empty", MiddlewareConfig{Default: time.Second, Max: time.Second, Route: ""}, "Route must be set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewMiddlewareConfig(tc.cfg)
			if err == nil {
				t.Fatalf("validation: no error, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validation: err = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestMiddleware_NewMetrics_RejectsBadNamespace(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := NewMetrics(reg, "bogus"); err == nil {
		t.Fatal("NewMetrics(\"bogus\"): no error, want one")
	}
}

func TestMiddleware_NoBudgetMeansIdentityNoOp(t *testing.T) {
	// A handler that does not itself install a budget should still
	// work — the middleware must not surface an artificial exceeded
	// when r.Context() is cancelled externally.
	cfg, _, _ := newCfg(t, 3*time.Second, 30*time.Second)
	h := cfg.Middleware(noopHandler())
	req := httptest.NewRequest(http.MethodPost, "/payment", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("noop: status = %d, want 200", rr.Code)
	}
}

func TestBudget_Observe_ClampsToTotal(t *testing.T) {
	cfg, _, reg := newCfg(t, 100*time.Millisecond, 1*time.Second)
	b := Budget{Total: 100 * time.Millisecond, Route: "forward", Endpoint: "POST:/payment"}
	// Simulate an exceeded outcome: remaining at attach was negative
	// (test fault) — observe must clamp to [0, Total].
	cfg.observe(b, "exceeded")
	// Histogram series must exist.
	if got := gatherSeriesCount(t, reg, "request_budget_seconds"); got != 1 {
		t.Fatalf("observe: histogram series count = %d, want 1", got)
	}
}

// TestMiddleware_DownstreamHopInheritsBudget exercises the
// end-to-end propagation contract: a handler that reads the
// inbound Budget via reqbudget.FromContext must see the
// middleware-stamped values. ADR-093 / PR-F wiring test.
func TestMiddleware_DownstreamHopInheritsBudget(t *testing.T) {
	cfg, _, _ := newCfg(t, 3*time.Second, 30*time.Second)
	var (
		mu                sync.Mutex
		observedBudget    Budget
		observedHasBudget bool
	)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := FromContext(r.Context())
		mu.Lock()
		observedBudget = b
		observedHasBudget = ok
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	h := cfg.Middleware(downstream)
	req := httptest.NewRequest(http.MethodPost, "/payment", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("downstream propagation: status = %d, want 200", rr.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if !observedHasBudget {
		t.Fatalf("downstream: no Budget on inner ctx (middleware failed to stamp)")
	}
	if observedBudget.Route != "forward" {
		t.Errorf("downstream: Budget.Route = %q, want forward", observedBudget.Route)
	}
	if observedBudget.Endpoint != "POST:/payment" {
		t.Errorf("downstream: Budget.Endpoint = %q, want POST:/payment", observedBudget.Endpoint)
	}
	if observedBudget.Total != 3*time.Second {
		t.Errorf("downstream: Budget.Total = %v, want 3s", observedBudget.Total)
	}
}

func TestMiddleware_DefaultForSelectsRouteBudget(t *testing.T) {
	cfg, _, _ := newCfg(t, 3*time.Second, 30*time.Second)
	cfg.DefaultFor = func(r *http.Request) time.Duration {
		if IsSyncInvokeRequest(r) {
			return 30 * time.Second
		}
		return 0
	}
	var got Budget
	h := cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		got, ok = FromContext(r.Context())
		if !ok {
			t.Fatal("inner handler did not receive a budget")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/apps/demo/invoke", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("sync invoke: status = %d, want 200", rr.Code)
	}
	if got.Total != 30*time.Second {
		t.Fatalf("sync invoke budget = %v, want 30s", got.Total)
	}

	got = Budget{}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/apps/demo/invoke/async", nil))
	if got.Total != 3*time.Second {
		t.Fatalf("async invoke budget = %v, want 3s", got.Total)
	}
}

func TestIsSyncInvokeRequest(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/v1/apps/demo/invoke", true},
		{http.MethodPost, "/v1/apps/demo/invoke/async", false},
		{http.MethodGet, "/v1/apps/demo/invoke", false},
		{http.MethodPost, "/v1/apps//invoke", false},
		{http.MethodPost, "/v1/apps/demo/invoked", false},
	}
	for _, tc := range cases {
		t.Run(tc.method+"_"+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if got := IsSyncInvokeRequest(r); got != tc.want {
				t.Fatalf("IsSyncInvokeRequest(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestMiddleware_EndToEndTimeoutEnforced is the ADR-093 §14
// metal-acceptance smoke: a 100ms budget with a 1s slow
// downstream fake must 504 in <200ms with a stable
// `request_budget_exceeded` envelope. Companion to
// TestMiddleware_Exceeded (which tests the same shape with a
// synthetic handler sleep) — this test exercises the same
// wiring from the outer middleware through a slow downstream
// fake, matching the production gatewayd-public → gatewayd-internal
// shape.
func TestMiddleware_EndToEndTimeoutEnforced(t *testing.T) {
	cfg, m, _ := newCfg(t, 100*time.Millisecond, 30*time.Second)
	clk := newTestClock(time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	cfg.Now = clk.now

	slowDownstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a stuck downstream. The handler must exit
		// on r.Context() cancellation — mirroring the cancel
		// propagating through the dial-with-timeout /
		// forwardproxy RoundTrip paths.
		select {
		case <-time.After(1 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	})
	h := cfg.Middleware(slowDownstream)
	req := httptest.NewRequest(http.MethodPost, "/payment", nil)
	rr := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("end-to-end: ServeHTTP took %v, want < 200ms (ctx cancel must propagate)", elapsed)
	}
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("end-to-end: status = %d, want 504", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
		t.Fatalf("end-to-end: Content-Type = %q, want problem+json", got)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("end-to-end: body unmarshal: %v", err)
	}
	if env.Code != "request_budget_exceeded" {
		t.Fatalf("end-to-end: code = %q, want request_budget_exceeded", env.Code)
	}
	if got := testutil.ToFloat64(m.RequestBudgetExceededTotal.WithLabelValues("forward", "POST:/payment", "gateway")); got != 1 {
		t.Fatalf("end-to-end: counter = %v, want 1", got)
	}
}

// gatherSeriesCount counts the number of distinct label tuples that
// have been observed for the metric family metricName on the
// supplied registry. HistogramVec / CounterVec series only
// materialize after at least one WithLabelValues+Observe/Inc call,
// so a count >= 1 confirms the middleware actually observed.
func gatherSeriesCount(t *testing.T, reg prometheus.Gatherer, metricName string) int {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	count := 0
	for _, mf := range mfs {
		if mf.GetName() != "gateway_"+metricName {
			continue
		}
		count += len(mf.GetMetric())
	}
	return count
}

// contextWithCancel is a thin wrapper around t.Context() + cancel
// that returns a context and its cancel func — the stdlib ctx
// variant t.Context() returns only the context.
func contextWithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
}
