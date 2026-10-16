// adr: 196
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
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
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
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

func TestServiceProxyDependencyHealthCountsFinalOutcomeByTrustedDeployment(t *testing.T) {
	appID, deploymentID := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name              string
		protocol          string
		status            int
		grpcStatus        string
		declareGRPCStatus bool
		wantResult        string
	}{
		{name: "successful dependency", status: http.StatusOK, wantResult: "success"},
		{name: "failed dependency", status: http.StatusBadGateway, wantResult: "error"},
		{name: "successful gRPC dependency", protocol: "grpc", status: http.StatusOK, grpcStatus: "0", declareGRPCStatus: true, wantResult: "success"},
		{name: "failed gRPC dependency", protocol: "grpc", status: http.StatusOK, grpcStatus: "14", declareGRPCStatus: true, wantResult: "error"},
		{name: "missing gRPC status", protocol: "grpc", status: http.StatusOK, wantResult: "error"},
		{name: "malformed gRPC status", protocol: "grpc", status: http.StatusOK, grpcStatus: "unknown", declareGRPCStatus: true, wantResult: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMetrics()
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "target", NodeID: "n", Port: 8080}}},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "app-orders", AppProtocol: tc.protocol}, true, nil
				},
				ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
					return appID, deploymentID, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: appID, AccountID: "account"}, nil
				},
				Metrics: m,
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if tc.protocol == "grpc" {
							w.Header().Set("Content-Type", "application/grpc")
							if tc.declareGRPCStatus {
								w.Header().Set("Trailer", "grpc-status")
							}
						}
						w.WriteHeader(tc.status)
						if tc.protocol == "grpc" {
							_, _ = w.Write([]byte("payload"))
							if tc.declareGRPCStatus {
								w.Header().Set("grpc-status", tc.grpcStatus)
							}
						}
					})
				},
			})
			req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
			req.Header.Set(ServiceProxyCallerAppHeader, appID)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("response status = %d, want %d", rec.Code, tc.status)
			}
			if got := testutil.ToFloat64(m.serviceDependencyCalls.WithLabelValues(appID, deploymentID, tc.wantResult)); got != 1 {
				t.Fatalf("dependency outcome count = %g, want 1", got)
			}
			other := "success"
			if other == tc.wantResult {
				other = "error"
			}
			if got := testutil.ToFloat64(m.serviceDependencyCalls.WithLabelValues(appID, deploymentID, other)); got != 0 {
				t.Fatalf("other dependency outcome count = %g, want 0", got)
			}
		})
	}
}

func TestServiceProxyEmitsUnsampledCallerTargetEdge(t *testing.T) {
	callerID, targetID := uuid.NewString(), uuid.NewString()
	m := NewMetrics()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "target", NodeID: "n", Port: 8080}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: callerID}, nil
		},
		Metrics: m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
		},
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, callerID)
	proxy.ServeHTTP(httptest.NewRecorder(), req)
	if got := testutil.ToFloat64(m.serviceDependencyEdges.WithLabelValues(callerID, targetID, "error")); got != 1 {
		t.Fatalf("caller-target error count = %g, want 1", got)
	}
	if got := testutil.CollectAndCount(m.serviceDependencyDuration); got != 1 {
		t.Fatalf("caller-target latency series = %d, want 1", got)
	}
	metric := &dto.Metric{}
	observer := m.serviceDependencyDuration.WithLabelValues(callerID, targetID, "error")
	if err := observer.(prometheus.Metric).Write(metric); err != nil {
		t.Fatalf("dependency duration metric write: %v", err)
	}
	if got := histogramExemplarTraceID(t, metric); got != "" {
		t.Fatalf("unsampled dependency exemplar trace_id = %q, want empty", got)
	}
}

func TestServiceProxyDependencyHealthCountsManagedRoutingFailures(t *testing.T) {
	appID, deploymentID := uuid.NewString(), uuid.NewString()
	m := NewMetrics()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return appID, deploymentID, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: appID, AccountID: "account"}, nil
		},
		Metrics: m,
		Forward: func(Target) http.Handler { return http.NotFoundHandler() },
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, appID)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := testutil.ToFloat64(m.serviceDependencyCalls.WithLabelValues(appID, deploymentID, "error")); got != 1 {
		t.Fatalf("managed routing error count = %g, want 1", got)
	}
}

func TestServiceDependencyCounterPreinstantiatesCoverageSentinel(t *testing.T) {
	m := NewMetrics()
	if got := testutil.CollectAndCount(m.serviceDependencyCalls); got != 2 {
		t.Fatalf("dependency outcome series = %d, want success/error coverage sentinels", got)
	}
}

func TestServiceProxyReportsBindingDenialSeparately(t *testing.T) {
	m := NewMetrics()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, ErrServiceProxyBindingDenied
		},
		Metrics: m,
	})
	rec := meteredGET(t, proxy)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "has not declared") {
		t.Fatalf("response = %d %q, want binding-specific 403", rec.Code, rec.Body.String())
	}
	if got := callCount(t, m, ServiceCallBindingDenied); got != 1 {
		t.Fatalf("binding_denied = %v, want 1", got)
	}
	if got := callCount(t, m, ServiceCallDenied); got != 0 {
		t.Fatalf("denied = %v, want 0 for a binding policy rejection", got)
	}
}

// adr: 239
func TestServiceProxyReportsTargetCallerDenialSeparately(t *testing.T) {
	m := NewMetrics()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, ErrServiceProxyCallerDenied
		},
		Metrics: m,
	})
	rec := meteredGET(t, proxy)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "target does not allow") {
		t.Fatalf("response = %d %q, want target-specific 403", rec.Code, rec.Body.String())
	}
	if got := callCount(t, m, ServiceCallCallerDenied); got != 1 {
		t.Fatalf("caller_denied = %v, want 1", got)
	}
}

func TestServiceProxyReportsPreviewDenialSeparately(t *testing.T) {
	m := NewMetrics()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, ErrServiceProxyPreviewDenied
		},
		Metrics: m,
	})
	rec := meteredGET(t, proxy)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "does not accept calls from preview apps") {
		t.Fatalf("response = %d %q, want preview-specific 403", rec.Code, rec.Body.String())
	}
	if got := callCount(t, m, ServiceCallPreviewDenied); got != 1 {
		t.Fatalf("preview_denied = %v, want 1", got)
	}
	if got := callCount(t, m, ServiceCallDenied); got != 0 {
		t.Fatalf("denied = %v, want 0 for preview policy rejection", got)
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
	kinds := []string{"latency", "http_status", "http_malformed_json", "http_truncated_body", "http_response_lost"}
	if got := testutil.CollectAndCount(m.serviceChaosInjected); got != len(kinds) {
		t.Errorf("pre-instantiated chaos kind series = %d, want %d", got, len(kinds))
	}
	for _, kind := range kinds {
		if got := testutil.ToFloat64(m.serviceChaosInjected.WithLabelValues(kind)); got != 0 {
			t.Errorf("chaos kind %s = %v, want 0", kind, got)
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

// edgeWakeProvider lists no endpoint until the wake installs one.
type edgeWakeProvider struct {
	mu        sync.Mutex
	endpoints []ServiceEndpoint
}

func (p *edgeWakeProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ServiceEndpointsSnapshot{Endpoints: append([]ServiceEndpoint(nil), p.endpoints...)}, nil
}

// ADR-1131: the service map's cold-call share needs to know which edge calls
// had to restore a parked target. A woken call counts once; a warm call on
// the same edge does not.
func TestServiceProxyCountsEdgeCallsThatWokeTheTarget(t *testing.T) {
	callerID, targetID := uuid.NewString(), uuid.NewString()
	m := NewMetrics()
	provider := &edgeWakeProvider{}
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: callerID}, nil
		},
		Wake: func(context.Context, string) error {
			provider.mu.Lock()
			defer provider.mu.Unlock()
			provider.endpoints = []ServiceEndpoint{{InstanceID: "target", NodeID: "n", Port: 8080}}
			return nil
		},
		Metrics: m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		},
	})
	call := func() {
		req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, callerID)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	}
	call() // parked: the proxy wakes the target
	call() // warm
	if got := testutil.ToFloat64(m.serviceDependencyEdges.WithLabelValues(callerID, targetID, "success")); got != 2 {
		t.Fatalf("edge calls = %g, want 2", got)
	}
	if got := testutil.ToFloat64(m.serviceDependencyEdgeWakes.WithLabelValues(callerID, targetID)); got != 1 {
		t.Fatalf("edge wakes = %g, want 1 (only the call that restored the target)", got)
	}
}

// ADR-1131/ADR-1133: a call whose leased endpoint has just parked is
// re-routed, and the wake that re-route does still makes it a cold edge call.
func TestServiceProxyCountsRerouteWakeAsColdEdgeCall(t *testing.T) {
	callerID, targetID := uuid.NewString(), uuid.NewString()
	m := NewMetrics()
	provider := &parkingProvider{}
	provider.set(ServiceEndpoint{InstanceID: "instance-parked", NodeID: "node-a", Port: 8080})
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: callerID}, nil
		},
		Wake: func(context.Context, string) error {
			provider.set(ServiceEndpoint{InstanceID: "instance-woken", NodeID: "node-a", Port: 8081})
			return nil
		},
		EvictEndpoint: func(_, instanceID string) { provider.remove(instanceID) },
		Metrics:       m,
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target.InstanceID == "instance-parked" {
					markUndeliveredTarget(r.Context())
					http.Error(w, "gone", http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			})
		},
		EndpointTTL: 5 * time.Second,
	})
	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/internal/services/orders", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, callerID)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %q, want 200 from the woken instance", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(m.serviceDependencyEdgeWakes.WithLabelValues(callerID, targetID)); got != 1 {
		t.Fatalf("edge wakes = %g, want 1 for the call whose re-route woke the target", got)
	}
}
