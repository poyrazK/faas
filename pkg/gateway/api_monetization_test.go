package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// monetizationOn releases API monetization in tests of its enforcement.
func monetizationOn() bool { return true }

// adr: 20261018090210 — while API monetization is off the gateway enforces no
// consumer plan, spend cap or overdue hold.
func TestConsumerPlanUnenforcedWhileMonetizationOff(t *testing.T) {
	due := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for _, policy := range []ConsumerPlanPolicy{
		{SpendCapped: true, SpendCapMillicents: 100},
		{OverdueBlocked: true, OverdueDueAt: due},
		{MaxRequestsPerMinute: 1},
	} {
		store := &fakeConsumerPlanStore{policy: policy, decision: ConsumerPlanDecision{Scope: "minute", Limit: 1}}
		for _, on := range []bool{false, true} {
			h := (&Handler{apiMonetization: func() bool { return on }}).WithConsumerPlanStore(store)
			w := httptest.NewRecorder()
			admitted := h.enforceConsumerPlan(w, planRequest("GET /items"), &statusRecorder{}, App{ID: "app-1", AccountID: "acct-1"}, false)
			if admitted == on {
				t.Fatalf("policy %+v with monetization %t: admitted=%t code=%d", policy, on, admitted, w.Code)
			}
		}
	}
	if (&Handler{}).apiMonetizationEnabled() {
		t.Fatal("an unwired gate must keep monetization off")
	}
}

// adr: 20261018090210 — while API monetization is off a tenant surface
// response keeps its Gregale-Usage header, is not held for its usage, and a
// tenant held for an overdue invoice is served.
func TestTenantSurfaceWhileMonetizationOff(t *testing.T) {
	accountID, appID, tenantID, surfaceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Gregale-Usage", "tokens=7")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro,
		ConsumerAuthMode: api.ConsumerAuthModeOptional, RoutedSurfaceID: surfaceID, PlatformTenantID: tenantID},
		host: "units-customer.example", upstream: upstream.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	q, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	var on atomic.Bool
	store := &overdueTenantStore{}
	store.held.Store(true)
	h := NewHandlerWith(backend, NewMetrics(), nil).WithAPIMonetization(on.Load).WithTenantRequestBudgetStore(store)
	h.usageOutbox = q
	h.requestTelemetry = makeTestRecorder()
	request := func() *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://units-customer.example/generate", nil))
		return w
	}
	if w := request(); w.Code != http.StatusOK || w.Header().Get("Gregale-Usage") != "tokens=7" {
		t.Fatalf("monetization off: status=%d headers=%v", w.Code, w.Header())
	}
	for {
		item, ok, err := q.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if len(item.Event.MeterUnits) != 0 {
			t.Fatalf("app-reported units recorded while monetization is off: %+v", item.Event.MeterUnits)
		}
		if err := q.Ack(item); err != nil {
			t.Fatal(err)
		}
	}
	on.Store(true)
	if w := request(); w.Code != http.StatusPaymentRequired {
		t.Fatalf("monetization on: held tenant status=%d", w.Code)
	}
}
