// adr: 196
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newMeteredProxy builds a proxy with real Metrics and a controllable clock so
// the wake-latency histogram can be asserted deterministically.
func newMeteredProxy(t *testing.T, m *Metrics, provider ServiceEndpointProvider, wake ServiceProxyWaker, clock func() time.Time) *ServiceProxy {
	t.Helper()
	if clock == nil {
		fixed := time.Unix(100, 0)
		clock = func() time.Time { return fixed }
	}
	return NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Wake:      wake,
		Metrics:   m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		},
		EndpointTTL: time.Minute,
		Now:         clock,
	})
}

func meteredGET(t *testing.T, proxy *ServiceProxy) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec
}

func callCount(t *testing.T, m *Metrics, outcome ServiceCallOutcome) float64 {
	t.Helper()
	return testutil.ToFloat64(m.serviceCallTotal.WithLabelValues(string(outcome)))
}

// Internal calls never reach the public edge, so gateway_requests_total cannot
// see them. Without this counter an operator has no view of service-to-service
// traffic at all.
func TestServiceProxyMetricsOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		provider ServiceEndpointProvider
		wake     ServiceProxyWaker
		want     ServiceCallOutcome
	}{
		{
			name:     "warm target counts forwarded",
			provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}},
			want:     ServiceCallForwarded,
		},
		{
			name:     "registry failure is distinct from a parked service",
			provider: &errProvider{},
			wake:     func(context.Context, string) error { return nil },
			want:     ServiceCallRegistryUnavailable,
		},
		{
			name:     "saturated wake queue",
			provider: staticProvider{},
			wake: func(context.Context, string) error {
				return &WakeQueueFullError{Depth: 512, Limit: 512, RetryAfter: 30 * time.Second}
			},
			want: ServiceCallWakeQueueFull,
		},
		{
			name:     "admission failure",
			provider: staticProvider{},
			wake:     func(context.Context, string) error { return errors.New("no headroom") },
			want:     ServiceCallWakeFailed,
		},
		{
			name:     "wake produced nothing routable",
			provider: staticProvider{},
			wake:     func(context.Context, string) error { return nil },
			want:     ServiceCallNoReplica,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMetrics()
			proxy := newMeteredProxy(t, m, tc.provider, tc.wake, nil)

			meteredGET(t, proxy)

			if got := callCount(t, m, tc.want); got != 1 {
				t.Errorf("%s = %v, want 1", tc.want, got)
			}
		})
	}
}

// The warm/cold split is the internal cold-start rate — the signal ADR-196
// defers the depends_on wake-ahead decision on. A wake that succeeds must be
// counted as "woken", not folded into "forwarded".
func TestServiceProxyMetricsCountsWokenSeparately(t *testing.T) {
	m := NewMetrics()
	var wakeDone atomic.Bool
	provider := &wakingProvider{wakeDone: &wakeDone}
	proxy := newMeteredProxy(t, m, provider, func(context.Context, string) error {
		wakeDone.Store(true)
		return nil
	}, nil)

	if rec := meteredGET(t, proxy); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if got := callCount(t, m, ServiceCallWoken); got != 1 {
		t.Errorf("woken = %v, want 1", got)
	}
	if got := callCount(t, m, ServiceCallForwarded); got != 0 {
		t.Errorf("forwarded = %v, want 0 (a cold call must not be counted warm)", got)
	}
}

// Only the cold path observes latency; a warm call must record nothing, or the
// histogram's percentiles collapse toward zero and stop describing wakes.
func TestServiceProxyMetricsWakeLatency(t *testing.T) {
	m := NewMetrics()
	var wakeDone atomic.Bool
	provider := &wakingProvider{wakeDone: &wakeDone}

	now := time.Unix(100, 0)
	clock := func() time.Time { return now }
	proxy := newMeteredProxy(t, m, provider, func(context.Context, string) error {
		now = now.Add(250 * time.Millisecond) // the restore
		wakeDone.Store(true)
		return nil
	}, clock)

	if rec := meteredGET(t, proxy); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := testutil.CollectAndCount(m.serviceWakeLatency); got != 1 {
		t.Fatalf("histogram series = %d, want 1", got)
	}

	// A second, now-warm call must not add an observation.
	before := testutil.ToFloat64(m.serviceCallTotal.WithLabelValues(string(ServiceCallForwarded)))
	meteredGET(t, proxy)
	if after := testutil.ToFloat64(m.serviceCallTotal.WithLabelValues(string(ServiceCallForwarded))); after != before+1 {
		t.Errorf("forwarded = %v, want %v (second call should be warm)", after, before+1)
	}
}

// An idle node must render zeros rather than absent series: a dashboard cannot
// otherwise distinguish "no internal traffic" from "the proxy is not wired".
func TestServiceProxyMetricsPreInstantiatesOutcomes(t *testing.T) {
	m := NewMetrics()
	if got := testutil.CollectAndCount(m.serviceCallTotal); got != len(ServiceCallOutcomes) {
		t.Errorf("pre-instantiated series = %d, want %d", got, len(ServiceCallOutcomes))
	}
	for _, outcome := range ServiceCallOutcomes {
		if got := callCount(t, m, outcome); got != 0 {
			t.Errorf("%s = %v, want 0", outcome, got)
		}
	}
}

// A nil Metrics must stay a no-op: the pre-metrics test corpus and single-box
// dev wiring construct proxies without one.
func TestServiceProxyMetricsNilIsSafe(t *testing.T) {
	proxy := newMeteredProxy(t, nil,
		staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}}, nil, nil)
	if rec := meteredGET(t, proxy); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with nil metrics", rec.Code)
	}
}
