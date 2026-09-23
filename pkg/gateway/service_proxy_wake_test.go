// adr: 196
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// wakingProvider reports no endpoints until wakeDone is flipped, then reports
// one. It models the real registry across a park→wake transition: the
// pre-wake read is authoritative and empty, the post-wake read is
// authoritative and populated.
type wakingProvider struct {
	wakeDone *atomic.Bool
	calls    atomic.Int32
}

func (p *wakingProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	p.calls.Add(1)
	snapshot := ServiceEndpointsSnapshot{AppID: "app-orders"}
	if p.wakeDone.Load() {
		snapshot.Endpoints = []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}
	}
	return snapshot, nil
}

func newWakeTestProxy(t *testing.T, provider ServiceEndpointProvider, wake ServiceProxyWaker, forwarded *atomic.Int32) *ServiceProxy {
	t.Helper()
	now := time.Unix(100, 0)
	return NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Wake:      wake,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if forwarded != nil {
					forwarded.Add(1)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})
		},
		EndpointTTL: time.Minute,
		Now:         func() time.Time { return now },
	})
}

func serviceProxyGET(t *testing.T, proxy *ServiceProxy) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec
}

// A parked target must be woken and then served, not 503'd. This is the whole
// point of ADR-196: without it an internal service can never scale to zero.
func TestServiceProxyWakesParkedTargetAndForwards(t *testing.T) {
	var wakeDone atomic.Bool
	provider := &wakingProvider{wakeDone: &wakeDone}
	var wakes, forwarded atomic.Int32
	proxy := newWakeTestProxy(t, provider, func(context.Context, string) error {
		wakes.Add(1)
		wakeDone.Store(true)
		return nil
	}, &forwarded)

	rec := serviceProxyGET(t, proxy)

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status/body = %d/%q, want 200/ok", rec.Code, rec.Body.String())
	}
	if got := wakes.Load(); got != 1 {
		t.Errorf("wake calls = %d, want 1", got)
	}
	if got := forwarded.Load(); got != 1 {
		t.Errorf("forwarded requests = %d, want 1", got)
	}
}

// The endpoint lease exists to keep the hot path off Postgres, but the wake
// has just invalidated exactly what it caches. If the post-wake read is
// served from the stale empty snapshot, every cold internal call 503s no
// matter how fast the restore was. Pin the invalidation.
func TestServiceProxyInvalidatesEndpointLeaseAfterWake(t *testing.T) {
	var wakeDone atomic.Bool
	provider := &wakingProvider{wakeDone: &wakeDone}
	proxy := newWakeTestProxy(t, provider, func(context.Context, string) error {
		wakeDone.Store(true)
		return nil
	}, nil)

	if rec := serviceProxyGET(t, proxy); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (post-wake read served a stale empty lease)", rec.Code)
	}
	// One pre-wake read plus one post-wake read. A third would mean the
	// invalidation is not scoped to the wake path.
	if got := provider.calls.Load(); got != 2 {
		t.Errorf("provider reads = %d, want 2 (pre-wake + post-wake)", got)
	}
}

// A warm target must never pay for a wake attempt.
func TestServiceProxyDoesNotWakeWarmTarget(t *testing.T) {
	var wakeDone atomic.Bool
	wakeDone.Store(true)
	provider := &wakingProvider{wakeDone: &wakeDone}
	var wakes atomic.Int32
	proxy := newWakeTestProxy(t, provider, func(context.Context, string) error {
		wakes.Add(1)
		return nil
	}, nil)

	if rec := serviceProxyGET(t, proxy); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := wakes.Load(); got != 0 {
		t.Errorf("wake calls = %d, want 0 for a warm target", got)
	}
}

func TestServiceProxyWakeOutcomes(t *testing.T) {
	tests := []struct {
		name           string
		wake           ServiceProxyWaker
		wantStatus     int
		wantRetryAfter string
		wantBody       string
	}{
		{
			// A saturated wake queue is bounded and retryable, so the caller
			// gets the same Retry-After contract the public edge offers
			// rather than being left to hot-loop on a restoring dependency.
			name: "queue full surfaces retry-after",
			wake: func(context.Context, string) error {
				return &WakeQueueFullError{Depth: 512, Limit: 512, RetryAfter: 30 * time.Second}
			},
			wantStatus:     http.StatusServiceUnavailable,
			wantRetryAfter: "30",
			wantBody:       "wake queue is full",
		},
		{
			name:       "admission failure is 503",
			wake:       func(context.Context, string) error { return errors.New("no ram headroom") },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "could not be woken",
		},
		{
			// A wake that succeeds but produces nothing routable (the app is
			// at its plan concurrency ceiling) is not an error; it is simply
			// no replica, and keeps the pre-ADR-196 wording.
			name:       "wake produced no replica",
			wake:       func(context.Context, string) error { return nil },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "no healthy replicas",
		},
		{
			// nil waker preserves the pre-ADR-196 fail-fast behaviour for
			// wiring without a scheduler seam.
			name:       "nil waker keeps legacy behaviour",
			wake:       nil,
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "no healthy replicas",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var wakeDone atomic.Bool // never flipped: target stays parked
			provider := &wakingProvider{wakeDone: &wakeDone}
			var forwarded atomic.Int32
			proxy := newWakeTestProxy(t, provider, tc.wake, &forwarded)

			rec := serviceProxyGET(t, proxy)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to mention %q", rec.Body.String(), tc.wantBody)
			}
			if got := forwarded.Load(); got != 0 {
				t.Errorf("forwarded = %d, want 0 when no replica is routable", got)
			}
		})
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30"},
		{1500 * time.Millisecond, "2"},
		// A sub-second budget must never render "0": clients read that as
		// "retry immediately" and turn a restoring dependency into a hot loop.
		{100 * time.Millisecond, "1"},
		{0, "1"},
		{-time.Second, "1"},
	}
	for _, tc := range tests {
		if got := retryAfterSeconds(tc.in); got != tc.want {
			t.Errorf("retryAfterSeconds(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// errProvider fails every registry read.
type errProvider struct{ calls atomic.Int32 }

func (p *errProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	p.calls.Add(1)
	return ServiceEndpointsSnapshot{}, errors.New("registry unavailable")
}

// A registry failure is not a parked service. Waking on it would burn an
// admission slot to work around what is actually a control-plane read error,
// and would do it on every request while the registry stays down.
func TestServiceProxyDoesNotWakeOnRegistryError(t *testing.T) {
	var wakes atomic.Int32
	proxy := newWakeTestProxy(t, &errProvider{}, func(context.Context, string) error {
		wakes.Add(1)
		return nil
	}, nil)

	rec := serviceProxyGET(t, proxy)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "registry is unavailable") {
		t.Errorf("body = %q, want it to name the registry failure", rec.Body.String())
	}
	if got := wakes.Load(); got != 0 {
		t.Errorf("wake calls = %d, want 0 on a registry read error", got)
	}
}
