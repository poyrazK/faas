// adr: 1151
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

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

type frozenServiceFixture struct {
	proxy     *ServiceProxy
	provider  *parkingProvider
	frozen    atomic.Bool
	wakes     atomic.Int32
	mu        sync.Mutex
	evicted   []string
	withdrawn []string
}

// newFrozenServiceFixture routes to instance-a, which vmmd refuses as frozen
// (ADR-1151) until the wake thaws it under the same instance ID.
func newFrozenServiceFixture(t *testing.T, metrics *Metrics, targetAppID string) *frozenServiceFixture {
	t.Helper()
	f := &frozenServiceFixture{provider: &parkingProvider{}}
	f.provider.set(ServiceEndpoint{InstanceID: "instance-a", NodeID: "node-a", Port: 8080})
	f.frozen.Store(true)
	now := time.Unix(100, 0)
	record := func(list *[]string, instanceID string) {
		f.mu.Lock()
		*list = append(*list, instanceID)
		f.mu.Unlock()
		f.provider.remove(instanceID)
	}
	f.proxy = NewServiceProxy(ServiceProxyConfig{
		Provider: f.provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: targetAppID}, true, nil
		},
		Authorize: func(_ context.Context, caller, _ string) (ServiceCaller, error) {
			return ServiceCaller{AppID: caller}, nil
		},
		Wake: func(context.Context, string) error {
			f.wakes.Add(1)
			f.frozen.Store(false)
			f.provider.set(ServiceEndpoint{InstanceID: "instance-a", NodeID: "node-a", Port: 8080})
			return nil
		},
		EvictEndpoint:    func(_, instanceID string) { record(&f.evicted, instanceID) },
		WithdrawEndpoint: func(_, instanceID string) { record(&f.withdrawn, instanceID) },
		Metrics:          metrics,
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if f.frozen.Load() {
					markUndeliveredTargetErr(r.Context(), grpcerr.ToStatus(api.NewProblem(int(codes.Unavailable), api.CodeInstanceFrozen, "Instance frozen", "the guest is frozen")))
					http.Error(w, "frozen", http.StatusServiceUnavailable)
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

func (f *frozenServiceFixture) call(method, caller string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, caller)
	rec := httptest.NewRecorder()
	f.proxy.ServeHTTP(rec, req)
	return rec
}

// A call that reaches a just-frozen target must withdraw its routes without
// the stale-target quarantine or a breaker failure, then wake it: schedd
// thaws it under the same instance ID, which must serve the call. This holds
// for any method, because vmmd refused the forward before any guest saw it.
func TestServiceProxyThawsFrozenTargetUnderSameID(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		f := newFrozenServiceFixture(t, nil, "app-orders")
		rec := f.call(method, "app-client")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "instance-a") {
			t.Fatalf("%s: response = %d %q, want 200 from the thawed instance-a", method, rec.Code, rec.Body.String())
		}
		if got := f.wakes.Load(); got != 1 {
			t.Fatalf("%s: wakes = %d, want 1", method, got)
		}
		f.mu.Lock()
		evicted, withdrawn := f.evicted, f.withdrawn
		f.mu.Unlock()
		if len(evicted) != 0 || len(withdrawn) != 1 || withdrawn[0] != "instance-a" {
			t.Fatalf("%s: evicted = %v, withdrawn = %v; want instance-a withdrawn and nothing evicted", method, evicted, withdrawn)
		}
		if rec := f.call(method, "app-client"); rec.Code != http.StatusOK {
			t.Fatalf("%s: next call = %d %q, want the thawed instance still routable", method, rec.Code, rec.Body.String())
		}
	}
}

// adr: 1131
// A wake done by the re-route after a stale target still makes the call a
// cold edge call on the service map.
func TestServiceProxyCountsRerouteWakeAsColdEdgeCall(t *testing.T) {
	callerID, targetID := uuid.NewString(), uuid.NewString()
	m := NewMetrics()
	f := newFrozenServiceFixture(t, m, targetID)
	if rec := f.call(http.MethodGet, callerID); rec.Code != http.StatusOK {
		t.Fatalf("response = %d %q, want 200", rec.Code, rec.Body.String())
	}
	if got := testutil.ToFloat64(m.serviceDependencyEdgeWakes.WithLabelValues(callerID, targetID)); got != 1 {
		t.Fatalf("edge wakes = %g, want 1 for the call whose re-route woke the target", got)
	}
}
