// adr: 1133
package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// undeliveredFixture serves one target whose instances can be marked dead.
// A call routed to a dead instance fails the way the bridge reports it:
// undelivered (vmmd NotFound before any guest) or merely stale.
type undeliveredFixture struct {
	proxy       *ServiceProxy
	mu          sync.Mutex
	endpoints   []ServiceEndpoint
	dead        map[string]bool
	undelivered bool
	wakes       atomic.Int32
	delivered   atomic.Int32 // requests that reached a live guest
}

func (f *undeliveredFixture) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: append([]ServiceEndpoint(nil), f.endpoints...)}, nil
}

func (f *undeliveredFixture) park(instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dead[instanceID] = true
}

func newUndeliveredFixture(t *testing.T, undelivered bool) *undeliveredFixture {
	t.Helper()
	f := &undeliveredFixture{
		endpoints:   []ServiceEndpoint{{InstanceID: "instance-1", NodeID: "node-a", Port: 8080}},
		dead:        map[string]bool{},
		undelivered: undelivered,
	}
	now := time.Unix(100, 0)
	f.proxy = NewServiceProxy(ServiceProxyConfig{
		Provider: f,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(_ context.Context, caller, _ string) (ServiceCaller, error) {
			return ServiceCaller{AppID: caller}, nil
		},
		Wake: func(context.Context, string) error {
			n := f.wakes.Add(1)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.endpoints = []ServiceEndpoint{{InstanceID: fmt.Sprintf("instance-woken-%d", n), NodeID: "node-a", Port: 8081}}
			return nil
		},
		// Production evicts from the routing cache the provider reads.
		EvictEndpoint: func(_, instanceID string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			kept := f.endpoints[:0:0]
			for _, e := range f.endpoints {
				if e.InstanceID != instanceID {
					kept = append(kept, e)
				}
			}
			f.endpoints = kept
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.mu.Lock()
				dead := f.dead[target.InstanceID]
				f.mu.Unlock()
				if dead {
					if f.undelivered {
						markUndeliveredTarget(r.Context())
					} else {
						markStaleTarget(r.Context())
					}
					http.Error(w, "gone", http.StatusServiceUnavailable)
					return
				}
				f.delivered.Add(1)
				body, _ := io.ReadAll(r.Body)
				_, _ = fmt.Fprintf(w, "ok from %s body=%q", target.InstanceID, body)
			})
		},
		EndpointTTL: 5 * time.Second,
		Now:         func() time.Time { return now },
	})
	return f
}

func (f *undeliveredFixture) call(t *testing.T, method string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://gateway/v1/internal/services/orders/work", body)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	f.proxy.ServeHTTP(rec, req)
	return rec
}

// A POST is never replayed after a stale failure that may have reached a
// guest. When the bridge proves no guest saw it, the re-route is safe and the
// caller gets the woken target's answer in the same request.
func TestServiceProxyReroutesUndeliveredNonIdempotentCall(t *testing.T) {
	f := newUndeliveredFixture(t, true)
	f.park("instance-1")
	rec := f.call(t, http.MethodPost, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "instance-woken-1") {
		t.Fatalf("POST = %d %q, want 200 from the woken instance", rec.Code, rec.Body.String())
	}
	if got := f.delivered.Load(); got != 1 {
		t.Fatalf("requests delivered to a guest = %d, want exactly 1", got)
	}
}

// Two stale events inside one retry-budget window: a quiet app's allowance
// (max(1, 10%)) covers only one replay. Undelivered re-routes do not spend
// it, so both calls recover; merely stale ones do, so the second fails.
func TestServiceProxyUndeliveredRerouteSkipsRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		undelivered bool
		wantSecond  int
	}{
		{name: "undelivered", undelivered: true, wantSecond: http.StatusOK},
		{name: "stale only", undelivered: false, wantSecond: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUndeliveredFixture(t, tc.undelivered)
			f.park("instance-1")
			if rec := f.call(t, http.MethodGet, nil); rec.Code != http.StatusOK {
				t.Fatalf("first GET = %d %q, want 200", rec.Code, rec.Body.String())
			}
			f.park("instance-woken-1")
			if rec := f.call(t, http.MethodGet, nil); rec.Code != tc.wantSecond {
				t.Fatalf("second GET = %d %q, want %d", rec.Code, rec.Body.String(), tc.wantSecond)
			}
		})
	}
}

// A body of unknown length (chunked, possibly a long-lived stream) is not
// buffered, so it is not replayable: it fails once, and the next call wakes
// the target. Small declared-length bodies are covered in
// service_proxy_body_test.go.
func TestServiceProxyUndeliveredCallWithBodyFailsOnce(t *testing.T) {
	f := newUndeliveredFixture(t, true)
	f.park("instance-1")
	body := io.NopCloser(strings.NewReader("payload")) // no GetBody: not rewindable
	if rec := f.call(t, http.MethodPost, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST with body = %d %q, want 503 (body not replayable)", rec.Code, rec.Body.String())
	}
	rec := f.call(t, http.MethodPost, io.NopCloser(strings.NewReader("payload")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `body="payload"`) {
		t.Fatalf("next POST = %d %q, want 200 with the body delivered", rec.Code, rec.Body.String())
	}
}
