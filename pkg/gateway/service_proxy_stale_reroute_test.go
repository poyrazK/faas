// adr: 196
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// parkingProvider models the registry across a park: it lists the stopped
// instance until the wake replaces it with a fresh one.
type parkingProvider struct {
	mu        sync.Mutex
	endpoints []ServiceEndpoint
	calls     atomic.Int32
}

func (p *parkingProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	p.calls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: append([]ServiceEndpoint(nil), p.endpoints...)}, nil
}

func (p *parkingProvider) remove(instanceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.endpoints[:0:0]
	for _, endpoint := range p.endpoints {
		if endpoint.InstanceID != instanceID {
			kept = append(kept, endpoint)
		}
	}
	p.endpoints = kept
}

func (p *parkingProvider) set(endpoints ...ServiceEndpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endpoints = endpoints
}

type staleRerouteFixture struct {
	proxy    *ServiceProxy
	provider *parkingProvider
	wakes    atomic.Int32
	evicted  []string
	mu       sync.Mutex
}

// newStaleRerouteFixture serves instance-parked as the only cached endpoint;
// the bridge reports it gone (vmmd NotFound), exactly what a call sees when it
// lands inside the endpoint lease of a park, restart, or rollout drain.
func newStaleRerouteFixture(t *testing.T) *staleRerouteFixture {
	t.Helper()
	f := &staleRerouteFixture{provider: &parkingProvider{}}
	f.provider.set(ServiceEndpoint{InstanceID: "instance-parked", NodeID: "node-a", Port: 8080})
	now := time.Unix(100, 0)
	f.proxy = NewServiceProxy(ServiceProxyConfig{
		Provider: f.provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(_ context.Context, caller, _ string) (ServiceCaller, error) {
			return ServiceCaller{AppID: caller}, nil
		},
		Wake: func(context.Context, string) error {
			f.wakes.Add(1)
			f.provider.set(ServiceEndpoint{InstanceID: "instance-woken", NodeID: "node-a", Port: 8081})
			return nil
		},
		// Production wires PGBackend.EvictInstance, which also removes the
		// instance from the registry the provider reads.
		EvictEndpoint: func(appID, instanceID string) {
			f.mu.Lock()
			f.evicted = append(f.evicted, appID+"/"+instanceID)
			f.mu.Unlock()
			f.provider.remove(instanceID)
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target.InstanceID == "instance-parked" {
					markStaleTarget(r.Context())
					http.Error(w, "gone", http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte("ok from " + target.InstanceID))
			})
		},
		EndpointTTL: 5 * time.Second,
		Now:         func() time.Time { return now },
	})
	return f
}

func (f *staleRerouteFixture) call(method string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	f.proxy.ServeHTTP(rec, req)
	return rec
}

func (f *staleRerouteFixture) evictions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.evicted...)
}

// A retryable call whose only cached endpoint has just stopped must wake the
// app and succeed, not answer "no healthy replicas" for a parked service.
func TestServiceProxyWakesWhenOnlyEndpointWasJustParked(t *testing.T) {
	f := newStaleRerouteFixture(t)
	rec := f.call(http.MethodGet)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "instance-woken") {
		t.Fatalf("response = %d %q, want 200 from the woken instance", rec.Code, rec.Body.String())
	}
	if got := f.wakes.Load(); got != 1 {
		t.Fatalf("wakes = %d, want 1", got)
	}
	if got := f.evictions(); len(got) != 1 || got[0] != "app-orders/instance-parked" {
		t.Fatalf("evictions = %v, want the stopped instance evicted from the routing cache", got)
	}
}

// A non-idempotent call is never replayed, so it still fails once; the next
// call inside the same lease must find the stopped endpoint evicted and wake
// the app, not keep answering "no healthy replicas" until the lease expires.
func TestServiceProxyNextCallWakesAfterStaleNonIdempotentCall(t *testing.T) {
	f := newStaleRerouteFixture(t)
	if rec := f.call(http.MethodPost); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("first POST = %d %q, want 503 (not replayable)", rec.Code, rec.Body.String())
	}
	if got := f.wakes.Load(); got != 0 {
		t.Fatalf("wakes after first POST = %d, want 0", got)
	}
	rec := f.call(http.MethodPost)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "instance-woken") {
		t.Fatalf("second POST = %d %q, want 200 from the woken instance", rec.Code, rec.Body.String())
	}
	if got := f.wakes.Load(); got != 1 {
		t.Fatalf("wakes = %d, want 1", got)
	}
	// The initial lookup, the reroute's lease-bypassing read, and the
	// post-wake refresh.
	if got := f.provider.calls.Load(); got != 3 {
		t.Fatalf("provider calls = %d, want 3", got)
	}
}
