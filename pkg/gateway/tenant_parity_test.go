package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

type overdueTenantStore struct{ held atomic.Bool }

func (s *overdueTenantStore) AdmitTenantRequest(context.Context, string, string) (TenantRequestBudgetDecision, error) {
	if s.held.Load() {
		return TenantRequestBudgetDecision{Scope: TenantBudgetScopeOverdue, OverdueDueAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}, nil
	}
	return TenantRequestBudgetDecision{Allowed: true}, nil
}

// adr: 1319, 1324 — a tenant surface request's app-reported units reach its
// usage event; a tenant held for an overdue invoice gets 402
// tenant_invoice_overdue before the upstream and without billable usage.
func TestTenantSurfaceUnitsAndOverdueHold(t *testing.T) {
	accountID, appID, tenantID, surfaceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	var forwarded atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
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
	store := &overdueTenantStore{}
	h := NewHandlerWith(backend, NewMetrics(), nil).WithAPIMonetization(monetizationOn).WithTenantRequestBudgetStore(store)
	h.usageOutbox = q
	h.requestTelemetry = makeTestRecorder()
	request := func() *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://units-customer.example/generate", nil))
		return w
	}
	w := request()
	if w.Code != http.StatusOK || w.Header().Get("Gregale-Usage") != "" {
		t.Fatalf("admitted request status=%d headers=%v", w.Code, w.Header())
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.PlatformTenantSurfaceID != surfaceID || item.Event.MeterUnits["tokens"] != 7 {
		t.Fatalf("surface usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}

	store.held.Store(true)
	w = request()
	var problem api.Problem
	if w.Code != http.StatusPaymentRequired || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != api.CodeTenantInvoiceOverdue {
		t.Fatalf("held tenant status=%d body=%s", w.Code, w.Body.String())
	}
	if got := forwarded.Load(); got != 1 {
		t.Fatalf("forwarded %d requests, want only the admitted one", got)
	}
	if got := q.Stats().PendingRecords; got != 1 {
		t.Fatalf("financial outbox has %d records, want only the admitted request", got)
	}
}
