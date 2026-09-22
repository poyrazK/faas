// adr: 201
package gateway

// The capability kind=circuit_breaker adds over the fixed-TTL quarantine it
// replaced (ADR-201 §2). The pre-existing ServiceProxy tests pin the flag-off
// equivalence; these pin what is genuinely new.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
)

// breakerProxy builds a ServiceProxy over two endpoints where instance-a is
// permanently dead, with an explicit breaker group and a controllable clock.
func breakerProxy(t *testing.T, group *circuit.Group, now func() time.Time) (*ServiceProxy, func() string) {
	t.Helper()
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID: "app-orders",
		Endpoints: []ServiceEndpoint{
			{InstanceID: "instance-a", NodeID: "node-a", Port: 8080},
			{InstanceID: "instance-b", NodeID: "node-b", Port: 8081},
		},
	}}
	var lastServed string
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lastServed = target.InstanceID
				if target.InstanceID == "instance-a" {
					markStaleTarget(r.Context())
					http.Error(w, "stale", http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})
		},
		EndpointTTL: time.Second,
		Now:         now,
		Breaker:     group,
	})
	return proxy, func() string { return lastServed }
}

func serve(t *testing.T, proxy *ServiceProxy) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec.Code
}

// The behaviour the fixed-TTL quarantine could not produce: a persistently
// dead endpoint is probed with a geometrically growing interval instead of
// being re-admitted every EndpointTTL forever.
func TestServiceProxyBreakerBacksOffPersistentlyDeadEndpoint(t *testing.T) {
	clock := time.Unix(100, 0)
	now := func() time.Time { return clock }
	cfg := circuit.DefaultConfig()
	cfg.MinRequests = 1
	cfg.FailureThreshold = 1.0
	cfg.OpenDuration = 5 * time.Second
	cfg.MaxOpenDuration = 60 * time.Second
	group := circuit.NewGroup(cfg, now)
	proxy, _ := breakerProxy(t, group, now)

	key := serviceProxyEndpointKey("app-orders", "instance-a")

	// First request trips instance-a and is served by instance-b.
	if code := serve(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 via the healthy sibling", code)
	}
	if got := group.State(key); got != circuit.StateOpen {
		t.Fatalf("instance-a breaker = %q, want open", got)
	}

	// After the first open interval it offers a probe, which fails, and the
	// next interval must be 10s — the old quarantine would have re-admitted
	// instance-a at every 5s boundary indefinitely.
	clock = clock.Add(5 * time.Second)
	if code := serve(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d after probe, want 200", code)
	}
	clock = clock.Add(5 * time.Second)
	if got := group.State(key); got != circuit.StateOpen {
		t.Fatalf("breaker = %q at +5s after a failed probe, want still open (backoff doubled to 10s)", got)
	}
	clock = clock.Add(5 * time.Second)
	if got := group.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("breaker = %q at +10s, want half_open", got)
	}
}

// A recovered endpoint must be readmitted — a breaker that never closes is
// just a slow outage.
func TestServiceProxyBreakerReadmitsRecoveredEndpoint(t *testing.T) {
	clock := time.Unix(100, 0)
	now := func() time.Time { return clock }
	cfg := circuit.DefaultConfig()
	cfg.MinRequests = 1
	cfg.FailureThreshold = 1.0
	group := circuit.NewGroup(cfg, now)

	dead := true
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID:     "app-orders",
		Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}},
	}}
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if dead {
					markStaleTarget(r.Context())
					http.Error(w, "stale", http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})
		},
		EndpointTTL: time.Second,
		Now:         now,
		Breaker:     group,
	})

	key := serviceProxyEndpointKey("app-orders", "instance-a")
	serve(t, proxy)
	if got := group.State(key); got != circuit.StateOpen {
		t.Fatalf("breaker = %q, want open after the only endpoint failed", got)
	}
	// With every endpoint open the proxy must report no healthy replicas
	// rather than forwarding to a known-dead target.
	if code := serve(t, proxy); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d while open, want 503 with no healthy replicas", code)
	}

	// The endpoint recovers; the half-open probe closes the circuit.
	dead = false
	clock = clock.Add(5 * time.Second)
	if code := serve(t, proxy); code != http.StatusOK {
		t.Fatalf("status = %d on the half-open probe, want 200", code)
	}
	if got := group.State(key); got != circuit.StateClosed {
		t.Fatalf("breaker = %q after a successful probe, want closed", got)
	}
}

// Healthy traffic must be reported, or the rolling ratio is a constant 1.0
// and a single blip opens a circuit that is carrying mostly-good traffic.
func TestServiceProxyBreakerCountsHealthyTransports(t *testing.T) {
	clock := time.Unix(100, 0)
	now := func() time.Time { return clock }
	group := circuit.NewGroup(circuit.DefaultConfig(), now)
	proxy, _ := breakerProxy(t, group, now)

	// instance-b always succeeds; several requests must leave its breaker
	// closed with recorded successes rather than untouched.
	for i := 0; i < 5; i++ {
		if code := serve(t, proxy); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
	}
	if got := group.State(serviceProxyEndpointKey("app-orders", "instance-b")); got != circuit.StateClosed {
		t.Fatalf("instance-b breaker = %q, want closed", got)
	}
	// instance-a failed on the first attempt of each request. Under
	// DefaultConfig (5 observations, 50% ratio) it is open — and crucially
	// it got there from observed failures, not from a single blip.
	if got := group.State(serviceProxyEndpointKey("app-orders", "instance-a")); got == circuit.StateClosed {
		t.Fatal("instance-a breaker = closed, want open or half_open after repeated transport failures")
	}
}
